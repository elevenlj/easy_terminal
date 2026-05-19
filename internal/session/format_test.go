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

func TestPickNotifyContentDiffsAfterPreviousTailAnchor(t *testing.T) {
	previous := strings.Join([]string{
		"old heading",
		"old line 1",
		"old line 2",
		"old line 3",
	}, "\n")
	visible := previous + "\n" + strings.Join([]string{
		"new line 1",
		"",
		"new line 2",
	}, "\n")
	got := PickNotifyContent(visible, previous, nil, "next")
	want := "new line 1\n\nnew line 2"
	if got != want {
		t.Fatalf("content should be diffed after previous tail anchor:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentUsesInputAnchorBeforeSnapshotDiff(t *testing.T) {
	previous := strings.Join([]string{
		"› 上海呢",
		"• 上海现在多云。",
	}, "\n")
	visible := strings.Join([]string{
		"› 上海呢",
		"• 上海现在多云，晚间有小雨。",
		"› 深圳呢",
		"• 深圳现在阴天。",
	}, "\n")
	got := PickNotifyContent(visible, previous, nil, "深圳呢")
	want := "• 深圳现在阴天。"
	if got != want {
		t.Fatalf("input anchor should prevent previous rounds leaking into notification:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentUsesLatestRepeatedInputAnchor(t *testing.T) {
	visible := strings.Join([]string{
		"› 北京呢",
		"• 北京旧结果。",
		"› 北京呢",
		"• 北京新结果。",
	}, "\n")
	got := PickNotifyContent(visible, "", nil, "北京呢")
	want := "• 北京新结果。"
	if got != want {
		t.Fatalf("latest repeated input should be used as anchor:\n%q\nwant:\n%q", got, want)
	}
}

func TestNotifyContentNeedsMoreSnapshotUsesLatestRepeatedInputInWindow(t *testing.T) {
	visible := strings.Join([]string{
		"> ask",
		"old answer",
		"> ask",
	}, "\n")
	if !notifyContentNeedsMoreSnapshotWithWindow(visible, "", nil, "ask", "ask") {
		t.Fatalf("window should wait when the latest repeated input has no reply")
	}

	visible += "\nnew answer"
	if notifyContentNeedsMoreSnapshotWithWindow(visible, "", nil, "ask", "ask") {
		t.Fatalf("window should be ready once the latest repeated input has a reply")
	}
}

func TestPickNotifyContentSkipsMultilineInputAnchor(t *testing.T) {
	input := "第一行\n第二行\n第三行"
	visible := strings.Join([]string{
		"› 第一行",
		"第二行",
		"第三行",
		"• 多行输入后的回复。",
	}, "\n")
	got := PickNotifyContent(visible, "", nil, input)
	want := "• 多行输入后的回复。"
	if got != want {
		t.Fatalf("multiline input anchor should be skipped from notification:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentDiffsInsertedMiddleBeforeStableFooter(t *testing.T) {
	previous := strings.Join([]string{
		"old output",
		"gpt-5.4 low fast · ~/Easy_Terminal_Workspace/测试",
	}, "\n")
	visible := strings.Join([]string{
		"old output",
		"• Ran lsof -nP -iTCP:8083 -sTCP:LISTEN",
		"  (no output)",
		"已关闭 8083 接口。",
		"gpt-5.4 low fast · ~/Easy_Terminal_Workspace/测试",
	}, "\n")
	got := PickNotifyContent(visible, previous, nil, "关闭 8083")
	want := strings.Join([]string{
		"• Ran lsof -nP -iTCP:8083 -sTCP:LISTEN",
		"  (no output)",
		"已关闭 8083 接口。",
	}, "\n")
	if got != want {
		t.Fatalf("middle insertion before stable footer should be diffed:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentUsesLastRepeatedPreviousTailAnchor(t *testing.T) {
	previous := strings.Join([]string{
		"anchor one",
		"anchor two",
		"anchor three",
	}, "\n")
	visible := strings.Join([]string{
		"anchor one",
		"anchor two",
		"anchor three",
		"old duplicate content",
		"anchor one",
		"anchor two",
		"anchor three",
		"new content only",
	}, "\n")
	got := PickNotifyContent(visible, previous, nil, "next")
	if got != "new content only" {
		t.Fatalf("last repeated anchor should be used, got %q", got)
	}
}

func TestNotifyContentNeedsMoreSnapshotWhenPreviousTailAnchorHasNoNewContent(t *testing.T) {
	previous := strings.Join([]string{
		"anchor one",
		"anchor two",
		"anchor three",
	}, "\n")
	if !NotifyContentNeedsMoreSnapshot(previous, previous, nil, "next") {
		t.Fatalf("matching previous tail anchor with no new content should wait")
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

func TestPickNotifyContentAppliesDropLinePatterns(t *testing.T) {
	if err := SetLarkNotifyDropLinePatterns([]string{`^noise:`, `secret`}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := SetLarkNotifyDropLinePatterns(nil); err != nil {
			t.Fatal(err)
		}
	})

	got := PickNotifyContent(strings.Join([]string{
		"keep first",
		"noise: drop this",
		"keep second",
		"contains secret token",
	}, "\n"), "", nil, "")
	want := "keep first\nkeep second"
	if got != want {
		t.Fatalf("drop line patterns were not applied:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentAppliesBlockHeadDropRule(t *testing.T) {
	if err := SetLarkNotifyDropLineRules([]LarkNotifyDropLineRule{
		{Kind: "block_head", Pattern: `^• 已完成发布`, Action: "drop_block"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := SetLarkNotifyDropLineRules(nil); err != nil {
			t.Fatal(err)
		}
	})

	got := PickNotifyContent(strings.Join([]string{
		"• 已完成发布。",
		"  发布结果：",
		"  - GitHub Release 已生成",
		"下一段保留",
	}, "\n"), "", nil, "")
	if got != "下一段保留" {
		t.Fatalf("block should be dropped:\n%q", got)
	}
}

func TestPickNotifyContentAppliesBlockHeadKeepRule(t *testing.T) {
	if err := SetLarkNotifyDropLineRules([]LarkNotifyDropLineRule{
		{Kind: "block_head", Pattern: `^• 已完成发布`, Action: "keep_head"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := SetLarkNotifyDropLineRules(nil); err != nil {
			t.Fatal(err)
		}
	})

	got := PickNotifyContent(strings.Join([]string{
		"• 已完成发布。",
		"  发布结果：",
		"  - GitHub Release 已生成",
		"下一段保留",
	}, "\n"), "", nil, "")
	want := "• 已完成发布。\n下一段保留"
	if got != want {
		t.Fatalf("block body should be dropped:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentAppliesLineGroupRule(t *testing.T) {
	if err := SetLarkNotifyDropLineRules([]LarkNotifyDropLineRule{
		{Kind: "line_group", Pattern: `(token=)([^ ]+)`, Groups: []int{2}},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := SetLarkNotifyDropLineRules(nil); err != nil {
			t.Fatal(err)
		}
	})

	got := PickNotifyContent("deploy token=abc123 done", "", nil, "")
	want := "deploy token= done"
	if got != want {
		t.Fatalf("capture group should be removed:\n%q\nwant:\n%q", got, want)
	}
}

func TestSetLarkNotifyDropLineRulesRejectsMissingGroup(t *testing.T) {
	err := SetLarkNotifyDropLineRules([]LarkNotifyDropLineRule{
		{Kind: "line_group", Pattern: `(token=)([^ ]+)`, Groups: []int{3}},
	})
	if err == nil {
		t.Fatal("expected missing capture group to be rejected")
	}
}

func TestLarkTerminalPlainTextMergesWrappedLinesWhenEnabled(t *testing.T) {
	SetLarkNotifyMergeWrappedLines(true)
	t.Cleanup(func() { SetLarkNotifyMergeWrappedLines(false) })

	got := larkTerminalPlainText(strings.Join([]string{
		"这是一个因为终端宽度被折断的长句",
		"下一段仍然属于同一句话。",
		"新的一句保留换行",
		"",
		"1. 列表保留换行",
	}, "\n"))
	want := strings.Join([]string{
		"这是一个因为终端宽度被折断的长句下一段仍然属于同一句话。",
		"新的一句保留换行",
		"",
		"1. 列表保留换行",
	}, "\n")
	if got != want {
		t.Fatalf("wrapped lines were not merged as expected:\n%q\nwant:\n%q", got, want)
	}
}

func TestLarkTerminalPlainTextKeepsWrappedLinesByDefault(t *testing.T) {
	SetLarkNotifyMergeWrappedLines(false)
	got := larkTerminalPlainText("第一行\n第二行")
	if got != "第一行\n第二行" {
		t.Fatalf("wrapped line merge should be disabled by default: %q", got)
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
