package session

import (
	"regexp"
	"strings"
	"sync/atomic"
	"unicode"
)

var emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

const (
	defaultMaxLarkTextLines           = 300
	defaultCodexNoAnchorFallbackLines = 80
	maxLarkTextRunes                  = 12000
	larkTruncatedPrefix               = "[truncated]\n"
	codexNoAnchorFallbackPrefix       = "[missing input anchor; showing tail]\n"
)

var larkNotifyMaxLines atomic.Int64
var codexNoAnchorFallbackLines atomic.Int64

func init() {
	larkNotifyMaxLines.Store(defaultMaxLarkTextLines)
	codexNoAnchorFallbackLines.Store(defaultCodexNoAnchorFallbackLines)
}

func SetLarkNotifyMaxLines(lines int) {
	if lines <= 0 {
		lines = defaultMaxLarkTextLines
	}
	larkNotifyMaxLines.Store(int64(lines))
}

func SetCodexNoAnchorFallbackLines(lines int) {
	if lines <= 0 {
		lines = defaultCodexNoAnchorFallbackLines
	}
	codexNoAnchorFallbackLines.Store(int64(lines))
}

func sanitizeForLarkAudit(text string) string {
	return emailRE.ReplaceAllString(text, "[email]")
}

func truncateForLark(text string) string {
	text = truncateLinesFromTail(text, int(larkNotifyMaxLines.Load()), larkTruncatedPrefix)
	return truncateRunesFromTail(text, maxLarkTextRunes, larkTruncatedPrefix)
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

func SanitizeRoundReply(data []byte) string {
	return strings.TrimSpace(StripTerminalControls(data))
}

func PickNotifyContent(visibleSnapshot string, snapshotAtRoundStart string, roundReply []byte, lastInputText string) string {
	lastInputText = strings.TrimSpace(lastInputText)
	body, fromVisible := currentRoundVisibleText(visibleSnapshot, snapshotAtRoundStart, lastInputText)
	if body == "" {
		body = currentRoundReplyText(roundReply, lastInputText)
		fromVisible = false
	}
	if body == "" {
		if lastInputText == "" {
			body = strings.TrimSpace(visibleSnapshot)
			fromVisible = true
		} else {
			body = lastInputText
			fromVisible = true
		}
	}
	if !fromVisible && lastInputText != "" {
		if strings.HasPrefix(body, lastInputText) {
			body = strings.TrimSpace(strings.TrimPrefix(body, lastInputText))
		}
		if body == "" {
			body = lastInputText
		} else if !startsWithInputEcho(body, lastInputText) {
			body = lastInputText + "\n\n" + body
		}
	}
	body = cleanupLarkNotifyText(body, lastInputText)
	return truncateForLark(sanitizeForLarkAudit(body))
}

func NotifyContentNeedsMoreSnapshot(visibleSnapshot string, snapshotAtRoundStart string, roundReply []byte, lastInputText string) bool {
	lastInputText = strings.TrimSpace(lastInputText)
	body, fromVisible := currentRoundVisibleText(visibleSnapshot, snapshotAtRoundStart, lastInputText)
	if !fromVisible || strings.TrimSpace(body) == "" {
		body = currentRoundReplyText(roundReply, lastInputText)
	}
	if strings.TrimSpace(body) == "" {
		return true
	}
	cleaned := cleanupLarkNotifyText(body, lastInputText)
	hasReply := hasReplyLine(cleaned, lastInputText)
	return !hasReply || (containsTransientStatusLine(body) && !hasReply)
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
	visibleSnapshot = strings.TrimSpace(visibleSnapshot)
	if visibleSnapshot == "" {
		return "", false
	}
	if lastInputText != "" {
		if current := visibleTextFromLastInput(visibleSnapshot, lastInputText); current != "" {
			return current, true
		}
		if current := visibleTextAfterRoundStart(visibleSnapshot, snapshotAtRoundStart); current != "" {
			if exit := codexExitSegment(current); exit != "" {
				return exit, true
			}
			if isFullScreenTUIScreen(current) {
				return codexNoAnchorFallbackText(current), true
			}
			return current, true
		}
		if exit := codexExitSegment(visibleSnapshot); exit != "" {
			return exit, true
		}
		if isFullScreenTUIScreen(visibleSnapshot) {
			return codexNoAnchorFallbackText(visibleSnapshot), true
		}
		if current := visibleTextFromLastShellInput(visibleSnapshot); current != "" {
			return current, true
		}
	}
	if isTrustTUIScreen(visibleSnapshot) || (lastInputText == "" && isFullScreenTUIScreen(visibleSnapshot)) {
		return visibleSnapshot, true
	}
	if lastInputText == "" {
		return visibleSnapshot, true
	}
	if isFullScreenTUIScreen(visibleSnapshot) {
		return codexNoAnchorFallbackText(visibleSnapshot), true
	}
	return visibleSnapshot, true
}

func codexNoAnchorFallbackText(text string) string {
	text = codexTUISegment(text)
	return truncateLinesFromTail(strings.TrimSpace(text), int(codexNoAnchorFallbackLines.Load()), codexNoAnchorFallbackPrefix)
}

func codexExitSegment(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.Contains(line, "Token usage:") || strings.Contains(line, "run codex resume") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}

func codexTUISegment(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if !strings.Contains(line, "OpenAI Codex") {
			continue
		}
		start := i
		if i > 0 && looksLikeBoxBorder(lines[i-1]) {
			start = i - 1
		}
		return strings.TrimSpace(strings.Join(lines[start:], "\n"))
	}
	return text
}

func looksLikeBoxBorder(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "─") &&
		(strings.Contains(trimmed, "╭") || strings.Contains(trimmed, "┌") || strings.Contains(trimmed, "+"))
}

func visibleTextAfterRoundStart(visibleSnapshot string, snapshotAtRoundStart string) string {
	visibleSnapshot = normalizeSnapshotText(visibleSnapshot)
	snapshotAtRoundStart = normalizeSnapshotText(snapshotAtRoundStart)
	if visibleSnapshot == "" || snapshotAtRoundStart == "" {
		return ""
	}
	if visibleSnapshot == snapshotAtRoundStart {
		return ""
	}
	if strings.HasPrefix(visibleSnapshot, snapshotAtRoundStart) {
		return strings.TrimSpace(strings.TrimPrefix(visibleSnapshot, snapshotAtRoundStart))
	}
	return ""
}

func normalizeSnapshotText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(text)
}

func isFullScreenTUIScreen(text string) bool {
	return (strings.Contains(text, "OpenAI Codex") &&
		strings.Contains(text, "model:") &&
		strings.Contains(text, "directory:")) ||
		isTrustTUIScreen(text)
}

func isTrustTUIScreen(text string) bool {
	return strings.Contains(text, "Do you trust the contents of this directory?") &&
		strings.Contains(text, "Press enter to continue")
}

func visibleTextFromLastInput(visibleSnapshot string, lastInputText string) string {
	lines := strings.Split(visibleSnapshot, "\n")
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

func currentRoundReplyText(roundReply []byte, lastInputText string) string {
	text := SanitizeRoundReply(roundReply)
	if text == "" {
		return ""
	}
	if lastInputText != "" {
		if current := visibleTextFromLastInput(text, lastInputText); current != "" {
			return current
		}
	}
	if lastInputText != "" && !hasAssistantBulletLine(text) {
		return ""
	}
	return text
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

func hasAssistantBulletLine(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "• ") {
			return true
		}
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
		if isCodexSuggestionLine(trimmed) && !isInputEchoLine(trimmed, lastInputText) {
			continue
		}
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
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
		if isPromptStatusLine(trimmed) || isCodexSuggestionLine(trimmed) {
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
