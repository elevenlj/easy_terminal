package session

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"unicode"
)

var emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

const (
	defaultMaxLarkTextLines     = 300
	maxLarkTextRunes            = 12000
	larkTruncatedPrefix         = "[truncated]\n"
	codexNoAnchorFallbackPrefix = "[missing input anchor; showing tail]\n"
)

var larkNotifyMaxLines atomic.Int64
var larkNotifyDropLinePatterns atomic.Value

type larkNotifyDropLinePattern struct {
	re *regexp.Regexp
}

func init() {
	larkNotifyMaxLines.Store(defaultMaxLarkTextLines)
	larkNotifyDropLinePatterns.Store([]larkNotifyDropLinePattern{})
}

func SetLarkNotifyMaxLines(lines int) {
	if lines <= 0 {
		lines = defaultMaxLarkTextLines
	}
	larkNotifyMaxLines.Store(int64(lines))
}

func SetLarkNotifyDropLinePatterns(patterns []string) error {
	rules := make([]LarkNotifyDropLineRule, 0, len(patterns))
	for _, pattern := range patterns {
		rules = append(rules, LarkNotifyDropLineRule{Pattern: pattern})
	}
	return SetLarkNotifyDropLineRules(rules)
}

func SetLarkNotifyDropLineRules(rules []LarkNotifyDropLineRule) error {
	compiled := make([]larkNotifyDropLinePattern, 0, len(rules))
	for _, rule := range rules {
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			title := strings.TrimSpace(rule.Title)
			if title != "" {
				return fmt.Errorf("invalid lark notify drop line pattern %q (%s): %w", pattern, title, err)
			}
			return fmt.Errorf("invalid lark notify drop line pattern %q: %w", pattern, err)
		}
		compiled = append(compiled, larkNotifyDropLinePattern{re: re})
	}
	larkNotifyDropLinePatterns.Store(compiled)
	return nil
}

func sanitizeForLarkAudit(text string) string {
	return emailRE.ReplaceAllString(text, "[email]")
}

func truncateForLark(text string) string {
	text = truncateLinesFromTail(text, int(larkNotifyMaxLines.Load()), "")
	return truncateRunesFromTail(text, maxLarkTextRunes, larkTruncatedPrefix)
}

func dropConfiguredLarkNotifyLines(text string) string {
	patterns, _ := larkNotifyDropLinePatterns.Load().([]larkNotifyDropLinePattern)
	if len(patterns) == 0 || text == "" {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		drop := false
		for _, pattern := range patterns {
			if pattern.re.MatchString(line) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func truncateLinesFromTail(text string, maxLines int, prefix string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	return prefix + strings.Join(lines[len(lines)-maxLines:], "\n")
}

func truncateRunesFromTail(text string, maxRunes int, prefix string) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	prefixRunes := []rune(prefix)
	keep := maxRunes - len(prefixRunes)
	if keep < 1 {
		return string(runes[len(runes)-maxRunes:])
	}
	return prefix + string(runes[len(runes)-keep:])
}

func StripTerminalControls(data []byte) string {
	var b strings.Builder
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch c {
		case 0x1b:
			i = skipEscape(data, i)
		case '\r':
			b.WriteByte('\n')
		case '\b':
			s := b.String()
			if len(s) > 0 {
				b.Reset()
				b.WriteString(s[:len(s)-1])
			}
		default:
			if c == '\n' || c == '\t' || (c >= 0x20 && c != 0x7f) {
				b.WriteByte(c)
			}
		}
	}
	return compactRepeatedLines(b.String())
}

func HasRenderableContent(data []byte) bool {
	text := StripTerminalControls(data)
	for _, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func PickNotifyContent(visibleSnapshot string, snapshotAtRoundStart string, roundReply []byte, lastInputText string) string {
	lastInputText = strings.TrimSpace(lastInputText)
	body, _ := selectNotifyBody(visibleSnapshot, snapshotAtRoundStart, roundReply, lastInputText)
	if body == "" {
		return ""
	}
	body = trimVisibleText(body)
	return truncateForLark(sanitizeForLarkAudit(body))
}

func NotifyContentNeedsMoreSnapshot(visibleSnapshot string, snapshotAtRoundStart string, roundReply []byte, lastInputText string) bool {
	lastInputText = strings.TrimSpace(lastInputText)
	body, _ := selectNotifyBody(visibleSnapshot, snapshotAtRoundStart, roundReply, lastInputText)
	if strings.TrimSpace(body) == "" {
		return true
	}
	hasReply := hasReplyLine(trimVisibleText(body), lastInputText)
	return !hasReply || (containsTransientStatusLine(body) && !hasReply)
}

func selectNotifyBody(visibleSnapshot string, snapshotAtRoundStart string, _ []byte, lastInputText string) (string, bool) {
	visibleBody, fromVisible := currentRoundVisibleText(visibleSnapshot, snapshotAtRoundStart, lastInputText)
	if strings.TrimSpace(visibleBody) == "" {
		return "", false
	}
	return visibleBody, fromVisible
}

func NotifyContentNeedsConservativeDelay(visibleSnapshot string, snapshotAtRoundStart string, lastInputText string) bool {
	lastInputText = strings.TrimSpace(lastInputText)
	body, fromVisible := currentRoundVisibleText(visibleSnapshot, snapshotAtRoundStart, lastInputText)
	if !fromVisible || strings.TrimSpace(body) == "" {
		return true
	}
	if containsTransientStatusLine(body) {
		return true
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isPromptStatusLine(trimmed) {
			return true
		}
		if isCodexSuggestionLine(trimmed) && !isInputEchoLine(trimmed, lastInputText) {
			return true
		}
	}
	return false
}

func currentRoundVisibleText(visibleSnapshot string, snapshotAtRoundStart string, lastInputText string) (string, bool) {
	visibleSnapshot = trimVisibleText(visibleSnapshot)
	if visibleSnapshot == "" {
		return "", false
	}
	return visibleSnapshot, true
}

func visibleTextAfterRoundStart(visibleSnapshot string, snapshotAtRoundStart string) string {
	visibleSnapshot = trimVisibleText(visibleSnapshot)
	snapshotAtRoundStart = trimVisibleText(snapshotAtRoundStart)
	if visibleSnapshot == "" || snapshotAtRoundStart == "" {
		return ""
	}
	if visibleSnapshot == snapshotAtRoundStart {
		return ""
	}
	if strings.HasPrefix(visibleSnapshot, snapshotAtRoundStart) {
		return trimVisibleText(strings.TrimPrefix(visibleSnapshot, snapshotAtRoundStart))
	}
	return ""
}

func trimVisibleText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func normalizeSnapshotText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(text)
}

func visibleTextFromLastInput(visibleSnapshot string, lastInputText string) string {
	lines := strings.Split(visibleSnapshot, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if isStructuredInputAnchorLine(lines[i], lastInputText) {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if isInputEchoLine(lines[i], lastInputText) {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
		if isWrappedInputEchoAt(lines, i, lastInputText) {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}

func visibleTextFromLastAnyInput(visibleSnapshot string) string {
	lines := strings.Split(visibleSnapshot, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if text, ok := inputEchoText(lines[i]); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
		if text, ok := shellInputEchoText(lines[i]); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}

func isWrappedInputEchoAt(lines []string, i int, lastInputText string) bool {
	text, ok := inputEchoText(lines[i])
	if !ok {
		return false
	}
	target := compactAnchorText(lastInputText)
	current := compactAnchorText(text)
	if target == "" || current == "" || !strings.HasPrefix(target, current) {
		return false
	}
	for j := i + 1; j < len(lines) && j <= i+6; j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if _, ok := inputEchoText(trimmed); ok {
			return false
		}
		if strings.HasPrefix(trimmed, "• ") || isPromptStatusLine(trimmed) || isCodexSuggestionLine(trimmed) {
			return false
		}
		current += compactAnchorText(trimmed)
		if current == target || strings.HasPrefix(current, target) {
			return true
		}
		if !strings.HasPrefix(target, current) {
			return false
		}
	}
	return false
}

func compactAnchorText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), "")
}

func visibleTextFromLastShellInput(visibleSnapshot string) string {
	lines := strings.Split(visibleSnapshot, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		text, ok := shellInputEchoText(lines[i])
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		return strings.TrimSpace(strings.Join(lines[i:], "\n"))
	}
	return ""
}

func startsWithInputEcho(text string, input string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return isInputEchoLine(line, input)
	}
	return false
}

func inputEchoText(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if rest, ok := trimPromptPrefix(trimmed, "›"); ok {
		return rest, true
	}
	if rest, ok := trimPromptPrefix(trimmed, ">"); ok {
		return rest, true
	}
	for _, prompt := range []string{"%", "$", "#", ">"} {
		if rest, ok := trimPromptPrefix(trimmed, prompt); ok {
			return rest, true
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmed, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[idx+len(marker):]), true
		}
	}
	return "", false
}

func isStructuredInputAnchorLine(line string, input string) bool {
	raw, ok := inputEchoTextRaw(line)
	if !ok || !strings.HasPrefix(raw, " ") {
		return false
	}
	return strings.TrimSpace(raw) == strings.TrimSpace(input)
}

func inputEchoTextRaw(line string) (string, bool) {
	trimmedLeft := strings.TrimLeft(line, " \t")
	if rest, ok := trimPromptPrefixRaw(trimmedLeft, "›"); ok {
		return rest, true
	}
	if rest, ok := trimPromptPrefixRaw(trimmedLeft, ">"); ok {
		return rest, true
	}
	for _, prompt := range []string{"%", "$", "#", ">"} {
		if rest, ok := trimPromptPrefixRaw(trimmedLeft, prompt); ok {
			return rest, true
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmedLeft, marker); idx >= 0 {
			return strings.TrimRight(trimmedLeft[idx+len(marker):], "\r\n"), true
		}
	}
	return "", false
}

func shellInputEchoText(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	for _, prompt := range []string{"%", "$", "#"} {
		if rest, ok := trimPromptPrefix(trimmed, prompt); ok {
			return rest, true
		}
		if strings.HasSuffix(trimmed, " "+prompt) {
			return "", true
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmed, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[idx+len(marker):]), true
		}
	}
	if strings.HasSuffix(trimmed, " >") {
		return "", true
	}
	marker := " > "
	if idx := strings.LastIndex(trimmed, marker); idx > 0 {
		return strings.TrimSpace(trimmed[idx+len(marker):]), true
	}
	return "", false
}

func trimPromptPrefix(line string, prompt string) (string, bool) {
	if line == prompt {
		return "", true
	}
	prefix := prompt + " "
	if strings.HasPrefix(line, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
	}
	return "", false
}

func trimPromptPrefixRaw(line string, prompt string) (string, bool) {
	if line == prompt {
		return "", true
	}
	prefix := prompt + " "
	if strings.HasPrefix(line, prefix) {
		return strings.TrimRight(strings.TrimPrefix(line, prefix), "\r\n"), true
	}
	return "", false
}

func cleanupLarkNotifyText(text string, lastInputText string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isTransientStatusLine(trimmed) {
			continue
		}
		if isPromptStatusLine(trimmed) {
			continue
		}
		if isPureHorizontalRuleLine(trimmed) {
			continue
		}
		if isCodexSuggestionLine(trimmed) && !isInputEchoLine(trimmed, lastInputText) {
			continue
		}
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func restoreWrappedInputEcho(text string, lastInputText string) string {
	input := strings.TrimSpace(lastInputText)
	if strings.TrimSpace(text) == "" || input == "" {
		return text
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if restored, end, ok := restoredWrappedInputEchoAt(lines, i, input); ok {
			out = append(out, restored)
			i = end
			continue
		}
		out = append(out, lines[i])
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func restoredWrappedInputEchoAt(lines []string, i int, input string) (string, int, bool) {
	text, ok := inputEchoText(lines[i])
	if !ok {
		return "", i, false
	}
	target := compactAnchorText(input)
	current := compactAnchorText(text)
	if target == "" || current == "" || current == target || !strings.HasPrefix(target, current) {
		return "", i, false
	}
	for j := i + 1; j < len(lines) && j <= i+6; j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if _, ok := inputEchoText(trimmed); ok {
			return "", i, false
		}
		if strings.HasPrefix(trimmed, "• ") || isPromptStatusLine(trimmed) || isCodexSuggestionLine(trimmed) {
			return "", i, false
		}
		current += compactAnchorText(trimmed)
		if current == target || strings.HasPrefix(current, target) {
			return inputEchoPrefix(lines[i]) + input, j, true
		}
		if !strings.HasPrefix(target, current) {
			return "", i, false
		}
	}
	return "", i, false
}

func inputEchoPrefix(line string) string {
	trimmed := strings.TrimSpace(line)
	for _, prompt := range []string{"›", ">", "%", "$", "#"} {
		if trimmed == prompt {
			return prompt + " "
		}
		if strings.HasPrefix(trimmed, prompt+" ") {
			return prompt + " "
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmed, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[:idx+len(marker)])
		}
	}
	return ""
}

func isPureHorizontalRuleLine(line string) bool {
	line = strings.TrimSpace(line)
	if len([]rune(line)) < 3 {
		return false
	}
	for _, r := range line {
		if unicode.IsSpace(r) {
			continue
		}
		switch r {
		case '-', '_', '=', '─', '━', '—', '―', '═', '╌', '╍', '┄', '┅', '┈', '┉':
			continue
		default:
			return false
		}
	}
	return true
}

func containsTransientStatusLine(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if isTransientStatusLine(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

func isTransientStatusLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "working (") ||
		strings.Contains(lower, "esc to interrupt") ||
		strings.Contains(lower, "falling back from websockets") ||
		strings.Contains(lower, "stream disconnected before completion")
}

func hasReplyLine(text string, lastInputText string) bool {
	input := strings.TrimSpace(lastInputText)
	sawInput := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if input != "" && isInputEchoLine(trimmed, input) {
			sawInput = true
			continue
		}
		if _, ok := inputEchoText(trimmed); ok {
			continue
		}
		if shellText, ok := shellInputEchoText(trimmed); ok {
			if input != "" && strings.TrimSpace(shellText) == input {
				sawInput = true
				continue
			}
			if sawInput && strings.TrimSpace(shellText) == "" {
				return true
			}
			continue
		}
		if isTransientStatusLine(trimmed) || isPromptStatusLine(trimmed) || isCodexSuggestionLine(trimmed) {
			continue
		}
		return true
	}
	return input == ""
}

func isInputEchoLine(line string, input string) bool {
	text, ok := inputEchoText(line)
	return ok && strings.TrimSpace(text) == strings.TrimSpace(input)
}

func isPromptStatusLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasPrefix(lower, "gpt-") && strings.Contains(lower, "medium") && strings.Contains(line, "~")
}

func isCodexSuggestionLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if text, ok := inputEchoText(trimmed); ok {
		trimmed = text
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "implement {feature}"):
		return true
	case strings.HasPrefix(lower, "find and fix a bug in @filename"):
		return true
	case strings.HasPrefix(lower, "improve documentation in @filename"):
		return true
	case strings.HasPrefix(lower, "run /review on my current changes"):
		return true
	case strings.HasPrefix(lower, "use /skills to list available skills"):
		return true
	default:
		return false
	}
}

func compactRepeatedLines(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	var prev string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed != prev {
			out = append(out, line)
		}
		prev = trimmed
	}
	return strings.Join(out, "\n")
}

func skipEscape(data []byte, i int) int {
	if i+1 >= len(data) {
		return i
	}
	next := data[i+1]
	if next == '[' {
		j := i + 2
		for j < len(data) {
			c := data[j]
			if c >= 0x40 && c <= 0x7e {
				return j
			}
			j++
		}
		return len(data) - 1
	}
	if next == ']' {
		j := i + 2
		for j < len(data) {
			if data[j] == 0x07 {
				return j
			}
			if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
				return j + 1
			}
			j++
		}
		return len(data) - 1
	}
	return i + 1
}
