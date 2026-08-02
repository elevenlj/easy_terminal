package session

import "testing"

func TestRuntimeSessionAccumulatesCharacterInputForNotificationAnchor(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	for _, chunk := range []string{"I", "m", "p", "l", "e", "m", "e", "n", "t", " ", "{", "f", "e", "a", "t", "u", "r", "e", "}", "\r"} {
		rt.MarkInputActivity(chunk)
	}
	if rt.lastInputText != "Implement {feature}" {
		t.Fatalf("lastInputText = %q, want full command", rt.lastInputText)
	}
}

func TestRuntimeSessionAccumulatesComposerInputForNotificationAnchor(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("Run /review on my current changes\r")
	if rt.lastInputText != "Run /review on my current changes" {
		t.Fatalf("lastInputText = %q, want full composer command", rt.lastInputText)
	}
}

func TestRuntimeSessionKeepsStructuredMultilineInputForNotificationAnchor(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	input := "不要每次都把\nmemory的内容输出出来\n好吗"
	rt.MarkStructuredInputActivity(input)
	if rt.lastInputText != input {
		t.Fatalf("lastInputText = %q, want full multiline input", rt.lastInputText)
	}
}

func TestRuntimeSessionStripsBracketedPasteControlsForNotificationAnchor(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("\x1b[200~今天天气怎么样\x1b[201~\r")
	if rt.lastInputText != "今天天气怎么样" {
		t.Fatalf("lastInputText = %q, want clean pasted command", rt.lastInputText)
	}
}

func TestRuntimeSessionDoesNotGuessAnchorAfterHistoryNavigation(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("\x1bOAImplement {feature}\r")
	if rt.lastInputText != "" {
		t.Fatalf("history navigation makes the edited line unknowable; anchor must fail closed, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionTracksCursorEditingForNotificationAnchor(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("helo")
	rt.MarkInputActivity("\x1b[D")
	rt.MarkInputActivity("l\r")
	if rt.lastInputText != "hello" {
		t.Fatalf("left-arrow insertion should preserve the submitted line, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionTracksCommonControlKeyEditing(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("wrong text")
	rt.MarkInputActivity("\x15right") // Ctrl-U then replacement text.
	rt.MarkInputActivity("\x01say ")  // Ctrl-A then prefix insertion.
	rt.MarkInputActivity("\r")
	if rt.lastInputText != "say right" {
		t.Fatalf("common line-editor controls should produce an exact anchor, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionKeepsBracketedMultilinePasteAsOneInput(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("\x1b[200~first line\nsecond line\x1b[201~\r")
	if rt.lastInputText != "first line\nsecond line" {
		t.Fatalf("bracketed multiline paste should remain one submitted anchor, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionFailsClosedAfterTabCompletion(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("git che\t\r")
	if rt.lastInputText != "" {
		t.Fatalf("tab completion result is unknown and must not become a guessed anchor, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionKeepsLastSubmittedCommandDuringTUINavigation(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("/model\r")
	rt.MarkInputActivity("\x1b[B")
	if rt.lastInputText != "/model" {
		t.Fatalf("menu navigation before a new submission should keep the command context, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionUsesOrderedBrowserInputBaseline(t *testing.T) {
	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		session:  Session{ID: "sess-input-baseline", Status: StatusWaiting, Live: true},
		terminal: term,
	}
	if err := rt.WriteInputWithSnapshotBaseline("typed command\r", "old output\n› typed command", "browser:buffer"); err != nil {
		t.Fatal(err)
	}
	if rt.snapshotAtRoundStart != "old output\n› typed command" || !rt.snapshotAtRoundStartSet {
		t.Fatalf("raw Enter should bind the exact browser baseline, got %q", rt.snapshotAtRoundStart)
	}
	if rt.snapshotAtRoundVersion != 1 {
		t.Fatalf("baseline snapshot version = %d, want 1", rt.snapshotAtRoundVersion)
	}
}

func TestRuntimeSessionWriteInputTracksEditingKeysAcrossFrames(t *testing.T) {
	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		terminal: term,
		session:  Session{ID: "sess-edit-frames", Status: StatusWaiting, Live: true},
	}
	for _, frame := range []string{"helo", "\x1b[D", "l", "\x1b[3~", "o", "\r"} {
		if err := rt.WriteInput(frame); err != nil {
			t.Fatal(err)
		}
	}
	if rt.lastInputText != "hello" {
		t.Fatalf("per-key WebSocket frames should reconstruct the edited command, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionWriteInputFailsClosedForHistoryFrame(t *testing.T) {
	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		terminal: term,
		session:  Session{ID: "sess-history-frame", Status: StatusWaiting, Live: true},
	}
	if err := rt.WriteInput("\x1b[A"); err != nil {
		t.Fatal(err)
	}
	if got := rt.Snapshot().Status; got != StatusWaiting {
		t.Fatalf("history navigation alone must not change state, got %s", got)
	}
	if err := rt.WriteInput("\r"); err != nil {
		t.Fatal(err)
	}
	if rt.lastInputText != "" {
		t.Fatalf("unknown history-selected command must not become a guessed anchor, got %q", rt.lastInputText)
	}
}

func TestRuntimeSessionNavigationInputDoesNotLeaveWaiting(t *testing.T) {
	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		terminal: term,
		session:  Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	if err := rt.WriteInput("\x1b[A"); err != nil {
		t.Fatal(err)
	}
	if got := rt.Snapshot().Status; got != StatusWaiting {
		t.Fatalf("navigation input should not change waiting status, got %s", got)
	}
	if got := term.writes(); got != "\x1b[A" {
		t.Fatalf("navigation input should still be written to terminal, got %q", got)
	}
}

func TestRuntimeSessionPrintableInputLeavesWaiting(t *testing.T) {
	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		terminal: term,
		session:  Session{ID: "sess-1", Name: "A", Status: StatusWaiting, Live: true},
	}
	if err := rt.WriteInput("hello"); err != nil {
		t.Fatal(err)
	}
	if got := rt.Snapshot().Status; got != StatusRunning {
		t.Fatalf("printable input should change waiting status to running, got %s", got)
	}
}
