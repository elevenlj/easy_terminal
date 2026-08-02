package session

import (
	"testing"
	"time"
)

func TestRequestFreshSnapshotLeavesPurposeEmpty(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil),
		session:     Session{ID: "sess-purpose", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	ch, cancel := rt.Subscribe()
	defer cancel()

	done := make(chan bool, 1)
	go func() { done <- rt.RequestFreshSnapshot(time.Second) }()
	event := receiveSnapshotRequestEvent(t, ch)
	if event.Purpose != "" {
		t.Fatalf("ordinary snapshot purpose = %q, want empty", event.Purpose)
	}
	rt.SetVisibleSnapshotResponseFrom("ordinary snapshot", "browser:buffer", event.RequestID, ch)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("ordinary snapshot request should succeed")
	}
}

func TestPrepareInputSnapshotBaselineStopsAfterFirstSuccessfulRequest(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil),
		session:     Session{ID: "sess-baseline", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	ch, cancel := rt.Subscribe()
	defer cancel()

	done := make(chan bool, 1)
	go func() { done <- rt.prepareInputSnapshotBaseline(time.Second) }()
	event := receiveSnapshotRequestEvent(t, ch)
	if event.Purpose != SnapshotPurposeInputBaseline {
		t.Fatalf("baseline snapshot purpose = %q, want %q", event.Purpose, SnapshotPurposeInputBaseline)
	}
	rt.SetVisibleSnapshotResponseFrom("primary baseline", "browser:buffer", event.RequestID, ch)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("baseline snapshot preparation should succeed")
	}
	assertNoSnapshotRequestEvent(t, ch, "a successful baseline must not rebuild the renderer marker")
}

func TestPrepareInputSnapshotBaselineRetriesAfterFirstFailure(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil),
		session:     Session{ID: "sess-baseline-retry", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	ch, cancel := rt.Subscribe()
	defer cancel()

	done := make(chan bool, 1)
	go func() { done <- rt.prepareInputSnapshotBaseline(1500 * time.Millisecond) }()
	first := receiveSnapshotRequestEvent(t, ch)
	if first.Purpose != SnapshotPurposeInputBaseline {
		t.Fatalf("first baseline purpose = %q", first.Purpose)
	}
	second := receiveSnapshotRequestEvent(t, ch)
	if second.Purpose != SnapshotPurposeInputBaseline || second.RequestID == first.RequestID {
		t.Fatalf("retry baseline event = %#v after %#v", second, first)
	}
	rt.SetVisibleSnapshotResponseFrom("retry baseline", "browser:buffer", second.RequestID, ch)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("second baseline request should recover after the first timeout")
	}
}

func TestPrepareInputSnapshotBaselineMarksHeadlessFallbackRequests(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil, WithBrowserNeeded(func(string) {})),
		session:     Session{ID: "sess-baseline-fallback", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	real, cancelReal := rt.SubscribeWithMode(false)
	defer cancelReal()
	idleReal, cancelIdleReal := rt.SubscribeWithMode(false)
	defer cancelIdleReal()
	headless, cancelHeadless := rt.SubscribeWithMode(true)
	defer cancelHeadless()

	done := make(chan bool, 1)
	go func() { done <- rt.prepareInputSnapshotBaselineFrom(1100*time.Millisecond, real) }()

	primary := receiveSnapshotRequestEvent(t, real)
	if primary.Purpose != SnapshotPurposeInputBaseline {
		t.Fatalf("primary purpose = %q, want %q", primary.Purpose, SnapshotPurposeInputBaseline)
	}
	assertNoSnapshotRequestEvent(t, idleReal, "idle browser must not receive the origin-bound baseline request")
	fallback := receiveSnapshotRequestEvent(t, headless)
	if fallback.Purpose != SnapshotPurposeInputBaseline {
		t.Fatalf("fallback purpose = %q, want %q", fallback.Purpose, SnapshotPurposeInputBaseline)
	}
	rt.SetVisibleSnapshotResponseFrom("headless baseline", "headless:buffer", fallback.RequestID, headless)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("baseline preparation should succeed through headless fallback")
	}
	assertNoSnapshotRequestEvent(t, real, "successful headless fallback must not start a second baseline attempt")
}

func TestOriginBaselineStillFallsBackAfterNewerRendererSnapshot(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil, WithBrowserNeeded(func(string) {})),
		session:     Session{ID: "sess-origin-newer-fallback", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	origin, cancelOrigin := rt.SubscribeWithMode(false)
	defer cancelOrigin()
	other, cancelOther := rt.SubscribeWithMode(false)
	defer cancelOther()
	headless, cancelHeadless := rt.SubscribeWithMode(true)
	defer cancelHeadless()

	originResult := make(chan bool, 1)
	go func() {
		originResult <- rt.requestFreshSnapshotFrom(400*time.Millisecond, SnapshotPurposeInputBaseline, origin)
	}()
	_ = receiveSnapshotRequestEvent(t, origin)

	newerResult := make(chan bool, 1)
	go func() {
		newerResult <- rt.requestFreshSnapshotFrom(time.Second, SnapshotPurposeInputBaseline, other)
	}()
	newer := receiveSnapshotRequestEvent(t, other)
	rt.SetVisibleSnapshotResponseFrom("newer other-browser snapshot", "browser:buffer", newer.RequestID, other)
	if !receiveSnapshotResult(t, newerResult) {
		t.Fatal("newer exact renderer request should succeed")
	}

	// The newer request advances latestAppliedSnapshotRequestID, but it cannot
	// satisfy the older origin-bound request. That request must still time out
	// its exact origin and enter the permitted input-baseline headless fallback.
	fallback := receiveSnapshotRequestEvent(t, headless)
	rt.SetVisibleSnapshotResponseFrom("origin headless fallback", "headless:buffer", fallback.RequestID, headless)
	if !receiveSnapshotResult(t, originResult) {
		t.Fatal("origin baseline should succeed only after its own headless fallback")
	}
}

func TestFreshSnapshotStaysWithHeadlessRoundOwner(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil),
		session:     Session{ID: "sess-headless-owner", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	real, cancelReal := rt.SubscribeWithMode(false)
	defer cancelReal()
	headless, cancelHeadless := rt.SubscribeWithMode(true)
	defer cancelHeadless()
	rt.mu.Lock()
	rt.snapshotAtRoundStartSet = true
	rt.snapshotAtRoundResponder = headless
	rt.snapshotAtRoundSource = "headless:buffer"
	rt.mu.Unlock()

	done := make(chan bool, 1)
	go func() { done <- rt.RequestFreshSnapshot(time.Second) }()
	event := receiveSnapshotRequestEvent(t, headless)
	assertNoSnapshotRequestEvent(t, real, "browser must not receive a headless-owned round request")
	rt.SetVisibleSnapshotResponseFrom("headless current", "headless:buffer", event.RequestID, headless)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("headless round owner should satisfy the ordinary fresh request")
	}
}

func TestFreshSnapshotStaysWithBrowserRoundOwner(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil),
		session:     Session{ID: "sess-browser-round-owner", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	owner, cancelOwner := rt.SubscribeWithMode(false)
	defer cancelOwner()
	idle, cancelIdle := rt.SubscribeWithMode(false)
	defer cancelIdle()
	rt.mu.Lock()
	rt.snapshotAtRoundStartSet = true
	rt.snapshotAtRoundResponder = owner
	rt.snapshotAtRoundSource = "browser:buffer"
	rt.mu.Unlock()

	done := make(chan bool, 1)
	go func() { done <- rt.RequestFreshSnapshot(time.Second) }()
	event := receiveSnapshotRequestEvent(t, owner)
	assertNoSnapshotRequestEvent(t, idle, "idle browser must not receive another browser's round request")
	rt.SetVisibleSnapshotResponseFrom("browser current", "browser:buffer", event.RequestID, owner)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("browser round owner should satisfy the ordinary fresh request")
	}
}

func TestFreshSnapshotReassignsDisconnectedRoundOwner(t *testing.T) {
	rt := &RuntimeSession{
		manager:     NewManager(nil, nil, WithBrowserNeeded(func(string) {})),
		session:     Session{ID: "sess-disconnected-round-owner", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	owner, cancelOwner := rt.SubscribeWithMode(false)
	fallback, cancelFallback := rt.SubscribeWithMode(false)
	defer cancelFallback()
	rt.mu.Lock()
	rt.snapshotAtRoundStartSet = true
	rt.snapshotAtRoundResponder = owner
	rt.snapshotAtRoundSource = "browser:buffer"
	rt.mu.Unlock()

	cancelOwner()
	done := make(chan bool, 1)
	go func() { done <- rt.RequestFreshSnapshot(5 * time.Second) }()
	event := receiveSnapshotRequestEvent(t, fallback)
	rt.SetVisibleSnapshotResponseFrom("replacement current snapshot", "browser:buffer", event.RequestID, fallback)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("a live renderer must take over after the round owner disconnects")
	}
}

func TestRealBrowserDoesNotStopHeadlessRoundOwner(t *testing.T) {
	active := make(chan string, 1)
	rt := &RuntimeSession{
		manager: NewManager(nil, nil, WithBrowserActive(func(sessionID string) {
			active <- sessionID
		})),
		session:     Session{ID: "sess-headless-round-browser-open", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	headless, cancelHeadless := rt.SubscribeWithMode(true)
	defer cancelHeadless()
	rt.mu.Lock()
	rt.snapshotAtRoundStartSet = true
	rt.snapshotAtRoundResponder = headless
	rt.snapshotAtRoundSource = "headless:buffer"
	rt.mu.Unlock()

	_, cancelReal := rt.SubscribeWithMode(false)
	defer cancelReal()
	assertNoBrowserActiveCallback(t, active, "opening another computer must not stop the headless round owner")
}

func TestRealBrowserDoesNotStopPendingHeadlessBaseline(t *testing.T) {
	active := make(chan string, 1)
	rt := &RuntimeSession{
		manager: NewManager(nil, nil, WithBrowserActive(func(sessionID string) {
			active <- sessionID
		})),
		session:     Session{ID: "sess-headless-baseline-browser-open", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	headless, cancelHeadless := rt.SubscribeWithMode(true)
	defer cancelHeadless()
	done := make(chan bool, 1)
	go func() {
		done <- rt.requestFreshSnapshot(time.Second, SnapshotPurposeInputBaseline)
	}()
	request := receiveSnapshotRequestEvent(t, headless)

	_, cancelReal := rt.SubscribeWithMode(false)
	defer cancelReal()
	assertNoBrowserActiveCallback(t, active, "opening another computer must not stop a pending headless baseline")
	rt.SetVisibleSnapshotResponseFrom("headless baseline", "headless:buffer", request.RequestID, headless)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("pending headless baseline should still complete")
	}
}

func TestRealBrowserDoesNotStopAcceptedHeadlessBaselineBeforeRoundCommit(t *testing.T) {
	active := make(chan string, 1)
	rt := &RuntimeSession{
		manager: NewManager(nil, nil, WithBrowserActive(func(sessionID string) {
			active <- sessionID
		})),
		session:     Session{ID: "sess-accepted-headless-baseline", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	existingBrowser, cancelExistingBrowser := rt.SubscribeWithMode(false)
	defer cancelExistingBrowser()
	select {
	case <-active:
	case <-time.After(time.Second):
		t.Fatal("initial browser subscription callback missing")
	}
	headless, cancelHeadless := rt.SubscribeWithMode(true)
	defer cancelHeadless()
	rt.mu.Lock()
	rt.snapshotAtRoundStartSet = true
	rt.snapshotAtRoundResponder = existingBrowser
	rt.snapshotAtRoundSource = "browser:buffer"
	rt.mu.Unlock()

	done := make(chan bool, 1)
	go func() {
		done <- rt.requestFreshSnapshotFrom(time.Second, SnapshotPurposeInputBaseline, headless)
	}()
	request := receiveSnapshotRequestEvent(t, headless)
	rt.SetVisibleSnapshotResponseFrom("captured headless composer", "headless:buffer", request.RequestID, headless)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("headless composer baseline should be accepted")
	}

	_, cancelNewBrowser := rt.SubscribeWithMode(false)
	defer cancelNewBrowser()
	assertNoBrowserActiveCallback(t, active, "an accepted headless baseline remains protected until its round is committed")
}

func TestBrowserBaselineTakeoverStopsHeadlessAfterRoundCommit(t *testing.T) {
	active := make(chan string, 1)
	rt := &RuntimeSession{
		manager: NewManager(nil, nil, WithBrowserActive(func(sessionID string) {
			active <- sessionID
		})),
		session:     Session{ID: "sess-browser-baseline-takeover", Live: true},
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	headless, cancelHeadless := rt.SubscribeWithMode(true)
	defer cancelHeadless()
	rt.mu.Lock()
	rt.snapshotAtRoundStartSet = true
	rt.snapshotAtRoundResponder = headless
	rt.snapshotAtRoundSource = "headless:buffer"
	rt.mu.Unlock()
	real, cancelReal := rt.SubscribeWithMode(false)
	defer cancelReal()
	assertNoBrowserActiveCallback(t, active, "browser must not take over before a baseline is committed")

	done := make(chan bool, 1)
	go func() { done <- rt.prepareInputSnapshotBaselineFrom(time.Second, real) }()
	request := receiveSnapshotRequestEvent(t, real)
	rt.SetVisibleSnapshotResponseFrom("browser composer", "browser:buffer", request.RequestID, real)
	if !receiveSnapshotResult(t, done) {
		t.Fatal("browser baseline capture should succeed")
	}
	assertNoBrowserActiveCallback(t, active, "capturing alone must not stop the old round owner")

	rt.MarkStructuredInputActivity("next round")
	select {
	case got := <-active:
		if got != "sess-browser-baseline-takeover" {
			t.Fatalf("active session = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("committing a browser-owned round should stop the old headless renderer")
	}
}

func assertNoBrowserActiveCallback(t *testing.T, active <-chan string, message string) {
	t.Helper()
	select {
	case sessionID := <-active:
		t.Fatalf("%s: callback for %q", message, sessionID)
	case <-time.After(50 * time.Millisecond):
	}
}

func receiveSnapshotRequestEvent(t *testing.T, ch <-chan RuntimeEvent) RuntimeEvent {
	t.Helper()
	select {
	case event := <-ch:
		if event.Type != RuntimeEventSnapshotRequest || event.RequestID == "" {
			t.Fatalf("unexpected snapshot event: %#v", event)
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot request")
		return RuntimeEvent{}
	}
}

func receiveSnapshotResult(t *testing.T, done <-chan bool) bool {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for snapshot request result")
		return false
	}
}

func assertNoSnapshotRequestEvent(t *testing.T, ch <-chan RuntimeEvent, message string) {
	t.Helper()
	select {
	case event := <-ch:
		t.Fatalf("%s: %#v", message, event)
	default:
	}
}
