package session

import "sync"

var defaultLarkMessageRegistry = &LarkMessageRegistry{messageToSession: make(map[string]string)}

type LarkMessageRegistry struct {
	mu               sync.RWMutex
	messageToSession map[string]string
	latestSessionID  string
}

func (r *LarkMessageRegistry) remember(sessionID string, messageIDs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
