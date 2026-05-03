package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

type LarkAppNotifier struct {
	appID     string
	appSecret string
	client    *lark.Client
	receiveID string
	mention   bool
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
	}
}

func (n *LarkAppNotifier) Available() bool {
	return n != nil && n.client != nil && n.receiveID != ""
}

func (n *LarkAppNotifier) NotifyWaiting(note WaitingNotification) error {
	if !n.Available() {
		return errors.New("lark notifier is not configured")
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": note.Name},
		},
		"elements": []map[string]any{
			{"tag": "div", "text": map[string]any{"tag": "plain_text", "content": note.Content}},
			{"tag": "note", "elements": []map[string]any{{"tag": "plain_text", "content": note.SessionID}}},
		},
	}
	if n.mention {
		card["elements"] = append([]map[string]any{{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": "<at id=" + n.receiveID + "></at>"}}}, card["elements"].([]map[string]any)...)
	}
	content, _ := json.Marshal(card)
	token, err := n.tenantAccessToken(context.Background())
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"receive_id": n.receiveID,
		"msg_type":   "interactive",
		"content":    string(content),
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=open_id", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("lark message API returned %s", resp.Status)
	}
	defaultLarkMessageRegistry.rememberLatest(note.SessionID)
	return nil
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
