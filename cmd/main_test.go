package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestParseStartupOptionsPort(t *testing.T) {
	opts, err := parseStartupOptions([]string{"--port", "9090"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Port != "9090" {
		t.Fatalf("expected port override, got %q", opts.Port)
	}

	opts, err = parseStartupOptions([]string{"-p", "7070"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Port != "7070" {
		t.Fatalf("expected short port override, got %q", opts.Port)
	}
}

func TestHeadlessChromeArgsUseStableViewport(t *testing.T) {
	args := headlessChromeArgs("/tmp/profile", "http://localhost:8080/?session=sess-1")
	for _, want := range []string{
		"--window-size=1440,1000",
		"--force-device-scale-factor=1",
		"--hide-scrollbars",
		"--user-data-dir=/tmp/profile",
		"http://localhost:8080/?session=sess-1",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("headless args missing %q: %#v", want, args)
		}
	}
}

func TestLoadConfigUsesCurrentDefaultsWhenFieldsMissing(t *testing.T) {
	wd := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})
	t.Setenv("PORT", "")
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("LARK_APP_SECRET", "")
	t.Setenv("LARK_NOTIFY_RECEIVE_ID", "")
	t.Setenv("LARK_DEFAULT_SESSION_NAME", "")
	t.Setenv("LARK_SESSION_CHAT_PREFIX", "")
	t.Setenv("SESSION_PRE_START_COMMAND", "")
	t.Setenv("LARK_MENTION_ENABLED", "")

	cfg := loadConfig()
	if cfg.FastWaitingTransitionMs != 1000 || cfg.ConservativeWaitingTransitionMs != 3000 || cfg.LarkAutoRefreshIntervalMs != 5000 || cfg.LarkNotifyMaxLines != 100 {
		t.Fatalf("numeric defaults = %d,%d,%d,%d", cfg.FastWaitingTransitionMs, cfg.ConservativeWaitingTransitionMs, cfg.LarkAutoRefreshIntervalMs, cfg.LarkNotifyMaxLines)
	}
	if cfg.LarkDefaultSessionName != "默认会话" || cfg.LarkSessionChatPrefix != "ET ·" {
		t.Fatalf("lark defaults = name %q prefix %q", cfg.LarkDefaultSessionName, cfg.LarkSessionChatPrefix)
	}
}

func TestAppConfigServiceUpdatesRuntimeConfigAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.local.json")
	cfg := Config{
		Port:                            "8080",
		LarkMentionEnabled:              true,
		LarkDefaultSessionName:          "默认会话",
		FastWaitingTransitionMs:         300,
		ConservativeWaitingTransitionMs: 700,
		LarkAutoRefreshIntervalMs:       5000,
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
		LarkAutoRefreshIntervalMs:       6000,
		LarkNotifyMaxLines:              120,
		LarkNotifyDropLineRules: session.LarkNotifyDropLineRules{
			{Title: "noise", Pattern: "noise"},
			{Title: "debug", Pattern: "debug"},
		},
		LarkCustomShortcuts:    []session.LarkCustomShortcut{{Label: "状态", Command: "git status"}},
		OnboardingCompleted:    true,
		SessionPreStartCommand: "source ~/.zshrc",
		SessionStartPresets:    map[string]session.SessionStartPreset{"1": {Commands: []string{"codex"}}},
		SessionNamePresets:     map[string]session.SessionStartPreset{"会话 A": {Commands: []string{"pwd"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.FastWaitingTransitionMs != 450 || got.LarkAutoRefreshIntervalMs != 6000 || got.LarkAppID != "app" {
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
	if saved.FastWaitingTransitionMs != 450 || saved.LarkAutoRefreshIntervalMs != 6000 || saved.SessionPreStartCommand != "source ~/.zshrc" || saved.LarkAppSecret != "secret" {
		t.Fatalf("config file was not updated: %#v", saved)
	}
	if len(saved.LarkNotifyDropLineRules) != 2 || saved.LarkNotifyDropLineRules[0].Pattern != "noise" {
		t.Fatalf("drop patterns were not persisted: %#v", saved.LarkNotifyDropLineRules)
	}
	if len(saved.LarkCustomShortcuts) != 1 || saved.LarkCustomShortcuts[0].Command != "git status" {
		t.Fatalf("custom shortcuts were not persisted: %#v", saved.LarkCustomShortcuts)
	}
	if !saved.OnboardingCompleted {
		t.Fatalf("onboarding completion was not persisted: %#v", saved)
	}
	if saved.SessionStartPresets["1"].Commands[0] != "codex" || saved.SessionNamePresets["会话 A"].Commands[0] != "pwd" {
		t.Fatalf("presets were not persisted: start=%#v name=%#v", saved.SessionStartPresets, saved.SessionNamePresets)
	}
}
