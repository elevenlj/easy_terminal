package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	appID                   string
	appSecret               string
	manager                 *Manager
	apiClient               *lark.Client
	wsClient                *larkws.Client
	agent                   *CommandAgent
	uploadsDir              string
	defaultStartSessionName string
	startPresets            map[string]SessionStartPreset
	namePresets             map[string]SessionStartPreset
	mu                      sync.Mutex
	seenMessages            map[string]time.Time
	pendingFiles            map[string][]pendingLarkAttachment
	pipelines               map[string][]string
	replyText               func(context.Context, string, string) error
	downloadFile            func(context.Context, string, string, larkAttachmentRef) (pendingLarkAttachment, error)
	addReaction             func(context.Context, string, string) error
	createChat              func(context.Context, string, string, string) (string, error)
	sendChatText            func(context.Context, string, string) error
}

var structuredInputEnterDelay = 200 * time.Millisecond
var structuredInputEnterSequence = "\r\n"

const larkProcessingReactionEmoji = "THINKING"

type SessionStartPreset struct {
	Commands []string `json:"commands"`
}

type larkRouteContext struct {
	MessageID    string
	ParentID     string
	RootID       string
	ChatID       string
	ChatType     string
	SenderOpenID string
}

func NewLarkReplyBridge(appID, appSecret string, manager *Manager, agentCfg *CommandAgentConfig, uploadsDir string) *LarkReplyBridge {
	b := &LarkReplyBridge{
		appID: appID, appSecret: appSecret, manager: manager, agent: NewCommandAgent(agentCfg), uploadsDir: uploadsDir,
		seenMessages: make(map[string]time.Time), pendingFiles: make(map[string][]pendingLarkAttachment), pipelines: make(map[string][]string),
	}
	if manager != nil {
		manager.SetNotificationSentHook(b.OnNotificationSent)
	}
	if appID != "" && appSecret != "" {
		b.apiClient = lark.NewClient(appID, appSecret)
	}
	b.replyText = b.replyTextToMessage
	b.downloadFile = b.downloadLarkAttachment
	b.addReaction = b.addLarkMessageReaction
	b.createChat = b.createLarkChat
	b.sendChatText = b.sendTextToChat
	return b
}

func (b *LarkReplyBridge) SetStartPresets(presets map[string]SessionStartPreset) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.startPresets = copySessionStartPresets(presets)
}

func (b *LarkReplyBridge) SetNamePresets(presets map[string]SessionStartPreset) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.namePresets = copySessionStartPresets(presets)
}

func (b *LarkReplyBridge) SetDefaultStartSessionName(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.defaultStartSessionName = strings.TrimSpace(name)
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
		}).
		OnP2MessageReactionCreatedV1(func(ctx context.Context, event *larkim.P2MessageReactionCreatedV1) error {
			return nil
		}).
		OnP2MessageReactionDeletedV1(func(ctx context.Context, event *larkim.P2MessageReactionDeletedV1) error {
			return nil
		}).
		OnP2MessageReadV1(func(ctx context.Context, event *larkim.P2MessageReadV1) error {
			return nil
		}).
		OnP1MessageReadV1(func(ctx context.Context, event *larkim.P1MessageReadV1) error {
			return nil
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
	if shouldIgnoreLarkP2Message(event.Event.Sender, valueOf(msg.MessageType)) {
		return nil
	}
	incoming := extractLarkIncomingMessage(valueOf(msg.Content), valueOf(msg.MessageType))
	log.Printf("lark reply bridge received P2 message=%s chat=%s chat_type=%s msg_type=%s text_len=%d attachments=%d",
		valueOf(msg.MessageId), valueOf(msg.ChatId), valueOf(msg.ChatType), valueOf(msg.MessageType), len(incoming.Text), len(incoming.Attachments))
	if incoming.Text == "" && len(incoming.Attachments) == 0 {
		return nil
	}
	b.markLarkMessageProcessing(ctx, valueOf(msg.MessageId))
	routeCtx := larkRouteContext{
		MessageID:    valueOf(msg.MessageId),
		ParentID:     valueOf(msg.ParentId),
		RootID:       valueOf(msg.RootId),
		ChatID:       valueOf(msg.ChatId),
		ChatType:     valueOf(msg.ChatType),
		SenderOpenID: larkSenderOpenID(event.Event.Sender),
	}
	sessionID, err := b.RouteIncomingWithContext(ctx, routeCtx, incoming)
	if err != nil {
		log.Printf("lark reply bridge failed to route message %s: %v", valueOf(msg.MessageId), err)
		return err
	}
	log.Printf("lark reply bridge routed message %s to %s", valueOf(msg.MessageId), sessionID)
	return nil
}

func (b *LarkReplyBridge) markLarkMessageProcessing(ctx context.Context, messageID string) {
	if b.addReaction == nil || messageID == "" {
		return
	}
	if err := b.addReaction(ctx, messageID, larkProcessingReactionEmoji); err != nil {
		log.Printf("lark reply bridge failed to add processing reaction message=%s emoji=%s: %v", messageID, larkProcessingReactionEmoji, err)
	}
}

func shouldIgnoreLarkP2Message(sender *larkim.EventSender, messageType string) bool {
	if messageType == "interactive" {
		return true
	}
	if sender == nil || sender.SenderType == nil {
		return false
	}
	return *sender.SenderType != "" && *sender.SenderType != "user"
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
	log.Printf("lark reply bridge received P1 message=%s chat=%s chat_type=%s msg_type=%s mention=%v text_len=%d",
		e.OpenMessageID, e.OpenChatID, e.ChatType, e.MsgType, e.IsMention, len(text))
	b.markLarkMessageProcessing(ctx, e.OpenMessageID)
	routeCtx := larkRouteContext{
		MessageID:    e.OpenMessageID,
		ParentID:     e.ParentID,
		RootID:       e.RootID,
		ChatID:       e.OpenChatID,
		ChatType:     e.ChatType,
		SenderOpenID: e.OpenID,
	}
	sessionID, err := b.RouteIncomingWithContext(ctx, routeCtx, larkIncomingMessage{Text: text})
	if err != nil {
		log.Printf("lark reply bridge failed to route P1 message %s: %v", e.OpenMessageID, err)
		return err
	}
	log.Printf("lark reply bridge routed P1 message %s to %s", e.OpenMessageID, sessionID)
	return nil
}

func (b *LarkReplyBridge) RouteText(ctx context.Context, messageID, parentID, rootID, text string) (string, error) {
	return b.RouteIncoming(ctx, messageID, parentID, rootID, larkIncomingMessage{Text: text})
}

func (b *LarkReplyBridge) RouteIncoming(ctx context.Context, messageID, parentID, rootID string, incoming larkIncomingMessage) (string, error) {
	return b.RouteIncomingWithContext(ctx, larkRouteContext{MessageID: messageID, ParentID: parentID, RootID: rootID}, incoming)
}

func (b *LarkReplyBridge) RouteIncomingWithContext(ctx context.Context, routeCtx larkRouteContext, incoming larkIncomingMessage) (string, error) {
	messageID, parentID, rootID := routeCtx.MessageID, routeCtx.ParentID, routeCtx.RootID
	if b.duplicate(messageID) {
		return "", nil
	}
	text := cleanLarkText(incoming.Text)
	parts := splitLarkPipeline(text)
	if len(incoming.Attachments) > 0 {
		return b.routeAttachments(ctx, routeCtx, text, parts, incoming.Attachments)
	}
	if len(parts) == 0 {
		return "", nil
	}
	text = parts[0]
	if sessionID := b.resolveSessionID(ctx, text, parentID, rootID, routeCtx.ChatID, routeCtx.ChatType); sessionID != "" && b.hasPendingFiles(sessionID) {
		rt, ok := b.manager.GetRuntime(sessionID)
		if !ok {
			if err := b.replyLarkText(ctx, messageID, "会话不在线"); err != nil {
				return sessionID, err
			}
			return sessionID, nil
		}
		b.manager.EnsureBrowser(sessionID)
		b.enqueuePipeline(sessionID, parts[1:])
		if err := SubmitStructuredInput(rt, text); err != nil {
			return sessionID, err
		}
		b.clearPendingFiles(sessionID)
		defaultLarkMessageRegistry.remember(sessionID, messageID, parentID, rootID)
		return sessionID, nil
	}
	if name, presetCodes, ok := b.parseLarkStartCommand(text); ok {
		s, err := b.createLarkSessionForMessage(ctx, name, routeCtx)
		if err == nil {
			defaultLarkMessageRegistry.remember(s.ID, messageID)
			if presetErr := b.runSessionNamePreset(s, presetCodes); presetErr != nil {
				log.Printf("lark name preset failed session=%s name=%q: %v", s.ID, s.Name, presetErr)
			}
			if presetErr := b.runSessionStartPresets(s, presetCodes); presetErr != nil {
				log.Printf("lark start presets failed session=%s codes=%q: %v", s.ID, presetCodes, presetErr)
			}
			b.enqueuePipeline(s.ID, parts[1:])
		}
		return s.ID, err
	}
	sessionID := b.resolveSessionID(ctx, text, parentID, rootID, routeCtx.ChatID, routeCtx.ChatType)
	if isStopCommand(text) {
		if sessionID == "" {
			if err := b.replyLarkText(ctx, messageID, "未找到会话"); err != nil {
				return "", err
			}
			return "", nil
		}
		rt, ok := b.manager.GetRuntime(sessionID)
		if !ok {
			if err := b.replyLarkText(ctx, messageID, "会话不在线"); err != nil {
				return sessionID, err
			}
			return sessionID, nil
		}
		if err := rt.WriteInput("\x03"); err != nil {
			return sessionID, err
		}
		defaultLarkMessageRegistry.remember(sessionID, messageID, parentID, rootID)
		return sessionID, nil
	}
	if isCurrentRoundCommand(text) {
		if sessionID == "" {
			if err := b.replyLarkText(ctx, messageID, "未找到会话"); err != nil {
				return "", err
			}
			return "", nil
		}
		rt, ok := b.manager.GetRuntime(sessionID)
		if !ok {
			if err := b.replyLarkText(ctx, messageID, "会话不在线"); err != nil {
				return sessionID, err
			}
			return sessionID, nil
		}
		content := rt.CurrentRoundContent()
		if strings.TrimSpace(content) == "" {
			content = "当前轮暂无内容"
		}
		if err := b.replyLarkText(ctx, messageID, content); err != nil {
			return sessionID, err
		}
		defaultLarkMessageRegistry.remember(sessionID, messageID, parentID, rootID)
		return sessionID, nil
	}
	if sessionID == "" {
		s, err := b.createLarkSessionForMessage(ctx, "lark-session", routeCtx)
		if err != nil {
			return "", err
		}
		sessionID = s.ID
	}
	rt, ok := b.manager.GetRuntime(sessionID)
	if !ok {
		s, err := b.createLarkSessionForMessage(ctx, sessionID, routeCtx)
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
	b.manager.EnsureBrowser(sessionID)
	b.enqueuePipeline(sessionID, parts[1:])
	if err := SubmitStructuredInput(rt, text); err != nil {
		return sessionID, err
	}
	defaultLarkMessageRegistry.remember(sessionID, messageID, parentID, rootID)
	return sessionID, nil
}

func (b *LarkReplyBridge) routeAttachments(ctx context.Context, routeCtx larkRouteContext, text string, parts []string, refs []larkAttachmentRef) (string, error) {
	messageID, parentID, rootID := routeCtx.MessageID, routeCtx.ParentID, routeCtx.RootID
	if len(parts) > 0 {
		text = parts[0]
	}
	sessionID := b.resolveSessionID(ctx, text, parentID, rootID, routeCtx.ChatID, routeCtx.ChatType)
	if sessionID == "" {
		s, err := b.createLarkSessionForMessage(ctx, "lark-session", routeCtx)
		if err != nil {
			return "", err
		}
		sessionID = s.ID
	}
	rt, ok := b.manager.GetRuntime(sessionID)
	if !ok {
		s, err := b.createLarkSessionForMessage(ctx, sessionID, routeCtx)
		if err != nil {
			return "", err
		}
		sessionID = s.ID
		rt, _ = b.manager.GetRuntime(sessionID)
	}

	files := make([]pendingLarkAttachment, 0, len(refs))
	for _, ref := range refs {
		file, err := b.downloadFile(ctx, messageID, sessionID, ref)
		if err != nil {
			kind := larkAttachmentKindLabel(ref.Kind)
			if replyErr := b.replyLarkText(ctx, messageID, kind+"上传失败："+err.Error()); replyErr != nil {
				return sessionID, replyErr
			}
			return sessionID, err
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return sessionID, nil
	}

	b.manager.EnsureBrowser(sessionID)
	input := formatLarkAttachmentInput(files)
	if strings.TrimSpace(text) == "" {
		if err := rt.WriteInput(input + " "); err != nil {
			return sessionID, err
		}
		b.appendPendingFiles(sessionID, files)
		defaultLarkMessageRegistry.remember(sessionID, messageID, parentID, rootID)
		defaultLarkMessageRegistry.rememberLatest(sessionID)
		if err := b.replyLarkText(ctx, messageID, larkAttachmentUploadSuccessMessage(files)); err != nil {
			return sessionID, err
		}
		return sessionID, nil
	}

	if b.hasPendingFiles(sessionID) {
		if err := rt.WriteInput("\x15"); err != nil {
			return sessionID, err
		}
		b.clearPendingFiles(sessionID)
	}
	b.enqueuePipeline(sessionID, parts[1:])
	if err := SubmitStructuredInput(rt, input+" "+text); err != nil {
		return sessionID, err
	}
	b.clearPendingFiles(sessionID)
	defaultLarkMessageRegistry.remember(sessionID, messageID, parentID, rootID)
	return sessionID, nil
}

func (b *LarkReplyBridge) createLarkSession(ctx context.Context, name string) (Session, error) {
	s, err := b.manager.CreateSession(ctx, name)
	if err != nil {
		return s, err
	}
	updated, ok, err := b.manager.UpdateNotifyOnWaiting(ctx, s.ID, true)
	if err != nil || !ok {
		return s, err
	}
	b.manager.EnsureBrowser(updated.ID)
	return updated, nil
}

func (b *LarkReplyBridge) createLarkSessionForMessage(ctx context.Context, name string, routeCtx larkRouteContext) (Session, error) {
	s, err := b.createLarkSession(ctx, name)
	if err != nil {
		return s, err
	}
	if routeCtx.ChatID != "" && routeCtx.ChatType == "group" {
		return b.bindSessionToLarkChat(ctx, s, routeCtx.ChatID)
	}
	if routeCtx.SenderOpenID == "" || b.createChat == nil {
		log.Printf("lark reply bridge skipped dedicated chat creation session=%s name=%q reason=missing_sender_or_creator sender=%q",
			s.ID, s.Name, routeCtx.SenderOpenID)
		return s, nil
	}
	log.Printf("lark reply bridge creating dedicated chat session=%s name=%q owner=%s", s.ID, s.Name, routeCtx.SenderOpenID)
	chatID, err := b.createChat(ctx, s.ID, s.Name, routeCtx.SenderOpenID)
	if err != nil {
		log.Printf("lark reply bridge failed to create session chat session=%s name=%q: %v", s.ID, s.Name, err)
		return s, nil
	}
	updated, err := b.bindSessionToLarkChat(ctx, s, chatID)
	if err != nil {
		return updated, err
	}
	log.Printf("lark reply bridge bound session=%s to lark chat=%s", updated.ID, chatID)
	if b.sendChatText != nil {
		if err := b.sendChatText(ctx, chatID, fmt.Sprintf("已创建会话 %s（%s）。之后直接在这个对话里发消息。", updated.Name, updated.ID)); err != nil {
			log.Printf("lark reply bridge failed to send session chat intro session=%s chat=%s: %v", updated.ID, chatID, err)
		}
	}
	return updated, nil
}

func (b *LarkReplyBridge) bindSessionToLarkChat(ctx context.Context, sess Session, chatID string) (Session, error) {
	updated, ok, err := b.manager.BindLarkChat(ctx, sess.ID, chatID)
	if err != nil || !ok {
		return sess, err
	}
	defaultLarkMessageRegistry.rememberChat(chatID, updated.ID)
	return updated, nil
}

func (b *LarkReplyBridge) parseLarkStartCommand(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	prefixes := []string{"/new", "新会话", "开始"}
	for _, prefix := range prefixes {
		if text != prefix && !strings.HasPrefix(text, prefix+" ") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(text, prefix))
		name, codes := splitSessionNameAndPresetCodes(body)
		if name == "" {
			b.mu.Lock()
			name = b.defaultStartSessionName
			b.mu.Unlock()
			if name == "" {
				return "", "", false
			}
		}
		return name, codes, true
	}
	return "", "", false
}

func splitSessionNameAndPresetCodes(body string) (string, string) {
	fields := strings.Fields(strings.TrimSpace(body))
	if len(fields) <= 1 {
		return strings.TrimSpace(body), ""
	}
	last := fields[len(fields)-1]
	if !isPresetCodeSuffix(last) {
		return strings.TrimSpace(body), ""
	}
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body), last))
	return name, last
}

func isPresetCodeSuffix(text string) bool {
	if text == "" {
		return false
	}
	hasDigit := false
	prevDash := false
	for i, r := range text {
		if r >= '0' && r <= '9' {
			hasDigit = true
			prevDash = false
			continue
		}
		if r == '-' {
			if i == 0 || prevDash {
				return false
			}
			prevDash = true
			continue
		}
		return false
	}
	return hasDigit && !prevDash
}

func splitPresetCodes(codes string) []string {
	codes = strings.TrimSpace(codes)
	if codes == "" {
		return nil
	}
	parts := strings.Split(codes, "-")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return nil
			}
		}
		out = append(out, part)
	}
	return out
}

func (b *LarkReplyBridge) runSessionStartPresets(sess Session, codes string) error {
	if strings.TrimSpace(codes) == "" {
		return nil
	}
	rt, ok := b.manager.GetRuntime(sess.ID)
	if !ok {
		return fmt.Errorf("runtime not found")
	}
	b.mu.Lock()
	presets := copySessionStartPresets(b.startPresets)
	b.mu.Unlock()
	vars := sessionStartPresetVars(sess, codes)
	presetCodes := splitPresetCodes(codes)
	if len(presetCodes) > 0 {
		rt.SuppressStartupNotifications()
	}
	for _, code := range presetCodes {
		preset, ok := presets[code]
		if !ok {
			log.Printf("lark start preset not configured session=%s code=%q", sess.ID, code)
			continue
		}
		if err := runSessionPresetCommands(rt, preset, vars); err != nil {
			return err
		}
	}
	if len(presetCodes) > 0 {
		rt.FinishStartupNotifications()
	}
	return nil
}

func (b *LarkReplyBridge) runSessionNamePreset(sess Session, codes string) error {
	rt, ok := b.manager.GetRuntime(sess.ID)
	if !ok {
		return fmt.Errorf("runtime not found")
	}
	b.mu.Lock()
	presets := copySessionStartPresets(b.namePresets)
	b.mu.Unlock()
	preset, ok := presets[sess.Name]
	if !ok {
		return nil
	}
	rt.SuppressStartupNotifications()
	vars := sessionStartPresetVars(sess, codes)
	if err := runSessionPresetCommands(rt, preset, vars); err != nil {
		return err
	}
	rt.FinishStartupNotifications()
	return nil
}

func runSessionPresetCommands(rt *RuntimeSession, preset SessionStartPreset, vars map[string]string) error {
	for _, template := range preset.Commands {
		command := renderSessionStartPresetCommand(template, vars)
		if strings.TrimSpace(command) == "" {
			continue
		}
		if !strings.HasSuffix(command, "\r") && !strings.HasSuffix(command, "\n") {
			command += "\r"
		}
		if _, err := rt.terminal.Write([]byte(command)); err != nil {
			return err
		}
	}
	return nil
}

func copySessionStartPresets(in map[string]SessionStartPreset) map[string]SessionStartPreset {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]SessionStartPreset, len(in))
	for key, preset := range in {
		cp := make([]string, len(preset.Commands))
		copy(cp, preset.Commands)
		out[key] = SessionStartPreset{Commands: cp}
	}
	return out
}

func sessionStartPresetVars(sess Session, codes string) map[string]string {
	timestamp := time.Now().Format(time.RFC3339)
	return map[string]string{
		"session_name":          shellQuote(sess.Name),
		"session_name_raw":      sess.Name,
		"session_id":            shellQuote(sess.ID),
		"session_id_raw":        sess.ID,
		"preset_codes":          shellQuote(codes),
		"preset_codes_raw":      codes,
		"timestamp":             shellQuote(timestamp),
		"timestamp_raw":         timestamp,
		"session_name_slug":     shellQuote(slugForShellPath(sess.Name)),
		"session_name_slug_raw": slugForShellPath(sess.Name),
	}
}

func renderSessionStartPresetCommand(template string, vars map[string]string) string {
	out := template
	for key, value := range vars {
		out = strings.ReplaceAll(out, "{{"+key+"}}", value)
	}
	return out
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func slugForShellPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "session"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r > 127 {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (b *LarkReplyBridge) OnNotificationSent(sessionID string) {
	next := b.popPipeline(sessionID)
	if next == "" {
		return
	}
	rt, ok := b.manager.GetRuntime(sessionID)
	if !ok {
		return
	}
	b.manager.EnsureBrowser(sessionID)
	if err := SubmitStructuredInput(rt, next); err != nil {
		log.Printf("lark reply bridge failed to continue pipeline for %s: %v", sessionID, err)
	}
}

func (b *LarkReplyBridge) enqueuePipeline(sessionID string, parts []string) {
	if sessionID == "" || len(parts) == 0 {
		return
	}
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pipelines[sessionID] = append(b.pipelines[sessionID], cleaned...)
}

func (b *LarkReplyBridge) popPipeline(sessionID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := b.pipelines[sessionID]
	if len(queue) == 0 {
		return ""
	}
	next := queue[0]
	if len(queue) == 1 {
		delete(b.pipelines, sessionID)
	} else {
		b.pipelines[sessionID] = queue[1:]
	}
	return next
}

func (b *LarkReplyBridge) appendPendingFiles(sessionID string, files []pendingLarkAttachment) {
	if sessionID == "" || len(files) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingFiles[sessionID] = append(b.pendingFiles[sessionID], files...)
}

func (b *LarkReplyBridge) hasPendingFiles(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pendingFiles[sessionID]) > 0
}

func (b *LarkReplyBridge) clearPendingFiles(sessionID string) {
	if sessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pendingFiles, sessionID)
}

func isCurrentRoundCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "/c" || text == "／c"
}

func isStopCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "/stop" || text == "／stop"
}

func (b *LarkReplyBridge) replyLarkText(ctx context.Context, messageID string, text string) error {
	if b.replyText == nil || messageID == "" {
		return nil
	}
	return b.replyText(ctx, messageID, truncateForLark(sanitizeForLarkAudit(text)))
}

func (b *LarkReplyBridge) replyTextToMessage(ctx context.Context, messageID string, text string) error {
	if b.apiClient == nil {
		return nil
	}
	content, _ := json.Marshal(map[string]string{"text": text})
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType("text").
			Content(string(content)).
			ReplyInThread(false).
			Build()).
		Build()
	resp, err := b.apiClient.Im.V1.Message.Reply(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("lark reply API returned code %d: %s", resp.Code, resp.Msg)
	}
	return nil
}

func (b *LarkReplyBridge) createLarkChat(ctx context.Context, sessionID, sessionName, ownerOpenID string) (string, error) {
	if b.apiClient == nil {
		return "", nil
	}
	name := larkSessionChatName(sessionName)
	body := larkim.NewCreateChatReqBodyBuilder().
		Name(name).
		UserIdList([]string{ownerOpenID}).
		ChatMode("group").
		ChatType("private").
		JoinMessageVisibility("not_anyone").
		LeaveMessageVisibility("not_anyone").
		MembershipApproval("no_approval_required").
		Build()
	req := larkim.NewCreateChatReqBuilder().
		UserIdType("open_id").
		Uuid("easy-terminal-" + sessionID).
		Body(body).
		Build()
	resp, err := b.apiClient.Im.V1.Chat.Create(ctx, req)
	if err != nil {
		return "", err
	}
	if !resp.Success() {
		return "", fmt.Errorf("lark create chat API returned code %d: %s", resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.ChatId == nil || *resp.Data.ChatId == "" {
		return "", fmt.Errorf("lark create chat API returned empty chat_id")
	}
	return *resp.Data.ChatId, nil
}

func larkSessionChatName(sessionName string) string {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		sessionName = "session"
	}
	const prefix = "ET · "
	rs := []rune(sessionName)
	if len(rs) > 40 {
		sessionName = string(rs[:40])
	}
	return prefix + sessionName
}

func (b *LarkReplyBridge) sendTextToChat(ctx context.Context, chatID string, text string) error {
	if b.apiClient == nil || chatID == "" {
		return nil
	}
	content, _ := json.Marshal(map[string]string{"text": text})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("text").
			Content(string(content)).
			Build()).
		Build()
	resp, err := b.apiClient.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("lark chat text API returned code %d: %s", resp.Code, resp.Msg)
	}
	return nil
}

func (b *LarkReplyBridge) addLarkMessageReaction(ctx context.Context, messageID string, emoji string) error {
	if b.apiClient == nil || messageID == "" || emoji == "" {
		return nil
	}
	req := larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(emoji).Build()).
			Build()).
		Build()
	resp, err := b.apiClient.Im.V1.MessageReaction.Create(ctx, req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("lark reaction API returned code %d: %s", resp.Code, resp.Msg)
	}
	return nil
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

func (b *LarkReplyBridge) resolveSessionID(ctx context.Context, text, parentID, rootID, chatID, chatType string) string {
	if id, ok := defaultLarkMessageRegistry.lookupChat(chatID); ok {
		return id
	}
	if b.manager != nil && chatID != "" {
		if s, ok, err := b.manager.FindSessionByLarkChatID(ctx, chatID); err == nil && ok && s.Live && s.Status != StatusExited && s.Status != StatusFailed {
			defaultLarkMessageRegistry.rememberChat(chatID, s.ID)
			return s.ID
		}
	}
	if id, ok := defaultLarkMessageRegistry.lookup(parentID, rootID); ok {
		return id
	}
	if m := regexp.MustCompile(`sess-\d+`).FindString(text); m != "" {
		return m
	}
	if chatID != "" && chatType == "group" {
		return ""
	}
	return defaultLarkMessageRegistry.latestNotifiedSessionID()
}

func larkSenderOpenID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil || sender.SenderId.OpenId == nil {
		return ""
	}
	return *sender.SenderId.OpenId
}

func cleanLarkText(text string) string {
	text = regexp.MustCompile(`<at[^>]*>.*?</at>`).ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func splitLarkPipeline(text string) []string {
	var parts []string
	var b strings.Builder
	escaped := false
	for _, r := range text {
		switch {
		case escaped:
			if r != '|' {
				b.WriteRune('\\')
			}
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case isLarkPipelineSeparator(r):
			if part := strings.TrimSpace(b.String()); part != "" {
				parts = append(parts, part)
			}
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if part := strings.TrimSpace(b.String()); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func isLarkPipelineSeparator(r rune) bool {
	switch r {
	case '|', '｜', '︱', '￨':
		return true
	default:
		return false
	}
}

func PrepareStructuredInput(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text + "\r"
}

func SubmitStructuredInput(rt *RuntimeSession, text string) error {
	if rt == nil {
		return fmt.Errorf("runtime not found")
	}
	text = strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	sessionID := rt.Snapshot().ID
	log.Printf("lark reply bridge submitting structured input session=%s text_len=%d enter=true enter_len=%d", sessionID, len(text), len(structuredInputEnterSequence))
	rt.MarkInputActivity(text + "\r")
	if _, err := rt.terminal.Write([]byte(text)); err != nil {
		return err
	}
	if structuredInputEnterDelay > 0 {
		time.Sleep(structuredInputEnterDelay)
	}
	enter := structuredInputEnterSequence
	if enter == "" {
		enter = "\r"
	}
	_, err := rt.terminal.Write([]byte(enter))
	return err
}

type larkIncomingMessage struct {
	Text        string
	Attachments []larkAttachmentRef
}

type larkAttachmentRef struct {
	Kind string
	Key  string
	Name string
}

type pendingLarkAttachment struct {
	Kind string
	Path string
}

func extractLarkMessageText(content string, messageType string) string {
	return extractLarkIncomingMessage(content, messageType).Text
}

func extractLarkIncomingMessage(content string, messageType string) larkIncomingMessage {
	content = strings.TrimSpace(content)
	if content == "" {
		return larkIncomingMessage{}
	}
	var raw any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return larkIncomingMessage{Text: content}
	}
	var incoming larkIncomingMessage
	switch messageType {
	case "text":
		if m, ok := raw.(map[string]any); ok {
			incoming.Text = strings.TrimSpace(stringFromAny(m["text"]))
		}
	case "post":
		var parts []string
		collectPostText(raw, &parts)
		incoming.Text = strings.TrimSpace(strings.Join(parts, ""))
		collectLarkAttachmentRefs(raw, &incoming.Attachments)
	case "image":
		collectLarkAttachmentRefs(raw, &incoming.Attachments)
		incoming.Text = strings.TrimSpace(collectLarkPlainTextFields(raw))
	case "file":
		collectLarkAttachmentRefs(raw, &incoming.Attachments)
		incoming.Text = strings.TrimSpace(collectLarkPlainTextFields(raw))
	default:
		if m, ok := raw.(map[string]any); ok {
			if text := strings.TrimSpace(stringFromAny(m["text"])); text != "" {
				incoming.Text = text
			}
		}
		var parts []string
		collectPostText(raw, &parts)
		if incoming.Text == "" && len(parts) > 0 {
			incoming.Text = strings.TrimSpace(strings.Join(parts, ""))
		}
		collectLarkAttachmentRefs(raw, &incoming.Attachments)
	}
	if messageType == "image" || messageType == "file" {
		collectLarkAttachmentRefs(raw, &incoming.Attachments)
	}
	incoming.Attachments = dedupeLarkAttachmentRefs(incoming.Attachments)
	return incoming
}

func collectLarkPlainTextFields(v any) string {
	var parts []string
	collectLarkPlainTextFieldParts(v, &parts)
	return strings.Join(parts, "")
}

func collectLarkPlainTextFieldParts(v any, parts *[]string) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			collectLarkPlainTextFieldParts(item, parts)
		}
	case map[string]any:
		for _, key := range []string{"text", "caption"} {
			if text := stringFromAny(x[key]); strings.TrimSpace(text) != "" {
				*parts = append(*parts, text)
			}
		}
		for _, key := range []string{"content", "elements"} {
			if child, ok := x[key]; ok {
				collectLarkPlainTextFieldParts(child, parts)
			}
		}
	}
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

func collectLarkAttachmentRefs(v any, refs *[]larkAttachmentRef) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			collectLarkAttachmentRefs(item, refs)
		}
	case map[string]any:
		if key := stringFromAny(x["image_key"]); key != "" {
			*refs = append(*refs, larkAttachmentRef{Kind: "image", Key: key, Name: stringFromAny(x["file_name"])})
		}
		if key := stringFromAny(x["file_key"]); key != "" {
			*refs = append(*refs, larkAttachmentRef{Kind: "file", Key: key, Name: stringFromAny(x["file_name"])})
		}
		for _, child := range x {
			collectLarkAttachmentRefs(child, refs)
		}
	}
}

func dedupeLarkAttachmentRefs(refs []larkAttachmentRef) []larkAttachmentRef {
	if len(refs) < 2 {
		return refs
	}
	seen := make(map[string]bool, len(refs))
	out := refs[:0]
	for _, ref := range refs {
		id := ref.Kind + ":" + ref.Key
		if ref.Key == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ref)
	}
	return out
}

func formatLarkAttachmentInput(files []pendingLarkAttachment) string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if file.Path != "" {
			paths = append(paths, quoteLarkInputPath(file.Path))
		}
	}
	return strings.Join(paths, " ")
}

func quoteLarkInputPath(path string) string {
	if path == "" {
		return ""
	}
	if !strings.ContainsAny(path, " \t\n\r\"'\\") {
		return path
	}
	return strconvQuote(path)
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func larkAttachmentUploadSuccessMessage(files []pendingLarkAttachment) string {
	images, regularFiles := 0, 0
	for _, file := range files {
		if file.Kind == "image" {
			images++
		} else {
			regularFiles++
		}
	}
	switch {
	case images > 0 && regularFiles > 0:
		return fmt.Sprintf("%d 张图片、%d 个文件已上传成功，等待你的说明后执行。", images, regularFiles)
	case images > 1:
		return fmt.Sprintf("%d 张图片已上传成功，等待你的说明后执行。", images)
	case images == 1:
		return "图片已上传成功，等待你的说明后执行。"
	case regularFiles > 1:
		return fmt.Sprintf("%d 个文件已上传成功，等待你的说明后执行。", regularFiles)
	default:
		return "文件已上传成功，等待你的说明后执行。"
	}
}

func larkAttachmentKindLabel(kind string) string {
	if kind == "image" {
		return "图片"
	}
	return "文件"
}

func (b *LarkReplyBridge) downloadLarkAttachment(ctx context.Context, messageID, sessionID string, ref larkAttachmentRef) (pendingLarkAttachment, error) {
	if b.apiClient == nil {
		return pendingLarkAttachment{}, errors.New("lark client is not configured")
	}
	resourceType := ref.Kind
	if resourceType == "" {
		resourceType = "file"
	}
	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(ref.Key).
		Type(resourceType).
		Build()
	resp, err := b.apiClient.Im.V1.MessageResource.Get(ctx, req)
	if err != nil {
		return pendingLarkAttachment{}, err
	}
	if !resp.Success() {
		return pendingLarkAttachment{}, fmt.Errorf("飞书资源接口返回 code %d: %s", resp.Code, resp.Msg)
	}
	dir := filepath.Join(b.uploadsDir, sessionID, "lark")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return pendingLarkAttachment{}, err
	}
	filename := safeLarkAttachmentFilename(ref, resp.FileName)
	path := filepath.Join(dir, time.Now().Format("20060102150405.000000000")+"_"+filename)
	if err := resp.WriteFile(path); err != nil {
		return pendingLarkAttachment{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return pendingLarkAttachment{Kind: resourceType, Path: abs}, nil
}

func safeLarkAttachmentFilename(ref larkAttachmentRef, responseName string) string {
	name := ref.Name
	if name == "" {
		name = responseName
	}
	if name == "" {
		if ref.Kind == "image" {
			name = "image"
		} else {
			name = "file"
		}
	}
	name = filepath.Base(name)
	name = regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(name, "_")
	name = strings.Trim(name, "._-")
	if name == "" {
		name = "file"
	}
	if filepath.Ext(name) == "" && ref.Kind == "image" {
		name += ".png"
	}
	return name
}

func valueOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
