package session

import (
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

func TestNotifyDelayFastForPlainOutputAndConservativeForCodex(t *testing.T) {
	m := NewManager(nil, nil, WithNotifyIdleTimeout(7*time.Second))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	rt.mu.Lock()
	fast := rt.notifyDelayLocked()
	rt.mu.Unlock()
	if fast != 0 {
		t.Fatalf("plain output notify delay = %v, want fast path", fast)
	}

	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot("> 今天天气怎么样\n• 你想查哪个城市的天气？\n> Use /skills to list available skills\ngpt-5.5 medium · ~")
	rt.mu.Lock()
	conservative := rt.notifyDelayLocked()
	rt.mu.Unlock()
	if conservative != 7*time.Second {
		t.Fatalf("codex output notify delay = %v, want configured delay", conservative)
	}
}

func TestNotifyIfStillWaitingRetriesUntilCurrentRoundIsReady(t *testing.T) {
	notifier := &recordingNotifier{}
	m := NewManager(nil, nil, WithNotifyIdleTimeout(time.Second), WithNotifier(notifier))
	rt := &RuntimeSession{
		manager: m,
		session: Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
	}
	rt.notifyVersion = 1
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	rt.notifyVersion = 2
	version := rt.notifyVersion
	rt.visibleSnapshot = "> 今天天气怎么样\n• Working (8s • esc to interrupt)"
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

type recordingNotifier struct {
	mu    sync.Mutex
	items []WaitingNotification
}

func (n *recordingNotifier) Available() bool { return true }

func (n *recordingNotifier) NotifyWaiting(note WaitingNotification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.items = append(n.items, note)
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
