package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"easy_terminal/internal/httpapi"
	"easy_terminal/internal/session"
)

func TestEnvFallback(t *testing.T) {
	t.Setenv("EASY_TERMINAL_TEST_ENV", "")
	if got := env("EASY_TERMINAL_TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
	t.Setenv("EASY_TERMINAL_TEST_ENV", "value")
	if got := env("EASY_TERMINAL_TEST_ENV", "fallback"); got != "value" {
		t.Fatalf("expected env value, got %q", got)
	}
}

func TestAppConfigServiceUpdatesRuntimeConfigAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.local.json")
	cfg := Config{
		Port:                            "8080",
		LarkMentionEnabled:              true,
		LarkDefaultSessionName:          "临时",
		FastWaitingTransitionMs:         300,
		ConservativeWaitingTransitionMs: 700,
		LarkNotifyMaxLines:              300,
	}
	mgr := session.NewManager(nil, nil)
	svc := &appConfigService{path: path, cfg: &cfg, manager: mgr}

	got, err := svc.UpdateRuntimeConfig(httpapi.RuntimeConfig{
		LarkAppID:                       "app",
		LarkAppSecret:                   "secret",
		LarkNotifyReceiveID:             "ou_1",
		LarkMentionEnabled:              false,
		LarkDefaultSessionName:          "默认",
		FastWaitingTransitionMs:         450,
		ConservativeWaitingTransitionMs: 900,
		LarkNotifyMaxLines:              120,
		LarkNotifyDropLinePatterns:      []string{"noise", "debug"},
		SessionPreStartCommand:          "source ~/.zshrc",
		SessionStartPresets:             map[string]session.SessionStartPreset{"1": {Commands: []string{"codex"}}},
		SessionNamePresets:              map[string]session.SessionStartPreset{"会话 A": {Commands: []string{"pwd"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.FastWaitingTransitionMs != 450 || got.LarkAppID != "app" {
		t.Fatalf("unexpected runtime config: %#v", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.FastWaitingTransitionMs != 450 || saved.SessionPreStartCommand != "source ~/.zshrc" || saved.LarkAppSecret != "secret" {
		t.Fatalf("config file was not updated: %#v", saved)
	}
	if len(saved.LarkNotifyDropLinePatterns) != 2 || saved.LarkNotifyDropLinePatterns[0] != "noise" {
		t.Fatalf("drop patterns were not persisted: %#v", saved.LarkNotifyDropLinePatterns)
	}
	if saved.SessionStartPresets["1"].Commands[0] != "codex" || saved.SessionNamePresets["会话 A"].Commands[0] != "pwd" {
		t.Fatalf("presets were not persisted: start=%#v name=%#v", saved.SessionStartPresets, saved.SessionNamePresets)
	}
}
