package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
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
	Port                            string                                `json:"port"`
	LarkAppID                       string                                `json:"lark_app_id"`
	LarkAppSecret                   string                                `json:"lark_app_secret"`
	LarkNotifyReceiveID             string                                `json:"lark_notify_receive_id"`
	LarkMentionEnabled              bool                                  `json:"lark_mention_enabled"`
	FastWaitingTransitionMs         int                                   `json:"fast_waiting_transition_ms"`
	ConservativeWaitingTransitionMs int                                   `json:"conservative_waiting_transition_ms"`
	LarkNotifyMaxLines              int                                   `json:"lark_notify_max_lines"`
	CodexNoAnchorFallbackLines      int                                   `json:"codex_no_anchor_fallback_lines"`
	SessionPreStartCommand          string                                `json:"session_pre_start_command"`
	SessionStartPresets             map[string]session.SessionStartPreset `json:"session_start_presets"`
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
	logFile, err := os.OpenFile(filepath.Join(logDir, "easy_terminal.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("failed to open log file: %v", err)
	} else {
		defer logFile.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		log.Printf("logging to %s", filepath.Join(logDir, "easy_terminal.log"))
	}

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
		session.WithWaitingTransitionDelays(
			time.Duration(cfg.FastWaitingTransitionMs)*time.Millisecond,
			time.Duration(cfg.ConservativeWaitingTransitionMs)*time.Millisecond,
		),
		session.WithBrowserNeeded(headless.Ensure),
		session.WithPreStartCommand(cfg.SessionPreStartCommand),
	)

	bridge := session.NewLarkReplyBridge(cfg.LarkAppID, cfg.LarkAppSecret, mgr, commandCfg, uploadsDir)
	bridge.SetStartPresets(cfg.SessionStartPresets)
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
	cfg := Config{Port: "8080", LarkMentionEnabled: true, FastWaitingTransitionMs: 300, ConservativeWaitingTransitionMs: 700, LarkNotifyMaxLines: 300, CodexNoAnchorFallbackLines: 80}
	if b, err := os.ReadFile(filepath.Join("conf", "config.local.json")); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	cfg.Port = env("PORT", cfg.Port)
	cfg.LarkAppID = env("LARK_APP_ID", cfg.LarkAppID)
	cfg.LarkAppSecret = env("LARK_APP_SECRET", cfg.LarkAppSecret)
	cfg.LarkNotifyReceiveID = env("LARK_NOTIFY_RECEIVE_ID", cfg.LarkNotifyReceiveID)
	cfg.SessionPreStartCommand = env("SESSION_PRE_START_COMMAND", cfg.SessionPreStartCommand)
	if v := os.Getenv("LARK_MENTION_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.LarkMentionEnabled = parsed
		}
	}
	if cfg.FastWaitingTransitionMs <= 0 {
		cfg.FastWaitingTransitionMs = 300
	}
	if cfg.ConservativeWaitingTransitionMs <= 0 {
		cfg.ConservativeWaitingTransitionMs = 700
	}
	if cfg.LarkNotifyMaxLines <= 0 {
		cfg.LarkNotifyMaxLines = 300
	}
	if cfg.CodexNoAnchorFallbackLines <= 0 {
		cfg.CodexNoAnchorFallbackLines = 80
	}
	session.SetLarkNotifyMaxLines(cfg.LarkNotifyMaxLines)
	session.SetCodexNoAnchorFallbackLines(cfg.CodexNoAnchorFallbackLines)
	return cfg
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type headlessBrowserManager struct {
	port     string
	mu       sync.Mutex
	sessions map[string]*headlessBrowserSession
}

type headlessBrowserSession struct {
	cmd     *exec.Cmd
	profile string
	started time.Time
}

func newHeadlessBrowserManager(port string) *headlessBrowserManager {
	return &headlessBrowserManager{port: port, sessions: make(map[string]*headlessBrowserSession)}
}

func (m *headlessBrowserManager) Ensure(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	if sess := m.sessions[sessionID]; sess != nil && sess.cmd != nil && sess.cmd.ProcessState == nil {
		if time.Since(sess.started) < 15*time.Second {
			m.mu.Unlock()
			return
		}
		log.Printf("headless browser appears stale; restarting for terminal snapshots (pid=%d, session=%s)", sess.cmd.Process.Pid, sessionID)
		_ = sess.cmd.Process.Kill()
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

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
	pageURL := "http://localhost:" + m.port + "/?session=" + url.QueryEscape(sessionID)
	cmd := exec.Command(chrome,
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--user-data-dir="+profile,
		pageURL,
	)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profile)
		log.Printf("headless browser start failed: %v", err)
		return
	}
	m.mu.Lock()
	m.sessions[sessionID] = &headlessBrowserSession{cmd: cmd, profile: profile, started: time.Now()}
	m.mu.Unlock()
	log.Printf("headless browser started for terminal snapshots (pid=%d, session=%s)", cmd.Process.Pid, sessionID)
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("headless browser exited: %v", err)
		}
		m.mu.Lock()
		if sess := m.sessions[sessionID]; sess != nil && sess.cmd == cmd {
			delete(m.sessions, sessionID)
		}
		m.mu.Unlock()
		_ = os.RemoveAll(profile)
	}()
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
