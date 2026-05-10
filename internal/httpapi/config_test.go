package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easy_terminal/internal/session"
)

func TestConfigEndpointGetAndPatch(t *testing.T) {
	svc := &fakeConfigService{cfg: RuntimeConfig{
		LarkAppID:                       "app",
		LarkAppSecret:                   "secret",
		LarkNotifyReceiveID:             "ou_1",
		LarkMentionEnabled:              true,
		LarkDefaultSessionName:          "临时",
		FastWaitingTransitionMs:         300,
		ConservativeWaitingTransitionMs: 700,
		LarkNotifyMaxLines:              300,
		SessionStartPresets:             map[string]session.SessionStartPreset{},
		SessionNamePresets:              map[string]session.SessionStartPreset{},
	}}
	srv := NewServer(nil, "", svc)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}

	body := `{"lark_app_id":"new-app","lark_app_secret":"new-secret","lark_notify_receive_id":"ou_new","lark_mention_enabled":false,"lark_default_session_name":"默认","fast_waiting_transition_ms":450,"conservative_waiting_transition_ms":900,"lark_notify_max_lines":120,"lark_notify_drop_line_patterns":["noise"],"session_pre_start_command":"source ~/.zshrc","session_start_presets":{"1":{"commands":["codex"]}},"session_name_presets":{"会话 A":{"commands":["pwd"]}}}`
	req = httptest.NewRequest(http.MethodPatch, "/api/config", strings.NewReader(body))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got RuntimeConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.FastWaitingTransitionMs != 450 || got.LarkAppID != "new-app" {
		t.Fatalf("unexpected config response: %#v", got)
	}
	if svc.cfg.SessionPreStartCommand != "source ~/.zshrc" || svc.cfg.SessionStartPresets["1"].Commands[0] != "codex" {
		t.Fatalf("service was not updated: %#v", svc.cfg)
	}
}

type fakeConfigService struct {
	cfg RuntimeConfig
}

func (s *fakeConfigService) RuntimeConfig() RuntimeConfig {
	return s.cfg
}

func (s *fakeConfigService) UpdateRuntimeConfig(cfg RuntimeConfig) (RuntimeConfig, error) {
	s.cfg = cfg
	return s.cfg, nil
}
