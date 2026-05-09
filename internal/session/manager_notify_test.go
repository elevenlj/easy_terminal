package session

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
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
	m := NewManager(nil, nil, WithNotifier(notifier))
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
	notifier := &recordingNotifier{messageID: "msg-1", replaceMessageID: "msg-2"}
	m := NewManager(nil, nil, WithNotifier(notifier))
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
	if notes[1].MessageID != "msg-1" || notes[1].UpdateNo != 1 {
		t.Fatalf("second notification should replace msg-1 with update no 1, got %#v", notes[1])
	}
	if notes[1].Running {
		t.Fatalf("waiting notification update should clear running title state, got %#v", notes[1])
	}
	if rt.lastNotifiedMessageID != "msg-2" {
		t.Fatalf("runtime should store replacement message id, got %q", rt.lastNotifiedMessageID)
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
	notifier := &advancingNotifier{recordingNotifier: recordingNotifier{messageID: "msg-1", replaceMessageID: "msg-2"}}
	m := NewManager(nil, nil, WithNotifier(notifier), WithWaitingTransitionDelays(time.Hour, time.Hour))
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
	if notes[1].MessageID != "msg-1" || notes[1].UpdateNo != 1 {
		t.Fatalf("second same-round notification should replace msg-1, got %#v", notes[1])
	}
}

func TestOutputAfterNotificationMarksSameRoundMessageRunning(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier))
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

	running := notifier.runningNotes()
	if len(running) != 1 {
		t.Fatalf("expected one running marker update, got %#v", running)
	}
	if running[0].MessageID != "msg-1" || !running[0].Running || running[0].Name != "A" {
		t.Fatalf("unexpected running marker note: %#v", running[0])
	}
	if running[0].Content != "> 今天天气怎么样\n• 你想查哪个城市的天气？" {
		t.Fatalf("running marker should keep last notified content, got %q", running[0].Content)
	}
}

func TestOutputAfterNotificationMarksRunningEvenIfAlreadyRunning(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier))
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
	if len(running) != 1 {
		t.Fatalf("expected running marker even when session was already running, got %#v", running)
	}
	if running[0].MessageID != "msg-1" || !running[0].Running {
		t.Fatalf("unexpected running marker note: %#v", running[0])
	}
}

func TestWaitingTransitionKeepsRunningTitleUntilFinalNotification(t *testing.T) {
	notifier := &recordingNotifier{messageID: "msg-1"}
	m := NewManager(nil, nil, WithNotifier(notifier))
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

func TestRunningTitleUpdateWaitsForInFlightWaitingPatch(t *testing.T) {
	notifier := newSequencingNotifier("msg-1")
	m := NewManager(nil, nil, WithNotifier(notifier), WithWaitingTransitionDelays(time.Hour, time.Hour))
	rt := &RuntimeSession{
		manager:                m,
		session:                Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID:  "msg-1",
		lastNotifiedContent:    "> echo hello\npartial",
		notificationUpdateNo:   1,
		notificationRunning:    false,
		lastInputText:          "echo hello",
		visibleSnapshot:        "> echo hello\npartial\ncomplete",
		visibleSnapshotVersion: 1,
		snapshotAtRoundStart:   "> echo hello",
		roundReply:             []byte("partial\ncomplete"),
		notifyVersion:          4,
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
		t.Fatal("running title update should wait for in-flight waiting patch")
	default:
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
		t.Fatal("running title update did not run after waiting patch")
	}
	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatal("output handling did not finish")
	}

	events := notifier.events()
	if len(events) != 2 {
		t.Fatalf("expected waiting patch then running patch, got %#v", events)
	}
	if events[0] != "notify:false" || events[1] != "running:true" {
		t.Fatalf("running title patch should happen after waiting patch, got %#v", events)
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
	mu               sync.Mutex
	items            []WaitingNotification
	runningItems     []WaitingNotification
	messageID        string
	replaceMessageID string
}

func (n *recordingNotifier) Available() bool { return true }

func (n *recordingNotifier) NotifyWaiting(note WaitingNotification) (WaitingNotificationResult, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.items = append(n.items, note)
	messageID := n.messageID
	if note.MessageID != "" && n.replaceMessageID != "" {
		messageID = n.replaceMessageID
	}
	if messageID == "" {
		messageID = "msg-recording"
	}
	return WaitingNotificationResult{MessageID: messageID}, nil
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
