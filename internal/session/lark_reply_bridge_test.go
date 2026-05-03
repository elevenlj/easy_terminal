package session

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestExtractLarkMessageText(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		messageType string
		want        string
	}{
		{name: "text", content: `{"text":"开始 会话A"}`, messageType: "text", want: "开始 会话A"},
		{name: "post", content: `{"content":[[{"tag":"text","text":"echo "},{"tag":"a","text":"hello"}]]}`, messageType: "post", want: "echo hello"},
		{name: "raw fallback", content: `开始 会话B`, messageType: "text", want: "开始 会话B"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractLarkMessageText(tt.content, tt.messageType); got != tt.want {
				t.Fatalf("extractLarkMessageText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLarkReplyBridgeRoutesP2StartAndFollowup(t *testing.T) {
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, &CommandAgentConfig{}, t.TempDir())

	err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start", "", "", "text", `{"text":"开始 飞书会话"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 1 {
		t.Fatalf("expected one terminal, got %d", len(launcher.terminals))
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "飞书会话" {
		t.Fatalf("unexpected sessions: %#v", sessions)
	}

	err = bridge.HandleP2MessageReceive(context.Background(), p2Message("m-follow", "m-start", "", "text", `{"text":"echo from lark"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := launcher.terminals[0].writes()
	if !strings.Contains(got, "echo from lark\r") {
		t.Fatalf("terminal did not receive followup input: %q", got)
	}
}

func TestLarkReplyBridgeRoutesP1Start(t *testing.T) {
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, &CommandAgentConfig{}, t.TempDir())

	err := bridge.HandleP1MessageReceive(context.Background(), &larkim.P1MessageReceiveV1{
		Event: &larkim.P1MessageReceiveV1Data{
			OpenMessageID:    "p1-start",
			TextWithoutAtBot: "新会话 P1会话",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "P1会话" {
		t.Fatalf("unexpected sessions: %#v", sessions)
	}
}

func p2Message(messageID, parentID, rootID, messageType, content string) *larkim.P2MessageReceiveV1 {
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageId:   strPtr(messageID),
				ParentId:    strPtr(parentID),
				RootId:      strPtr(rootID),
				MessageType: strPtr(messageType),
				Content:     strPtr(content),
			},
		},
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type recordingLauncher struct {
	mu        sync.Mutex
	terminals []*recordingTerminal
}

func (l *recordingLauncher) Launch(context.Context) (ProcessHandle, error) {
	term := &recordingTerminal{readCh: make(chan []byte)}
	l.mu.Lock()
	l.terminals = append(l.terminals, term)
	l.mu.Unlock()
	return recordingHandle{terminal: term}, nil
}

type recordingHandle struct {
	terminal *recordingTerminal
}

func (h recordingHandle) Terminal() Terminal { return h.terminal }
func (h recordingHandle) Process() Waiter    { return blockingWaiter{} }

type recordingTerminal struct {
	mu     sync.Mutex
	buf    strings.Builder
	readCh chan []byte
	closed bool
}

func (t *recordingTerminal) Read(p []byte) (int, error) {
	b, ok := <-t.readCh
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func (t *recordingTerminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.Write(p)
}

func (t *recordingTerminal) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		close(t.readCh)
		t.closed = true
	}
	return nil
}

func (t *recordingTerminal) Resize(cols, rows uint16) error { return nil }

func (t *recordingTerminal) writes() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

type blockingWaiter struct{}

func (blockingWaiter) Wait() error {
	select {}
}
