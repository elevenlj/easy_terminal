package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"easy_terminal/internal/httpapi"
	"easy_terminal/internal/session"
	"easy_terminal/internal/store"
)

type Config struct {
	Port                     string `json:"port"`
	LarkAppID                string `json:"lark_app_id"`
	LarkAppSecret            string `json:"lark_app_secret"`
	LarkNotifyReceiveID      string `json:"lark_notify_receive_id"`
	LarkMentionEnabled       bool   `json:"lark_mention_enabled"`
	WaitingTransitionSeconds int    `json:"waiting_transition_seconds"`
	NotifyIdleSeconds        int    `json:"notify_idle_seconds"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := loadConfig()
	dbPath := env("AGENT_MONITOR_DB", "./easy_terminal.db")
	uploadsDir := env("AGENT_MONITOR_UPLOADS_DIR", "./data/uploads")
	logDir := env("AGENT_MONITOR_LOG_DIR", "./log")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)
	_ = os.MkdirAll(uploadsDir, 0o755)
	_ = os.MkdirAll(logDir, 0o755)

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.MarkAllNonTerminalSessionsExited(context.Background()); err != nil {
		return err
	}

	commandCfg, err := session.LoadCommandAgentConfig(session.DefaultCommandAgentConfigPath())
	if err != nil {
		return err
	}
	notifier := session.NewLarkAppNotifier(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkNotifyReceiveID, cfg.LarkMentionEnabled)
	mgr := session.NewManager(
		st,
		session.ShellLauncher{},
		session.WithNotifier(notifier),
		session.WithIdleTimeout(time.Duration(cfg.WaitingTransitionSeconds)*time.Second),
		session.WithNotifyIdleTimeout(time.Duration(cfg.NotifyIdleSeconds)*time.Second),
	)

	bridge := session.NewLarkReplyBridge(cfg.LarkAppID, cfg.LarkAppSecret, mgr, commandCfg, uploadsDir)
	if bridge.Available() {
		go func() {
			if err := bridge.Start(context.Background()); err != nil {
				log.Printf("lark reply bridge stopped: %v", err)
			}
		}()
	}

	srv := httpapi.NewServer(mgr, uploadsDir)
	addr := ":" + cfg.Port
	log.Printf("easy_terminal listening on http://localhost%s", addr)
	return http.ListenAndServe(addr, srv.Handler())
}

func loadConfig() Config {
	cfg := Config{Port: "8080", LarkMentionEnabled: true, WaitingTransitionSeconds: 2, NotifyIdleSeconds: 5}
	if b, err := os.ReadFile(filepath.Join("conf", "config.local.json")); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	cfg.Port = env("PORT", cfg.Port)
	cfg.LarkAppID = env("LARK_APP_ID", cfg.LarkAppID)
	cfg.LarkAppSecret = env("LARK_APP_SECRET", cfg.LarkAppSecret)
	cfg.LarkNotifyReceiveID = env("LARK_NOTIFY_RECEIVE_ID", cfg.LarkNotifyReceiveID)
	if v := os.Getenv("LARK_MENTION_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.LarkMentionEnabled = parsed
		}
	}
	if cfg.WaitingTransitionSeconds <= 0 {
		cfg.WaitingTransitionSeconds = 2
	}
	if cfg.NotifyIdleSeconds <= 0 {
		cfg.NotifyIdleSeconds = 5
	}
	return cfg
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
