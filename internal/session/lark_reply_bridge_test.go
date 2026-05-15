package session

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
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

func TestExtractLarkIncomingMessageWithPostAttachments(t *testing.T) {
	got := extractLarkIncomingMessage(`{"content":[[{"tag":"img","image_key":"img_a"},{"tag":"text","text":"请分析"},{"tag":"file","file_key":"file_a","file_name":"报告.pdf"}]]}`, "post")
	if got.Text != "请分析" {
		t.Fatalf("text = %q, want 请分析", got.Text)
	}
	if len(got.Attachments) != 2 {
		t.Fatalf("attachments length = %d, want 2: %#v", len(got.Attachments), got.Attachments)
	}
	if got.Attachments[0].Kind != "image" || got.Attachments[0].Key != "img_a" {
		t.Fatalf("first attachment = %#v, want image img_a", got.Attachments[0])
	}
	if got.Attachments[1].Kind != "file" || got.Attachments[1].Key != "file_a" || got.Attachments[1].Name != "报告.pdf" {
		t.Fatalf("second attachment = %#v, want file file_a", got.Attachments[1])
	}
}

func TestLarkReplyBridgeAddsProcessingReactionForP2Message(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	var reactions []string
	bridge.addReaction = func(_ context.Context, messageID string, emoji string) error {
		reactions = append(reactions, messageID+":"+emoji)
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-react", "", "", "text", `{"text":"echo from lark"}`)); err != nil {
		t.Fatal(err)
	}
	if len(reactions) != 1 || reactions[0] != "m-react:"+larkProcessingReactionEmoji {
		t.Fatalf("expected processing reaction on incoming message, got %#v", reactions)
	}
	if len(launcher.terminals) != 1 {
		t.Fatalf("message should still route to terminal, got %d terminals", len(launcher.terminals))
	}
	if got := launcher.terminals[0].writes(); !strings.Contains(got, PrepareStructuredInput("echo from lark")) {
		t.Fatalf("terminal should receive submitted input despite reaction, got %q", got)
	}
}

func TestLarkReplyBridgeContinuesWhenProcessingReactionFails(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.addReaction = func(context.Context, string, string) error {
		return errors.New("missing reaction permission")
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-react-fail", "", "", "text", `{"text":"echo from lark"}`)); err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 1 {
		t.Fatalf("reaction failure should not block routing, got %d terminals", len(launcher.terminals))
	}
	if got := launcher.terminals[0].writes(); !strings.Contains(got, PrepareStructuredInput("echo from lark")) {
		t.Fatalf("terminal should receive submitted input despite reaction failure, got %q", got)
	}
}

func TestLarkReplyBridgeStartCreatesDedicatedChat(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.createChat = func(_ context.Context, sessionID, name, ownerOpenID string) (string, error) {
		if sessionID != "sess-1" || name != "手机会话" || ownerOpenID != "ou-user" {
			t.Fatalf("unexpected create chat args: session=%q name=%q owner=%q", sessionID, name, ownerOpenID)
		}
		return "oc-chat-1", nil
	}
	var chatMessages []string
	bridge.sendChatText = func(_ context.Context, chatID, text string) error {
		chatMessages = append(chatMessages, chatID+":"+text)
		return nil
	}

	err := bridge.HandleP2MessageReceive(context.Background(), p2MessageWithChat("m-start-chat", "", "", "text", `{"text":"开始 手机会话"}`, "p2p", "oc-main", "ou-user"))
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].LarkChatID != "oc-chat-1" {
		t.Fatalf("created session should bind lark chat, got %#v", sessions)
	}
	if got, ok := defaultLarkMessageRegistry.lookupChat("oc-chat-1"); !ok || got != "sess-1" {
		t.Fatalf("registry chat lookup = %q,%v; want sess-1,true", got, ok)
	}
	if len(chatMessages) != 1 || !strings.Contains(chatMessages[0], "oc-chat-1:已创建会话 手机会话") {
		t.Fatalf("expected intro message to session chat, got %#v", chatMessages)
	}
}

func TestLarkReplyBridgeDoesNotNotifyBeforeDedicatedChatBind(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	notifier := &recordingNotifier{messageID: "bot-card"}
	manager := NewManager(
		nil,
		launcher,
		WithNotifier(notifier),
		WithWaitingTransitionDelays(10*time.Millisecond, 10*time.Millisecond),
		WithNotificationUpdateCoalesce(0),
	)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.createChat = func(_ context.Context, sessionID, name, ownerOpenID string) (string, error) {
		if len(launcher.terminals) != 1 {
			t.Fatalf("expected terminal before chat creation, got %d", len(launcher.terminals))
		}
		launcher.terminals[0].readCh <- []byte("eleven ~ > ")
		time.Sleep(80 * time.Millisecond)
		return "oc-chat-1", nil
	}

	err := bridge.HandleP2MessageReceive(context.Background(), p2MessageWithChat("m-start-chat-bind", "", "", "text", `{"text":"开始 手机会话"}`, "p2p", "oc-main", "ou-user"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	for _, note := range notifier.notes() {
		if note.ChatID != "oc-chat-1" {
			t.Fatalf("notification should wait until dedicated chat is bound, got %#v", note)
		}
	}
}

func TestLarkReplyBridgeDoesNotFallbackToDefaultReceiverWhenDedicatedChatMissing(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	notifier := &recordingNotifier{messageID: "bot-card"}
	manager := NewManager(nil, launcher, WithNotifier(notifier), WithWaitingTransitionDelays(10*time.Millisecond, 10*time.Millisecond), WithNotificationUpdateCoalesce(0))
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.createChat = func(context.Context, string, string, string) (string, error) {
		return "", nil
	}

	err := bridge.HandleP2MessageReceive(context.Background(), p2MessageWithChat("m-start-chat-empty", "", "", "text", `{"text":"开始 手机会话"}`, "p2p", "oc-main", "ou-user"))
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 1 {
		t.Fatalf("expected created terminal, got %d", len(launcher.terminals))
	}
	launcher.terminals[0].readCh <- []byte("eleven ~ > ")
	time.Sleep(80 * time.Millisecond)
	if notes := notifier.notes(); len(notes) != 0 {
		t.Fatalf("dedicated-chat session must not notify default receiver before chat bind, got %#v", notes)
	}
}

func TestLarkReplyBridgeUsesConfiguredChatPrefix(t *testing.T) {
	bridge := NewLarkReplyBridge("app", "secret", NewManager(nil, &recordingLauncher{}), t.TempDir())
	bridge.SetSessionChatPrefix("DEV ·")

	if got := bridge.larkSessionChatName("手机会话"); got != "DEV ·手机会话" {
		t.Fatalf("chat name = %q", got)
	}
}

func TestLarkCreateChatUUIDIsUniqueAcrossSameSessionID(t *testing.T) {
	first := larkCreateChatUUID("sess-1")
	time.Sleep(time.Nanosecond)
	second := larkCreateChatUUID("sess-1")
	if first == second {
		t.Fatalf("chat create uuid should be unique across reused session ids, got %q", first)
	}
	if !strings.HasPrefix(first, "easy-terminal-sess-1-") || !strings.HasPrefix(second, "easy-terminal-sess-1-") {
		t.Fatalf("unexpected chat create uuid format: %q %q", first, second)
	}
}

func TestLarkReplyBridgeRoutesByDedicatedChatID(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := manager.BindLarkChat(context.Background(), sess.ID, "oc-chat-a"); err != nil || !ok {
		t.Fatalf("BindLarkChat ok=%v err=%v", ok, err)
	}

	err = bridge.HandleP2MessageReceive(context.Background(), p2MessageWithChat("m-chat-input", "", "", "text", `{"text":"pwd"}`, "group", "oc-chat-a", "ou-user"))
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(launcher.terminals))
	}
	if got := launcher.terminals[0].writes(); !strings.Contains(got, PrepareStructuredInput("pwd")) {
		t.Fatalf("dedicated chat input should route to existing terminal, got %q", got)
	}
}

func TestLarkReplyBridgeIgnoresStaleDedicatedChatRegistry(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := manager.BindLarkChat(context.Background(), sess.ID, "oc-chat-a"); err != nil || !ok {
		t.Fatalf("BindLarkChat ok=%v err=%v", ok, err)
	}
	rt, ok := manager.GetRuntime(sess.ID)
	if !ok {
		t.Fatal("runtime not found")
	}
	rt.markTerminal(StatusExited, 0)
	defaultLarkMessageRegistry.rememberChat("oc-chat-a", sess.ID)

	err = bridge.HandleP2MessageReceive(context.Background(), p2MessageWithChat("m-chat-stale", "", "", "text", `{"text":"pwd"}`, "group", "oc-chat-a", "ou-user"))
	if err != nil {
		t.Fatal(err)
	}
	if got := launcher.terminals[0].writes(); strings.Contains(got, PrepareStructuredInput("pwd")) {
		t.Fatalf("stale dedicated chat should not route to exited terminal, got %q", got)
	}
	if got, ok := defaultLarkMessageRegistry.lookupChat("oc-chat-a"); ok && got == sess.ID {
		t.Fatalf("stale chat mapping should be cleared, got %q", got)
	}
}

func TestLarkReplyBridgeRoutesP1ByDedicatedChatID(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "P1A")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := manager.BindLarkChat(context.Background(), sess.ID, "oc-p1-chat"); err != nil || !ok {
		t.Fatalf("BindLarkChat ok=%v err=%v", ok, err)
	}

	err = bridge.HandleP1MessageReceive(context.Background(), &larkim.P1MessageReceiveV1{
		Event: &larkim.P1MessageReceiveV1Data{
			OpenMessageID:    "p1-chat-input",
			OpenChatID:       "oc-p1-chat",
			ChatType:         "group",
			OpenID:           "ou-user",
			TextWithoutAtBot: "echo p1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(launcher.terminals))
	}
	if got := launcher.terminals[0].writes(); !strings.Contains(got, PrepareStructuredInput("echo p1")) {
		t.Fatalf("P1 dedicated chat input should route to existing terminal, got %q", got)
	}
}

func TestLarkNotificationCardCanTargetDedicatedChat(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "A",
		Content:   "done",
		ChatID:    "oc-chat-a",
	}, "open-id", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "done") {
		t.Fatalf("card content should still render body, got %s", content)
	}
}

func TestLarkReplyBridgeImageWaitsForTextBeforeEnter(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.downloadFile = func(_ context.Context, _ string, _ string, ref larkAttachmentRef) (pendingLarkAttachment, error) {
		return pendingLarkAttachment{Kind: ref.Kind, Path: "/tmp/lark-image.png"}, nil
	}
	var replies []string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		replies = append(replies, text)
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-image", "", "", "image", `{"image_key":"img_a"}`)); err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 1 {
		t.Fatalf("expected one terminal, got %d", len(launcher.terminals))
	}
	if got := launcher.terminals[0].writes(); got != "/tmp/lark-image.png " {
		t.Fatalf("image-only message should write path without enter, got %q", got)
	}
	if len(replies) != 1 || replies[0] != "图片已上传成功，等待你的说明后执行。" {
		t.Fatalf("unexpected replies: %#v", replies)
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-image-text", "", "", "text", `{"text":"请分析这张图"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "请分析这张图") {
		t.Fatalf("followup text should append text and enter, got %#v", parts)
	}
	if got := launcher.terminals[0].writes(); strings.Count(got, "/tmp/lark-image.png") != 1 {
		t.Fatalf("pending image path should not be duplicated, writes: %q", got)
	}
}

func TestSubmitStructuredInputDelaysEnterForTUI(t *testing.T) {
	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		terminal: term,
		session:  Session{ID: "sess-1", Name: "TUI", Status: StatusWaiting, Live: true},
	}

	if err := SubmitStructuredInput(rt, "hello tui"); err != nil {
		t.Fatal(err)
	}
	parts := term.writeParts()
	if !lastSubmittedWrite(parts, "hello tui") {
		t.Fatalf("structured input should write text and enter separately, got %#v", parts)
	}
	times := term.writeTimes()
	if len(times) < 2 {
		t.Fatalf("expected text and enter write times, got %d", len(times))
	}
	if got := times[len(times)-1].Sub(times[len(times)-2]); got < structuredInputEnterDelay {
		t.Fatalf("enter should be delayed after text by at least %s, got %s", structuredInputEnterDelay, got)
	}
}

func TestSubmitStructuredInputUsesSingleCarriageReturnEnter(t *testing.T) {
	previousDelay := structuredInputEnterDelay
	structuredInputEnterDelay = 0
	defer func() {
		structuredInputEnterDelay = previousDelay
	}()

	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		terminal: term,
		session:  Session{ID: "sess-1", Name: "TUI", Status: StatusWaiting, Live: true},
	}

	if err := SubmitStructuredInput(rt, "谢谢哈"); err != nil {
		t.Fatal(err)
	}
	parts := term.writeParts()
	if len(parts) < 2 {
		t.Fatalf("expected text and enter writes, got %#v", parts)
	}
	if got := parts[len(parts)-1]; got != "\r" {
		t.Fatalf("structured input should submit with a single carriage return, got %q", got)
	}
}

func TestSubmitStructuredInputSkipsEnterForNumericInput(t *testing.T) {
	previousDelay := structuredInputEnterDelay
	structuredInputEnterDelay = 0
	defer func() {
		structuredInputEnterDelay = previousDelay
	}()

	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:  NewManager(nil, nil),
		terminal: term,
		session:  Session{ID: "sess-1", Name: "TUI", Status: StatusWaiting, Live: true},
		visibleSnapshot: strings.Join([]string{
			"Select Model and Effort",
			"Access legacy models by running codex -m <model_name>",
			"› 1. gpt-5.5 (current)",
			"2. gpt-5.4",
			"3. gpt-5.4-mini",
			"Press enter to confirm or esc to go back",
		}, "\n"),
	}

	if err := SubmitStructuredInput(rt, "1"); err != nil {
		t.Fatal(err)
	}
	parts := term.writeParts()
	if len(parts) != 1 || parts[0] != "1" {
		t.Fatalf("numeric input should write only the digit, got %#v", parts)
	}
}

func TestSubmitStructuredInputSkipsEnterForPlainNumericInput(t *testing.T) {
	previousDelay := structuredInputEnterDelay
	structuredInputEnterDelay = 0
	defer func() {
		structuredInputEnterDelay = previousDelay
	}()

	term := &recordingTerminal{readCh: make(chan []byte)}
	rt := &RuntimeSession{
		manager:         NewManager(nil, nil),
		terminal:        term,
		session:         Session{ID: "sess-1", Name: "TUI", Status: StatusWaiting, Live: true},
		visibleSnapshot: "› 1",
	}

	if err := SubmitStructuredInput(rt, "1"); err != nil {
		t.Fatal(err)
	}
	if parts := term.writeParts(); len(parts) != 1 || parts[0] != "1" {
		t.Fatalf("plain numeric input should write only the digit, got %#v", parts)
	}
}

func TestPrepareStructuredInputKeepsInputText(t *testing.T) {
	if got := PrepareStructuredInput("hello tui"); got != "hello tui\r" {
		t.Fatalf("structured input = %q", got)
	}
	if got := PrepareStructuredInput("/exit"); got != "/exit\r" {
		t.Fatalf("slash command should be kept as-is, got %q", got)
	}
}

func TestSubmitStructuredInputClearsPreviousNotificationBeforeEcho(t *testing.T) {
	previousDelay := structuredInputEnterDelay
	structuredInputEnterDelay = 0
	defer func() { structuredInputEnterDelay = previousDelay }()

	notifier := &recordingNotifier{}
	manager := NewManager(nil, nil, WithNotifier(notifier))
	rt := &RuntimeSession{
		manager:                 manager,
		session:                 Session{ID: "sess-1", Name: "TUI", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		lastNotifiedMessageID:   "msg-old",
		lastNotifiedContent:     "old reply",
		notificationUpdateNo:    0,
		notificationRunning:     false,
		snapshotAtRoundStart:    "old snapshot",
		snapshotAtRoundStartSet: true,
		visibleSnapshot:         "old snapshot",
		visibleSnapshotVersion:  1,
	}
	term := &recordingTerminal{readCh: make(chan []byte)}
	term.onWrite = func(data string) {
		if data == "second input" {
			rt.HandleOutput([]byte(data))
		}
	}
	rt.terminal = term

	if err := SubmitStructuredInput(rt, "second input"); err != nil {
		t.Fatal(err)
	}
	if running := notifier.runningNotes(); len(running) != 0 {
		t.Fatalf("input echo should not mark previous notification running, got %#v", running)
	}
	if rt.lastNotifiedMessageID != "" {
		t.Fatalf("new input round should clear previous message id before terminal echo, got %q", rt.lastNotifiedMessageID)
	}
}

func TestSubmitStructuredInputRefreshesBaselineBeforeWrite(t *testing.T) {
	previousDelay := structuredInputEnterDelay
	structuredInputEnterDelay = 0
	defer func() { structuredInputEnterDelay = previousDelay }()

	manager := NewManager(nil, nil)
	rt := &RuntimeSession{
		manager:                manager,
		session:                Session{ID: "sess-1", Name: "TUI", Status: StatusWaiting, Live: true},
		visibleSnapshot:        "old cached snapshot",
		visibleSnapshotVersion: 1,
		subscribers:            make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	ch, cancel := rt.SubscribeWithMode(false)
	defer cancel()
	go func() {
		for i := 0; i < 2; i++ {
			if ev := <-ch; ev.Type == RuntimeEventSnapshotRequest {
				rt.SetVisibleSnapshotWithSource("fresh baseline", "browser:buffer")
			}
		}
	}()
	term := &recordingTerminal{readCh: make(chan []byte)}
	term.onWrite = func(data string) {
		if data != "new input" {
			return
		}
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if rt.snapshotAtRoundStart != "fresh baseline" {
			t.Fatalf("input baseline = %q, want fresh baseline", rt.snapshotAtRoundStart)
		}
	}
	rt.terminal = term

	if err := SubmitStructuredInput(rt, "new input"); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitStructuredInputFreshBaselineKeepsPreviousRoundsOutOfDiff(t *testing.T) {
	previousDelay := structuredInputEnterDelay
	structuredInputEnterDelay = 0
	defer func() { structuredInputEnterDelay = previousDelay }()

	manager := NewManager(nil, nil)
	rt := &RuntimeSession{
		manager:                manager,
		session:                Session{ID: "sess-1", Name: "TUI", Status: StatusWaiting, Live: true, NotifyOnWaiting: true},
		terminal:               &recordingTerminal{readCh: make(chan []byte)},
		visibleSnapshot:        "stale cached screen",
		visibleSnapshotVersion: 1,
		subscribers:            make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	ch, cancel := rt.SubscribeWithMode(false)
	defer cancel()
	go func() {
		for i := 0; i < 2; i++ {
			if ev := <-ch; ev.Type == RuntimeEventSnapshotRequest {
				rt.SetVisibleSnapshotWithSource("previous question\nprevious answer", "browser:buffer")
			}
		}
	}()

	if err := SubmitStructuredInput(rt, "next question"); err != nil {
		t.Fatal(err)
	}
	rt.SetVisibleSnapshotWithSource("previous question\nprevious answer\n› next question\nnew answer", "browser:buffer")
	rt.mu.Lock()
	rt.session.Status = StatusWaiting
	n, _, ok, reason := rt.waitingNotificationCandidateLocked()
	rt.mu.Unlock()
	if !ok {
		t.Fatalf("expected notification candidate, reason=%s", reason)
	}
	if strings.Contains(n.Content, "previous answer") {
		t.Fatalf("current round diff should not include previous round, got %q", n.Content)
	}
	if !strings.Contains(n.Content, "new answer") {
		t.Fatalf("current round diff should include new answer, got %q", n.Content)
	}
}

func TestLarkReplyBridgeMultiImageWithTextSubmitsImmediately(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	paths := map[string]string{"img_a": "/tmp/a.png", "img_b": "/tmp/b.png"}
	bridge.downloadFile = func(_ context.Context, _ string, _ string, ref larkAttachmentRef) (pendingLarkAttachment, error) {
		return pendingLarkAttachment{Kind: ref.Kind, Path: paths[ref.Key]}, nil
	}
	var replies []string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		replies = append(replies, text)
		return nil
	}

	content := `{"content":[[{"tag":"img","image_key":"img_a"},{"tag":"img","image_key":"img_b"},{"tag":"text","text":"对比这两张图"}]]}`
	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-images-text", "", "", "post", content)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "/tmp/a.png /tmp/b.png 对比这两张图") {
		t.Fatalf("image+text should submit immediately, got %#v", parts)
	}
	if len(replies) != 0 {
		t.Fatalf("image+text should not send upload-success reply, got %#v", replies)
	}
}

func TestLarkReplyBridgeImageMessageWithTextSubmitsImmediately(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.downloadFile = func(_ context.Context, _ string, _ string, ref larkAttachmentRef) (pendingLarkAttachment, error) {
		return pendingLarkAttachment{Kind: ref.Kind, Path: "/tmp/a.png"}, nil
	}
	var replies []string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		replies = append(replies, text)
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-image-caption", "", "", "image", `{"image_key":"img_a","text":"请分析这张图"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "/tmp/a.png 请分析这张图") {
		t.Fatalf("image message with text should submit immediately, got %#v", parts)
	}
	if len(replies) != 0 {
		t.Fatalf("image message with text should not send upload-success reply, got %#v", replies)
	}
}

func TestLarkReplyBridgeAttachmentWithTextClearsStalePendingInput(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	paths := map[string]string{"old_img": "/tmp/old.png", "new_img": "/tmp/new.png"}
	bridge.downloadFile = func(_ context.Context, _ string, _ string, ref larkAttachmentRef) (pendingLarkAttachment, error) {
		return pendingLarkAttachment{Kind: ref.Kind, Path: paths[ref.Key]}, nil
	}
	bridge.replyText = func(_ context.Context, _ string, _ string) error { return nil }

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-old-image", "", "", "image", `{"image_key":"old_img"}`)); err != nil {
		t.Fatal(err)
	}
	content := `{"content":[[{"tag":"img","image_key":"new_img"},{"tag":"text","text":"分析新的"}]]}`
	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-new-image-text", "", "", "post", content)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) < 3 {
		t.Fatalf("expected pending input, clear, submitted new input; got %#v", parts)
	}
	if parts[len(parts)-3] != "\x15" {
		t.Fatalf("new attachment+text should clear stale pending input before submit, got %#v", parts)
	}
	if !lastSubmittedWrite(parts, "/tmp/new.png 分析新的") {
		t.Fatalf("new attachment+text should submit only current attachment and text, got %#v", parts)
	}
}

func TestLarkReplyBridgeFileWaitsForTextBeforeEnter(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.downloadFile = func(_ context.Context, _ string, _ string, ref larkAttachmentRef) (pendingLarkAttachment, error) {
		return pendingLarkAttachment{Kind: ref.Kind, Path: "/tmp/report.pdf"}, nil
	}
	var replies []string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		replies = append(replies, text)
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-file", "", "", "file", `{"file_key":"file_a","file_name":"报告.pdf"}`)); err != nil {
		t.Fatal(err)
	}
	if got := launcher.terminals[0].writes(); got != "/tmp/report.pdf " {
		t.Fatalf("file-only message should write path without enter, got %q", got)
	}
	if len(replies) != 1 || replies[0] != "文件已上传成功，等待你的说明后执行。" {
		t.Fatalf("unexpected replies: %#v", replies)
	}
}

func TestLarkReplyBridgeIgnoresInteractiveCards(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	var reply string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		reply = text
		return nil
	}

	err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-card", "", "", "interactive", `{"title":"测试","elements":[{"tag":"div"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 0 {
		t.Fatalf("interactive card should not create or write a terminal, got %d", len(launcher.terminals))
	}
	if !strings.Contains(reply, "收到转发卡片") {
		t.Fatalf("interactive card should get an explanatory reply, got %q", reply)
	}
}

func TestLarkReplyBridgeRepliesWhenPostContentIsUnreadable(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	var reply string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		reply = text
		return nil
	}

	err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-unreadable-post", "", "", "post", `{"content":[[{"tag":"media","media_key":"unsupported"}]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 0 {
		t.Fatalf("unreadable post should not create or write a terminal, got %d", len(launcher.terminals))
	}
	if !strings.Contains(reply, "无法读取") {
		t.Fatalf("unreadable post should get an explanatory reply, got %q", reply)
	}
}

func TestLarkReplyBridgeIgnoresNonUserSender(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

	err := bridge.HandleP2MessageReceive(context.Background(), p2MessageWithSender("m-app", "", "", "text", `{"text":"开始 测试"}`, "app"))
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 0 {
		t.Fatalf("app sender should not create or write a terminal, got %d", len(launcher.terminals))
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
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

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
	if !strings.Contains(got, PrepareStructuredInput("echo from lark")) {
		t.Fatalf("terminal did not receive followup input: %q", got)
	}
	parts := launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "echo from lark") {
		t.Fatalf("lark followup should submit text and enter atomically, got %#v", parts)
	}
	waitForBrowserRequest(t, &browserMu, &browserRequests, "sess-1")
}

func TestLarkReplyBridgeFollowupCreatesRunningCard(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	notifier := &recordingNotifier{messageID: "bot-running"}
	manager := NewManager(nil, launcher, WithNotifier(notifier))
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-running", "", "", "text", `{"text":"开始 Running会话"}`)); err != nil {
		t.Fatal(err)
	}
	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-follow-running", "m-start-running", "", "text", `{"text":"echo from lark"}`)); err != nil {
		t.Fatal(err)
	}

	notes := notifier.notes()
	if len(notes) == 0 {
		t.Fatal("expected an immediate running card")
	}
	got := notes[len(notes)-1]
	if got.Content != RunningNotificationPlaceholder || !got.Running {
		t.Fatalf("running card = %#v", got)
	}
	if got.SessionID == "" {
		t.Fatalf("running card should include session id: %#v", got)
	}
}

func TestLarkReplyBridgeCardShortcutSendsCtrlC(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	notifier := &recordingNotifier{messageID: "bot-card"}
	manager := NewManager(nil, launcher, WithNotifier(notifier))
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "Shortcut")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.UpdateNotifyOnWaiting(context.Background(), sess.ID, true); err != nil {
		t.Fatal(err)
	}
	event := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
		Action: &callback.CallBackAction{Value: map[string]interface{}{
			"easy_terminal_action": "shortcut",
			"session_id":           sess.ID,
			"key":                  "ctrl_c",
		}},
		Context: &callback.Context{OpenMessageID: "bot-card"},
	}}

	resp, err := bridge.HandleCardActionTrigger(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatalf("unexpected response: %#v", resp)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) == 0 || parts[len(parts)-1] != "\x03" {
		t.Fatalf("shortcut should send Ctrl-C to terminal, got %#v", parts)
	}
	notes := notifier.notes()
	if len(notes) != 0 {
		t.Fatalf("shortcut should not overwrite clicked card with placeholder, got %#v", notes)
	}
	rt, ok := manager.GetRuntime(sess.ID)
	if !ok {
		t.Fatal("runtime not found")
	}
	if rt.lastNotifiedMessageID != "bot-card" {
		t.Fatalf("shortcut should keep clicked card as notification anchor, got %q", rt.lastNotifiedMessageID)
	}
}

func TestLarkReplyBridgeLegacyCardPayloadSendsCtrlC(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "Shortcut")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"open_message_id":"bot-card","action":{"value":{"easy_terminal_action":"shortcut","session_id":"` + sess.ID + `","key":"ctrl_c"}}}`)

	resp, err := bridge.handleCardActionPayload(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatalf("unexpected response: %#v", resp)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) == 0 || parts[len(parts)-1] != "\x03" {
		t.Fatalf("legacy card payload should send Ctrl-C to terminal, got %#v", parts)
	}
}

func TestLarkReplyBridgeCardShortcutExitsAgentWithDoubleCtrlC(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "Shortcut")
	if err != nil {
		t.Fatal(err)
	}
	event := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
		Action: &callback.CallBackAction{Value: map[string]interface{}{
			"easy_terminal_action": "shortcut",
			"session_id":           sess.ID,
			"key":                  "exit_agent",
		}},
		Context: &callback.Context{OpenMessageID: "bot-card"},
	}}

	resp, err := bridge.HandleCardActionTrigger(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatalf("unexpected response: %#v", resp)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) == 0 || parts[len(parts)-1] != "\x03\x03" {
		t.Fatalf("exit agent shortcut should send two Ctrl-C inputs, got %#v", parts)
	}
}

func TestLarkReplyBridgeCardRefreshUpdatesClickedMessage(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	notifier := &recordingNotifier{messageID: "bot-card"}
	manager := NewManager(nil, launcher, WithNotifier(notifier))
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "Shortcut")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.UpdateNotifyOnWaiting(context.Background(), sess.ID, true); err != nil {
		t.Fatal(err)
	}
	rt, _ := manager.GetRuntime(sess.ID)
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	event := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
		Action: &callback.CallBackAction{Value: map[string]interface{}{
			"easy_terminal_action": "refresh",
			"session_id":           sess.ID,
		}},
		Context: &callback.Context{OpenMessageID: "bot-card"},
	}}

	resp, err := bridge.HandleCardActionTrigger(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Content != "刷新成功" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	notes := waitForNotifierNotes(t, notifier, 1)
	if len(notes) != 1 || notes[0].MessageID != "bot-card" || notes[0].Content != "hello\n$" {
		t.Fatalf("manual refresh should patch clicked card, got %#v", notes)
	}
}

func TestLarkReplyBridgeCardToggleAutoRefreshWaitsForInterval(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	notifier := &recordingNotifier{messageID: "bot-card"}
	manager := NewManager(nil, launcher, WithNotifier(notifier), WithAutoRefreshInterval(80*time.Millisecond))
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "Shortcut")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.UpdateNotifyOnWaiting(context.Background(), sess.ID, true); err != nil {
		t.Fatal(err)
	}
	rt, _ := manager.GetRuntime(sess.ID)
	rt.MarkInputActivity("echo hello\r")
	rt.SetVisibleSnapshot("$ echo hello\nhello\n$")
	event := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
		Action: &callback.CallBackAction{Value: map[string]interface{}{
			"easy_terminal_action": "toggle_auto_refresh",
			"session_id":           sess.ID,
		}},
		Context: &callback.Context{OpenMessageID: "bot-card"},
	}}

	resp, err := bridge.HandleCardActionTrigger(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Content != "已开启自动刷新" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	time.Sleep(40 * time.Millisecond)
	if notes := notifier.notes(); len(notes) != 0 {
		t.Fatalf("toggle should not refresh before configured interval, got %#v", notes)
	}
	notes := waitForNotifierNotes(t, notifier, 1)
	if len(notes) != 1 || notes[0].MessageID != "bot-card" || !notes[0].AutoRefreshEnabled || notes[0].SuppressUpdateTip {
		t.Fatalf("auto refresh should patch clicked card after interval, got %#v", notes)
	}

	resp, err = bridge.HandleCardActionTrigger(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Content != "已关闭自动刷新" {
		t.Fatalf("unexpected close response: %#v", resp)
	}
	notes = waitForNotifierNotes(t, notifier, 2)
	if len(notes) < 2 || notes[1].AutoRefreshEnabled {
		t.Fatalf("toggle close should patch clicked card as auto refresh disabled, got %#v", notes)
	}
}

func waitForNotifierNotes(t *testing.T, notifier *recordingNotifier, want int) []WaitingNotification {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		notes := notifier.notes()
		if len(notes) >= want {
			return notes
		}
		time.Sleep(10 * time.Millisecond)
	}
	return notifier.notes()
}

func TestLarkReplyBridgeCardCustomShortcutSubmitsCommand(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	notifier := &recordingNotifier{messageID: "new-card"}
	manager := NewManager(nil, launcher, WithNotifier(notifier))
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	sess, err := manager.CreateSession(context.Background(), "Shortcut")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.UpdateNotifyOnWaiting(context.Background(), sess.ID, true); err != nil {
		t.Fatal(err)
	}
	event := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{
		Action: &callback.CallBackAction{Value: map[string]interface{}{
			"easy_terminal_action": "custom_shortcut",
			"session_id":           sess.ID,
			"command":              "git status",
		}},
		Context: &callback.Context{OpenMessageID: "bot-card"},
	}}

	resp, err := bridge.HandleCardActionTrigger(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Fatalf("unexpected response: %#v", resp)
	}
	writes := launcher.terminals[0].writes()
	if !strings.Contains(writes, "git status") {
		t.Fatalf("custom shortcut should submit command, writes=%q", writes)
	}
	notes := notifier.notes()
	if len(notes) != 1 || notes[0].MessageID != "" || !notes[0].Running {
		t.Fatalf("custom shortcut should create a new running card instead of updating clicked card, got %#v", notes)
	}
	if rt, ok := manager.GetRuntime(sess.ID); !ok || rt.lastInputText != "git status" {
		t.Fatalf("custom shortcut should be recorded as user input, runtime=%v input=%q", ok, rt.lastInputText)
	}
}

func TestLarkReplyBridgeStartRunsConfiguredPresets(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"12": {Commands: []string{"mkdir -p {{session_name}}", "cd {{session_name}}", "codex"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-presets", "", "", "text", `{"text":"开始 测试 12"}`)); err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "测试" {
		t.Fatalf("preset suffix should not be part of session name, got %#v", sessions)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{
		"mkdir -p ${HOME}/'Easy Terminal Workspace/测试'\r",
		"cd ${HOME}/'Easy Terminal Workspace/测试'\r",
		"mkdir -p '测试'\r",
		"cd '测试'\r",
		"codex\r",
	}
	if len(parts) != len(want) {
		t.Fatalf("preset writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("preset write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartWithoutCodesUsesDefaultAgentPreset(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"999999": {Commands: []string{"codex --dangerously-bypass-approvals-and-sandbox"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-no-code", "", "", "text", `{"text":"开始 测试"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{
		"mkdir -p ${HOME}/'Easy Terminal Workspace/测试'\r",
		"cd ${HOME}/'Easy Terminal Workspace/测试'\r",
		"codex --dangerously-bypass-approvals-and-sandbox\r",
	}
	if len(parts) != len(want) {
		t.Fatalf("default workspace writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("default workspace write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeSlashStartWithoutCodesUsesDefaultAgentPreset(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"999999": {Commands: []string{"codex --dangerously-bypass-approvals-and-sandbox"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-slash-start-no-code", "", "", "text", `{"text":"/start 测试"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{
		"mkdir -p ${HOME}/'Easy Terminal Workspace/测试'\r",
		"cd ${HOME}/'Easy Terminal Workspace/测试'\r",
		"codex --dangerously-bypass-approvals-and-sandbox\r",
	}
	if len(parts) != len(want) {
		t.Fatalf("slash start default workspace writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("slash start default workspace write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartCodeZeroOnlyEntersWorkspace(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"0": {Commands: []string{"codex --dangerously-bypass-approvals-and-sandbox"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-code-zero", "", "", "text", `{"text":"开始 测试 0"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{
		"mkdir -p ${HOME}/'Easy Terminal Workspace/测试'\r",
		"cd ${HOME}/'Easy Terminal Workspace/测试'\r",
	}
	if len(parts) != len(want) {
		t.Fatalf("code zero writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("code zero write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartDefaultAgentPresetUsesReservedCode(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"999999": {Commands: []string{"codex --dangerously-bypass-approvals-and-sandbox"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-default-agent-code", "", "", "text", `{"text":"开始 测试 999999"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{
		"mkdir -p ${HOME}/'Easy Terminal Workspace/测试'\r",
		"cd ${HOME}/'Easy Terminal Workspace/测试'\r",
		"codex --dangerously-bypass-approvals-and-sandbox\r",
	}
	if len(parts) != len(want) {
		t.Fatalf("default agent writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("default agent write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartUsesConfiguredDefaultName(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetDefaultStartSessionName("默认会话")

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-default", "", "", "text", `{"text":"开始"}`)); err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "默认会话" {
		t.Fatalf("start command should use configured default session name, got %#v", sessions)
	}
}

func TestLarkReplyBridgeStartWithoutDefaultKeepsFallbackBehavior(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-no-default", "", "", "text", `{"text":"开始"}`)); err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "lark-session" {
		t.Fatalf("start without configured default should keep fallback behavior, got %#v", sessions)
	}
	parts := launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "开始") {
		t.Fatalf("fallback session should receive original text, got %#v", parts)
	}
}

func TestLarkReplyBridgeStartRunsNamePresetOnExactMatch(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetNamePresets(map[string]SessionStartPreset{
		"会话 A": {Commands: []string{"cd sessions/a", "echo {{session_name_raw}}"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-name-preset", "", "", "text", `{"text":"开始 会话 A"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{"cd sessions/a\r", "echo 会话 A\r"}
	if len(parts) != len(want) {
		t.Fatalf("name preset writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("name preset write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartNamePresetRequiresExactMatch(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetNamePresets(map[string]SessionStartPreset{
		"会话 A": {Commands: []string{"cd sessions/a"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-name-preset-miss", "", "", "text", `{"text":"开始 会话 A 草稿"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{
		"mkdir -p ${HOME}/'Easy Terminal Workspace/会话 A 草稿'\r",
		"cd ${HOME}/'Easy Terminal Workspace/会话 A 草稿'\r",
	}
	if len(parts) != len(want) {
		t.Fatalf("non-exact name preset should only run default workspace commands, got %#v want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("workspace write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartNamePresetTakesPriorityOverCodePresets(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetNamePresets(map[string]SessionStartPreset{
		"会话 A": {Commands: []string{"name-one", "name-two"}},
	})
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"1": {Commands: []string{"code-one"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-name-before-code", "", "", "text", `{"text":"开始 会话 A 1"}`)); err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "会话 A" {
		t.Fatalf("preset suffix should not be part of session name, got %#v", sessions)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{"name-one\r", "name-two\r"}
	if len(parts) != len(want) {
		t.Fatalf("preset writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("preset write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartRunsHyphenSeparatedPresetCodes(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"1":   {Commands: []string{"one"}},
		"23":  {Commands: []string{"twenty-three"}},
		"223": {Commands: []string{"two-two-three"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-hyphen-presets", "", "", "text", `{"text":"开始 测试 1-23-223"}`)); err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "测试" {
		t.Fatalf("hyphen preset suffix should not be part of session name, got %#v", sessions)
	}
	parts := launcher.terminals[0].writeParts()
	want := []string{
		"mkdir -p ${HOME}/'Easy Terminal Workspace/测试'\r",
		"cd ${HOME}/'Easy Terminal Workspace/测试'\r",
		"one\r",
		"twenty-three\r",
		"two-two-three\r",
	}
	if len(parts) != len(want) {
		t.Fatalf("preset writes = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("preset write %d = %q, want %q; all writes=%#v", i, parts[i], want[i], parts)
		}
	}
}

func TestLarkReplyBridgeStartPresetQuotesVariables(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	bridge.SetStartPresets(map[string]SessionStartPreset{
		"1": {Commands: []string{"mkdir -p {{session_name}}", "echo {{session_name_raw}}"}},
	})

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-quoted", "", "", "text", `{"text":"开始 项目 O'Brien 1"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) != 4 {
		t.Fatalf("preset writes = %#v", parts)
	}
	if parts[0] != "mkdir -p ${HOME}/'Easy Terminal Workspace/项目 O'\\''Brien'\r" {
		t.Fatalf("workspace mkdir write = %q", parts[0])
	}
	if parts[1] != "cd ${HOME}/'Easy Terminal Workspace/项目 O'\\''Brien'\r" {
		t.Fatalf("workspace cd write = %q", parts[1])
	}
	if parts[2] != "mkdir -p '项目 O'\\''Brien'\r" {
		t.Fatalf("quoted session name write = %q", parts[2])
	}
	if parts[3] != "echo 项目 O'Brien\r" {
		t.Fatalf("raw session name write = %q", parts[3])
	}
}

func TestLarkReplyBridgePipelineRunsNextCommandAfterNotification(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-pipe", "", "", "text", `{"text":"开始 Pipe会话"}`)); err != nil {
		t.Fatal(err)
	}
	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-pipeline", "m-start-pipe", "", "text", `{"text":"pwd | cd /tmp | pwd"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "pwd") {
		t.Fatalf("first pipeline command should be submitted immediately, got %#v", parts)
	}
	if strings.Contains(launcher.terminals[0].writes(), "cd /tmp") {
		t.Fatalf("later pipeline commands should wait for notification, writes: %q", launcher.terminals[0].writes())
	}

	bridge.OnNotificationSent("sess-1")
	parts = launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "cd /tmp") {
		t.Fatalf("second pipeline command should run after notification, got %#v", parts)
	}
	if strings.Contains(launcher.terminals[0].writes(), "pwdpwd") {
		t.Fatalf("pipeline commands should be submitted as separate turns, writes: %q", launcher.terminals[0].writes())
	}

	bridge.OnNotificationSent("sess-1")
	parts = launcher.terminals[0].writeParts()
	if !lastSubmittedWrite(parts, "pwd") {
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
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

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
	if !lastSubmittedWrite(parts, "pwd") {
		t.Fatalf("queued start pipeline command should run after notification, got %#v", parts)
	}
}

func TestLarkReplyBridgeRoutesP1Start(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	var reactions []string
	bridge.addReaction = func(_ context.Context, messageID string, emoji string) error {
		reactions = append(reactions, messageID+":"+emoji)
		return nil
	}

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
	if len(reactions) != 1 || reactions[0] != "p1-start:"+larkProcessingReactionEmoji {
		t.Fatalf("expected processing reaction on P1 message, got %#v", reactions)
	}
}

func TestLarkReplyBridgeFallbackSessionEnablesNotifications(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

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
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
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
	if strings.Contains(replies[0], "> 今天天气怎么样") || !strings.Contains(replies[0], "你想查哪个城市") {
		t.Fatalf("reply did not include current round content: %#v", replies)
	}
}

func TestLarkReplyBridgeStopCommandSendsCtrlC(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-stop", "", "", "text", `{"text":"开始 Stop会话"}`)); err != nil {
		t.Fatal(err)
	}
	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-stop", "m-start-stop", "", "text", `{"text":"/stop"}`)); err != nil {
		t.Fatal(err)
	}
	parts := launcher.terminals[0].writeParts()
	if len(parts) == 0 || parts[len(parts)-1] != "\x03" {
		t.Fatalf("/stop should send Ctrl-C to terminal, got %#v", parts)
	}
	if strings.Contains(launcher.terminals[0].writes(), "/stop") {
		t.Fatalf("/stop should not be sent as text, writes: %q", launcher.terminals[0].writes())
	}
}

func TestLarkReplyBridgeStopCommandWithoutSessionDoesNotCreateTerminal(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	var reply string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		reply = text
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-stop-missing", "", "", "text", `{"text":"/stop"}`)); err != nil {
		t.Fatal(err)
	}
	if len(launcher.terminals) != 0 {
		t.Fatalf("/stop without a session should not create a terminal, got %d", len(launcher.terminals))
	}
	if reply != "未找到会话" {
		t.Fatalf("reply = %q, want 未找到会话", reply)
	}
}

func TestLarkReplyBridgeCurrentRoundCommandUsesRepliedNotificationSession(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
	var reply string
	bridge.replyText = func(_ context.Context, _ string, text string) error {
		reply = text
		return nil
	}

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-a", "", "", "text", `{"text":"开始 A会话"}`)); err != nil {
		t.Fatal(err)
	}
	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-start-b", "", "", "text", `{"text":"开始 B会话"}`)); err != nil {
		t.Fatal(err)
	}
	rtA, ok := manager.GetRuntime("sess-1")
	if !ok {
		t.Fatal("expected sess-1 runtime")
	}
	rtA.MarkInputActivity("echo A\r")
	rtA.SetVisibleSnapshot("eleven ~ > echo A\nA content\neleven ~ >")
	rtB, ok := manager.GetRuntime("sess-2")
	if !ok {
		t.Fatal("expected sess-2 runtime")
	}
	rtB.MarkInputActivity("echo B\r")
	rtB.SetVisibleSnapshot("eleven ~ > echo B\nB content\neleven ~ >")
	defaultLarkMessageRegistry.remember("sess-1", "bot-notify-a")
	defaultLarkMessageRegistry.rememberLatest("sess-2")

	if err := bridge.HandleP2MessageReceive(context.Background(), p2Message("m-current-a", "bot-notify-a", "", "text", `{"text":"/c"}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "A content") || strings.Contains(reply, "B content") {
		t.Fatalf("/c should use replied notification session, reply=%q", reply)
	}
}

func TestLarkReplyBridgeCurrentRoundCommandWithoutSessionDoesNotCreateTerminal(t *testing.T) {
	resetLarkRegistryForTest()
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher)
	bridge := NewLarkReplyBridge("app", "secret", manager, t.TempDir())
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
	defaultLarkMessageRegistry.chatToSession = make(map[string]string)
}

func p2MessageWithChat(messageID, parentID, rootID, messageType, content, chatType, chatID, openID string) *larkim.P2MessageReceiveV1 {
	msg := p2MessageWithSender(messageID, parentID, rootID, messageType, content, "user")
	msg.Event.Message.ChatId = strPtr(chatID)
	msg.Event.Message.ChatType = strPtr(chatType)
	msg.Event.Sender.SenderId = &larkim.UserId{OpenId: strPtr(openID)}
	return msg
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
	return p2MessageWithSender(messageID, parentID, rootID, messageType, content, "")
}

func p2MessageWithSender(messageID, parentID, rootID, messageType, content, senderType string) *larkim.P2MessageReceiveV1 {
	var sender *larkim.EventSender
	if senderType != "" {
		sender = &larkim.EventSender{SenderType: strPtr(senderType)}
	}
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: sender,
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
	mu        sync.Mutex
	buf       strings.Builder
	parts     []string
	writeTime []time.Time
	onWrite   func(string)
	readCh    chan []byte
	closed    bool
}

func (t *recordingTerminal) Read(p []byte) (int, error) {
	b, ok := <-t.readCh
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func (t *recordingTerminal) Write(p []byte) (int, error) {
	data := string(p)
	t.mu.Lock()
	t.parts = append(t.parts, data)
	t.writeTime = append(t.writeTime, time.Now())
	n, err := t.buf.Write(p)
	onWrite := t.onWrite
	t.mu.Unlock()
	if onWrite != nil {
		onWrite(data)
	}
	return n, err
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

func (t *recordingTerminal) writeTimes() []time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]time.Time, len(t.writeTime))
	copy(cp, t.writeTime)
	return cp
}

func lastSubmittedWrite(parts []string, text string) bool {
	return len(parts) >= 2 && parts[len(parts)-2] == text && isEnterWrite(parts[len(parts)-1])
}

func isEnterWrite(text string) bool {
	return text == "\r"
}

type blockingWaiter struct{}

func (blockingWaiter) Wait() error {
	select {}
}
