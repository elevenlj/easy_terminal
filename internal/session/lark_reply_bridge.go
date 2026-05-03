package session

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type LarkReplyBridge struct {
	appID         string
	appSecret     string
	manager       *Manager
	apiClient     *lark.Client
	wsClient      *larkws.Client
	agent         *CommandAgent
	uploadsDir    string
	mu            sync.Mutex
	seenMessages  map[string]time.Time
	pendingImages map[string][]string
}

func NewLarkReplyBridge(appID, appSecret string, manager *Manager, agentCfg *CommandAgentConfig, uploadsDir string) *LarkReplyBridge {
	b := &LarkReplyBridge{
		appID: appID, appSecret: appSecret, manager: manager, agent: NewCommandAgent(agentCfg), uploadsDir: uploadsDir,
		seenMessages: make(map[string]time.Time), pendingImages: make(map[string][]string),
	}
	if appID != "" && appSecret != "" {
		b.apiClient = lark.NewClient(appID, appSecret)
	}
	return b
}

func (b *LarkReplyBridge) Available() bool {
	return b != nil && b.apiClient != nil && b.manager != nil && b.appID != "" && b.appSecret != ""
}

func (b *LarkReplyBridge) Start(ctx context.Context) error {
	if !b.Available() {
		return nil
	}
	handler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return b.HandleP2MessageReceive(ctx, event)
		}).
		OnP1MessageReceiveV1(func(ctx context.Context, event *larkim.P1MessageReceiveV1) error {
			return b.HandleP1MessageReceive(ctx, event)
		})
	b.wsClient = larkws.NewClient(b.appID, b.appSecret, larkws.WithEventHandler(handler))
	log.Printf("lark reply bridge listening for incoming messages")
	return b.wsClient.Start(ctx)
}

func (b *LarkReplyBridge) HandleP2MessageReceive(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}
	msg := event.Event.Message
	text := extractLarkMessageText(valueOf(msg.Content), valueOf(msg.MessageType))
	if text == "" {
		return nil
	}
	sessionID, err := b.RouteText(ctx, valueOf(msg.MessageId), valueOf(msg.ParentId), valueOf(msg.RootId), text)
	if err != nil {
		log.Printf("lark reply bridge failed to route message %s: %v", valueOf(msg.MessageId), err)
		return err
	}
	log.Printf("lark reply bridge routed message %s to %s", valueOf(msg.MessageId), sessionID)
	return nil
}

func (b *LarkReplyBridge) HandleP1MessageReceive(ctx context.Context, event *larkim.P1MessageReceiveV1) error {
	if event == nil || event.Event == nil {
		return nil
	}
	e := event.Event
	text := strings.TrimSpace(e.TextWithoutAtBot)
	if text == "" {
		text = strings.TrimSpace(e.Text)
	}
	if text == "" {
		return nil
	}
	sessionID, err := b.RouteText(ctx, e.OpenMessageID, e.ParentID, e.RootID, text)
	if err != nil {
		log.Printf("lark reply bridge failed to route P1 message %s: %v", e.OpenMessageID, err)
		return err
	}
	log.Printf("lark reply bridge routed P1 message %s to %s", e.OpenMessageID, sessionID)
	return nil
}

func (b *LarkReplyBridge) RouteText(ctx context.Context, messageID, parentID, rootID, text string) (string, error) {
	if b.duplicate(messageID) {
		return "", nil
	}
	text = cleanLarkText(text)
	if strings.HasPrefix(text, "/new ") || strings.HasPrefix(text, "新会话 ") || strings.HasPrefix(text, "开始 ") {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(text, "/new "), "新会话 "), "开始 "))
		s, err := b.manager.CreateSession(ctx, name)
		if err == nil {
			defaultLarkMessageRegistry.remember(s.ID, messageID)
		}
		return s.ID, err
	}
	sessionID := b.resolveSessionID(text, parentID, rootID)
	if sessionID == "" {
		s, err := b.manager.CreateSession(ctx, "lark-session")
		if err != nil {
			return "", err
		}
		sessionID = s.ID
	}
	rt, ok := b.manager.GetRuntime(sessionID)
	if !ok {
		s, err := b.manager.CreateSession(ctx, sessionID)
		if err != nil {
			return "", err
		}
		sessionID = s.ID
		rt, _ = b.manager.GetRuntime(sessionID)
	}
	if strings.HasPrefix(text, "$") {
		cmd, err := b.agent.Translate(ctx, strings.TrimSpace(strings.TrimPrefix(text, "$")))
		if err != nil {
			return sessionID, err
		}
		text = cmd
	}
	if err := rt.WriteInput(PrepareStructuredInput(text)); err != nil {
		return sessionID, err
	}
	defaultLarkMessageRegistry.remember(sessionID, messageID, parentID, rootID)
	return sessionID, nil
}

func (b *LarkReplyBridge) duplicate(messageID string) bool {
	if messageID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for id, t := range b.seenMessages {
		if now.Sub(t) > 10*time.Minute {
			delete(b.seenMessages, id)
		}
	}
	if _, ok := b.seenMessages[messageID]; ok {
		return true
	}
	b.seenMessages[messageID] = now
	return false
}

func (b *LarkReplyBridge) resolveSessionID(text, parentID, rootID string) string {
	if id, ok := defaultLarkMessageRegistry.lookup(parentID, rootID); ok {
		return id
	}
	if m := regexp.MustCompile(`sess-\d+`).FindString(text); m != "" {
		return m
	}
	return defaultLarkMessageRegistry.latestNotifiedSessionID()
}

func cleanLarkText(text string) string {
	text = regexp.MustCompile(`<at[^>]*>.*?</at>`).ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func PrepareStructuredInput(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text + "\r"
}

func extractLarkMessageText(content string, messageType string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	var raw any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return content
	}
	switch messageType {
	case "text":
		if m, ok := raw.(map[string]any); ok {
			return strings.TrimSpace(stringFromAny(m["text"]))
		}
	case "post":
		var parts []string
		collectPostText(raw, &parts)
		return strings.TrimSpace(strings.Join(parts, ""))
	default:
		if m, ok := raw.(map[string]any); ok {
			if text := strings.TrimSpace(stringFromAny(m["text"])); text != "" {
				return text
			}
		}
		var parts []string
		collectPostText(raw, &parts)
		if len(parts) > 0 {
			return strings.TrimSpace(strings.Join(parts, ""))
		}
	}
	return ""
}

func collectPostText(v any, parts *[]string) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			collectPostText(item, parts)
		}
	case map[string]any:
		tag := stringFromAny(x["tag"])
		switch tag {
		case "text", "a":
			*parts = append(*parts, stringFromAny(x["text"]))
		case "at":
			*parts = append(*parts, " ")
		}
		for _, key := range []string{"content", "elements"} {
			if child, ok := x[key]; ok {
				collectPostText(child, parts)
			}
		}
	}
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func valueOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
