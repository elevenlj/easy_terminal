package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
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
	headless := newHeadlessBrowserManager(cfg.Port)
	mgr := session.NewManager(
		st,
		session.ShellLauncher{},
		session.WithNotifier(notifier),
		session.WithIdleTimeout(time.Duration(cfg.WaitingTransitionSeconds)*time.Second),
		session.WithNotifyIdleTimeout(time.Duration(cfg.NotifyIdleSeconds)*time.Second),
		session.WithBrowserNeeded(headless.Ensure),
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

type headlessBrowserManager struct {
	port string
	once sync.Once
}

func newHeadlessBrowserManager(port string) *headlessBrowserManager {
	return &headlessBrowserManager{port: port}
}

func (m *headlessBrowserManager) Ensure(sessionID string) {
	m.once.Do(func() {
		chrome := findChrome()
		if chrome == "" {
			log.Printf("headless browser unavailable: Chrome/Chromium not found")
			return
		}
		profile, err := os.MkdirTemp("", "easy-terminal-headless-*")
		if err != nil {
			log.Printf("headless browser profile setup failed: %v", err)
			return
		}
		url := "http://localhost:" + m.port + "/"
		cmd := exec.Command(chrome,
			"--headless=new",
			"--disable-gpu",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-dev-shm-usage",
			"--user-data-dir="+profile,
			url,
		)
		if err := cmd.Start(); err != nil {
			log.Printf("headless browser start failed: %v", err)
			return
		}
		log.Printf("headless browser started for terminal snapshots (pid=%d, session=%s)", cmd.Process.Pid, sessionID)
		go func() {
			if err := cmd.Wait(); err != nil {
				log.Printf("headless browser exited: %v", err)
			}
			_ = os.RemoveAll(profile)
		}()
	})
}

func findChrome() string {
	candidates := []string{
		os.Getenv("CHROME_BIN"),
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}
