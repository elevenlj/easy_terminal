package session

import "time"

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
	SessionID string
	Name      string
	Content   string
}

type WaitingNotifier interface {
	Available() bool
	NotifyWaiting(WaitingNotification) error
}
