package session

import (
	"strconv"
	"strings"
	"testing"
)

func anchorMetadataSource(epoch string, bufferType string, atCapacity bool, guardActive bool) string {
	return anchorMetadataSourceWithBaseAndLine("browser:buffer", epoch, bufferType, atCapacity, guardActive, 10)
}

func anchorMetadataSourceWithBaseAndLine(base string, epoch string, bufferType string, atCapacity bool, guardActive bool, guardLine int) string {
	return base + ";continuity_version=2;render_epoch=" + epoch +
		";buffer_type=" + bufferType +
		";buffer_at_capacity=" + boolString(atCapacity) +
		";anchor_guard_active=" + boolString(guardActive) +
		";anchor_guard_line=" + strconv.Itoa(guardLine)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestPreviousTextAnchorsAllowedRequiresRendererContinuityMetadata(t *testing.T) {
	firstResponder := make(chan RuntimeEvent)
	secondResponder := make(chan RuntimeEvent)
	normalEpochSeven := anchorMetadataSource("7", "normal", false, true)

	tests := []struct {
		name              string
		previousSource    string
		currentSource     string
		previousResponder chan RuntimeEvent
		currentResponder  chan RuntimeEvent
		previousCols      uint16
		currentCols       uint16
		want              bool
	}{
		{
			name:              "real responder without metadata is rejected",
			previousSource:    "browser:buffer",
			currentSource:     "browser:buffer",
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "same normal non-capacity epoch is allowed",
			previousSource:    normalEpochSeven,
			currentSource:     normalEpochSeven,
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
			want:              true,
		},
		{
			name:              "continuity v1 is rejected",
			previousSource:    strings.Replace(normalEpochSeven, "continuity_version=2", "continuity_version=1", 1),
			currentSource:     strings.Replace(normalEpochSeven, "continuity_version=2", "continuity_version=1", 1),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "missing continuity version is rejected",
			previousSource:    strings.Replace(normalEpochSeven, "continuity_version=2;", "", 1),
			currentSource:     strings.Replace(normalEpochSeven, "continuity_version=2;", "", 1),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "inactive guard is rejected before capacity",
			previousSource:    anchorMetadataSource("7", "normal", false, false),
			currentSource:     anchorMetadataSource("7", "normal", false, false),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "negative guard line is rejected",
			previousSource:    anchorMetadataSourceWithBaseAndLine("browser:buffer", "7", "normal", false, true, -1),
			currentSource:     anchorMetadataSourceWithBaseAndLine("browser:buffer", "7", "normal", false, true, -1),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "missing guard line is rejected",
			previousSource:    strings.Replace(normalEpochSeven, ";anchor_guard_line=10", "", 1),
			currentSource:     strings.Replace(normalEpochSeven, ";anchor_guard_line=10", "", 1),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "malformed guard line is rejected",
			previousSource:    strings.Replace(normalEpochSeven, "anchor_guard_line=10", "anchor_guard_line=not-a-line", 1),
			currentSource:     strings.Replace(normalEpochSeven, "anchor_guard_line=10", "anchor_guard_line=not-a-line", 1),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "malformed cursor line is rejected",
			previousSource:    normalEpochSeven + ";cursor_line=not-a-line",
			currentSource:     normalEpochSeven + ";cursor_line=12",
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "epoch change is rejected",
			previousSource:    normalEpochSeven,
			currentSource:     anchorMetadataSource("8", "normal", false, false),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "buffer type change is rejected",
			previousSource:    normalEpochSeven,
			currentSource:     anchorMetadataSource("7", "alternate", false, false),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "same alternate buffer is rejected",
			previousSource:    anchorMetadataSource("7", "alternate", false, false),
			currentSource:     anchorMetadataSource("7", "alternate", false, false),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "capture source change is rejected",
			previousSource:    normalEpochSeven,
			currentSource:     strings.Replace(normalEpochSeven, "browser:buffer", "browser:dom", 1),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "same DOM capture source is rejected",
			previousSource:    anchorMetadataSourceWithBaseAndLine("browser:dom", "7", "normal", false, true, 10),
			currentSource:     anchorMetadataSourceWithBaseAndLine("browser:dom", "7", "normal", false, true, 10),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "zero renderer epoch is rejected",
			previousSource:    anchorMetadataSource("0", "normal", false, false),
			currentSource:     anchorMetadataSource("0", "normal", false, false),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "malformed renderer epoch is rejected",
			previousSource:    anchorMetadataSource("not-a-number", "normal", false, false),
			currentSource:     anchorMetadataSource("not-a-number", "normal", false, false),
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "column change is rejected",
			previousSource:    normalEpochSeven,
			currentSource:     normalEpochSeven,
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      120,
			currentCols:       100,
		},
		{
			name:              "missing previous column identity is rejected",
			previousSource:    normalEpochSeven,
			currentSource:     normalEpochSeven,
			previousResponder: firstResponder,
			currentResponder:  firstResponder,
			previousCols:      0,
			currentCols:       120,
		},
		{
			name:              "responder change is rejected",
			previousSource:    normalEpochSeven,
			currentSource:     normalEpochSeven,
			previousResponder: firstResponder,
			currentResponder:  secondResponder,
			previousCols:      120,
			currentCols:       120,
		},
		{
			name:              "legacy nil responder fixture remains compatible",
			previousSource:    "browser:buffer",
			currentSource:     "browser:buffer",
			previousResponder: nil,
			currentResponder:  nil,
			previousCols:      120,
			currentCols:       120,
			want:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &RuntimeSession{
				snapshotAtRoundStartSet:  true,
				snapshotAtRoundSource:    tt.previousSource,
				snapshotAtRoundResponder: tt.previousResponder,
				snapshotAtRoundCols:      tt.previousCols,
				visibleSnapshotSource:    tt.currentSource,
				visibleSnapshotResponder: tt.currentResponder,
				visibleSnapshotCols:      tt.currentCols,
			}
			if got := rt.previousTextAnchorsAllowedLocked(); got != tt.want {
				t.Fatalf("previousTextAnchorsAllowedLocked() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreviousTextAnchorsAllowedAtCapacityRequiresActiveGuard(t *testing.T) {
	responder := make(chan RuntimeEvent)
	for _, tt := range []struct {
		name             string
		previousCapacity bool
		currentCapacity  bool
		previousGuard    bool
		currentGuard     bool
		want             bool
	}{
		{name: "inactive guard is rejected", previousCapacity: true, currentCapacity: true},
		{name: "active guard is allowed", previousCapacity: true, currentCapacity: true, previousGuard: true, currentGuard: true, want: true},
		{name: "capacity transition with active guard is allowed", currentCapacity: true, previousGuard: true, currentGuard: true, want: true},
		{name: "capacity transition without baseline guard is rejected", currentCapacity: true, currentGuard: true},
		{name: "capacity cannot decrease within one epoch", previousCapacity: true, previousGuard: true, currentGuard: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			previousSource := anchorMetadataSource("9", "normal", tt.previousCapacity, tt.previousGuard)
			currentSource := anchorMetadataSource("9", "normal", tt.currentCapacity, tt.currentGuard)
			rt := &RuntimeSession{
				snapshotAtRoundStartSet:  true,
				snapshotAtRoundSource:    previousSource,
				snapshotAtRoundResponder: responder,
				snapshotAtRoundCols:      120,
				visibleSnapshotSource:    currentSource,
				visibleSnapshotResponder: responder,
				visibleSnapshotCols:      120,
			}
			if got := rt.previousTextAnchorsAllowedLocked(); got != tt.want {
				t.Fatalf("previousTextAnchorsAllowedLocked() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNotifyTextAnchorPolicyExposesV2GuardIdentity(t *testing.T) {
	responder := make(chan RuntimeEvent)
	rt := &RuntimeSession{
		snapshotAtRoundStartSet:  true,
		snapshotAtRoundSource:    anchorMetadataSourceWithBaseAndLine("browser:buffer", "17", "normal", false, true, 23) + ";cursor_line=27",
		snapshotAtRoundResponder: responder,
		snapshotAtRoundCols:      120,
		visibleSnapshotSource:    anchorMetadataSourceWithBaseAndLine("browser:buffer", "17", "normal", true, true, 31),
		visibleSnapshotResponder: responder,
		visibleSnapshotCols:      120,
	}

	policy := rt.notifyTextAnchorPolicyLocked()
	if !policy.allowed || !policy.enforceIdentity || policy.previousGuardLine != 23 || policy.currentGuardLine != 31 || policy.previousCursorLine != 27 {
		t.Fatalf("unexpected v2 anchor policy: %#v", policy)
	}

	legacy := &RuntimeSession{
		snapshotAtRoundStartSet: true,
		snapshotAtRoundSource:   "legacy",
		visibleSnapshotSource:   "legacy",
	}
	legacyPolicy := legacy.notifyTextAnchorPolicyLocked()
	if !legacyPolicy.allowed || legacyPolicy.enforceIdentity || legacyPolicy.previousGuardLine != -1 || legacyPolicy.currentGuardLine != -1 {
		t.Fatalf("unexpected legacy anchor policy: %#v", legacyPolicy)
	}
}

func TestManagerRejectsQuotedPreviousTailWhenCapacityGuardIsInactive(t *testing.T) {
	anchor := []string{
		"distinctive guarded boundary one alpha beta",
		"distinctive guarded boundary two gamma delta",
		"distinctive guarded boundary three epsilon zeta",
		"distinctive guarded boundary four eta theta",
		"distinctive guarded boundary five iota kappa",
	}
	previous := strings.Join(anchor, "\n")
	visible := strings.Join(append(append([]string{
		"OLD_HISTORY_MUST_NOT_LEAK",
		"• the reply quotes the evicted previous tail below",
	}, anchor...), "• CURRENT_REPLY_AFTER_UNTRUSTED_QUOTE"), "\n")
	responder := make(chan RuntimeEvent)
	unsafeSource := anchorMetadataSource("12", "normal", true, false)
	rt := &RuntimeSession{
		lastInputText:            "input echo missing from current snapshot",
		snapshotAtRoundStart:     previous,
		snapshotAtRoundStartSet:  true,
		snapshotAtRoundSource:    unsafeSource,
		snapshotAtRoundResponder: responder,
		snapshotAtRoundCols:      120,
		visibleSnapshot:          visible,
		visibleSnapshotSource:    unsafeSource,
		visibleSnapshotResponder: responder,
		visibleSnapshotCols:      120,
	}

	if rt.previousTextAnchorsAllowedLocked() {
		t.Fatal("capacity metadata without an active guard must disable previous text anchors")
	}
	if got := rt.currentNotifyContentLocked(); got != "" {
		t.Fatalf("an unguarded reply quote must not produce notification content: %q", got)
	}
}

func TestManagerRejectsMarkdownQuoteAsInputWithoutBaselineCursorProof(t *testing.T) {
	const input = "与本轮输入完全相同"
	responder := make(chan RuntimeEvent)
	previousSource := anchorMetadataSourceWithBaseAndLine("browser:buffer", "21", "normal", false, true, 0) + ";cursor_line=-1"
	currentSource := anchorMetadataSourceWithBaseAndLine("browser:buffer", "21", "normal", false, true, 0) + ";cursor_line=1"
	rt := &RuntimeSession{
		lastInputText:            input,
		snapshotAtRoundStart:     "> " + input,
		snapshotAtRoundStartSet:  true,
		snapshotAtRoundSource:    previousSource,
		snapshotAtRoundResponder: responder,
		snapshotAtRoundCols:      120,
		visibleSnapshot:          "> " + input + "\n• HISTORICAL_QUOTE_MUST_NOT_AUTHORIZE_THIS_REPLY",
		visibleSnapshotSource:    currentSource,
		visibleSnapshotResponder: responder,
		visibleSnapshotCols:      120,
	}

	if got := rt.currentNotifyContentLocked(); got != "" {
		t.Fatalf("a baseline Markdown quote without cursor proof must fail closed: %q", got)
	}
}

func TestManagerAcceptsStructuredInputAtBaselineCursor(t *testing.T) {
	const input = "来自飞书的结构化输入"
	responder := make(chan RuntimeEvent)
	previousSource := anchorMetadataSourceWithBaseAndLine("browser:buffer", "22", "normal", false, true, 0) + ";cursor_line=0"
	currentSource := anchorMetadataSourceWithBaseAndLine("browser:buffer", "22", "normal", false, true, 0) + ";cursor_line=1"
	rt := &RuntimeSession{
		lastInputText:            input,
		snapshotAtRoundStart:     "> " + input,
		snapshotAtRoundStartSet:  true,
		snapshotAtRoundSource:    previousSource,
		snapshotAtRoundResponder: responder,
		snapshotAtRoundCols:      120,
		visibleSnapshot:          "> " + input + "\n• CURRENT_REPLY_ONLY",
		visibleSnapshotSource:    currentSource,
		visibleSnapshotResponder: responder,
		visibleSnapshotCols:      120,
	}

	if got := rt.currentNotifyContentLocked(); got != "• CURRENT_REPLY_ONLY" {
		t.Fatalf("a structured composer proven by the Enter-time cursor must remain usable: %q", got)
	}
}
