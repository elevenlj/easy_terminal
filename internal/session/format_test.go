package session

import (
	"strings"
	"testing"
)

func TestPickNotifyContentPrefersVisibleSnapshot(t *testing.T) {
	got := PickNotifyContent("cmd\nrendered output", "", []byte("raw"), "cmd")
	if got != "cmd\nrendered output" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestPickNotifyContentUsesOnlyCurrentRoundFromVisibleSnapshot(t *testing.T) {
	before := strings.Join([]string{
		"> 今天天气怎么样",
		"• 你想查哪个城市的天气？",
		"> 成都",
		"• 成都今天预计晴转多云。",
	}, "\n")
	after := before + "\n> 好的谢谢\n• 不客气。"
	got := PickNotifyContent(after, before, []byte("fallback history"), "好的谢谢")
	want := "> 好的谢谢\n• 不客气。"
	if got != want {
		t.Fatalf("unexpected current round content:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "天气") || strings.Contains(got, "成都") {
		t.Fatalf("previous round leaked into notification: %q", got)
	}
}

func TestPickNotifyContentAnchorsOnLastInputWhenSnapshotPrefixChanged(t *testing.T) {
	before := strings.Join([]string{
		"> 今天天气怎么样",
		"• 你想查哪个城市的天气？",
		"> 成都",
		"• Searching the web",
		"• 成都今天预计晴转多云。",
	}, "\n")
	visible := strings.Join([]string{
		"OpenAI Codex (v0.128.0)",
		"Tip: Try the Codex App.",
		before,
		"> Run /review on my current changes",
		"gpt-5.5 medium · ~",
	}, "\n")
	got := PickNotifyContent(visible, "stale snapshot that no longer matches", []byte("round fallback"), "Run /review on my current changes")
	want := "> Run /review on my current changes"
	if got != want {
		t.Fatalf("unexpected anchored content:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "天气") || strings.Contains(got, "成都") || strings.Contains(got, "Codex (v") {
		t.Fatalf("history leaked into anchored notification: %q", got)
	}
}

func TestPickNotifyContentDoesNotUseRoundReplyWhenVisibleSnapshotExists(t *testing.T) {
	got := PickNotifyContent("old visible history", "stale snapshot", []byte("current answer only"), "missing input")
	if got != "missing input" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestPickNotifyContentDoesNotUseRoundReplyWithoutVisibleSnapshot(t *testing.T) {
	got := PickNotifyContent("", "", []byte("current answer only"), "missing input")
	if got != "missing input" {
		t.Fatalf("unexpected no-browser content: %q", got)
	}
}

func TestPickNotifyContentSuppressesDirtyRoundReplyWhenVisibleSnapshotExists(t *testing.T) {
	dirty := []byte("[O\r\n•RneReececctcotionin2nnngneg.ec..ct..ti.")
	got := PickNotifyContent("OpenAI Codex old screen", "stale snapshot", dirty, "Implement {feature}")
	if got != "Implement {feature}" {
		t.Fatalf("dirty round reply should not be used when visible snapshot exists: %q", got)
	}
}

func TestPickNotifyContentDropsTransientWorkingLines(t *testing.T) {
	visible := strings.Join([]string{
		"> 今天天气怎么样",
		"• Working (2s • esc to interrupt)",
		"⚠️ Falling back from WebSockets to HTTPS transport.",
		"• 你想查哪个城市的天气？",
		"比如：上海、北京、纽约。",
	}, "\n")
	got := PickNotifyContent(visible, "", nil, "今天天气怎么样")
	if strings.Contains(got, "Working") || strings.Contains(got, "WebSockets") {
		t.Fatalf("transient status leaked: %q", got)
	}
	want := "> 今天天气怎么样\n• 你想查哪个城市的天气？\n比如：上海、北京、纽约。"
	if got != want {
		t.Fatalf("unexpected cleaned content:\n%q\nwant:\n%q", got, want)
	}
}

func TestNotifyContentNeedsMoreSnapshotForTransientOnlyRound(t *testing.T) {
	visible := strings.Join([]string{
		"> 今天天气怎么样",
		"• Working (2s • esc to interrupt)",
		"> Find and fix a bug in @filename",
		"gpt-5.5 medium · ~",
	}, "\n")
	if !NotifyContentNeedsMoreSnapshot(visible, "", "今天天气怎么样") {
		t.Fatalf("transient-only content should wait for a newer frontend snapshot")
	}
	complete := visible + "\n• 你想查哪个城市的天气？\n比如：上海、北京、纽约。"
	if NotifyContentNeedsMoreSnapshot(complete, "", "今天天气怎么样") {
		t.Fatalf("completed content should be ready to notify")
	}
}

func TestNotifyContentNeedsConservativeDelayForCodexPrompt(t *testing.T) {
	visible := strings.Join([]string{
		"> 今天天气怎么样",
		"• 你想查哪个城市的天气？例如：上海、北京。",
		"> Use /skills to list available skills",
		"gpt-5.5 medium · ~",
	}, "\n")
	if !NotifyContentNeedsConservativeDelay(visible, "", "今天天气怎么样") {
		t.Fatalf("codex prompt should use configured conservative notify delay")
	}
}

func TestNotifyContentCanUseFastDelayForPlainOutput(t *testing.T) {
	visible := strings.Join([]string{
		"$ echo hello",
		"hello",
		"$",
	}, "\n")
	if NotifyContentNeedsConservativeDelay(visible, "", "echo hello") {
		t.Fatalf("plain completed output should use fast notify delay")
	}
}

func TestPickNotifyContentDropsPromptStatusAndCodexSuggestions(t *testing.T) {
	visible := strings.Join([]string{
		"> 今天天气怎么样",
		"• 你想查哪个城市的天气？",
		"比如：上海、北京、纽约。",
		"> Find and fix a bug in @filename",
		"gpt-5.5 medium · ~",
	}, "\n")
	got := PickNotifyContent(visible, "", nil, "今天天气怎么样")
	want := "> 今天天气怎么样\n• 你想查哪个城市的天气？\n比如：上海、北京、纽约。"
	if got != want {
		t.Fatalf("unexpected content:\n%q\nwant:\n%q", got, want)
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
	got := PickNotifyContent(visible, "", nil, "")
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
	got := PickNotifyContent("contact me@example.com", "", nil, "")
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
