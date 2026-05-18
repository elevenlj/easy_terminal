package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	larkAPIRetryAttempts            = 3
	larkAPIRetryDelay               = 120 * time.Millisecond
	larkCustomShortcutButtonsPerRow = 3
)

type LarkAppNotifier struct {
	appID            string
	appSecret        string
	client           *lark.Client
	receiveID        string
	mention          bool
	customShortcutMu sync.RWMutex
	customShortcuts  []LarkCustomShortcut
	tipMu            sync.Mutex
	tipSent          map[string]map[int]bool
	tipSender        func(string, string, int) error
}

func NewLarkAppNotifier(appID, appSecret, receiveID string, mention bool) *LarkAppNotifier {
	if appID == "" || appSecret == "" || receiveID == "" {
		return &LarkAppNotifier{receiveID: receiveID, mention: mention}
	}
	return &LarkAppNotifier{
		appID:     appID,
		appSecret: appSecret,
		client:    lark.NewClient(appID, appSecret),
		receiveID: receiveID,
		mention:   mention,
		tipSent:   make(map[string]map[int]bool),
	}
}

func (n *LarkAppNotifier) Available() bool {
	return n != nil && n.client != nil
}

func (n *LarkAppNotifier) SetCustomShortcuts(shortcuts []LarkCustomShortcut) {
	if n == nil {
		return
	}
	n.customShortcutMu.Lock()
	defer n.customShortcutMu.Unlock()
	n.customShortcuts = normalizeLarkCustomShortcuts(shortcuts)
}

func (n *LarkAppNotifier) customShortcutSnapshot() []LarkCustomShortcut {
	n.customShortcutMu.RLock()
	defer n.customShortcutMu.RUnlock()
	cp := make([]LarkCustomShortcut, len(n.customShortcuts))
	copy(cp, n.customShortcuts)
	return cp
}

func (n *LarkAppNotifier) NotifyWaiting(note WaitingNotification) (WaitingNotificationResult, error) {
	if !n.Available() {
		return WaitingNotificationResult{}, errors.New("lark notifier is not configured")
	}
	content, err := larkNotificationCardContent(note, n.receiveID, n.mention, n.customShortcutSnapshot()...)
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	if note.MessageID != "" {
		return n.updateWaiting(note, content)
	}
	return n.createWaiting(note, content)
}

func larkNotificationCardContent(note WaitingNotification, receiveID string, mention bool, customShortcuts ...LarkCustomShortcut) (string, error) {
	elements := []map[string]any{}
	mentionID := larkNotificationMentionID(note, receiveID)
	if mention && mentionID != "" {
		elements = append(elements, map[string]any{"tag": "markdown", "content": "<at id=" + mentionID + "></at>"})
	}
	elements = append(elements, larkTerminalTextElement(note.Content))
	elements = append(elements, map[string]any{"tag": "markdown", "content": larkNotificationStatusLine(note)})
	if !note.Disabled {
		elements = append(elements, larkShortcutActionElements(note.SessionID, note.UpdateNo, note.AutoRefreshEnabled, note.AutoSummaryEnabled, note.MentionModeEnabled)...)
		if shortcuts := normalizeLarkCustomShortcuts(customShortcuts); len(shortcuts) > 0 {
			elements = append(elements, larkCustomShortcutActionElements(note.SessionID, shortcuts)...)
		}
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true, "update_multi": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": larkNotificationTitle(note)},
		},
		"body": map[string]any{"elements": elements},
	}
	b, err := json.Marshal(card)
	return string(b), err
}

func larkNotificationMentionID(note WaitingNotification, receiveID string) string {
	if id := strings.TrimSpace(note.MentionOpenID); id != "" {
		return id
	}
	return strings.TrimSpace(receiveID)
}

func normalizeLarkCustomShortcuts(shortcuts []LarkCustomShortcut) []LarkCustomShortcut {
	out := make([]LarkCustomShortcut, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		label := strings.TrimSpace(shortcut.Label)
		command := strings.TrimSpace(shortcut.Command)
		if label == "" || command == "" {
			continue
		}
		out = append(out, LarkCustomShortcut{Label: label, Command: command})
	}
	return out
}

func larkTerminalTextElement(content string) map[string]any {
	return map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "plain_text",
			"content": larkTerminalPlainText(content),
		},
	}
}

func larkTerminalPlainText(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if larkNotifyMergeWrappedLines.Load() {
		content = mergeTerminalWrappedLinesForLark(content)
	}
	return content
}

func larkShortcutActionElements(sessionID string, updateNo int, autoRefreshEnabled bool, autoSummaryEnabled bool, mentionModeEnabled bool) []map[string]any {
	return []map[string]any{
		larkFlowShortcutActionElement(
			larkRefreshButtonColumn(sessionID, updateNo),
			larkAutoRefreshButtonColumn(sessionID, updateNo, autoRefreshEnabled),
			larkAutoSummaryButtonColumn(sessionID, updateNo, autoSummaryEnabled),
			larkMentionModeButtonColumn(sessionID, updateNo, mentionModeEnabled),
			larkDeleteSessionButtonColumn(sessionID),
			larkShortcutButtonColumn("Ctrl-C", "primary", sessionID, "ctrl_c"),
			larkShortcutButtonColumn("退出agent", "primary", sessionID, "exit_agent"),
			larkShortcutButtonColumn("Esc", "primary", sessionID, "esc"),
			larkShortcutButtonColumn("Enter", "primary", sessionID, "enter"),
		),
	}
}

func larkShortcutActionElement(columns ...map[string]any) map[string]any {
	return larkShortcutActionElementWithFlexMode("none", columns...)
}

func larkFlowShortcutActionElement(columns ...map[string]any) map[string]any {
	return larkShortcutActionElementWithFlexMode("flow", columns...)
}

func larkShortcutActionElementWithFlexMode(flexMode string, columns ...map[string]any) map[string]any {
	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          flexMode,
		"horizontal_align":   "left",
		"horizontal_spacing": "4px",
		"columns":            columns,
	}
}

func larkMentionModeButtonColumn(sessionID string, updateNo int, enabled bool) map[string]any {
	label := "艾特模式"
	if enabled {
		label = "停艾特"
	}
	return map[string]any{
		"tag":              "column",
		"width":            "auto",
		"vertical_spacing": "8px",
		"elements": []map[string]any{
			{
				"tag":   "button",
				"type":  "primary",
				"size":  "tiny",
				"width": "default",
				"text":  map[string]any{"tag": "plain_text", "content": label},
				"behaviors": []map[string]any{
					{
						"type": "callback",
						"value": map[string]any{
							"easy_terminal_action": "toggle_mention_mode",
							"session_id":           sessionID,
							"update_no":            updateNo,
						},
					},
				},
			},
		},
	}
}

func larkAutoRefreshButtonColumn(sessionID string, updateNo int, enabled bool) map[string]any {
	label := "自动刷新"
	if enabled {
		label = "停自动"
	}
	return map[string]any{
		"tag":              "column",
		"width":            "auto",
		"vertical_spacing": "8px",
		"elements": []map[string]any{
			{
				"tag":   "button",
				"type":  "primary",
				"size":  "tiny",
				"width": "default",
				"text":  map[string]any{"tag": "plain_text", "content": label},
				"behaviors": []map[string]any{
					{
						"type": "callback",
						"value": map[string]any{
							"easy_terminal_action": "toggle_auto_refresh",
							"session_id":           sessionID,
							"update_no":            updateNo,
						},
					},
				},
			},
		},
	}
}

func larkAutoSummaryButtonColumn(sessionID string, updateNo int, enabled bool) map[string]any {
	label := "自动总结"
	if enabled {
		label = "停总结"
	}
	return map[string]any{
		"tag":              "column",
		"width":            "auto",
		"vertical_spacing": "8px",
		"elements": []map[string]any{
			{
				"tag":   "button",
				"type":  "primary",
				"size":  "tiny",
				"width": "default",
				"text":  map[string]any{"tag": "plain_text", "content": label},
				"behaviors": []map[string]any{
					{
						"type": "callback",
						"value": map[string]any{
							"easy_terminal_action": "toggle_auto_summary",
							"session_id":           sessionID,
							"update_no":            updateNo,
						},
					},
				},
			},
		},
	}
}

func larkShortcutButtonColumn(label, buttonType, sessionID, key string) map[string]any {
	return map[string]any{
		"tag":              "column",
		"width":            "auto",
		"vertical_spacing": "8px",
		"elements": []map[string]any{
			larkShortcutButton(label, buttonType, sessionID, key),
		},
	}
}

func larkShortcutButton(label, buttonType, sessionID, key string) map[string]any {
	return map[string]any{
		"tag":   "button",
		"type":  buttonType,
		"size":  "tiny",
		"width": "default",
		"text":  map[string]any{"tag": "plain_text", "content": label},
		"behaviors": []map[string]any{
			{
				"type": "callback",
				"value": map[string]any{
					"easy_terminal_action": "shortcut",
					"session_id":           sessionID,
					"key":                  key,
				},
			},
		},
	}
}

func larkDeleteSessionButtonColumn(sessionID string) map[string]any {
	return map[string]any{
		"tag":              "column",
		"width":            "auto",
		"vertical_spacing": "8px",
		"elements": []map[string]any{
			{
				"tag":     "button",
				"type":    "danger",
				"size":    "tiny",
				"width":   "default",
				"text":    map[string]any{"tag": "plain_text", "content": "删除会话"},
				"confirm": larkDeleteSessionConfirm(),
				"behaviors": []map[string]any{
					{
						"type": "callback",
						"value": map[string]any{
							"easy_terminal_action": "delete_session",
							"session_id":           sessionID,
						},
					},
				},
			},
		},
	}
}

func larkDeleteSessionConfirm() map[string]any {
	return map[string]any{
		"title": map[string]any{"tag": "plain_text", "content": "确认删除会话？"},
		"text":  map[string]any{"tag": "plain_text", "content": "删除后会关闭终端会话，并把机器人从当前群聊移除。"},
	}
}

func larkRefreshButtonColumn(sessionID string, updateNo int) map[string]any {
	return map[string]any{
		"tag":              "column",
		"width":            "auto",
		"vertical_spacing": "8px",
		"elements": []map[string]any{
			{
				"tag":   "button",
				"type":  "primary",
				"size":  "tiny",
				"width": "default",
				"text":  map[string]any{"tag": "plain_text", "content": "刷新"},
				"behaviors": []map[string]any{
					{
						"type": "callback",
						"value": map[string]any{
							"easy_terminal_action": "refresh",
							"session_id":           sessionID,
							"update_no":            updateNo,
						},
					},
				},
			},
		},
	}
}

func larkCustomShortcutActionElements(sessionID string, shortcuts []LarkCustomShortcut) []map[string]any {
	rows := make([]map[string]any, 0, (len(shortcuts)+larkCustomShortcutButtonsPerRow-1)/larkCustomShortcutButtonsPerRow)
	for start := 0; start < len(shortcuts); start += larkCustomShortcutButtonsPerRow {
		end := start + larkCustomShortcutButtonsPerRow
		if end > len(shortcuts) {
			end = len(shortcuts)
		}
		rows = append(rows, larkCustomShortcutActionElement(sessionID, shortcuts[start:end]))
	}
	return rows
}

func larkCustomShortcutActionElement(sessionID string, shortcuts []LarkCustomShortcut) map[string]any {
	columns := make([]map[string]any, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		columns = append(columns, larkCustomShortcutButtonColumn(sessionID, shortcut))
	}
	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          "none",
		"horizontal_align":   "left",
		"horizontal_spacing": "4px",
		"columns":            columns,
	}
}

func larkCustomShortcutButtonColumn(sessionID string, shortcut LarkCustomShortcut) map[string]any {
	return map[string]any{
		"tag":              "column",
		"width":            "auto",
		"vertical_spacing": "8px",
		"elements": []map[string]any{
			{
				"tag":   "button",
				"type":  "primary",
				"size":  "tiny",
				"width": "default",
				"text":  map[string]any{"tag": "plain_text", "content": shortcut.Label},
				"behaviors": []map[string]any{
					{
						"type": "callback",
						"value": map[string]any{
							"easy_terminal_action": "custom_shortcut",
							"session_id":           sessionID,
							"command":              shortcut.Command,
						},
					},
				},
			},
		},
	}
}

func larkNotificationTitle(note WaitingNotification) string {
	if note.Running && !note.Disabled {
		return note.Name + "（Running）"
	}
	return note.Name
}

func larkNotificationStatusLine(note WaitingNotification) string {
	prefix := ""
	if note.UpdateNo > 0 {
		prefix = fmt.Sprintf("已更新-%d · ", note.UpdateNo)
	}
	if note.Disabled {
		return prefix + "状态：disabled"
	}
	if note.Running {
		return prefix + `状态：<font color="green">Running</font>` + larkMentionModeStatusSuffix(note.MentionModeEnabled)
	}
	return prefix + "状态：Not Running" + larkMentionModeStatusSuffix(note.MentionModeEnabled)
}

func larkMentionModeStatusSuffix(enabled bool) string {
	if enabled {
		return " · 艾特模式：开"
	}
	return " · 艾特模式：关"
}

func (n *LarkAppNotifier) createWaiting(note WaitingNotification, content string) (WaitingNotificationResult, error) {
	token, err := n.tenantAccessToken(context.Background())
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	receiveID := n.receiveID
	receiveIDType := "open_id"
	if note.ChatID != "" {
		receiveID = note.ChatID
		receiveIDType = "chat_id"
	}
	if receiveID == "" {
		return WaitingNotificationResult{}, errors.New("lark notification receiver is not configured")
	}
	payload, _ := json.Marshal(map[string]any{
		"receive_id": receiveID,
		"msg_type":   "interactive",
		"content":    string(content),
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type="+receiveIDType, bytes.NewReader(payload))
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := doHTTPRequestWithRetry(req)
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return WaitingNotificationResult{}, fmt.Errorf("lark message API returned %s: %s", resp.Status, string(body))
	}
	var createResp struct {
		Code int `json:"code"`
		Data struct {
			MessageID string `json:"message_id"`
			RootID    string `json:"root_id"`
			ParentID  string `json:"parent_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err == nil && createResp.Code == 0 {
		defaultLarkMessageRegistry.remember(note.SessionID, createResp.Data.MessageID, createResp.Data.RootID, createResp.Data.ParentID)
		return WaitingNotificationResult{MessageID: createResp.Data.MessageID, RootID: createResp.Data.RootID, ParentID: createResp.Data.ParentID}, nil
	} else {
		defaultLarkMessageRegistry.rememberLatest(note.SessionID)
		if createResp.Code != 0 {
			return WaitingNotificationResult{}, fmt.Errorf("lark message API returned code %d", createResp.Code)
		}
	}
	return WaitingNotificationResult{}, nil
}

func (n *LarkAppNotifier) updateWaiting(note WaitingNotification, content string) (WaitingNotificationResult, error) {
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(note.MessageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(content).
			Build()).
		Build()
	resp, err := retryLarkPatchMessage(func() (*larkim.PatchMessageResp, error) {
		return n.client.Im.V1.Message.Patch(context.Background(), req)
	})
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	if !resp.Success() {
		return WaitingNotificationResult{}, fmt.Errorf("lark patch message API returned code %d: %s", resp.Code, resp.Msg)
	}
	tipSent := false
	if note.UpdateNo > 0 && !note.SuppressUpdateTip {
		if err := n.sendUpdateTipOnce(note.MessageID, note.ChatID, note.UpdateNo, larkNotificationMentionID(note, n.receiveID)); err == nil {
			tipSent = true
		}
	}
	defaultLarkMessageRegistry.remember(note.SessionID, note.MessageID)
	return WaitingNotificationResult{MessageID: note.MessageID, Updated: true, TipSent: tipSent}, nil
}

func (n *LarkAppNotifier) UpdateWaitingRunning(note WaitingNotification, running bool) error {
	if !n.Available() || note.MessageID == "" {
		return nil
	}
	note.Running = running
	content, err := larkNotificationCardContent(note, n.receiveID, n.mention, n.customShortcutSnapshot()...)
	if err != nil {
		return err
	}
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(note.MessageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(content).
			Build()).
		Build()
	resp, err := retryLarkPatchMessage(func() (*larkim.PatchMessageResp, error) {
		return n.client.Im.V1.Message.Patch(context.Background(), req)
	})
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("lark patch message API returned code %d: %s", resp.Code, resp.Msg)
	}
	defaultLarkMessageRegistry.remember(note.SessionID, note.MessageID)
	return nil
}

func (n *LarkAppNotifier) sendUpdateTipOnce(messageID string, chatID string, updateNo int, mentionID string) error {
	if messageID == "" || updateNo <= 0 {
		return nil
	}
	n.tipMu.Lock()
	if n.tipSent == nil {
		n.tipSent = make(map[string]map[int]bool)
	}
	sent := n.tipSent[messageID]
	if sent == nil {
		sent = make(map[int]bool)
		n.tipSent[messageID] = sent
	}
	if sent[updateNo] {
		n.tipMu.Unlock()
		return nil
	}
	n.tipMu.Unlock()

	send := n.sendUpdateTip
	if n.tipSender != nil {
		send = func(messageID string, chatID string, updateNo int, _ string) error {
			return n.tipSender(messageID, chatID, updateNo)
		}
	}
	if err := retryLarkVoid(func() error { return send(messageID, chatID, updateNo, mentionID) }); err != nil {
		return err
	}

	n.tipMu.Lock()
	sent = n.tipSent[messageID]
	if sent == nil {
		sent = make(map[int]bool)
		n.tipSent[messageID] = sent
	}
	sent[updateNo] = true
	n.tipMu.Unlock()
	return nil
}

func (n *LarkAppNotifier) sendUpdateTip(messageID string, chatID string, updateNo int, mentionID string) error {
	content, err := larkUpdateTipCardContent(updateNo, mentionID, n.mention)
	if err != nil {
		return err
	}
	receiveID := strings.TrimSpace(chatID)
	receiveIDType := "chat_id"
	if receiveID == "" {
		receiveID = strings.TrimSpace(n.receiveID)
		receiveIDType = "open_id"
	}
	if receiveID == "" {
		return nil
	}
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(receiveID).
			MsgType("interactive").
			Content(content).
			Build()).
		Build()
	resp, err := retryLarkCreateMessage(func() (*larkim.CreateMessageResp, error) {
		return n.client.Im.V1.Message.Create(context.Background(), req)
	})
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("lark update tip message API returned code %d: %s", resp.Code, resp.Msg)
	}
	return nil
}

func larkUpdateTipCardContent(updateNo int, receiveID string, mention bool) (string, error) {
	elements := []map[string]any{}
	if mention && strings.TrimSpace(receiveID) != "" {
		elements = append(elements, map[string]any{"tag": "markdown", "content": "<at id=" + strings.TrimSpace(receiveID) + "></at>"})
	}
	elements = append(elements, map[string]any{"tag": "note", "elements": []map[string]any{{"tag": "plain_text", "content": fmt.Sprintf("已更新-%d", updateNo)}}})
	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": false},
		"elements": elements,
	}
	b, err := json.Marshal(card)
	return string(b), err
}

func (n *LarkAppNotifier) tenantAccessToken(ctx context.Context) (string, error) {
	payload, _ := json.Marshal(map[string]string{"app_id": n.appID, "app_secret": n.appSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := doHTTPRequestWithRetry(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var data struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 || data.Code != 0 || data.TenantAccessToken == "" {
		if data.Msg == "" {
			data.Msg = resp.Status
		}
		return "", errors.New(data.Msg)
	}
	return data.TenantAccessToken, nil
}

func doHTTPRequestWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= larkAPIRetryAttempts; attempt++ {
		cloned := req.Clone(req.Context())
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			cloned.Body = body
		}
		resp, err := http.DefaultClient.Do(cloned)
		if err == nil && resp != nil && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("%s: %s", resp.Status, string(body))
		} else {
			lastErr = err
		}
		if attempt < larkAPIRetryAttempts {
			time.Sleep(time.Duration(attempt) * larkAPIRetryDelay)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("lark request failed")
	}
	return nil, lastErr
}

func retryLarkPatchMessage(fn func() (*larkim.PatchMessageResp, error)) (*larkim.PatchMessageResp, error) {
	var lastResp *larkim.PatchMessageResp
	err := retryLarkVoid(func() error {
		resp, err := fn()
		lastResp = resp
		if err != nil {
			return err
		}
		if resp == nil {
			return errors.New("lark patch message API returned empty response")
		}
		if resp != nil && !resp.Success() && retryableLarkCode(resp.Code) {
			return fmt.Errorf("lark patch message API returned code %d: %s", resp.Code, resp.Msg)
		}
		return nil
	})
	return lastResp, err
}

func retryLarkCreateMessage(fn func() (*larkim.CreateMessageResp, error)) (*larkim.CreateMessageResp, error) {
	var lastResp *larkim.CreateMessageResp
	err := retryLarkVoid(func() error {
		resp, err := fn()
		lastResp = resp
		if err != nil {
			return err
		}
		if resp == nil {
			return errors.New("lark create message API returned empty response")
		}
		if resp != nil && !resp.Success() && retryableLarkCode(resp.Code) {
			return fmt.Errorf("lark create message API returned code %d: %s", resp.Code, resp.Msg)
		}
		return nil
	})
	return lastResp, err
}

func retryLarkVoid(fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= larkAPIRetryAttempts; attempt++ {
		if err := fn(); err != nil {
			lastErr = err
			if attempt < larkAPIRetryAttempts {
				time.Sleep(time.Duration(attempt) * larkAPIRetryDelay)
			}
			continue
		}
		return nil
	}
	return lastErr
}

func retryableLarkCode(code int) bool {
	return code == 99991400 || code == 99991663 || code >= 50000000
}
