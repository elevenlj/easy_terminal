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

func PickNotifyContent(visibleSnapshot string, roundReply []byte, lastInputText string) string {
	body := strings.TrimSpace(visibleSnapshot)
	if body == "" {
		body = SanitizeRoundReply(roundReply)
	}
	lastInputText = strings.TrimSpace(lastInputText)
	if lastInputText != "" && strings.HasPrefix(body, lastInputText) {
		body = strings.TrimSpace(strings.TrimPrefix(body, lastInputText))
	}
	if lastInputText != "" && body != "" {
		body = lastInputText + "\n\n" + body
	} else if lastInputText != "" {
		body = lastInputText
	}
	body = cleanupLarkNotifyText(body)
	return truncateForLark(sanitizeForLarkAudit(body))
}

func cleanupLarkNotifyText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blank = true
			continue
		}
		if isPureHorizontalRule(trimmed) {
			blank = true
			continue
		}
		if blank && len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, strings.TrimRight(line, " \t"))
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
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
