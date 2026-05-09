package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type LarkAppNotifier struct {
	appID     string
	appSecret string
	client    *lark.Client
	receiveID string
	mention   bool
	tipMu     sync.Mutex
	tipSent   map[string]map[int]bool
	tipSender func(string, int) error
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
	return n != nil && n.client != nil && n.receiveID != ""
}

func (n *LarkAppNotifier) NotifyWaiting(note WaitingNotification) (WaitingNotificationResult, error) {
	if !n.Available() {
		return WaitingNotificationResult{}, errors.New("lark notifier is not configured")
	}
	content, err := larkNotificationCardContent(note, n.receiveID, n.mention)
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	if note.MessageID != "" {
		return n.updateWaiting(note, content)
	}
	return n.createWaiting(note, content)
}

func larkNotificationCardContent(note WaitingNotification, receiveID string, mention bool) (string, error) {
	elements := []map[string]any{
		{"tag": "div", "text": map[string]any{"tag": "plain_text", "content": note.Content}},
	}
	if note.UpdateNo > 0 {
		elements = append(elements, map[string]any{"tag": "note", "elements": []map[string]any{{"tag": "plain_text", "content": fmt.Sprintf("已更新-%d", note.UpdateNo)}}})
	}
	elements = append(elements, map[string]any{"tag": "note", "elements": []map[string]any{{"tag": "plain_text", "content": note.SessionID}}})
	if mention {
		elements = append([]map[string]any{{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": "<at id=" + receiveID + "></at>"}}}, elements...)
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true, "update_multi": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": larkNotificationTitle(note)},
		},
		"elements": elements,
	}
	b, err := json.Marshal(card)
	return string(b), err
}

func larkNotificationTitle(note WaitingNotification) string {
	if note.Running {
		return note.Name + "（Running）"
	}
	return note.Name
}

func (n *LarkAppNotifier) createWaiting(note WaitingNotification, content string) (WaitingNotificationResult, error) {
	token, err := n.tenantAccessToken(context.Background())
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"receive_id": n.receiveID,
		"msg_type":   "interactive",
		"content":    string(content),
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id", bytes.NewReader(payload))
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
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
	resp, err := n.client.Im.V1.Message.Patch(context.Background(), req)
	if err != nil {
		return WaitingNotificationResult{}, err
	}
	if !resp.Success() {
		return WaitingNotificationResult{}, fmt.Errorf("lark patch message API returned code %d: %s", resp.Code, resp.Msg)
	}
	tipSent := false
	if note.UpdateNo > 0 {
		if err := n.sendUpdateTipOnce(note.MessageID, note.UpdateNo); err == nil {
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
	content, err := larkNotificationCardContent(note, n.receiveID, n.mention)
	if err != nil {
		return err
	}
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(note.MessageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(content).
			Build()).
		Build()
	resp, err := n.client.Im.V1.Message.Patch(context.Background(), req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("lark patch message API returned code %d: %s", resp.Code, resp.Msg)
	}
	defaultLarkMessageRegistry.remember(note.SessionID, note.MessageID)
	return nil
}

func (n *LarkAppNotifier) sendUpdateTipOnce(messageID string, updateNo int) error {
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
		send = n.tipSender
	}
	if err := send(messageID, updateNo); err != nil {
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

func (n *LarkAppNotifier) sendUpdateTip(messageID string, updateNo int) error {
	if messageID == "" {
		return nil
	}
	content, err := larkUpdateTipCardContent(updateNo)
	if err != nil {
		return err
	}
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType("interactive").
			Content(content).
			ReplyInThread(false).
			Build()).
		Build()
	resp, err := n.client.Im.V1.Message.Reply(context.Background(), req)
	if err != nil {
		return err
	}
	if !resp.Success() {
		return fmt.Errorf("lark update tip reply API returned code %d: %s", resp.Code, resp.Msg)
	}
	return nil
}

func larkUpdateTipCardContent(updateNo int) (string, error) {
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": false},
		"elements": []map[string]any{
			{"tag": "note", "elements": []map[string]any{{"tag": "plain_text", "content": fmt.Sprintf("已更新-%d", updateNo)}}},
		},
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
	resp, err := http.DefaultClient.Do(req)
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
