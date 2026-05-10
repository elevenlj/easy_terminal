package session

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	StatusRunning = "running"
	StatusWaiting = "waiting"
	StatusExited  = "exited"
	StatusFailed  = "failed"
)

type Session struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Status                 string    `json:"status"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	ExitCode               *int      `json:"exit_code"`
	Live                   bool      `json:"live"`
	NotifyOnWaiting        bool      `json:"notify_on_waiting"`
	PeerSessionID          string    `json:"peer_session_id,omitempty"`
	BridgeEnabled          bool      `json:"bridge_enabled,omitempty"`
	LarkChatID             string    `json:"lark_chat_id,omitempty"`
	HistorySize            int64     `json:"history_size,omitempty"`
	NotificationsAvailable bool      `json:"notifications_available"`
}

type QuickCommand struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type WaitingNotification struct {
	SessionID           string
	Name                string
	Content             string
	MessageID           string
	ChatID              string
	UpdateNo            int
	Running             bool
	SuppressUpdateTip   bool
	NotificationVersion int64
}

type LarkCustomShortcut struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

type LarkNotifyDropLineRule struct {
	Title   string `json:"title"`
	Pattern string `json:"pattern"`
}

type LarkNotifyDropLineRules []LarkNotifyDropLineRule

func (r *LarkNotifyDropLineRules) UnmarshalJSON(data []byte) error {
	var rules []LarkNotifyDropLineRule
	if err := json.Unmarshal(data, &rules); err == nil {
		*r = NormalizeLarkNotifyDropLineRules(rules)
		return nil
	}
	var patterns []string
	if err := json.Unmarshal(data, &patterns); err != nil {
		return err
	}
	rules = make([]LarkNotifyDropLineRule, 0, len(patterns))
	for _, pattern := range patterns {
		rules = append(rules, LarkNotifyDropLineRule{Pattern: pattern})
	}
	*r = NormalizeLarkNotifyDropLineRules(rules)
	return nil
}

func (r LarkNotifyDropLineRules) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Rules())
}

func (r LarkNotifyDropLineRules) Rules() []LarkNotifyDropLineRule {
	return NormalizeLarkNotifyDropLineRules([]LarkNotifyDropLineRule(r))
}

func NormalizeLarkNotifyDropLineRules(rules []LarkNotifyDropLineRule) []LarkNotifyDropLineRule {
	out := make([]LarkNotifyDropLineRule, 0, len(rules))
	for _, rule := range rules {
		title := strings.TrimSpace(rule.Title)
		pattern := strings.TrimSpace(rule.Pattern)
		if title == "" && pattern == "" {
			continue
		}
		out = append(out, LarkNotifyDropLineRule{Title: title, Pattern: pattern})
	}
	return out
}

type WaitingNotificationResult struct {
	MessageID string
	RootID    string
	ParentID  string
	Updated   bool
	TipSent   bool
}

type WaitingNotifier interface {
	Available() bool
	NotifyWaiting(WaitingNotification) (WaitingNotificationResult, error)
}

type WaitingRunningNotifier interface {
	UpdateWaitingRunning(WaitingNotification, bool) error
}

const RunningNotificationPlaceholder = "正在执行中，请稍等。"
