package httpapi

import (
	"encoding/json"
	"net/http"

	"easy_terminal/internal/session"
)

type RuntimeConfig struct {
	LarkAppID                       string                                `json:"lark_app_id"`
	LarkAppSecret                   string                                `json:"lark_app_secret"`
	LarkNotifyReceiveID             string                                `json:"lark_notify_receive_id"`
	LarkMentionEnabled              bool                                  `json:"lark_mention_enabled"`
	LarkDefaultSessionName          string                                `json:"lark_default_session_name"`
	FastWaitingTransitionMs         int                                   `json:"fast_waiting_transition_ms"`
	ConservativeWaitingTransitionMs int                                   `json:"conservative_waiting_transition_ms"`
	LarkNotifyMaxLines              int                                   `json:"lark_notify_max_lines"`
	LarkNotifyDropLinePatterns      []string                              `json:"lark_notify_drop_line_patterns"`
	SessionPreStartCommand          string                                `json:"session_pre_start_command"`
	SessionStartPresets             map[string]session.SessionStartPreset `json:"session_start_presets"`
	SessionNamePresets              map[string]session.SessionStartPreset `json:"session_name_presets"`
}

type ConfigService interface {
	RuntimeConfig() RuntimeConfig
	UpdateRuntimeConfig(RuntimeConfig) (RuntimeConfig, error)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.config == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.config.RuntimeConfig(), nil)
	case http.MethodPatch:
		var req RuntimeConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		cfg, err := s.config.UpdateRuntimeConfig(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg, nil)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
