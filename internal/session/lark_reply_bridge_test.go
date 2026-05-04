package session

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

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
	var browserMu sync.Mutex
	var browserRequests []string
	manager := NewManager(nil, launcher, WithBrowserNeeded(func(sessionID string) {
		browserMu.Lock()
		defer browserMu.Unlock()
		browserRequests = append(browserRequests, sessionID)
	}))
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
	waitForBrowserRequest(t, &browserMu, &browserRequests, "sess-1")

	err = bridge.HandleP2MessageReceive(context.Background(), p2Message("m-follow", "m-start", "", "text", `{"text":"echo from lark"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := launcher.terminals[0].writes()
	if !strings.Contains(got, "echo from lark\r") {
		t.Fatalf("terminal did not receive followup input: %q", got)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) < 2 || parts[len(parts)-2] != "echo from lark" || parts[len(parts)-1] != "\r" {
		t.Fatalf("lark followup should submit text and enter separately, got %#v", parts)
	}
	waitForBrowserRequest(t, &browserMu, &browserRequests, "sess-1")
}

func TestLarkReplyBridgePipelineRunsNextCommandAfterNotification(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, &CommandAgentConfig{}, t.TempDir())

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-pipe", "", "", "text", `{"text":"开始 Pipe会话"}`)); err != nil {
		t.Fatal(err)
	}
	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-pipeline", "m-start-pipe", "", "text", `{"text":"pwd | cd /tmp | pwd"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) < 2 || parts[len(parts)-2] != "pwd" || parts[len(parts)-1] != "\r" {
		t.Fatalf("first pipeline command should be submitted immediately, got %#v", parts)
	}
	if strings.Contains(launcher.terminals[0].writes(), "cd /tmp") {
		t.Fatalf("later pipeline commands should wait for notification, writes: %q", launcher.terminals[0].writes())
	}

	bridge.OnNotificationSent("sess-1")
	parts = launcher.terminals[0].writeParts()
	if len(parts) < 4 || parts[len(parts)-2] != "cd /tmp" || parts[len(parts)-1] != "\r" {
		t.Fatalf("second pipeline command should run after notification, got %#v", parts)
	}
	if strings.Contains(launcher.terminals[0].writes(), "pwdpwd") {
		t.Fatalf("pipeline commands should be submitted as separate turns, writes: %q", launcher.terminals[0].writes())
	}

	bridge.OnNotificationSent("sess-1")
	parts = launcher.terminals[0].writeParts()
	if len(parts) < 6 || parts[len(parts)-2] != "pwd" || parts[len(parts)-1] != "\r" {
		t.Fatalf("third pipeline command should run after next notification, got %#v", parts)
	}
}

func TestSplitLarkPipelineSupportsEscapedPipe(t *testing.T) {
	got := splitLarkPipeline(`echo a \| b | pwd`)
	want := []string{"echo a | b", "pwd"}
	if len(got) != len(want) {
		t.Fatalf("split length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("part %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitLarkPipelineSupportsFullWidthPipe(t *testing.T) {
	got := splitLarkPipeline("开始 测试 ｜ pwd")
	want := []string{"开始 测试", "pwd"}
	if len(got) != len(want) {
		t.Fatalf("split length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("part %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLarkReplyBridgeStartPipelineWithFullWidthPipe(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, &CommandAgentConfig{}, t.TempDir())

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-wide-pipe", "", "", "text", `{"text":"开始 测试 ｜ pwd"}`)); err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "测试" {
		t.Fatalf("start pipeline should use only first segment as session name, got %#v", sessions)
	}
	if got := launcher.terminals[0].writes(); strings.Contains(got, "pwd") {
		t.Fatalf("queued command should wait for first notification, writes: %q", got)
	}

	bridge.OnNotificationSent("sess-1")
	parts := launcher.terminals[0].writeParts()
	if len(parts) < 2 || parts[len(parts)-2] != "pwd" || parts[len(parts)-1] != "\r" {
		t.Fatalf("queued start pipeline command should run after notification, got %#v", parts)
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

func waitForBrowserRequest(t *testing.T, mu *sync.Mutex, requests *[]string, sessionID string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		mu.Lock()
		for _, got := range *requests {
			if got == sessionID {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("expected browser request for %s, got %#v", sessionID, *requests)
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
	parts  []string
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
	t.parts = append(t.parts, string(p))
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

func (t *recordingTerminal) writeParts() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]string, len(t.parts))
	copy(cp, t.parts)
	return cp
}

type blockingWaiter struct{}

func (blockingWaiter) Wait() error {
	select {}
}
