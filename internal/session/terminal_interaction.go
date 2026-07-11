package session

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	TerminalInteractionCodexModel     = "codex_model"
	TerminalInteractionCodexReasoning = "codex_reasoning"
	TerminalInteractionCodexResume    = "codex_resume"

	maxTerminalInteractionOptions = 20
)

var (
	terminalInteractionOptionRE  = regexp.MustCompile(`^\s*(?:[›❯>]\s*)?(\d{1,2})[.)]\s+(.+?)\s*$`)
	terminalInteractionMarkerRE  = regexp.MustCompile(`(?i)\s*\((current|default)\)\s*`)
	terminalInteractionColumnsRE = regexp.MustCompile(`\s{2,}`)
	terminalInteractionResumeRE  = regexp.MustCompile(`(?i)^\s*([❯›>]\s*)?((?:now|\d+\s*(?:s|m|h|d|w|mo|y)(?:\s+ago)?))\s+(.+?)\s*$`)

	errTerminalInteractionExpired       = errors.New("该选择已失效，请刷新后重试")
	errTerminalInteractionOptionInvalid = errors.New("未找到所选选项")
)

type TerminalInteraction struct {
	ID              string
	Kind            string
	Title           string
	Fingerprint     string
	NotifyVersion   int64
	SnapshotVersion int64
	MessageID       string
	Options         []TerminalInteractionOption
}

type TerminalInteractionOption struct {
	ID              string
	Label           string
	Description     string
	Input           string
	SubmitWithEnter bool
	Current         bool
	Default         bool
}

func DetectCodexTerminalInteraction(text, sessionID, lastInputText string, notifyVersion, snapshotVersion int64) *TerminalInteraction {
	text = trimVisibleText(text)
	if text == "" {
		return nil
	}
	lines := splitVisibleLines(text)
	kind, title, titleIndex := codexInteractionTitle(lines)
	if kind == "" {
		return nil
	}
	if kind == TerminalInteractionCodexModel && !isCodexModelInteractionInput(lastInputText) {
		return nil
	}
	if kind == TerminalInteractionCodexResume && !isCodexResumeInteractionInput(lastInputText) {
		return nil
	}
	footerIndex := codexInteractionFooterIndex(lines, titleIndex+1)
	if footerIndex < 0 {
		if kind != TerminalInteractionCodexReasoning {
			return nil
		}
		footerIndex = len(lines)
		if !codexReasoningMenuReachesTerminalTail(lines, titleIndex+1) {
			return nil
		}
	} else if !codexInteractionHasActiveTail(lines[footerIndex+1:]) {
		return nil
	}
	var options []TerminalInteractionOption
	if kind == TerminalInteractionCodexResume {
		options = parseCodexResumeInteractionOptions(lines[titleIndex+1 : footerIndex])
	} else {
		options = parseTerminalInteractionOptions(lines[titleIndex+1 : footerIndex])
	}
	minimumOptions := 2
	if kind == TerminalInteractionCodexResume {
		minimumOptions = 1
	}
	if len(options) < minimumOptions || len(options) > maxTerminalInteractionOptions ||
		(kind != TerminalInteractionCodexResume && !terminalInteractionOptionsSequential(options)) {
		return nil
	}
	fingerprint := terminalInteractionFingerprint(kind, title, options)
	idSeed := fmt.Sprintf("%s\x00%d\x00%s", sessionID, notifyVersion, fingerprint)
	idSum := sha256.Sum256([]byte(idSeed))
	return &TerminalInteraction{
		ID:              fmt.Sprintf("ti_%x", idSum[:10]),
		Kind:            kind,
		Title:           sanitizeForLarkAudit(strings.TrimSpace(title)),
		Fingerprint:     fingerprint,
		NotifyVersion:   notifyVersion,
		SnapshotVersion: snapshotVersion,
		Options:         options,
	}
}

func codexReasoningMenuReachesTerminalTail(lines []string, start int) bool {
	lastMeaningful := -1
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" && !isCodexInteractionStatusLine(lines[i]) {
			lastMeaningful = i
		}
	}
	if lastMeaningful < start {
		return false
	}
	return terminalInteractionOptionRE.MatchString(lines[lastMeaningful])
}

func codexInteractionHasActiveTail(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" && !isCodexInteractionStatusLine(line) {
			return false
		}
	}
	return true
}

func isCodexInteractionStatusLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "gpt-") ||
		strings.Contains(lower, "background terminal running") ||
		strings.Contains(lower, "esc to interrupt") ||
		(strings.Contains(lower, "ctrl+o") && strings.Contains(lower, "preview")) ||
		strings.Contains(lower, "comfortable view") ||
		strings.Contains(lower, "ctrl+t transcript") ||
		strings.Contains(lower, "ctrl+e expand") ||
		strings.Contains(lower, "browse")
}

func codexInteractionTitle(lines []string) (kind string, title string, index int) {
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		normalized := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
		switch {
		case normalized == "resume a previous session":
			return TerminalInteractionCodexResume, trimmed, i
		case strings.HasPrefix(normalized, "select reasoning ") &&
			(strings.Contains(normalized, " level") || strings.Contains(normalized, " effort")):
			return TerminalInteractionCodexReasoning, trimmed, i
		case strings.HasPrefix(normalized, "select model") &&
			(strings.Contains(normalized, " effort") || normalized == "select model"):
			return TerminalInteractionCodexModel, trimmed, i
		}
	}
	return "", "", -1
}

func codexInteractionFooterIndex(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(lines[i])), " "))
		if strings.Contains(normalized, "enter") && strings.Contains(normalized, "esc") &&
			(strings.Contains(normalized, "confirm") || strings.Contains(normalized, "go back") ||
				(strings.Contains(normalized, "resume") && (strings.Contains(normalized, "new") || strings.Contains(normalized, "exit")))) {
			return i
		}
	}
	return -1
}

func parseCodexResumeInteractionOptions(lines []string) []TerminalInteractionOption {
	type resumeRow struct {
		when    string
		label   string
		current bool
	}
	rows := make([]resumeRow, 0, len(lines))
	currentIndex := 0
	for _, line := range lines {
		match := terminalInteractionResumeRE.FindStringSubmatch(line)
		if len(match) != 4 {
			continue
		}
		row := resumeRow{
			when:    strings.TrimSpace(match[2]),
			label:   strings.TrimSpace(match[3]),
			current: strings.TrimSpace(match[1]) != "",
		}
		if row.label == "" {
			continue
		}
		if row.current {
			currentIndex = len(rows)
		}
		rows = append(rows, row)
		if len(rows) == maxTerminalInteractionOptions {
			break
		}
	}
	options := make([]TerminalInteractionOption, 0, len(rows))
	for i, row := range rows {
		movement := strings.Repeat("\x1b[B", max(0, i-currentIndex))
		if i < currentIndex {
			movement = strings.Repeat("\x1b[A", currentIndex-i)
		}
		options = append(options, TerminalInteractionOption{
			ID:              fmt.Sprintf("opt_%d", i+1),
			Label:           sanitizeForLarkAudit(row.label),
			Input:           movement,
			SubmitWithEnter: true,
			Current:         row.current,
			Description:     sanitizeForLarkAudit(row.when),
		})
	}
	return options
}

func parseTerminalInteractionOptions(lines []string) []TerminalInteractionOption {
	options := make([]TerminalInteractionOption, 0, len(lines))
	for _, line := range lines {
		match := terminalInteractionOptionRE.FindStringSubmatch(line)
		if len(match) == 3 {
			label, description, current, defaultOption := splitTerminalInteractionOptionText(match[2])
			if label == "" {
				continue
			}
			options = append(options, TerminalInteractionOption{
				ID:          fmt.Sprintf("opt_%d", len(options)+1),
				Label:       sanitizeForLarkAudit(label),
				Description: sanitizeForLarkAudit(description),
				Input:       match[1],
				Current:     current,
				Default:     defaultOption,
			})
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || len(options) == 0 {
			continue
		}
		last := &options[len(options)-1]
		if last.Description == "" {
			last.Description = sanitizeForLarkAudit(trimmed)
		} else {
			last.Description += " " + sanitizeForLarkAudit(trimmed)
		}
	}
	return options
}

func splitTerminalInteractionOptionText(text string) (label, description string, current, defaultOption bool) {
	parts := terminalInteractionColumnsRE.Split(strings.TrimSpace(text), 2)
	label = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		description = strings.TrimSpace(parts[1])
	}
	markers := terminalInteractionMarkerRE.FindAllStringSubmatch(label, -1)
	for _, marker := range markers {
		if len(marker) < 2 {
			continue
		}
		switch strings.ToLower(marker[1]) {
		case "current":
			current = true
		case "default":
			defaultOption = true
		}
	}
	label = strings.TrimSpace(terminalInteractionMarkerRE.ReplaceAllString(label, " "))
	return label, description, current, defaultOption
}

func terminalInteractionOptionsSequential(options []TerminalInteractionOption) bool {
	if len(options) == 0 {
		return false
	}
	previous := 0
	for i, option := range options {
		n, err := strconv.Atoi(option.Input)
		if err != nil || n <= 0 {
			return false
		}
		if i > 0 && n != previous+1 {
			return false
		}
		previous = n
	}
	return true
}

func terminalInteractionFingerprint(kind, title string, options []TerminalInteractionOption) string {
	var b strings.Builder
	b.WriteString(kind)
	b.WriteByte('\n')
	b.WriteString(strings.Join(strings.Fields(strings.ToLower(title)), " "))
	for _, option := range options {
		fmt.Fprintf(&b, "\n%s|%s|%t|%t", option.Input, strings.Join(strings.Fields(strings.ToLower(option.Label)), " "), option.Current, option.Default)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum[:16])
}

func isCodexModelInteractionInput(text string) bool {
	text = strings.TrimSpace(strings.ReplaceAll(text, "／", "/"))
	return text == "/model" || strings.HasPrefix(text, "/model ")
}

func isCodexResumeInteractionInput(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return text == "/resume" || strings.HasPrefix(text, "/resume ")
}

func cloneTerminalInteraction(interaction *TerminalInteraction) *TerminalInteraction {
	if interaction == nil {
		return nil
	}
	cloned := *interaction
	cloned.Options = append([]TerminalInteractionOption(nil), interaction.Options...)
	return &cloned
}

func (rt *RuntimeSession) notificationInteractionLocked(messageID string) *TerminalInteraction {
	body, _ := selectNotifyBodyWithWindow(
		rt.visibleSnapshot,
		rt.previousNotifySnapshotLocked(),
		rt.roundReply,
		rt.lastInputText,
		rt.notificationWindowInputText,
	)
	interaction := DetectCodexTerminalInteraction(body, rt.session.ID, rt.lastInputText, rt.notifyVersion, rt.visibleSnapshotVersion)
	if interaction == nil || interaction.ID == rt.lastConsumedTerminalInteractionID {
		rt.pendingTerminalInteraction = nil
		return nil
	}
	messageID = strings.TrimSpace(messageID)
	if rt.pendingTerminalInteraction != nil && rt.pendingTerminalInteraction.ID == interaction.ID {
		if messageID == "" {
			messageID = rt.pendingTerminalInteraction.MessageID
		}
	}
	interaction.MessageID = messageID
	rt.pendingTerminalInteraction = cloneTerminalInteraction(interaction)
	return cloneTerminalInteraction(interaction)
}

func (rt *RuntimeSession) bindTerminalInteractionMessageLocked(interaction *TerminalInteraction, messageID string) {
	if interaction == nil || rt.pendingTerminalInteraction == nil || rt.pendingTerminalInteraction.ID != interaction.ID {
		return
	}
	messageID = strings.TrimSpace(messageID)
	if messageID != "" {
		rt.pendingTerminalInteraction.MessageID = messageID
	}
}

func (rt *RuntimeSession) consumeTerminalInteraction(interactionID, optionID, messageID string) (TerminalInteractionOption, error) {
	if rt == nil {
		return TerminalInteractionOption{}, errTerminalInteractionExpired
	}
	interactionID = strings.TrimSpace(interactionID)
	optionID = strings.TrimSpace(optionID)
	messageID = strings.TrimSpace(messageID)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.session.Live || rt.session.Status != StatusWaiting || rt.pendingTerminalInteraction == nil ||
		rt.pendingTerminalInteraction.ID != interactionID || rt.pendingTerminalInteraction.NotifyVersion != rt.notifyVersion {
		return TerminalInteractionOption{}, errTerminalInteractionExpired
	}
	if err := rt.validateNotificationActionLocked(messageID); err != nil {
		return TerminalInteractionOption{}, errTerminalInteractionExpired
	}
	if expected := strings.TrimSpace(rt.pendingTerminalInteraction.MessageID); expected != "" && messageID != "" && expected != messageID {
		return TerminalInteractionOption{}, errTerminalInteractionExpired
	}
	for _, option := range rt.pendingTerminalInteraction.Options {
		if option.ID != optionID {
			continue
		}
		rt.lastConsumedTerminalInteractionID = interactionID
		rt.pendingTerminalInteraction = nil
		return option, nil
	}
	return TerminalInteractionOption{}, errTerminalInteractionOptionInvalid
}
