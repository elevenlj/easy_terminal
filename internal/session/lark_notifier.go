package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
)

type LarkNotifier struct {
	WebhookURL string
	MentionAll bool
}

func (n *LarkNotifier) Available() bool {
	return n != nil && n.WebhookURL != ""
}

func (n *LarkNotifier) NotifyWaiting(note WaitingNotification) error {
	if !n.Available() {
		return errors.New("lark webhook is not configured")
	}
	content := note.Content
	if n.MentionAll {
		content = "<at id=all></at>\n" + content
	}
	payload := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header":   map[string]any{"template": "blue", "title": map[string]any{"tag": "plain_text", "content": note.Name}},
			"elements": []map[string]any{{"tag": "div", "text": map[string]any{"tag": "plain_text", "content": content}}},
		},
	}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(n.WebhookURL, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errors.New(resp.Status)
	}
	defaultLarkMessageRegistry.rememberLatest(note.SessionID)
	return nil
}
