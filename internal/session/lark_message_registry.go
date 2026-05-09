package session

import (
	"sort"
	"sync"
	"time"
)

var defaultLarkMessageRegistry = &LarkMessageRegistry{messageToSession: make(map[string]string)}

type LarkMessageRegistry struct {
	mu               sync.RWMutex
	messageToSession map[string]string
	latestSessionID  string
	sessionLatest    map[string]LarkSessionNavItem
}

type LarkSessionNavItem struct {
	SessionID string
	Name      string
	Content   string
	MessageID string
	UpdateNo  int
	Running   bool
	UpdatedAt time.Time
}

func (r *LarkMessageRegistry) remember(sessionID string, messageIDs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.messageToSession == nil {
		r.messageToSession = make(map[string]string)
	}
	for _, id := range messageIDs {
		if id != "" {
			r.messageToSession[id] = sessionID
		}
	}
	if sessionID != "" {
		r.latestSessionID = sessionID
	}
}

func (r *LarkMessageRegistry) lookup(messageIDs ...string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range messageIDs {
		if id == "" {
			continue
		}
		if sess, ok := r.messageToSession[id]; ok {
			return sess, true
		}
	}
	return "", false
}

func (r *LarkMessageRegistry) rememberLatest(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latestSessionID = sessionID
}

func (r *LarkMessageRegistry) latestNotifiedSessionID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.latestSessionID
}

func (r *LarkMessageRegistry) rememberNotification(note WaitingNotification, messageID string) {
	if note.SessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.messageToSession == nil {
		r.messageToSession = make(map[string]string)
	}
	if r.sessionLatest == nil {
		r.sessionLatest = make(map[string]LarkSessionNavItem)
	}
	if messageID != "" {
		r.messageToSession[messageID] = note.SessionID
	}
	r.latestSessionID = note.SessionID
	r.sessionLatest[note.SessionID] = LarkSessionNavItem{
		SessionID: note.SessionID,
		Name:      note.Name,
		Content:   note.Content,
		MessageID: messageID,
		UpdateNo:  note.UpdateNo,
		Running:   note.Running,
		UpdatedAt: time.Now().UTC(),
	}
}

func (r *LarkMessageRegistry) navItems(selectedSessionID string, limit int) ([]LarkSessionNavItem, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]LarkSessionNavItem, 0, len(r.sessionLatest))
	for _, item := range r.sessionLatest {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	if selectedSessionID == "" {
		selectedSessionID = r.latestSessionID
	}
	if selectedSessionID == "" && len(items) > 0 {
		selectedSessionID = items[0].SessionID
	}
	return items, selectedSessionID
}
