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
	resetLarkRegistryForTest()
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
	if !sessions[0].NotifyOnWaiting {
		t.Fatalf("lark-created session should enable notifications by default: %#v", sessions[0])
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
	resetLarkRegistryForTest()
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
	if !sessions[0].NotifyOnWaiting {
		t.Fatalf("P1 lark-created session should enable notifications by default: %#v", sessions[0])
	}
}

func TestLarkReplyBridgeFallbackSessionEnablesNotifications(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, &CommandAgentConfig{}, t.TempDir())

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-fallback", "", "", "text", `{"text":"echo no explicit session"}`)); err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || !sessions[0].NotifyOnWaiting {
		t.Fatalf("fallback lark session should enable notifications by default: %#v", sessions)
	}
}

func TestLarkReplyBridgeCurrentRoundCommandRepliesWithoutWritingTerminal(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, &CommandAgentConfig{}, t.TempDir())
	var replies []string
	bridge.replyText = func(_ context.Context, messageID string, text string) error {
		replies = append(replies, messageID+":"+text)
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-c", "", "", "text", `{"text":"开始 C会话"}`)); err != nil {
		t.Fatal(err)
	}
	rt, ok := manager.GetRuntime("sess-1")
	if !ok {
		t.Fatal("expected sess-1 runtime")
	}
	rt.MarkInputActivity("今天天气怎么样\r")
	rt.SetVisibleSnapshot(strings.Join([]string{
		"> 今天天气怎么样",
		"• 你想查哪个城市的天气？",
		"比如：上海、北京、纽约。",
	}, "\n"))

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-current", "m-start-c", "", "text", `{"text":"/c"}`)); err != nil {
		t.Fatal(err)
	}
	if got := launcher.terminals[0].writes(); strings.Contains(got, "/c") {
		t.Fatalf("/c should not be sent to terminal, writes: %q", got)
	}
	if len(replies) != 1 {
		t.Fatalf("expected one lark reply, got %#v", replies)
	}
	if !strings.Contains(replies[0], "> 今天天气怎么样") || !strings.Contains(replies[0], "你想查哪个城市") {
		t.Fatalf("reply did not include current round content: %#v", replies)
	}
}

func TestLarkReplyBridgeCurrentRoundCommandWithoutSessionDoesNotCreateTerminal(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, &CommandAgentConfig{}, t.TempDir())
	var reply string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		reply = text
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-current-missing", "", "", "text", `{"text":"/c"}`)); err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 0 {
		t.Fatalf("/c without a session should not create a terminal, got %d", len(launcher.terminals))
	}
	if reply != "未找到会话" {
		t.Fatalf("reply = %q, want 未找到会话", reply)
	}
}

func resetLarkRegistryForTest() {
	defaultLarkMessageRegistry.mu.Lock()
	defer defaultLarkMessageRegistry.mu.Unlock()
	defaultLarkMessageRegistry.messageToSession = make(map[string]string)
	defaultLarkMessageRegistry.latestSessionID = ""
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
