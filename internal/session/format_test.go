package session

import (
	"strconv"
	"strings"
	"testing"
)

func TestPickNotifyContentPrefersVisibleSnapshot(t *testing.T) {
	got := PickNotifyContent("cmd\nrendered output", "", []byte("raw"), "")
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

func TestPickNotifyContentAnchorsInsideCodexScrollbackWhenReplyIsReady(t *testing.T) {
	visible := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.128.0) │",
		"│ model: gpt-5.5 medium      │",
		"│ directory: ~               │",
		"╰────────────────────────────╯",
		"Tip: New Use /fast to enable our fastest inference.",
		"> 今天天气怎么样",
		"• 你想查哪个城市的天气？给我城市名就行，比如“上海”或“纽约”。",
		"> 成都",
		"• Searching the web",
		"• Searched weather: China, Sichuan, Chengdu",
		"• 成都今天（5月4日）天气晴朗，目前约 28°C。",
		"> Use /skills to list available skills",
	}, "\n")
	got := PickNotifyContent(visible, "stale prefix", nil, "成都")
	want := strings.Join([]string{
		"> 成都",
		"• Searching the web",
		"• Searched weather: China, Sichuan, Chengdu",
		"• 成都今天（5月4日）天气晴朗，目前约 28°C。",
		"> Use /skills to list available skills",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected anchored content:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(got, "今天天气怎么样") || strings.Contains(got, "OpenAI Codex") {
		t.Fatalf("previous round leaked into notification: %q", got)
	}
}

func TestPickNotifyContentKeepsFullCodexTUIScreen(t *testing.T) {
	visible := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.128.0) │",
		"│ model: gpt-5.5 medium      │",
		"│ directory: ~               │",
		"╰────────────────────────────╯",
		"Tip: New Use /fast to enable our fastest inference.",
		"> Implement {feature}",
		"gpt-5.5 medium · ~",
	}, "\n")
	got := PickNotifyContent(visible, "stale prefix with codex", nil, "Implement {feature}")
	if got != "> Implement {feature}" {
		t.Fatalf("input anchor should win over full-screen TUI fallback, got %q", got)
	}
}

func TestPickNotifyContentKeepsCodexTUIScreenAfterCodexShellLaunch(t *testing.T) {
	visible := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.128.0) │",
		"│ model: gpt-5.5 medium      │",
		"│ directory: ~               │",
		"╰────────────────────────────╯",
		"Tip: Try the Codex App.",
		"› Run /review on my current changes",
		"gpt-5.5 medium · ~",
	}, "\n")
	if NotifyContentNeedsMoreSnapshot(visible, "", nil, "codex") {
		t.Fatalf("codex launch TUI should be ready to notify")
	}
	got := PickNotifyContent(visible, "", nil, "codex")
	if !strings.Contains(got, "OpenAI Codex") || !strings.Contains(got, "model: gpt-5.5 medium") || strings.Contains(got, "Run /review") {
		t.Fatalf("unexpected codex launch TUI notification: %q", got)
	}
}

func TestPickNotifyContentUsesVisibleSnapshotWhenInputAnchorMissing(t *testing.T) {
	visible := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.128.0) │",
		"│ model: gpt-5.5 medium      │",
		"│ directory: ~               │",
		"╰────────────────────────────╯",
		"> previous",
		"• old answer",
	}, "\n")
	got := PickNotifyContent(visible, "", nil, "missing long wrapped input")
	if got != visible {
		t.Fatalf("missing input anchor should fall back to visible snapshot:\n%q\nwant:\n%q", got, visible)
	}
}

func TestPickNotifyContentAnchorsOnWrappedCodexInput(t *testing.T) {
	visible := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.128.0) │",
		"│ model: gpt-5.5 medium      │",
		"│ directory: ~               │",
		"╰────────────────────────────╯",
		"> 之前的问题",
		"• 旧回复不应该出现。",
		"› 它应该有默认音色吧？无论哪一种 TTS，不一定都需要上传。参考音频吧。现在给我生成一",
		"段测试",
		"• Explored",
		"  └ Read model_worker.py",
		"• 这是当前轮回复。",
	}, "\n")
	input := "它应该有默认音色吧？无论哪一种 TTS，不一定都需要上传。参考音频吧。现在给我生成一段测试"
	got := PickNotifyContent(visible, "", nil, input)
	if strings.Contains(got, "旧回复") || strings.Contains(got, "OpenAI Codex") {
		t.Fatalf("wrapped input anchor did not trim previous codex history: %q", got)
	}
	if !strings.Contains(got, "› 它应该有默认音色吧") || !strings.Contains(got, "段测试") || !strings.Contains(got, "这是当前轮回复") {
		t.Fatalf("wrapped input anchored content missing expected current round: %q", got)
	}
}

func TestPickNotifyContentKeepsCodexTrustScreenAfterSingleCharInput(t *testing.T) {
	visible := strings.Join([]string{
		">_ You are in /Users/eleven/project/temp",
		"Do you trust the contents of this directory?",
		"Working with untrusted contents comes with higher risk of prompt injection.",
		"› 1. Yes, continue",
		"  2. No, quit",
		"Press enter to continue",
	}, "\n")
	got := PickNotifyContent(visible, "Continue anyway? [y/N]:", nil, "y")
	if !strings.Contains(got, "Do you trust the contents of this directory?") || !strings.Contains(got, "1. Yes, continue") {
		t.Fatalf("trust screen should be preserved after y input, got %q", got)
	}
}

func TestPickNotifyContentDoesNotAnchorSingleCharInputInsideWords(t *testing.T) {
	visible := strings.Join([]string{
		"directory:",
		"› 1. Yes, continue",
		"Press enter to continue",
	}, "\n")
	got := visibleTextFromLastInput(visible, "y")
	if got != "" {
		t.Fatalf("single-char input should not anchor inside unrelated words, got %q", got)
	}
}

func TestPickNotifyContentAnchorsOnShellPromptSuffix(t *testing.T) {
	visible := strings.Join([]string{
		"eleven@elevendeMacBook-Pro ~ % pwd",
		"/Users/eleven",
		"eleven@elevendeMacBook-Pro ~ %",
	}, "\n")
	got := PickNotifyContent(visible, "", nil, "pwd")
	want := strings.Join([]string{
		"eleven@elevendeMacBook-Pro ~ % pwd",
		"/Users/eleven",
		"eleven@elevendeMacBook-Pro ~ %",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected shell anchored content:\n%q\nwant:\n%q", got, want)
	}
}

func TestPickNotifyContentAnchorsOnBrowserShellPromptSuffix(t *testing.T) {
	visible := strings.Join([]string{
		"eleven ~ > pwd",
		"/Users/eleven",
		"eleven ~ >",
	}, "\n")
	got := PickNotifyContent(visible, "", nil, "pwd")
	want := strings.Join([]string{
		"eleven ~ > pwd",
		"/Users/eleven",
		"eleven ~ >",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected browser shell anchored content:\n%q\nwant:\n%q", got, want)
	}
}

func TestNotifyContentReadyForNoOutputShellCommand(t *testing.T) {
	visible := strings.Join([]string{
		"eleven ~ > cd develop/model",
		"eleven ~/develop/model >",
	}, "\n")
	if NotifyContentNeedsMoreSnapshot(visible, "", nil, "cd develop/model") {
		t.Fatalf("shell command with only a completed prompt should be ready to notify")
	}
	got := PickNotifyContent(visible, "", nil, "cd develop/model")
	if got != visible {
		t.Fatalf("unexpected prompt-only shell content:\n%q\nwant:\n%q", got, visible)
	}
}

func TestNotifyContentWaitsForNoOutputShellCommandBeforePromptReturns(t *testing.T) {
	visible := "eleven ~ > cd develop/model"
	if !NotifyContentNeedsMoreSnapshot(visible, "", nil, "cd develop/model") {
		t.Fatalf("shell command should wait until the prompt returns")
	}
}

func TestPickNotifyContentFallsBackToBrowserShellPromptWhenInputRecordIsDirty(t *testing.T) {
	visible := strings.Join([]string{
		"eleven ~ > pwd",
		"/Users/eleven",
		"eleven ~ >",
	}, "\n")
	if NotifyContentNeedsMoreSnapshot(visible, "", nil, "p w dpwdpwd") {
		t.Fatalf("dirty recorded input should not block a ready shell snapshot")
	}
	got := PickNotifyContent(visible, "", nil, "p w dpwdpwd")
	want := strings.Join([]string{
		"eleven ~ > pwd",
		"/Users/eleven",
		"eleven ~ >",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected dirty-input fallback content:\n%q\nwant:\n%q", got, want)
	}
}

func TestNotifyContentStillWaitsWhenDirtyInputFallbackHasNoOutput(t *testing.T) {
	visible := strings.Join([]string{
		"eleven ~ > pwd",
		"eleven ~ >",
	}, "\n")
	if !NotifyContentNeedsMoreSnapshot(visible, "", nil, "p w dpwdpwd") {
		t.Fatalf("dirty recorded input fallback should still wait when the shell command has no output")
	}
}

func TestPickNotifyContentDoesNotTreatPlainOutputAsInputAnchor(t *testing.T) {
	visible := strings.Join([]string{
		"> first",
		"repeat",
		"• repeat",
	}, "\n")
	got := visibleTextFromLastInput(visible, "repeat")
	if got != "" {
		t.Fatalf("plain output should not become an input anchor: %q", got)
	}
}

func TestPickNotifyContentDoesNotUseRoundReplyWhenVisibleSnapshotExists(t *testing.T) {
	got := PickNotifyContent("old visible history", "stale snapshot", []byte("current answer only"), "missing input")
	if got != "old visible history" {
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
	if got != "OpenAI Codex old screen" {
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
	if !NotifyContentNeedsMoreSnapshot(visible, "", nil, "今天天气怎么样") {
		t.Fatalf("transient-only content should wait for a newer frontend snapshot")
	}
	complete := visible + "\n• 你想查哪个城市的天气？\n比如：上海、北京、纽约。"
	if NotifyContentNeedsMoreSnapshot(complete, "", nil, "今天天气怎么样") {
		t.Fatalf("completed content should be ready to notify")
	}
}

func TestNotifyContentUsesVisibleSnapshotWhenSnapshotAnchorIsMissing(t *testing.T) {
	visible := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.128.0) │",
		"│ model: gpt-5.5 medium      │",
		"│ directory: ~               │",
		"╰────────────────────────────╯",
		"> old",
		"• old answer",
	}, "\n")
	round := []byte("> wrapped long input\n• current answer")
	if NotifyContentNeedsMoreSnapshot(visible, "", round, "wrapped long input") {
		t.Fatalf("visible snapshot should be enough when snapshot cannot anchor the input")
	}
	got := PickNotifyContent(visible, "", round, "wrapped long input")
	if got != visible {
		t.Fatalf("unexpected visible snapshot fallback:\n%q\nwant:\n%q", got, visible)
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

func TestPickNotifyContentRemovesExtraBlankLinesButKeepsHorizontalRules(t *testing.T) {
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
	want := strings.Join([]string{
		"Searching the web",
		"Searched weather: Chengdu, Sichuan, China",
		"────────────────────────────────────────",
		"________________________",
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

func TestTruncateForLarkKeepsTailForLongText(t *testing.T) {
	SetLarkNotifyMaxLines(defaultMaxLarkTextLines)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	lines := make([]string, 0, defaultMaxLarkTextLines+20)
	for i := 0; i < defaultMaxLarkTextLines+20; i++ {
		lines = append(lines, "line-"+strconv.Itoa(i))
	}
	got := truncateForLark(strings.Join(lines, "\n"))
	if !strings.HasPrefix(got, larkTruncatedPrefix) {
		t.Fatalf("expected truncated prefix, got %q", got)
	}
	if strings.Contains(got, "line-0\n") {
		t.Fatalf("expected head lines to be dropped")
	}
	if !strings.Contains(got, "line-319") {
		t.Fatalf("expected tail line to be kept")
	}
}

func TestTruncateForLarkUsesConfiguredMaxLines(t *testing.T) {
	SetLarkNotifyMaxLines(3)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	got := truncateForLark("one\ntwo\nthree\nfour\nfive")
	want := "[truncated]\nthree\nfour\nfive"
	if got != want {
		t.Fatalf("truncateForLark() = %q, want %q", got, want)
	}
}

func TestTruncateForLarkKeepsTailForLongRunes(t *testing.T) {
	SetLarkNotifyMaxLines(defaultMaxLarkTextLines)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	got := truncateForLark("开头不能保留" + strings.Repeat("头", maxLarkTextRunes) + "最后这一段必须保留")
	if !strings.HasPrefix(got, larkTruncatedPrefix) {
		t.Fatalf("expected truncated prefix")
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
