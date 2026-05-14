package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestWaitingNotificationRequiresReplyContent(t *testing.T) {
	rt := &RuntimeSession{
		manager: NewManager(nil, nil),
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样")

	rt.mu.Lock()
	_, _, ok := rt.waitingNotificationLocked()
	rt.mu.Unlock()
	if ok {
		t.Fatalf("notification should not be ready when the current round only contains user input")
	}
}

func TestWaitingNotificationDedupesButRepushesFullRoundWhenMoreOutputArrives(t *testing.T) {
	rt := &RuntimeSession{
		manager: NewManager(nil, nil),
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 你想查哪个城市的天气？")

	rt.mu.Lock()
	first, firstHash, ok := rt.waitingNotificationLocked()
	if !ok {
		t.Fatal("expected first full-round notification to be ready")
	}
	rt.lastNotifiedRoundHash = firstHash
	_, _, duplicateOK := rt.waitingNotificationLocked()
	rt.mu.Unlock()
	if duplicateOK {
		t.Fatal("same full-round notification should be deduped")
	}
	if first.Content != "> 今天天气怎么样\n• 你想查哪个城市的天气？" {
		t.Fatalf("first content = %q", first.Content)
	}

	rt.HandleOutput([]byte("more output"))
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 你想查哪个城市的天气？\n• 成都今天晴转多云。")

	rt.mu.Lock()
	second, secondHash, ok := rt.waitingNotificationLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatal("expected updated full-round notification after more output")
	}
	if secondHash == firstHash {
		t.Fatal("updated full-round notification should have a different hash")
	}
	want := "> 今天天气怎么样\n• 你想查哪个城市的天气？\n• 成都今天晴转多云。"
	if second.Content != want {
		t.Fatalf("second content = %q, want %q", second.Content, want)
	}
}

func TestWaitingNotificationWaitsForSnapshotAfterCurrentInput(t *testing.T) {
	rt := &RuntimeSession{
		manager: NewManager(nil, nil),
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	rt.SetVisibleSnapshot("eleven ~ > ll\ntotal 8\n-rw-r--r-- file.txt\neleven ~ >")
	rt.MarkInputActivity("cdx\r")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	_, _, ok, reason := rt.waitingNotificationCandidateLocked()
	rt.mu.Unlock()
	if ok {
		t.Fatal("stale snapshot from the previous round should not be ready")
	}
	if reason != "stale_visible_snapshot" {
		t.Fatalf("reason = %q, want stale_visible_snapshot", reason)
	}

	rt.SetVisibleSnapshot("eleven ~ > ll\ntotal 8\n-rw-r--r-- file.txt\neleven ~ >")
	rt.mu.Lock()
	_, _, ok, reason = rt.waitingNotificationCandidateLocked()
	rt.mu.Unlock()
	if ok {
		t.Fatal("fresh snapshot event with unchanged previous-round content should not be ready")
	}
	if reason != "stale_visible_snapshot" {
		t.Fatalf("reason after unchanged snapshot = %q, want stale_visible_snapshot", reason)
	}

	rt.SetVisibleSnapshot("eleven ~ > cdx\nzsh: command not found: cdx\neleven ~ >")
	rt.mu.Lock()
	n, _, ok := rt.waitingNotificationLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatal("expected notification after a fresh snapshot for the current input")
	}
	want := "eleven ~ > cdx\nzsh: command not found: cdx\neleven ~ >"
	if n.Content != want {
		t.Fatalf("content = %q, want %q", n.Content, want)
	}
}

func TestWaitingNotificationWaitsWhenFreshVisibleSnapshotIsMissing(t *testing.T) {
	rt := &RuntimeSession{
		manager: NewManager(nil, nil),
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	rt.SetVisibleSnapshot("old TUI screen\nold answer")
	rt.MarkInputActivity("new hidden tui input\r")
	rt.HandleOutput([]byte("current answer from pty\n"))
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	_, _, ok, reason := rt.waitingNotificationCandidateLocked()
	rt.mu.Unlock()
	if ok {
		t.Fatalf("raw round reply should not create notification content")
	}
	if reason != "stale_visible_snapshot" {
		t.Fatalf("reason = %q, want stale_visible_snapshot", reason)
	}
}

func TestNotifyAfterStableDoesNotSendRoundReplyWhenSnapshotDoesNotShowInput(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: true},
	}
	rt.SetVisibleSnapshot("old TUI screen\nold answer")
	rt.MarkInputActivity("hidden tui input\r")
	rt.HandleOutput([]byte("current answer from pty\n"))
	rt.mu.Lock()
	version := rt.stateVersion
	rt.mu.Unlock()

	rt.notifyAfterStable(version)

	notes := notifier.notes()
	if len(notes) != 0 {
		t.Fatalf("raw round reply should not be sent as notification content, got %#v", notes)
	}
}

func TestWaitingNotificationUsesFreshVisibleListInsteadOfRawRoundReply(t *testing.T) {
	SetLarkNotifyMaxLines(4)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	rt := &RuntimeSession{
		manager: NewManager(nil, nil),
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	rt.SetVisibleSnapshot("menu command")
	rt.MarkInputActivity("menu command\r")
	rt.HandleOutput([]byte("Available options:1.Create session2.Attach session3.Quit\n"))
	rt.SetVisibleSnapshot(strings.Join([]string{
		"menu command",
		"Available options:",
		"1. Create session",
		"2. Attach session",
		"3. Quit",
	}, "\n"))
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	n, _, ok, reason := rt.waitingNotificationCandidateLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatalf("expected visible list notification, reason=%s", reason)
	}
	want := strings.Join([]string{
		"Available options:",
		"1. Create session",
		"2. Attach session",
		"3. Quit",
	}, "\n")
	if n.Content != want {
		t.Fatalf("notification should preserve visible list formatting:\n%q\nwant:\n%q", n.Content, want)
	}
}

func TestWaitingNotificationKeepsCodexModelMenusFromVisibleSnapshot(t *testing.T) {
	SetLarkNotifyMaxLines(5)
	t.Cleanup(func() { SetLarkNotifyMaxLines(defaultMaxLarkTextLines) })
	rt := &RuntimeSession{
		manager: NewManager(nil, nil),
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	start := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.130.0) │",
		"│ model: gpt-5.5 medium fast │",
		"│ directory: ~/project       │",
		"╰────────────────────────────╯",
	}, "\n")
	modelMenu := strings.Join([]string{
		start,
		"Select Model and Effort",
		"Access legacy models by running codex -m <model_name> or in your config.toml",
		"› 1. gpt-5.5 (current)   Frontier model for complex coding, research, and real-world work.",
		"  2. gpt-5.4             Strong model for everyday coding.",
		"Press enter to confirm or esc to go back",
	}, "\n")
	rt.SetVisibleSnapshot(start)
	rt.MarkInputActivity("/model\r")
	rt.HandleOutput([]byte("/model choose what model and reasoning effort to useSelect Model and EffortAccess legacy models by running codex -m <model_name> or in your config.toml› 1. gpt-5.5 (current) Frontier model2.gpt-5.4Strong model"))
	rt.SetVisibleSnapshot(modelMenu)
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	n, _, ok, reason := rt.waitingNotificationCandidateLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatalf("expected model menu notification, reason=%s", reason)
	}
	wantModel := strings.Join([]string{
		"Select Model and Effort",
		"Access legacy models by running codex -m <model_name> or in your config.toml",
		"› 1. gpt-5.5 (current)   Frontier model for complex coding, research, and real-world work.",
		"  2. gpt-5.4             Strong model for everyday coding.",
		"Press enter to confirm or esc to go back",
	}, "\n")
	if n.Content != wantModel {
		t.Fatalf("model menu should preserve visible formatting:\n%q\nwant:\n%q", n.Content, wantModel)
	}

	SetLarkNotifyMaxLines(11)
	reasoningMenu := strings.Join([]string{
		start,
		"Select Reasoning Level for gpt-5.5",
		"1. Low                  Fast responses with lighter reasoning",
		"2. Medium (default)     Balances speed and reasoning depth for everyday tasks",
		"3. High                 Greater reasoning depth for complex problems",
		"› 4. Extra high (current)  Extra high reasoning depth for complex problems",
		"Press enter to confirm or esc to go back",
	}, "\n")
	rt.MarkInputActivity("1\r")
	rt.HandleOutput([]byte("1Select Reasoning Level for gpt-5.51.LowFast responses with lighter reasoning2.Medium(default)Balances speed and reasoning depth for everyday tasks3.HighGreater reasoning depth for complex problems› 4. Extra high (current) Extra high reasoning depthPress enter to confirm or esc to go back"))
	rt.SetVisibleSnapshot(reasoningMenu)
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	n, _, ok, reason = rt.waitingNotificationCandidateLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatalf("expected reasoning menu notification, reason=%s", reason)
	}
	wantReasoning := strings.Join([]string{
		"Select Reasoning Level for gpt-5.5",
		"1. Low                  Fast responses with lighter reasoning",
		"2. Medium (default)     Balances speed and reasoning depth for everyday tasks",
		"3. High                 Greater reasoning depth for complex problems",
		"› 4. Extra high (current)  Extra high reasoning depth for complex problems",
		"Press enter to confirm or esc to go back",
	}, "\n")
	if n.Content != wantReasoning {
		t.Fatalf("reasoning menu should preserve visible formatting:\n%q\nwant:\n%q", n.Content, wantReasoning)
	}
}

func TestNotifyEndToEndRequestsFrontendSnapshotWhenNoBrowserIsOpen(t *testing.T) {
	notifier := &recordingNotifier{}
	launcher := &recordingLauncher{}
	browserRequested := make(chan struct{})
	var m *Manager
	m = NewManager(
		nil,
		launcher,
		WithNotifier(notifier),
		WithWaitingTransitionDelays(20*time.Millisecond, 20*time.Millisecond),
		WithNotificationUpdateCoalesce(0),
		WithBrowserNeeded(func(sessionID string) {
			select {
			case <-browserRequested:
			default:
				close(browserRequested)
			}
			if rt, ok := m.GetRuntime(sessionID); ok {
				rt.SetVisibleSnapshot("eleven ~ >  echo frontend-snapshot\nfrontend ok\neleven ~ >")
			}
		}),
	)
	sess, err := m.CreateSession(context.Background(), "no-browser")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.UpdateNotifyOnWaiting(context.Background(), sess.ID, true); err != nil {
		t.Fatal(err)
	}
	rt, ok := m.GetRuntime(sess.ID)
	if !ok {
		t.Fatal("runtime session missing")
	}

	if err := SubmitStructuredInput(rt, "echo frontend-snapshot"); err != nil {
		t.Fatal(err)
	}
	launcher.terminals[0].readCh <- []byte("frontend ok\r\neleven ~ > ")

	notes := waitForNotifierNotes(t, notifier, 1)
	select {
	case <-browserRequested:
	default:
		t.Fatal("frontend snapshot should be requested when no browser is open")
	}
	if notes[0].Content != "eleven ~ >  echo frontend-snapshot\nfrontend ok\neleven ~ >" {
		t.Fatalf("notification should come from frontend snapshot, got %q", notes[0].Content)
	}
}

func TestNotifyStableDelayFastForPlainOutputAndConservativeForCodex(t *testing.T) {
	m := NewManager(nil, nil, WithWaitingTransitionDelays(120*time.Millisecond, 450*time.Millisecond))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	rt.mu.Lock()
	fast := rt.notifyStableDelayLocked()
	rt.mu.Unlock()
	if fast != 120*time.Millisecond {
		t.Fatalf("plain output stable delay = %v, want %v", fast, 120*time.Millisecond)
	}

	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• Working (1s • esc to interrupt)")
	rt.mu.Lock()
	conservative := rt.notifyStableDelayLocked()
	rt.mu.Unlock()
	if conservative != 450*time.Millisecond {
		t.Fatalf("codex output stable delay = %v, want %v", conservative, 450*time.Millisecond)
	}
}

func TestNotifyAfterStableTransitionsWaitingAndSends(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	version := rt.stateVersion

	rt.notifyAfterStable(version)
	if got := rt.Snapshot().Status; got != StatusWaiting {
		t.Fatalf("stable output should transition to waiting, got %s", got)
	}
	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected stable notification, got %#v", notes)
	}
	if notes[0].Content != "$ echo hello\nhello\n$" {
		t.Fatalf("unexpected stable notification content: %q", notes[0].Content)
	}
}

func TestNotifyIfStillWaitingUpdatesSameRoundMessage(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 你想查哪个城市的天气？")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notifyIfStillWaiting(version)
	rt.HandleOutput([]byte("more output"))
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 你想查哪个城市的天气？\n• 成都今天晴转多云。")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version = rt.notifyVersion
	rt.mu.Unlock()
	rt.notifyIfStillWaiting(version)

	notes := notifier.notes()
	if len(notes) != 2 {
		t.Fatalf("expected create and update notifications, got %#v", notes)
	}
	if notes[0].MessageID != "" || notes[0].UpdateNo != 0 {
		t.Fatalf("first notification should create a new message, got %#v", notes[0])
	}
	if notes[1].MessageID != "msg-1" || notes[1].UpdateNo != 1 || notes[1].SuppressUpdateTip {
		t.Fatalf("second notification should update msg-1 with update marker 1, got %#v", notes[1])
	}
	if notes[1].Running {
		t.Fatalf("waiting notification update should clear running title state, got %#v", notes[1])
	}
	if rt.lastNotifiedMessageID != "msg-1" {
		t.Fatalf("runtime should keep updated message id, got %q", rt.lastNotifiedMessageID)
	}

	rt.MarkInputActivity("echo next\r")
	rt.SetVisibleSnapshot("$ echo next\nnext\n$")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version = rt.notifyVersion
	rt.mu.Unlock()
	rt.notifyIfStillWaiting(version)
	notes = notifier.notes()
	if len(notes) != 3 {
		t.Fatalf("expected next round notification, got %#v", notes)
	}
	if notes[2].MessageID != "" || notes[2].UpdateNo != 0 {
		t.Fatalf("new round should create a new message, got %#v", notes[2])
	}
}

func TestNotifyPreservesCreatedMessageIDWhenSameRoundAdvancesDuringSend(t *testing.T) {
	notifier := &advancingNotifier{recordingNotifier: recordingNotifier{messageID: "msg-1"}}
	m := NewManager(nil, nil, WithNotifier(notifier), WithWaitingTransitionDelays(time.Hour, time.Hour), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	notifier.afterNotify = func() {
		rt.HandleOutput([]byte("more output"))
		rt.SetVisibleSnapshot("> echo hello\npartial\ncomplete")
		rt.mu.Lock()
		rt.session.Status = StatusWaiting
		rt.mu.Unlock()
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("> echo hello\npartial")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notifyIfStillWaiting(version)
	rt.mu.Lock()
	preservedMessageID := rt.lastNotifiedMessageID
	nextVersion := rt.notifyVersion
	rt.mu.Unlock()
	if preservedMessageID != "msg-1" {
		t.Fatalf("same-round create should preserve message id after version advance, got %q", preservedMessageID)
	}

	rt.notifyIfStillWaiting(nextVersion)
	notes := notifier.notes()
	if len(notes) != 2 {
		t.Fatalf("expected create then update, got %#v", notes)
	}
	if notes[0].MessageID != "" {
		t.Fatalf("first notification should create, got %#v", notes[0])
	}
	if notes[1].MessageID != "msg-1" || notes[1].UpdateNo != 1 || notes[1].SuppressUpdateTip {
		t.Fatalf("second same-round notification should update msg-1 with update marker 1, got %#v", notes[1])
	}
}

func TestNotifyIfStillWaitingIncrementsExistingUpdateNumber(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:                 m,
		session:                 Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID:   "msg-1",
		notificationUpdateNo:    2,
		lastInputText:           "echo hello",
		snapshotAtRoundStart:    "$ echo hello",
		snapshotAtRoundVersion:  1,
		snapshotAtRoundStartSet: true,
		roundReply:              []byte("old\nnew"),
		visibleSnapshot:         "$ echo hello\nold\nnew\n$",
		visibleSnapshotVersion:  2,
	}
	rt.mu.Lock()
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notifyIfStillWaiting(version)

	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected one update notification, got %#v", notes)
	}
	if notes[0].MessageID != "msg-1" || notes[0].UpdateNo != 3 || notes[0].SuppressUpdateTip {
		t.Fatalf("automatic update should increment update marker and allow update tip, got %#v", notes[0])
	}
	if rt.notificationUpdateNo != 3 {
		t.Fatalf("runtime update marker should increment, got %d", rt.notificationUpdateNo)
	}
}

func TestWaitingNotificationUsesRoundStartSnapshotBeforeLastNotificationSnapshot(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:                     m,
		session:                     Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastInputText:               "关闭 8083",
		lastNotifiedVisibleSnapshot: "stale previous round\nold footer",
		snapshotAtRoundStart: strings.Join([]string{
			"old output",
			"gpt-5.4 low fast · ~/Easy Terminal Workspace/测试",
		}, "\n"),
		snapshotAtRoundStartSet: true,
		visibleSnapshot: strings.Join([]string{
			"old output",
			"• Ran lsof -nP -iTCP:8083 -sTCP:LISTEN",
			"  (no output)",
			"已关闭 8083 接口。",
			"gpt-5.4 low fast · ~/Easy Terminal Workspace/测试",
		}, "\n"),
		visibleSnapshotVersion: 2,
		snapshotAtRoundVersion: 1,
		notifyVersion:          7,
	}

	rt.notifyIfStillWaiting(7)

	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected one notification, got %#v", notes)
	}
	want := strings.Join([]string{
		"• Ran lsof -nP -iTCP:8083 -sTCP:LISTEN",
		"  (no output)",
		"已关闭 8083 接口。",
	}, "\n")
	if notes[0].Content != want {
		t.Fatalf("notification should diff from round start snapshot:\n%q\nwant:\n%q", notes[0].Content, want)
	}
}

func TestWaitingNotificationTreatsEmptyRoundStartAsCurrentRoundBaseline(t *testing.T) {
	rt := &RuntimeSession{
		manager:                     NewManager(nil, nil),
		session:                     Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastInputText:               "ask",
		lastNotifiedVisibleSnapshot: "> ask\nfirst",
		snapshotAtRoundStart:        "",
		snapshotAtRoundVersion:      0,
		snapshotAtRoundStartSet:     true,
		visibleSnapshot:             "> ask\nfirst\nsecond",
		visibleSnapshotVersion:      1,
	}

	rt.mu.Lock()
	n, _, ok := rt.waitingNotificationLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatal("expected notification to be ready")
	}
	want := "> ask\nfirst\nsecond"
	if n.Content != want {
		t.Fatalf("empty round baseline should not diff from last pushed snapshot:\n%q\nwant:\n%q", n.Content, want)
	}
}

func TestOutputAfterNotificationPatchesRunningTitleOnWaitingToRunning(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 你想查哪个城市的天气？")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notifyIfStillWaiting(version)
	rt.HandleOutput([]byte("more output"))

	running := waitForRunningNotes(t, notifier, 1)
	if len(running) != 1 || running[0].MessageID != "msg-1" || !running[0].Running {
		t.Fatalf("terminal output should patch the card title to running, got %#v", running)
	}
}

func TestNotifyInputRunningUsesClickedMessageAnchorWithoutPlaceholderPatch(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}

	rt.NotifyInputRunningOnMessage("bot-card")

	notes := notifier.notes()
	if len(notes) != 0 {
		t.Fatalf("running card should not overwrite clicked message with placeholder, got %#v", notes)
	}
	if rt.lastNotifiedMessageID != "bot-card" {
		t.Fatalf("runtime anchor = %q, want bot-card", rt.lastNotifiedMessageID)
	}
}

func TestRefreshNotificationMessageUsesCurrentRoundSnapshot(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:              m,
		session:              Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		notificationUpdateNo: 2,
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	rt.mu.Unlock()

	if err := rt.RefreshNotificationMessage("bot-card", 2); err != nil {
		t.Fatal(err)
	}

	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected one manual refresh update, got %#v", notes)
	}
	if notes[0].MessageID != "bot-card" || notes[0].Content != "$ echo hello\nhello\n$" || notes[0].Running {
		t.Fatalf("manual refresh should patch clicked card with current round, got %#v", notes[0])
	}
	if notes[0].UpdateNo != 2 || !notes[0].SuppressUpdateTip {
		t.Fatalf("manual refresh should preserve existing update marker without increasing count, got %#v", notes[0])
	}
	if rt.notificationUpdateNo != 2 {
		t.Fatalf("runtime update marker should not increase on manual refresh, got %d", rt.notificationUpdateNo)
	}
}

func TestRefreshNotificationMessageUsesFullCurrentRoundWhenRoundBaselineIsEmpty(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:                     m,
		session:                     Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastInputText:               "ask",
		lastNotifiedMessageID:       "bot-card",
		lastNotifiedVisibleSnapshot: "> ask\nfirst",
		snapshotAtRoundStart:        "",
		snapshotAtRoundVersion:      0,
		snapshotAtRoundStartSet:     true,
		visibleSnapshot:             "> ask\nfirst\nsecond",
		visibleSnapshotVersion:      1,
	}

	if err := rt.RefreshNotificationMessage("bot-card", 1); err != nil {
		t.Fatal(err)
	}

	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected one manual refresh update, got %#v", notes)
	}
	want := "> ask\nfirst\nsecond"
	if notes[0].Content != want {
		t.Fatalf("manual refresh should use full current round content:\n%q\nwant:\n%q", notes[0].Content, want)
	}
}

func TestRefreshNotificationMessageFallsBackToVisibleTailWhenRoundDiffIsEmpty(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	visible := "$ echo hello\nhello\n$"
	rt := &RuntimeSession{
		manager:                     m,
		session:                     Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID:       "bot-card",
		lastNotifiedVisibleSnapshot: visible,
	}
	rt.SetVisibleSnapshot(visible)

	if err := rt.RefreshNotificationMessage("bot-card"); err != nil {
		t.Fatal(err)
	}

	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected one manual refresh update, got %#v", notes)
	}
	if notes[0].Content != visible {
		t.Fatalf("empty diff should fall back to current visible content, got %#v", notes[0])
	}
}

func TestTerminalOutputSnapshotIsUnaffectedByNotificationDiff(t *testing.T) {
	rt := &RuntimeSession{
		manager:                 NewManager(nil, nil),
		session:                 Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastInputText:           "ask",
		snapshotAtRoundStart:    "old screen\n> ask",
		snapshotAtRoundVersion:  1,
		snapshotAtRoundStartSet: true,
		visibleSnapshot: strings.Join([]string{
			"old screen",
			"> ask",
			"first",
			"second",
		}, "\n"),
		visibleSnapshotVersion: 2,
	}
	rt.HandleOutput([]byte("old terminal history\n"))
	rt.HandleOutput([]byte("current terminal output\n"))

	rt.mu.Lock()
	n, _, ok := rt.waitingNotificationLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatal("expected notification to be ready")
	}
	if strings.Contains(n.Content, "old screen") {
		t.Fatalf("notification should use diff content, got %q", n.Content)
	}
	out := string(rt.OutputSnapshot())
	if !strings.Contains(out, "old terminal history") || !strings.Contains(out, "current terminal output") {
		t.Fatalf("terminal output snapshot should remain full raw history, got %q", out)
	}
}

func TestManualRefreshSchedulesAutoRefreshWhenEnabled(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithAutoRefreshInterval(20*time.Millisecond), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "bot-card",
		autoRefreshEnabled:    true,
		autoRefreshMessageID:  "bot-card",
		autoRefreshStop:       make(chan struct{}),
	}
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")

	if err := rt.RefreshNotificationMessage("bot-card"); err != nil {
		t.Fatal(err)
	}

	notes := waitForNotifierNotes(t, notifier, 2)
	if len(notes) != 2 {
		t.Fatalf("expected manual refresh plus one scheduled auto refresh, got %#v", notes)
	}
	if !notes[0].SuppressUpdateTip {
		t.Fatalf("first refresh should be manual, got %#v", notes[0])
	}
	if notes[1].SuppressUpdateTip {
		t.Fatalf("scheduled refresh should use auto refresh behavior, got %#v", notes[1])
	}
}

func TestAutoRefreshRebindsToNewRunningCard(t *testing.T) {
	notifier := &recordingNotifier{messageID: "new-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithAutoRefreshInterval(time.Hour))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	if enabled, err := rt.ToggleAutoRefresh("old-card"); err != nil || !enabled {
		t.Fatalf("toggle auto refresh = %v, %v", enabled, err)
	}

	rt.MarkInputActivity("echo hello\r")
	rt.NotifyInputRunning()

	rt.mu.Lock()
	messageID := rt.autoRefreshMessageID
	rt.mu.Unlock()
	if messageID != "new-card" {
		t.Fatalf("auto refresh should follow the new running card, got %q", messageID)
	}
	notes := notifier.notes()
	if len(notes) != 1 || notes[0].MessageID != "" || !notes[0].AutoRefreshEnabled {
		t.Fatalf("running notification should create a new auto-refresh-enabled card, got %#v", notes)
	}
}

func TestRefreshNotificationMessagePreventsStaleRunningPlaceholderOverwrite(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")

	rt.notificationPatchMu.Lock()
	runningDone := make(chan struct{})
	go func() {
		rt.NotifyInputRunningOnMessage("bot-card")
		close(runningDone)
	}()
	time.Sleep(50 * time.Millisecond)
	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- rt.RefreshNotificationMessage("bot-card")
	}()
	time.Sleep(50 * time.Millisecond)
	rt.notificationPatchMu.Unlock()

	select {
	case <-runningDone:
	case <-time.After(time.Second):
		t.Fatal("stale running update did not return")
	}
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh update did not return")
	}
	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("refresh should be the only card patch, got %#v", notes)
	}
	if notes[0].Content == RunningNotificationPlaceholder || notes[0].Content != "$ echo hello\nhello\n$" {
		t.Fatalf("stale running placeholder should not overwrite refresh content, got %#v", notes[0])
	}
}

func TestNotifyInputRunningDoesNotPatchExistingCard(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "bot-card",
		lastNotifiedContent:   "$ echo hello\nhello\n$",
	}

	rt.NotifyInputRunningOnMessage("bot-card")

	notes := notifier.notes()
	if len(notes) != 0 {
		t.Fatalf("running update should not patch existing card, got %#v", notes)
	}
}

func TestNotifyInputRunningDoesNotPatchExistingCardFromCurrentSnapshot(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "bot-card",
	}
	rt.MarkInputActivity("echo hello\r")
	rt.lastNotifiedMessageID = "bot-card"
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")

	rt.NotifyInputRunningOnMessage("bot-card")

	notes := notifier.notes()
	if len(notes) != 0 {
		t.Fatalf("running update should not patch existing card from current snapshot, got %#v", notes)
	}
}

func TestOutputPatchesRunningTitleFromCurrentRoundOnWaitingToRunning(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "bot-card",
	}
	rt.MarkInputActivity("echo hello\r")
	rt.lastNotifiedMessageID = "bot-card"
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	rt.mu.Unlock()

	rt.HandleOutput([]byte("still running\n"))

	running := waitForRunningNotes(t, notifier, 1)
	if len(running) != 1 || running[0].MessageID != "bot-card" || running[0].Content != "$ echo hello\nhello\n$" || !running[0].Running {
		t.Fatalf("terminal output should patch current round title as running, got %#v", running)
	}
}

func TestOutputAfterNotificationDoesNotPatchWhenAlreadyRunning(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 先给一版计划。")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notifyIfStillWaiting(version)
	rt.mu.Lock()
	rt.session.Status = StatusRunning
	rt.mu.Unlock()
	rt.HandleOutput([]byte("more output"))

	running := notifier.runningNotes()
	if len(running) != 0 {
		t.Fatalf("running output should not re-patch card when session is already running, got %#v", running)
	}
}

func TestManualRefreshAllowsWaitingToRunningTitleMarker(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "bot-card",
	}
	rt.MarkInputActivity("echo hello\r")
	rt.lastNotifiedMessageID = "bot-card"
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")

	if err := rt.RefreshNotificationMessage("bot-card"); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	rt.mu.Unlock()
	rt.HandleOutput([]byte("late output\n"))

	notes := notifier.notes()
	if len(notes) != 1 || notes[0].MessageID != "bot-card" || notes[0].Content != "$ echo hello\nhello\n$" {
		t.Fatalf("manual refresh should patch current content, got %#v", notes)
	}
	if running := waitForRunningNotes(t, notifier, 1); len(running) != 1 || running[0].MessageID != "bot-card" || !running[0].Running {
		t.Fatalf("waiting-to-running output should patch running title after manual refresh, got %#v", running)
	}
}

func TestManualRefreshWithConcurrentOutputPatchesRunningTitleAfterRefresh(t *testing.T) {
	notifier := newBlockingRefreshNotifier("bot-card")
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "bot-card",
		lastNotifiedContent:   "$ echo hello\nhello\n$",
	}
	rt.MarkInputActivity("echo hello\r")
	rt.lastNotifiedMessageID = "bot-card"
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- rt.RefreshNotificationMessage("bot-card")
	}()
	select {
	case <-notifier.notifyStarted:
	case <-time.After(time.Second):
		t.Fatal("manual refresh did not start")
	}
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	rt.mu.Unlock()
	rt.HandleOutput([]byte("late output\n"))
	close(notifier.releaseNotify)
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manual refresh did not finish")
	}

	notes := notifier.notes()
	if len(notes) != 1 || notes[0].MessageID != "bot-card" || notes[0].Content != "$ echo hello\nhello\n$" {
		t.Fatalf("manual refresh should patch current content, got %#v", notes)
	}
	if running := waitForBlockingRunningNotes(t, notifier, 1); len(running) != 1 || running[0].MessageID != "bot-card" || !running[0].Running {
		t.Fatalf("concurrent waiting-to-running output should patch running title after refresh, got %#v", running)
	}
}

func TestManualRefreshKeepsRunningStatus(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "bot-card",
	}
	rt.MarkInputActivity("echo hello\r")
	rt.lastNotifiedMessageID = "bot-card"
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")

	if err := rt.RefreshNotificationMessage("bot-card"); err != nil {
		t.Fatal(err)
	}

	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected one manual refresh update, got %#v", notes)
	}
	if !notes[0].Running {
		t.Fatalf("manual refresh should keep running status, got %#v", notes[0])
	}
	if !rt.notificationRunning {
		t.Fatalf("manual refresh should keep runtime notification state as running")
	}
}

func TestWaitingTransitionKeepsRunningTitleUntilFinalNotification(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:                m,
		session:                Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID:  "msg-1",
		lastNotifiedContent:    "> echo hello\nhello",
		notificationUpdateNo:   1,
		notificationRunning:    true,
		visibleSnapshot:        "> echo hello\nhello",
		visibleSnapshotVersion: 1,
		stateVersion:           7,
		notifyVersion:          3,
	}

	rt.notifyAfterStable(7)

	running := notifier.runningNotes()
	if len(running) != 0 {
		t.Fatalf("waiting transition should not clear running title before final notification, got %#v", running)
	}
	if got := rt.Snapshot().Status; got != StatusWaiting {
		t.Fatalf("session should transition to waiting, got %s", got)
	}
	if rt.notificationRunning {
		t.Fatal("runtime should clear running notification state after final waiting notification")
	}
	notes := notifier.notes()
	if len(notes) != 1 || notes[0].MessageID != "msg-1" || notes[0].Running {
		t.Fatalf("final waiting notification should replace current running message with non-running card, got %#v", notes)
	}
}

func TestOutputDuringInFlightWaitingPatchPatchesRunningTitleAfterWaitingPatch(t *testing.T) {
	notifier := newSequencingNotifier("msg-1")
	m := NewManager(nil, nil, WithNotifier(notifier), WithWaitingTransitionDelays(time.Hour, time.Hour), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:                 m,
		session:                 Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID:   "msg-1",
		lastNotifiedContent:     "> echo hello\npartial",
		notificationUpdateNo:    1,
		notificationRunning:     false,
		lastInputText:           "echo hello",
		visibleSnapshot:         "> echo hello\npartial\ncomplete",
		visibleSnapshotVersion:  1,
		snapshotAtRoundStart:    "> echo hello",
		snapshotAtRoundStartSet: true,
		roundReply:              []byte("partial\ncomplete"),
		notifyVersion:           4,
	}

	done := make(chan struct{})
	go func() {
		rt.notifyIfStillWaiting(4)
		close(done)
	}()

	select {
	case <-notifier.notifyStarted:
	case <-time.After(time.Second):
		t.Fatal("waiting notification update did not start")
	}
	outputDone := make(chan struct{})
	go func() {
		rt.HandleOutput([]byte("more output"))
		close(outputDone)
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-notifier.runningStarted:
		t.Fatal("terminal output should not patch running title")
	default:
	}
	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatal("output handling should not wait for waiting patch")
	}

	close(notifier.releaseNotify)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiting notification update did not finish")
	}
	select {
	case <-notifier.runningStarted:
	case <-time.After(time.Second):
		t.Fatal("terminal output should patch running title after waiting patch")
	}

	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatal("output handling should finish after running title patch")
	}

	events := notifier.events()
	if len(events) != 2 {
		t.Fatalf("expected waiting patch then running title patch, got %#v", events)
	}
	if events[0] != "notify:false" || events[1] != "running:true" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestNotifyIfStillWaitingSkipsStaleSendAfterNewOutput(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier), WithWaitingTransitionDelays(time.Hour, time.Hour))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\npartial")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notificationPatchMu.Lock()
	done := make(chan struct{})
	go func() {
		rt.notifyIfStillWaiting(version)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	rt.HandleOutput([]byte("complete\n"))
	rt.SetVisibleSnapshot("$ echo hello\npartial\ncomplete\n$")
	rt.notificationPatchMu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale waiting notification did not return")
	}
	if got := notifier.count(); got != 0 {
		t.Fatalf("stale waiting notification should not be sent after new output, got %d", got)
	}
}

func TestNotifyIfStillWaitingCoalescesSameRoundUpdate(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithWaitingTransitionDelays(time.Hour, time.Hour), WithNotificationUpdateCoalesce(250*time.Millisecond))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\npartial")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()
	rt.notifyIfStillWaiting(version)

	rt.HandleOutput([]byte(" more"))
	rt.SetVisibleSnapshot("$ echo hello\npartial more")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version = rt.notifyVersion
	rt.mu.Unlock()
	done := make(chan struct{})
	go func() {
		rt.notifyIfStillWaiting(version)
		close(done)
	}()
	time.Sleep(150 * time.Millisecond)
	rt.HandleOutput([]byte(" complete"))
	rt.SetVisibleSnapshot("$ echo hello\npartial more complete\n$")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("coalesced waiting notification did not return")
	}
	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("same-round update should be coalesced after newer output, got %#v", notes)
	}
}

func TestRunningTitleUpdateSkipsStaleMessageAfterReplacement(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier))
	rt := &RuntimeSession{
		manager:               m,
		session:               Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID: "msg-new",
	}

	rt.updateNotificationRunning(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   "old",
		MessageID: "msg-old",
		Running:   false,
	}, false)

	if got := len(notifier.runningNotes()); got != 0 {
		t.Fatalf("stale running title update should not patch old message, got %d", got)
	}
}

func TestLarkNotificationCardContentIncludesUpdateNumber(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   "done",
		UpdateNo:  2,
	}, "open-id", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "已更新-2") {
		t.Fatalf("card content should include update marker, got %s", content)
	}
	if !strings.Contains(content, `"update_no":2`) {
		t.Fatalf("refresh action should carry current update marker, got %s", content)
	}
	if !strings.Contains(content, "状态：Not Running") {
		t.Fatalf("card content should include non-running status marker, got %s", content)
	}
}

func TestLarkNotificationCardContentPreservesTerminalLineBreaks(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   "Select Model and Effort\n› 1. gpt-5.5 (current)\n  2. gpt-5.4\n  3. gpt-5.4-mini",
	}, "open-id", false)
	if err != nil {
		t.Fatal(err)
	}

	var card struct {
		Body struct {
			Elements []struct {
				Tag  string `json:"tag"`
				Text struct {
					Tag     string `json:"tag"`
					Content string `json:"content"`
				} `json:"text"`
			} `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(content), &card); err != nil {
		t.Fatal(err)
	}
	if len(card.Body.Elements) < 1 || card.Body.Elements[0].Tag != "div" || card.Body.Elements[0].Text.Tag != "plain_text" {
		t.Fatalf("card should put terminal output in an expanded plain text element, got %#v", card.Body.Elements)
	}
	want := "Select Model and Effort\n› 1. gpt-5.5 (current)\n  2. gpt-5.4\n  3. gpt-5.4-mini"
	if card.Body.Elements[0].Text.Content != want {
		t.Fatalf("terminal output should keep visible line breaks, got %q", card.Body.Elements[0].Text.Content)
	}
}

func TestLarkNotificationCardContentWarnsOnBufferFallback(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID:      "sess-1",
		Name:           "A",
		Content:        "line one\nline two",
		SnapshotSource: "buffer",
	}, "open-id", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "buffer") || !strings.Contains(content, "兜底") {
		t.Fatalf("buffer fallback card should include a visible warning, got %s", content)
	}
}

func TestLarkNotificationCardContentIncludesRunningTitleSuffix(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   "still working",
		Running:   true,
	}, "ou_1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "A（Running）") {
		t.Fatalf("card content should include running title suffix, got %s", content)
	}
	if !strings.Contains(content, `状态：\u003cfont color=\"green\"\u003eRunning\u003c/font\u003e`) {
		t.Fatalf("running card should include green running status text, got %s", content)
	}
	if !strings.Contains(content, `"template":"blue"`) || strings.Contains(content, `"background_style"`) {
		t.Fatalf("running card should use default header and no body background, got %s", content)
	}
	stopped, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   "done",
		Running:   false,
	}, "ou_1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stopped, "状态：Not Running") || !strings.Contains(stopped, `"template":"blue"`) || strings.Contains(stopped, `"background_style"`) {
		t.Fatalf("non-running card should use default header, no body background, and status text, got %s", stopped)
	}
}

func TestLarkNotificationCardContentIncludesShortcutButtons(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   RunningNotificationPlaceholder,
		Running:   true,
	}, "ou_1", false, LarkCustomShortcut{Label: "状态", Command: "git status"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Ctrl-C") || !strings.Contains(content, "ctrl_c") || !strings.Contains(content, "sess-1") {
		t.Fatalf("card content should include shortcut buttons, got %s", content)
	}
	if strings.Contains(content, `"content":"sess-1"`) {
		t.Fatalf("card content should not show session id as visible text, got %s", content)
	}
	if strings.Contains(content, "Ctrl-D") || strings.Contains(content, "ctrl_d") {
		t.Fatalf("card content should not include Ctrl-D, got %s", content)
	}
	if !strings.Contains(content, "退出agent") || !strings.Contains(content, "exit_agent") {
		t.Fatalf("card content should include exit agent shortcut, got %s", content)
	}
	if !strings.Contains(content, "刷新") || !strings.Contains(content, `"easy_terminal_action":"refresh"`) {
		t.Fatalf("card content should include manual refresh button, got %s", content)
	}
	if !strings.Contains(content, "自动刷新") || !strings.Contains(content, `"easy_terminal_action":"toggle_auto_refresh"`) {
		t.Fatalf("card content should include auto refresh button, got %s", content)
	}
	if !(strings.Index(content, `"content":"刷新"`) < strings.Index(content, `"content":"Ctrl-C"`) &&
		strings.Index(content, `"content":"自动刷新"`) < strings.Index(content, `"content":"Ctrl-C"`) &&
		strings.Index(content, `"content":"Ctrl-C"`) < strings.Index(content, `"content":"退出agent"`) &&
		strings.Index(content, `"content":"退出agent"`) < strings.Index(content, `"content":"Esc"`) &&
		strings.Index(content, `"content":"Esc"`) < strings.Index(content, `"content":"Enter"`) &&
		strings.Index(content, `"content":"Enter"`) < strings.Index(content, `"easy_terminal_action":"custom_shortcut"`)) {
		t.Fatalf("refresh button should be first and custom shortcuts below system shortcuts, got %s", content)
	}
	if !strings.Contains(content, "状态") || !strings.Contains(content, `"easy_terminal_action":"custom_shortcut"`) || !strings.Contains(content, "git status") {
		t.Fatalf("card content should include custom shortcut row, got %s", content)
	}
	for _, label := range []string{"刷新", "自动刷新", "Ctrl-C", "退出agent", "Esc", "Enter"} {
		if !strings.Contains(content, `"content":"`+label+`"`) {
			t.Fatalf("card content should include system shortcut %s, got %s", label, content)
		}
	}
	if strings.Count(content, `"type":"primary"`) < 7 {
		t.Fatalf("system and custom shortcut buttons should use blue primary color, got %s", content)
	}
	if strings.Contains(content, `"tag":"interactive_container"`) || strings.Contains(content, `"border_color":"green"`) || strings.Contains(content, `"background_style":"green"`) {
		t.Fatalf("custom shortcut actions should use native tiny buttons, got %s", content)
	}
	if !strings.Contains(content, `"content":"状态","tag":"plain_text"`) || !strings.Contains(content, `"type":"primary"`) {
		t.Fatalf("custom shortcut label should use a blue tiny button, got %s", content)
	}
	if !strings.Contains(content, `"size":"tiny"`) {
		t.Fatalf("card shortcut buttons should be small, got %s", content)
	}
	if !strings.Contains(content, `"schema":"2.0"`) || !strings.Contains(content, `"behaviors"`) || !strings.Contains(content, `"callback"`) {
		t.Fatalf("card shortcut buttons should use card 2.0 callback behavior, got %s", content)
	}
	enabled, err := larkNotificationCardContent(WaitingNotification{
		SessionID:          "sess-1",
		Name:               "A",
		Content:            RunningNotificationPlaceholder,
		Running:            true,
		AutoRefreshEnabled: true,
	}, "ou_1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(enabled, "停自动") {
		t.Fatalf("enabled auto refresh card should show close button, got %s", enabled)
	}
}

func TestLarkUpdateTipCardContentIsSmallNote(t *testing.T) {
	content, err := larkUpdateTipCardContent(3, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "已更新-3") {
		t.Fatalf("tip content should include update marker, got %s", content)
	}
	if !strings.Contains(content, `"tag":"note"`) {
		t.Fatalf("tip content should use note element, got %s", content)
	}
	if strings.Contains(content, `"header"`) {
		t.Fatalf("tip content should not include a header, got %s", content)
	}
}

func TestLarkUpdateTipCardContentIncludesMentionWhenEnabled(t *testing.T) {
	content, err := larkUpdateTipCardContent(3, "ou_1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `\u003cat id=ou_1\u003e\u003c/at\u003e`) {
		t.Fatalf("tip content should include mention when enabled, got %s", content)
	}
	if !strings.Contains(content, "已更新-3") {
		t.Fatalf("tip content should include update marker, got %s", content)
	}
}

func TestLarkUpdateTipSendsEachUpdateNumberOnce(t *testing.T) {
	notifier := &LarkAppNotifier{}
	var sent []string
	notifier.tipSender = func(messageID string, chatID string, updateNo int) error {
		sent = append(sent, fmt.Sprintf("%s:%s:%d", messageID, chatID, updateNo))
		return nil
	}

	if err := notifier.sendUpdateTipOnce("msg-1", "oc_1", 1); err != nil {
		t.Fatal(err)
	}
	if err := notifier.sendUpdateTipOnce("msg-1", "oc_1", 1); err != nil {
		t.Fatal(err)
	}
	if err := notifier.sendUpdateTipOnce("msg-1", "oc_1", 2); err != nil {
		t.Fatal(err)
	}
	if err := notifier.sendUpdateTipOnce("msg-2", "oc_2", 1); err != nil {
		t.Fatal(err)
	}

	want := []string{"msg-1:oc_1:1", "msg-1:oc_1:2", "msg-2:oc_2:1"}
	if len(sent) != len(want) {
		t.Fatalf("sent tips = %#v, want %#v", sent, want)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Fatalf("sent tip %d = %q, want %q; all=%#v", i, sent[i], want[i], sent)
		}
	}
}

func TestLarkUpdateWaitingSendsUpdateTip(t *testing.T) {
	notifier := &LarkAppNotifier{}
	notifier.client = fakeLarkSuccessClient(t)
	var sent []string
	notifier.tipSender = func(messageID string, chatID string, updateNo int) error {
		sent = append(sent, fmt.Sprintf("%s:%s:%d", messageID, chatID, updateNo))
		return nil
	}

	result, err := notifier.updateWaiting(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   "updated",
		MessageID: "msg-1",
		ChatID:    "oc_1",
		UpdateNo:  2,
	}, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !result.TipSent {
		t.Fatalf("expected update tip to be sent, got %#v", result)
	}
	if len(sent) != 1 || sent[0] != "msg-1:oc_1:2" {
		t.Fatalf("sent tips = %#v", sent)
	}
}

func TestLarkUpdateWaitingSuppressesManualRefreshTip(t *testing.T) {
	notifier := &LarkAppNotifier{}
	notifier.client = fakeLarkSuccessClient(t)
	var sent []string
	notifier.tipSender = func(messageID string, chatID string, updateNo int) error {
		sent = append(sent, fmt.Sprintf("%s:%s:%d", messageID, chatID, updateNo))
		return nil
	}

	result, err := notifier.updateWaiting(WaitingNotification{
		SessionID:         "sess-1",
		Name:              "A",
		Content:           "updated",
		MessageID:         "msg-1",
		UpdateNo:          2,
		SuppressUpdateTip: true,
	}, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if result.TipSent {
		t.Fatalf("manual refresh should suppress update tip, got %#v", result)
	}
	if len(sent) != 0 {
		t.Fatalf("manual refresh should not send tip messages, got %#v", sent)
	}
}

func TestAutoRefreshNotificationMessageKeepsUpdateNumberButAllowsTip(t *testing.T) {
	notifier := &recordingNotifier{messageID: "bot-card"}
	m := NewManager(nil, nil, WithNotifier(notifier), WithNotificationUpdateCoalesce(0))
	rt := &RuntimeSession{
		manager:              m,
		session:              Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: true},
		notificationUpdateNo: 2,
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	rt.mu.Lock()
	rt.session.Status = StatusRunning
	rt.mu.Unlock()

	if err := rt.AutoRefreshNotificationMessage("bot-card", 2); err != nil {
		t.Fatal(err)
	}

	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected one auto refresh update, got %#v", notes)
	}
	if notes[0].UpdateNo != 2 || notes[0].SuppressUpdateTip {
		t.Fatalf("auto refresh should preserve update number and allow tip, got %#v", notes[0])
	}
	if rt.notificationUpdateNo != 2 {
		t.Fatalf("auto refresh should not increase update number, got %d", rt.notificationUpdateNo)
	}
}

type fakeLarkHTTPClient struct{}

func (fakeLarkHTTPClient) Do(*http.Request) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json; charset=utf-8")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(bytes.NewBufferString(`{"code":0,"msg":"success","data":{}}`)),
	}, nil
}

func fakeLarkSuccessClient(t *testing.T) *lark.Client {
	t.Helper()
	return lark.NewClient("app", "secret", lark.WithHttpClient(fakeLarkHTTPClient{}))
}

func TestNotifyAfterStableDoesNotSendWhenNotificationDisabled(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: false},
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	version := rt.stateVersion

	rt.notifyAfterStable(version)
	if got := rt.Snapshot().Status; got != StatusWaiting {
		t.Fatalf("stable output should still transition to waiting, got %s", got)
	}
	if got := notifier.count(); got != 0 {
		t.Fatalf("disabled notification should not send, got %d", got)
	}
}

func TestVisibleSnapshotSyncDoesNotScheduleNotification(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.lastInputText = "echo hello"
	rt.visibleSnapshot = "$ echo hello\nhello\n$"

	rt.SetVisibleSnapshot("$ echo hello\nhello\n$ ")

	rt.mu.Lock()
	timer := rt.notifyStableTimer
	rt.mu.Unlock()
	if timer != nil {
		t.Fatal("snapshot-only sync should not schedule a notification timer")
	}
	time.Sleep(defaultFastWaitingTransition + 100*time.Millisecond)
	if got := notifier.count(); got != 0 {
		t.Fatalf("snapshot-only sync should not send notification, got %d", got)
	}
}

func TestRequestFreshSnapshotAsksSubscriberAndWaitsForUpdate(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil),
		session:     Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
		subscribers: make(map[chan RuntimeEvent]struct{}),
	}
	ch, cancel := rt.Subscribe()
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		done <- rt.RequestFreshSnapshot(time.Second)
	}()

	select {
	case ev := <-ch:
		if ev.Type != RuntimeEventSnapshotRequest {
			t.Fatalf("event type = %q, want snapshot request", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("expected snapshot request event")
	}
	rt.SetVisibleSnapshot("> hello\n• world")
	select {
	case fresh := <-done:
		if !fresh {
			t.Fatal("request should report a fresh snapshot")
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot request did not finish")
	}
}

func TestRequestFreshSnapshotRefreshesExistingSnapshot(t *testing.T) {
	rt := &RuntimeSession{
		manager:                NewManager(nil, nil),
		session:                Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
		visibleSnapshot:        "old snapshot",
		visibleSnapshotVersion: 1,
		subscribers:            make(map[chan RuntimeEvent]struct{}),
	}
	ch, cancel := rt.Subscribe()
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		done <- rt.RequestFreshSnapshot(time.Second)
	}()

	select {
	case ev := <-ch:
		if ev.Type != RuntimeEventSnapshotRequest {
			t.Fatalf("event type = %q, want snapshot request", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("expected snapshot request despite existing snapshot")
	}
	rt.SetVisibleSnapshot("fresh snapshot")
	select {
	case fresh := <-done:
		if !fresh {
			t.Fatal("request should report a fresh snapshot")
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot request did not finish")
	}
}

func TestNotifyIfStillWaitingRetriesUntilCurrentRoundIsReady(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.notifyVersion = 1
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• Working (8s • esc to interrupt)")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	rt.notifyVersion = 2
	version := rt.notifyVersion
	rt.mu.Unlock()

	go rt.notifyIfStillWaiting(version)
	time.Sleep(500 * time.Millisecond)
	if got := notifier.count(); got != 0 {
		t.Fatalf("notifier should not send while current round has only transient content, got %d", got)
	}

	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 你想查哪个城市的天气？例如：上海、北京。")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if notifier.count() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("expected retry to send once after snapshot becomes ready, got %#v", notes)
	}
	if notes[0].Content != "> 今天天气怎么样\n• 你想查哪个城市的天气？例如：上海、北京。" {
		t.Fatalf("unexpected retry notification content: %q", notes[0].Content)
	}
}

func TestStartupPresetNotificationSuppressionSkipsExternalNotifyAndHook(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier))
	hookCh := make(chan string, 1)
	m.SetNotificationSentHook(func(sessionID string) {
		hookCh <- sessionID
	})
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("echo setup\r")
	rt.SetVisibleSnapshot("$ echo setup\nsetup done\n$")
	rt.SuppressStartupNotifications()
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notifyIfStillWaiting(version)
	if got := notifier.count(); got != 0 {
		t.Fatalf("startup preset notification should be suppressed, got %d", got)
	}
	select {
	case got := <-hookCh:
		t.Fatalf("suppressed startup notification should not run hook, got %q", got)
	default:
	}

	rt.MarkInputActivity("echo real\r")
	rt.mu.Lock()
	mode := rt.startupNotifyMode
	rt.mu.Unlock()
	if mode != startupNotifyNormal {
		t.Fatal("real input should clear startup notification suppression")
	}
}

func TestStartupPresetFinalNotificationSendsOnce(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifier(notifier))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusRunning, Live: true, NotifyOnWaiting: true},
	}
	rt.MarkInputActivity("echo setup\r")
	rt.SetVisibleSnapshot("$ echo setup\nsetup done\n$")
	rt.SuppressStartupNotifications()
	rt.finishStartupNotificationsAfter(250 * time.Millisecond)
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	version := rt.notifyVersion
	rt.mu.Unlock()

	rt.notifyIfStillWaiting(version)
	if got := notifier.count(); got != 0 {
		t.Fatalf("startup notification should stay suppressed during settle window, got %d", got)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if notifier.count() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	notes := notifier.notes()
	if len(notes) != 1 {
		t.Fatalf("final startup preset notification should send once after settle, got %#v", notes)
	}
	if notes[0].Content != "$ echo setup\nsetup done\n$" {
		t.Fatalf("final startup notification content = %q", notes[0].Content)
	}
	rt.mu.Lock()
	mode := rt.startupNotifyMode
	rt.mu.Unlock()
	if mode != startupNotifyNormal {
		t.Fatalf("startup notification mode = %v, want normal", mode)
	}
}

type recordingNotifier struct {
	mu           sync.Mutex
	items        []WaitingNotification
	runningItems []WaitingNotification
	messageID    string
}

func (n *recordingNotifier) Available() bool { return true }

func (n *recordingNotifier) NotifyWaiting(note WaitingNotification) (WaitingNotificationResult, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.items = append(n.items, note)
	messageID := n.messageID
	if messageID == "" {
		messageID = "msg-recording"
	}
	return WaitingNotificationResult{MessageID: messageID, Updated: note.MessageID != ""}, nil
}

func (n *recordingNotifier) UpdateWaitingRunning(note WaitingNotification, running bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	note.Running = running
	n.runningItems = append(n.runningItems, note)
	return nil
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.items)
}

func (n *recordingNotifier) notes() []WaitingNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := make([]WaitingNotification, len(n.items))
	copy(cp, n.items)
	return cp
}

func (n *recordingNotifier) runningNotes() []WaitingNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := make([]WaitingNotification, len(n.runningItems))
	copy(cp, n.runningItems)
	return cp
}

type runningNoteRecorder interface {
	runningNotes() []WaitingNotification
}

func waitForRunningNotes(t *testing.T, notifier runningNoteRecorder, want int) []WaitingNotification {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		notes := notifier.runningNotes()
		if len(notes) >= want {
			return notes
		}
		time.Sleep(10 * time.Millisecond)
	}
	return notifier.runningNotes()
}

func waitForBlockingRunningNotes(t *testing.T, notifier runningNoteRecorder, want int) []WaitingNotification {
	t.Helper()
	return waitForRunningNotes(t, notifier, want)
}

type blockingRefreshNotifier struct {
	recordingNotifier
	notifyOnce    sync.Once
	notifyStarted chan struct{}
	releaseNotify chan struct{}
}

func newBlockingRefreshNotifier(messageID string) *blockingRefreshNotifier {
	return &blockingRefreshNotifier{
		recordingNotifier: recordingNotifier{messageID: messageID},
		notifyStarted:     make(chan struct{}),
		releaseNotify:     make(chan struct{}),
	}
}

func (n *blockingRefreshNotifier) NotifyWaiting(note WaitingNotification) (WaitingNotificationResult, error) {
	n.mu.Lock()
	n.items = append(n.items, note)
	messageID := n.messageID
	if messageID == "" {
		messageID = "msg-recording"
	}
	n.mu.Unlock()
	n.notifyOnce.Do(func() { close(n.notifyStarted) })
	<-n.releaseNotify
	return WaitingNotificationResult{MessageID: messageID, Updated: note.MessageID != ""}, nil
}

type advancingNotifier struct {
	recordingNotifier
	afterNotify func()
}

func (n *advancingNotifier) NotifyWaiting(note WaitingNotification) (WaitingNotificationResult, error) {
	result, err := n.recordingNotifier.NotifyWaiting(note)
	if n.afterNotify != nil {
		afterNotify := n.afterNotify
		n.afterNotify = nil
		afterNotify()
	}
	return result, err
}

type sequencingNotifier struct {
	mu             sync.Mutex
	notifyOnce     sync.Once
	runningOnce    sync.Once
	messageID      string
	eventsList     []string
	notifyStarted  chan struct{}
	runningStarted chan struct{}
	releaseNotify  chan struct{}
}

func newSequencingNotifier(messageID string) *sequencingNotifier {
	return &sequencingNotifier{
		messageID:      messageID,
		notifyStarted:  make(chan struct{}),
		runningStarted: make(chan struct{}),
		releaseNotify:  make(chan struct{}),
	}
}

func (n *sequencingNotifier) Available() bool { return true }

func (n *sequencingNotifier) NotifyWaiting(note WaitingNotification) (WaitingNotificationResult, error) {
	n.notifyOnce.Do(func() { close(n.notifyStarted) })
	<-n.releaseNotify
	n.mu.Lock()
	n.eventsList = append(n.eventsList, fmt.Sprintf("notify:%v", note.Running))
	n.mu.Unlock()
	return WaitingNotificationResult{MessageID: n.messageID}, nil
}

func (n *sequencingNotifier) UpdateWaitingRunning(note WaitingNotification, running bool) error {
	n.runningOnce.Do(func() { close(n.runningStarted) })
	n.mu.Lock()
	n.eventsList = append(n.eventsList, fmt.Sprintf("running:%v", running))
	n.mu.Unlock()
	return nil
}

func (n *sequencingNotifier) events() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := make([]string, len(n.eventsList))
	copy(cp, n.eventsList)
	return cp
}
