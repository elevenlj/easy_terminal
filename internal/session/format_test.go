package session

import (
	"strings"
	"testing"
)

func TestPickNotifyContentPrefersVisibleSnapshot(t *testing.T) {
	got := PickNotifyContent("cmd\nrendered output", []byte("raw"), "cmd")
	if got != "cmd\nrendered output" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestPickNotifyContentRemovesExtraBlankLinesAndHorizontalRules(t *testing.T) {
	visible := strings.Join([]string{
		"Searching the web",
		"",
		"Searched weather: Chengdu, Sichuan, China",
		"────────────────────────────────────────",
		"________________________",
		"成都今天预计晴转多云，18~29°C。",
		"",
		"",
		"Use /skills to list available skills",
	}, "\n")
	got := PickNotifyContent(visible, nil, "")
	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected blank lines to be collapsed, got %q", got)
	}
	if strings.Contains(got, "────") || strings.Contains(got, "____") {
		t.Fatalf("expected horizontal rule lines to be removed, got %q", got)
	}
	want := strings.Join([]string{
		"Searching the web",
		"Searched weather: Chengdu, Sichuan, China",
		"成都今天预计晴转多云，18~29°C。",
		"Use /skills to list available skills",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected cleaned content:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentSanitizesEmail(t *testing.T) {
	got := PickNotifyContent("", []byte("contact me@example.com"), "")
	if strings.Contains(got, "me@example.com") || !strings.Contains(got, "[email]") {
		t.Fatalf("email was not sanitized: %q", got)
	}
}

func TestStripTerminalControls(t *testing.T) {
	got := StripTerminalControls([]byte("\x1b[31mhello\x1b[0m\r\n"))
	if strings.TrimSpace(got) != "hello" {
		t.Fatalf("unexpected stripped output: %q", got)
	}
}
