package main

import (
	"context"
	"encoding/json"
	"errors"
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
	LarkDefaultSessionName          string                                `json:"lark_default_session_name"`
	FastWaitingTransitionMs         int                                   `json:"fast_waiting_transition_ms"`
	ConservativeWaitingTransitionMs int                                   `json:"conservative_waiting_transition_ms"`
	LarkNotifyMaxLines              int                                   `json:"lark_notify_max_lines"`
	LarkNotifyDropLinePatterns      []string                              `json:"lark_notify_drop_line_patterns"`
	SessionPreStartCommand          string                                `json:"session_pre_start_command"`
	SessionStartPresets             map[string]session.SessionStartPreset `json:"session_start_presets"`
	SessionNamePresets              map[string]session.SessionStartPreset `json:"session_name_presets"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := loadConfig()
	configPath := defaultConfigPath()
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
	bridge.SetDefaultStartSessionName(cfg.LarkDefaultSessionName)
	bridge.SetStartPresets(cfg.SessionStartPresets)
	bridge.SetNamePresets(cfg.SessionNamePresets)
	if bridge.Available() {
		go func() {
			if err := bridge.Start(context.Background()); err != nil {
				log.Printf("lark reply bridge stopped: %v", err)
			}
		}()
	}

	configSvc := &appConfigService{path: configPath, cfg: &cfg, manager: mgr, bridge: bridge}
	srv := httpapi.NewServer(mgr, uploadsDir, configSvc)
	addr := ":" + cfg.Port
	log.Printf("easy_terminal listening on http://localhost%s", addr)
	return http.ListenAndServe(addr, srv.Handler())
}

func loadConfig() Config {
	cfg := Config{
		Port:                            "8080",
		LarkMentionEnabled:              true,
		FastWaitingTransitionMs:         300,
		ConservativeWaitingTransitionMs: 700,
		LarkNotifyMaxLines:              300,
	}
	if b, err := os.ReadFile(defaultConfigPath()); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	cfg.Port = env("PORT", cfg.Port)
	cfg.LarkAppID = env("LARK_APP_ID", cfg.LarkAppID)
	cfg.LarkAppSecret = env("LARK_APP_SECRET", cfg.LarkAppSecret)
	cfg.LarkNotifyReceiveID = env("LARK_NOTIFY_RECEIVE_ID", cfg.LarkNotifyReceiveID)
	cfg.LarkDefaultSessionName = env("LARK_DEFAULT_SESSION_NAME", cfg.LarkDefaultSessionName)
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
	session.SetLarkNotifyMaxLines(cfg.LarkNotifyMaxLines)
	if err := session.SetLarkNotifyDropLinePatterns(cfg.LarkNotifyDropLinePatterns); err != nil {
		log.Printf("invalid lark_notify_drop_line_patterns: %v", err)
	}
	return cfg
}

func defaultConfigPath() string {
	return filepath.Join("conf", "config.local.json")
}

type appConfigService struct {
	mu      sync.Mutex
	path    string
	cfg     *Config
	manager *session.Manager
	bridge  *session.LarkReplyBridge
}

func (s *appConfigService) RuntimeConfig() httpapi.RuntimeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return runtimeConfigFromConfig(*s.cfg)
}

func (s *appConfigService) UpdateRuntimeConfig(req httpapi.RuntimeConfig) (httpapi.RuntimeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := *s.cfg
	if req.FastWaitingTransitionMs <= 0 || req.ConservativeWaitingTransitionMs <= 0 || req.LarkNotifyMaxLines <= 0 {
		return httpapi.RuntimeConfig{}, errors.New("numeric settings must be greater than zero")
	}
	if req.SessionStartPresets == nil {
		req.SessionStartPresets = map[string]session.SessionStartPreset{}
	}
	if req.SessionNamePresets == nil {
		req.SessionNamePresets = map[string]session.SessionStartPreset{}
	}
	cfg.LarkAppID = req.LarkAppID
	cfg.LarkAppSecret = req.LarkAppSecret
	cfg.LarkNotifyReceiveID = req.LarkNotifyReceiveID
	cfg.LarkMentionEnabled = req.LarkMentionEnabled
	cfg.LarkDefaultSessionName = req.LarkDefaultSessionName
	cfg.FastWaitingTransitionMs = req.FastWaitingTransitionMs
	cfg.ConservativeWaitingTransitionMs = req.ConservativeWaitingTransitionMs
	cfg.LarkNotifyMaxLines = req.LarkNotifyMaxLines
	cfg.LarkNotifyDropLinePatterns = req.LarkNotifyDropLinePatterns
	cfg.SessionPreStartCommand = req.SessionPreStartCommand
	cfg.SessionStartPresets = req.SessionStartPresets
	cfg.SessionNamePresets = req.SessionNamePresets
	if err := applyRuntimeConfig(cfg, s.manager, s.bridge); err != nil {
		return httpapi.RuntimeConfig{}, err
	}
	if err := writeConfigFile(s.path, cfg); err != nil {
		return httpapi.RuntimeConfig{}, err
	}
	*s.cfg = cfg
	return runtimeConfigFromConfig(cfg), nil
}

func applyRuntimeConfig(cfg Config, manager *session.Manager, bridge *session.LarkReplyBridge) error {
	manager.SetWaitingTransitionDelays(time.Duration(cfg.FastWaitingTransitionMs)*time.Millisecond, time.Duration(cfg.ConservativeWaitingTransitionMs)*time.Millisecond)
	manager.SetPreStartCommand(cfg.SessionPreStartCommand)
	manager.SetNotifier(session.NewLarkAppNotifier(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkNotifyReceiveID, cfg.LarkMentionEnabled))
	session.SetLarkNotifyMaxLines(cfg.LarkNotifyMaxLines)
	if err := session.SetLarkNotifyDropLinePatterns(cfg.LarkNotifyDropLinePatterns); err != nil {
		return err
	}
	if bridge != nil {
		bridge.SetDefaultStartSessionName(cfg.LarkDefaultSessionName)
		bridge.SetStartPresets(cfg.SessionStartPresets)
		bridge.SetNamePresets(cfg.SessionNamePresets)
	}
	return nil
}

func runtimeConfigFromConfig(cfg Config) httpapi.RuntimeConfig {
	return httpapi.RuntimeConfig{
		FastWaitingTransitionMs:         cfg.FastWaitingTransitionMs,
		ConservativeWaitingTransitionMs: cfg.ConservativeWaitingTransitionMs,
		LarkNotifyMaxLines:              cfg.LarkNotifyMaxLines,
		LarkNotifyDropLinePatterns:      cfg.LarkNotifyDropLinePatterns,
		SessionPreStartCommand:          cfg.SessionPreStartCommand,
		LarkAppID:                       cfg.LarkAppID,
		LarkAppSecret:                   cfg.LarkAppSecret,
		LarkNotifyReceiveID:             cfg.LarkNotifyReceiveID,
		LarkMentionEnabled:              cfg.LarkMentionEnabled,
		LarkDefaultSessionName:          cfg.LarkDefaultSessionName,
		SessionStartPresets:             cfg.SessionStartPresets,
		SessionNamePresets:              cfg.SessionNamePresets,
	}
}

func writeConfigFile(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
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
