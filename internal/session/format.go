package session

import (
	"regexp"
	"strings"
	"unicode"
)

var emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

func sanitizeForLarkAudit(text string) string {
	return emailRE.ReplaceAllString(text, "[email]")
}

func truncateForLark(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= 300 {
		return text
	}
	return "[truncated]\n" + strings.Join(lines[len(lines)-300:], "\n")
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
		body = lastInputText
		fromVisible = true
	}
	if !fromVisible && lastInputText != "" && strings.HasPrefix(body, lastInputText) {
		body = strings.TrimSpace(strings.TrimPrefix(body, lastInputText))
	}
	if !fromVisible && lastInputText != "" && body != "" {
		body = lastInputText + "\n\n" + body
	} else if !fromVisible && lastInputText != "" {
		body = lastInputText
	}
	body = cleanupLarkNotifyText(body, lastInputText)
	return truncateForLark(sanitizeForLarkAudit(body))
}

func NotifyContentNeedsMoreSnapshot(visibleSnapshot string, snapshotAtRoundStart string, lastInputText string) bool {
	lastInputText = strings.TrimSpace(lastInputText)
	body, fromVisible := currentRoundVisibleText(visibleSnapshot, snapshotAtRoundStart, lastInputText)
	if !fromVisible || strings.TrimSpace(body) == "" {
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
	snapshotAtRoundStart = strings.TrimSpace(snapshotAtRoundStart)
	if visibleSnapshot == "" {
		return "", false
	}
	if lastInputText != "" {
		if current := visibleTextFromLastInput(visibleSnapshot, lastInputText); current != "" {
			return current, true
		}
	}
	if snapshotAtRoundStart == "" {
		return visibleSnapshot, true
	}
	if strings.HasPrefix(visibleSnapshot, snapshotAtRoundStart) {
		return strings.TrimSpace(strings.TrimPrefix(visibleSnapshot, snapshotAtRoundStart)), true
	}
	if idx := strings.LastIndex(visibleSnapshot, snapshotAtRoundStart); idx >= 0 {
		return strings.TrimSpace(visibleSnapshot[idx+len(snapshotAtRoundStart):]), true
	}
	return "", false
}

func visibleTextFromLastInput(visibleSnapshot string, lastInputText string) string {
	idx := strings.LastIndex(visibleSnapshot, lastInputText)
	if idx < 0 {
		return ""
	}
	lineStart := strings.LastIndex(visibleSnapshot[:idx], "\n")
	if lineStart < 0 {
		lineStart = 0
	} else {
		lineStart++
	}
	return strings.TrimSpace(visibleSnapshot[lineStart:])
}

func cleanupLarkNotifyText(text string, lastInputText string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isPureHorizontalRule(trimmed) {
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
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if input != "" && isInputEchoLine(trimmed, input) {
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
	line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
	return strings.TrimSpace(line) == input
}

func isPromptStatusLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasPrefix(lower, "gpt-") && strings.Contains(lower, "medium") && strings.Contains(line, "~")
}

func isCodexSuggestionLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, ">"))
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

func isPureHorizontalRule(line string) bool {
	count := 0
	for _, r := range line {
		if unicode.IsSpace(r) {
			continue
		}
		if !isHorizontalRuleRune(r) {
			return false
		}
		count++
	}
	return count >= 3
}

func isHorizontalRuleRune(r rune) bool {
	switch r {
	case '-', '_', '=', '*', '─', '━', '—', '―', '－', '﹣', '＿':
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
