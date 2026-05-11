package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"easy_terminal/internal/session"
)

type RuntimeConfig struct {
	LarkAppID                       string                                `json:"lark_app_id"`
	LarkAppSecret                   string                                `json:"lark_app_secret"`
	LarkNotifyReceiveID             string                                `json:"lark_notify_receive_id"`
	LarkMentionEnabled              bool                                  `json:"lark_mention_enabled"`
	LarkDefaultSessionName          string                                `json:"lark_default_session_name"`
	LarkSessionChatPrefix           string                                `json:"lark_session_chat_prefix"`
	FastWaitingTransitionMs         int                                   `json:"fast_waiting_transition_ms"`
	ConservativeWaitingTransitionMs int                                   `json:"conservative_waiting_transition_ms"`
	LarkAutoRefreshIntervalMs       int                                   `json:"lark_auto_refresh_interval_ms"`
	LarkNotifyMaxLines              int                                   `json:"lark_notify_max_lines"`
	LarkNotifyDropLineRules         session.LarkNotifyDropLineRules       `json:"lark_notify_drop_line_patterns"`
	SessionPreStartCommand          string                                `json:"session_pre_start_command"`
	SessionStartPresets             map[string]session.SessionStartPreset `json:"session_start_presets"`
	SessionNamePresets              map[string]session.SessionStartPreset `json:"session_name_presets"`
	LarkCustomShortcuts             []session.LarkCustomShortcut          `json:"lark_custom_shortcuts"`
	OnboardingCompleted             bool                                  `json:"onboarding_completed"`
}

type ConfigService interface {
	RuntimeConfig() RuntimeConfig
	UpdateRuntimeConfig(RuntimeConfig) (RuntimeConfig, error)
}

type LarkConfigTestStep struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type LarkConfigTestResult struct {
	OK    bool                 `json:"ok"`
	Steps []LarkConfigTestStep `json:"steps"`
}

type LarkConfigTester interface {
	Test(RuntimeConfig) LarkConfigTestResult
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

func (s *Server) handleLarkConfigTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req RuntimeConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.larkConfigTester.Test(req), nil)
}

type realLarkConfigTester struct{}

func (realLarkConfigTester) Test(cfg RuntimeConfig) LarkConfigTestResult {
	result := LarkConfigTestResult{}
	appID := strings.TrimSpace(cfg.LarkAppID)
	appSecret := strings.TrimSpace(cfg.LarkAppSecret)
	receiveID := strings.TrimSpace(cfg.LarkNotifyReceiveID)
	missing := []string{}
	if appID == "" {
		missing = append(missing, "App ID")
	}
	if appSecret == "" {
		missing = append(missing, "App Secret")
	}
	if receiveID == "" {
		missing = append(missing, "通知接收 ID")
	}
	if len(missing) > 0 {
		result.Steps = append(result.Steps, LarkConfigTestStep{
			Name:    "配置完整性",
			OK:      false,
			Message: "缺少：" + strings.Join(missing, "、"),
		})
		return result
	}
	result.Steps = append(result.Steps, LarkConfigTestStep{Name: "配置完整性", OK: true, Message: "必填项已填写"})

	notifier := session.NewLarkAppNotifier(appID, appSecret, receiveID, cfg.LarkMentionEnabled)
	_, err := notifier.NotifyWaiting(session.WaitingNotification{
		SessionID: "config-test",
		Name:      "easy_terminal 测试通知",
		Content:   "这是一条配置测试消息，用于确认飞书 App 凭证和通知接收 ID 可以正常发送。\n\n时间：" + time.Now().Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		result.Steps = append(result.Steps, LarkConfigTestStep{
			Name:    "发送测试通知",
			OK:      false,
			Message: err.Error(),
		})
		return result
	}
	result.Steps = append(result.Steps, LarkConfigTestStep{Name: "发送测试通知", OK: true, Message: "已向通知接收 ID 发送测试卡片"})
	result.OK = true
	return result
}

func (s *Server) handleLarkAppRegistration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Brand string `json:"brand"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.larkAppRegistration.Begin(r.Context(), req.Brand)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result, nil)
}

func (s *Server) handleLarkAppRegistrationPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Brand      string `json:"brand"`
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.larkAppRegistration.Poll(r.Context(), req.Brand, req.DeviceCode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result, nil)
}

func (s *Server) handleLarkAppRegistrationQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	text := r.URL.Query().Get("text")
	if strings.TrimSpace(text) == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	png, err := qrPNG(text)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}
