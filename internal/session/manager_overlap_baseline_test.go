package session

import (
	"strings"
	"testing"
)

func TestOverlappingRunningInputRebasesPreparedRendererBaseline(t *testing.T) {
	responder := make(chan RuntimeEvent)
	oldSource := anchorMetadataSourceWithBaseAndLine("browser:buffer", "21", "normal", false, true, 0)
	newSource := anchorMetadataSourceWithBaseAndLine("browser:buffer", "22", "normal", false, true, 0)
	preparedBaseline := strings.Join([]string{
		"OLD_HISTORY_MUST_NOT_LEAK",
		"• partial answer from the interrupted round",
	}, "\n")
	rt := &RuntimeSession{
		manager:                     NewManager(nil, nil),
		session:                     Session{ID: "sess-overlap-baseline", Status: StatusRunning, Live: true},
		lastInputText:               "second question",
		lastNotifiedMessageID:       "card-second",
		notificationRunning:         true,
		notificationWindowInputText: "second question",
		snapshotAtRoundStart:        "older baseline that belongs to the previous guard",
		snapshotAtRoundSource:       oldSource,
		snapshotAtRoundResponder:    responder,
		snapshotAtRoundCols:         120,
		snapshotAtRoundVersion:      1,
		snapshotAtRoundStartSet:     true,
		visibleSnapshot:             preparedBaseline,
		visibleSnapshotSource:       newSource,
		visibleSnapshotResponder:    responder,
		visibleSnapshotCols:         120,
		visibleSnapshotVersion:      2,
		frozenNotificationMessages:  make(map[string]struct{}),
	}

	rt.MarkStructuredInputActivity("third question")

	rt.mu.Lock()
	if !rt.notificationMessageFrozenLocked("card-second") {
		rt.mu.Unlock()
		t.Fatal("the previous running card must still be frozen")
	}
	if rt.snapshotAtRoundStart != preparedBaseline || rt.snapshotAtRoundSource != newSource ||
		rt.snapshotAtRoundResponder != responder || rt.snapshotAtRoundCols != 120 || rt.snapshotAtRoundVersion != 2 {
		gotSnapshot := rt.snapshotAtRoundStart
		gotSource := rt.snapshotAtRoundSource
		gotVersion := rt.snapshotAtRoundVersion
		rt.mu.Unlock()
		t.Fatalf("overlap must bind the prepared v2 baseline: snapshot=%q source=%q version=%d", gotSnapshot, gotSource, gotVersion)
	}
	if rt.notificationWindowInputText != "" {
		got := rt.notificationWindowInputText
		rt.mu.Unlock()
		t.Fatalf("a new renderer baseline must start a fresh notification window, got %q", got)
	}
	rt.visibleSnapshot = preparedBaseline + "\n› third question\n• final answer for the third question"
	rt.visibleSnapshotSource = newSource
	rt.visibleSnapshotResponder = responder
	rt.visibleSnapshotCols = 120
	rt.visibleSnapshotVersion = 3
	rt.session.Status = StatusWaiting
	content := rt.currentNotifyContentLocked()
	policy := rt.notifyTextAnchorPolicyLocked()
	rt.mu.Unlock()

	if !policy.allowed || !policy.enforceIdentity {
		t.Fatalf("rebased overlap should retain renderer identity, got %#v", policy)
	}
	if content != "• final answer for the third question" {
		t.Fatalf("new card must contain only the new round reply, got %q", content)
	}
	if strings.Contains(content, "OLD_HISTORY_MUST_NOT_LEAK") || strings.Contains(content, "partial answer") {
		t.Fatalf("rebased overlap leaked the previous window: %q", content)
	}
}
