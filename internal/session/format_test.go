package session

import (
	"strconv"
	"strings"
	"testing"
)

func TestPickNotifyContentUsesVisibleSnapshot(t *testing.T) {
	got := PickNotifyContent("cmd\nrendered output", "", []byte("raw"), "")
	if got != "cmd\nrendered output" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestPickNotifyContentIgnoresRoundStartAndUsesVisibleTail(t *testing.T) {
	SetLarkNotifyMaxLines(4)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	before := strings.Join([]string{
		"> old question",
		"old answer",
	}, "\n")
	visible := before + "\n" + strings.Join([]string{
		"> current question",
		"current answer",
		"1. first",
		"2. second",
	}, "\n")
	got := PickNotifyContent(visible, before, []byte("current answer1.first2.second"), "current question")
	want := strings.Join([]string{
		"> current question",
		"current answer",
		"1. first",
		"2. second",
	}, "\n")
	if got != want {
		t.Fatalf("visible tail should be used:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentFallsBackToVisibleTailWhenDiffCannotMatch(t *testing.T) {
	SetLarkNotifyMaxLines(3)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	visible := strings.Join([]string{
		"header",
		"old answer",
		"current answer",
		"1. first",
		"2. second",
	}, "\n")
	got := PickNotifyContent(visible, "stale snapshot", []byte("current answer1.first2.second"), "current question")
	want := strings.Join([]string{
		"current answer",
		"1. first",
		"2. second",
	}, "\n")
	if got != want {
		t.Fatalf("fallback should use visible tail:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentDoesNotUseRoundReplyWithoutVisibleSnapshot(t *testing.T) {
	got := PickNotifyContent("", "", []byte("current answer only"), "missing input")
	if got != "" {
		t.Fatalf("raw round reply should not be used without a visible snapshot: %q", got)
	}
	if !NotifyContentNeedsMoreSnapshot("", "", []byte("current answer only"), "missing input") {
		t.Fatalf("missing visible snapshot should wait")
	}
}

func TestPickNotifyContentKeepsGenericVisibleListFormatting(t *testing.T) {
	SetLarkNotifyMaxLines(4)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	before := "menu command"
	visible := strings.Join([]string{
		"menu command",
		"Available options:",
		"1. Create session",
		"2. Attach session",
		"3. Quit",
	}, "\n")
	round := []byte("Available options:1.Create session2.Attach session3.Quit")
	got := PickNotifyContent(visible, before, round, "menu command")
	want := strings.Join([]string{
		"Available options:",
		"1. Create session",
		"2. Attach session",
		"3. Quit",
	}, "\n")
	if got != want {
		t.Fatalf("generic list should keep visible terminal formatting:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentKeepsCodexModelMenusAsVisibleText(t *testing.T) {
	SetLarkNotifyMaxLines(5)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	before := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.130.0) │",
		"│ model: gpt-5.5 medium fast │",
		"│ directory: ~/project       │",
		"╰────────────────────────────╯",
	}, "\n")
	modelVisible := before + "\n" + strings.Join([]string{
		"Select Model and Effort",
		"Access legacy models by running codex -m <model_name> or in your config.toml",
		"› 1. gpt-5.5 (current)   Frontier model for complex coding, research, and real-world work.",
		"  2. gpt-5.4             Strong model for everyday coding.",
		"Press enter to confirm or esc to go back",
	}, "\n")
	got := PickNotifyContent(modelVisible, before, []byte("Select Model and Effort1.gpt-5.52.gpt-5.4"), "/model")
	want := strings.Join([]string{
		"Select Model and Effort",
		"Access legacy models by running codex -m <model_name> or in your config.toml",
		"› 1. gpt-5.5 (current)   Frontier model for complex coding, research, and real-world work.",
		"  2. gpt-5.4             Strong model for everyday coding.",
		"Press enter to confirm or esc to go back",
	}, "\n")
	if got != want {
		t.Fatalf("model menu should come from visible text:\n%q\nwant:\n%q", got, want)
	}

	SetLarkNotifyMaxLines(6)
	reasoningVisible := before + "\n" + strings.Join([]string{
		"Select Reasoning Level for gpt-5.5",
		"1. Low                  Fast responses with lighter reasoning",
		"2. Medium (default)     Balances speed and reasoning depth for everyday tasks",
		"3. High                 Greater reasoning depth for complex problems",
		"› 4. Extra high (current)  Extra high reasoning depth for complex problems",
		"Press enter to confirm or esc to go back",
	}, "\n")
	got = PickNotifyContent(reasoningVisible, before, []byte("1Select Reasoning Level1.Low2.Medium3.High"), "1")
	want = strings.Join([]string{
		"Select Reasoning Level for gpt-5.5",
		"1. Low                  Fast responses with lighter reasoning",
		"2. Medium (default)     Balances speed and reasoning depth for everyday tasks",
		"3. High                 Greater reasoning depth for complex problems",
		"› 4. Extra high (current)  Extra high reasoning depth for complex problems",
		"Press enter to confirm or esc to go back",
	}, "\n")
	if got != want {
		t.Fatalf("reasoning menu should come from visible text:\n%q\nwant:\n%q", got, want)
	}
}

func TestNotifyContentNeedsMoreSnapshotForInputOnlyOrTransientOnly(t *testing.T) {
	if !NotifyContentNeedsMoreSnapshot("> current question", "", nil, "current question") {
		t.Fatalf("input-only visible text should wait")
	}
	visible := strings.Join([]string{
		"> current question",
		"• Working (2s • esc to interrupt)",
		"gpt-5.5 medium · ~",
	}, "\n")
	if !NotifyContentNeedsMoreSnapshot(visible, "", nil, "current question") {
		t.Fatalf("transient-only visible text should wait")
	}
	complete := visible + "\nanswer"
	if NotifyContentNeedsMoreSnapshot(complete, "", nil, "current question") {
		t.Fatalf("completed visible text should be ready")
	}
}

func TestPickNotifyContentSanitizesEmail(t *testing.T) {
	got := PickNotifyContent("contact me@example.com", "", nil, "")
	if strings.Contains(got, "me@example.com") || !strings.Contains(got, "[email]") {
		t.Fatalf("email was not sanitized: %q", got)
	}
}

func TestTruncateForLarkKeepsTailLinesWithoutPrefix(t *testing.T) {
	SetLarkNotifyMaxLines(3)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	got := truncateForLark("one\ntwo\nthree\nfour\nfive")
	want := "three\nfour\nfive"
	if got != want {
		t.Fatalf("truncateForLark() = %q, want %q", got, want)
	}
}

func TestTruncateForLarkKeepsTailForLongText(t *testing.T) {
	SetLarkNotifyMaxLines(defaultMaxLarkTextLines)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	lines := make([]string, 0, defaultMaxLarkTextLines+20)
	for i := 0; i < defaultMaxLarkTextLines+20; i++ {
		lines = append(lines, "line-"+strconv.Itoa(i))
	}
	got := truncateForLark(strings.Join(lines, "\n"))
	if strings.Contains(got, "line-0\n") {
		t.Fatalf("expected head lines to be dropped")
	}
	if strings.Contains(got, larkTruncatedPrefix) {
		t.Fatalf("line truncation should not add a prefix")
	}
	if !strings.Contains(got, "line-319") {
		t.Fatalf("expected tail line to be kept")
	}
}

func TestTruncateForLarkKeepsTailForLongRunes(t *testing.T) {
	SetLarkNotifyMaxLines(defaultMaxLarkTextLines)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	got := truncateForLark("开头不能保留" + strings.Repeat("头", maxLarkTextRunes) + "最后这一段必须保留")
	if !strings.HasPrefix(got, larkTruncatedPrefix) {
		t.Fatalf("expected rune truncation prefix")
	}
	if strings.Contains(got, "开头不能保留") {
		t.Fatalf("expected original head to be dropped")
	}
	if !strings.HasSuffix(got, "最后这一段必须保留") {
		t.Fatalf("expected final content to be kept, got %q", got)
	}
	if len([]rune(got)) > maxLarkTextRunes {
		t.Fatalf("truncated text has %d runes, want <= %d", len([]rune(got)), maxLarkTextRunes)
	}
}

func TestStripTerminalControls(t *testing.T) {
	got := StripTerminalControls([]byte("\x1b[31mhello\x1b[0m\r\n"))
	if strings.TrimSpace(got) != "hello" {
		t.Fatalf("unexpected stripped output: %q", got)
	}
}
