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

func TestRuntimeSessionStripsBracketedPasteControlsForNotificationAnchor(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("\x1b[200~今天天气怎么样\x1b[201~\r")
	if rt.lastInputText != "今天天气怎么样" {
		t.Fatalf("lastInputText = %q, want clean pasted command", rt.lastInputText)
	}
}

func TestRuntimeSessionStripsSS3ControlsForNotificationAnchor(t *testing.T) {
	rt := &RuntimeSession{manager: NewManager(nil, nil)}
	rt.MarkInputActivity("\x1bOAImplement {feature}\r")
	if rt.lastInputText != "Implement {feature}" {
		t.Fatalf("lastInputText = %q, want clean command", rt.lastInputText)
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
