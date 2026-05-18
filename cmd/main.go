package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy_terminal/internal/httpapi"
	"easy_terminal/internal/session"
	"easy_terminal/internal/store"
)

var version = "dev"

const (
	defaultLarkDefaultSessionName          = "默认会话"
	defaultLarkSessionChatPrefix           = "ET · "
	defaultLarkIgnoreMessagePrefix         = "/i"
	defaultFastWaitingTransitionMs         = 1000
	defaultConservativeWaitingTransitionMs = 3000
	defaultLarkAutoRefreshIntervalMs       = 5000
	defaultLarkNotifyMaxLines              = 100
	runtimeLogicVersion                    = "card-refresh-no-running-patch-v2"
)

var defaultLarkNotifyDropLineRules = session.LarkNotifyDropLineRules{
	{Title: "空行", Pattern: `^\s*$`},
	{Title: "横线", Pattern: `^\s*[-‐-‒–—―─━═]{3,}\s*$`},
}

type Config struct {
	Port                            string                                `json:"port"`
	LarkAppID                       string                                `json:"lark_app_id"`
	LarkAppSecret                   string                                `json:"lark_app_secret"`
	LarkNotifyReceiveID             string                                `json:"lark_notify_receive_id"`
	LarkMentionEnabled              bool                                  `json:"lark_mention_enabled"`
	LarkDefaultSessionName          string                                `json:"lark_default_session_name"`
	LarkSessionChatPrefix           string                                `json:"lark_session_chat_prefix"`
	LarkIgnoreMessagePrefix         string                                `json:"lark_ignore_message_prefix"`
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

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	opts, err := parseStartupOptions(os.Args[1:])
	if err != nil {
		return err
	}
	if opts.Version {
		fmt.Printf("easy_terminal %s\n", version)
		return nil
	}
	configPath := configPathFromDir(opts.ConfigDir)
	if err := ensureConfigFile(configPath); err != nil {
		return err
	}
	cfg := loadConfig(configPath)
	if opts.Port != "" {
		cfg.Port = opts.Port
	}
	dataDir := dataDirFromConfigDir(opts.ConfigDir)
	dbPath := env("AGENT_MONITOR_DB", dbPathInDataDir(dataDir))
	uploadsDir := env("AGENT_MONITOR_UPLOADS_DIR", uploadsDirInDataDir(dataDir))
	logDir := env("AGENT_MONITOR_LOG_DIR", logDirInDataDir(dataDir))
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
	wd, _ := os.Getwd()
	log.Printf("easy_terminal runtime_logic=%s pid=%d cwd=%s", runtimeLogicVersion, os.Getpid(), wd)

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteAllSessions(context.Background()); err != nil {
		return err
	}

	notifier := session.NewLarkAppNotifier(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkNotifyReceiveID, cfg.LarkMentionEnabled)
	notifier.SetCustomShortcuts(cfg.LarkCustomShortcuts)
	headless := newHeadlessBrowserManager(cfg.Port)
	mgr := session.NewManager(
		st,
		session.ShellLauncher{},
		session.WithNotifier(notifier),
		session.WithWaitingTransitionDelays(
			time.Duration(cfg.FastWaitingTransitionMs)*time.Millisecond,
			time.Duration(cfg.ConservativeWaitingTransitionMs)*time.Millisecond,
		),
		session.WithAutoRefreshInterval(time.Duration(cfg.LarkAutoRefreshIntervalMs)*time.Millisecond),
		session.WithBrowserNeeded(headless.Ensure),
		session.WithBrowserStopped(headless.Stop),
		session.WithPreStartCommand(cfg.SessionPreStartCommand),
		session.WithSessionEnded(func(sessionID string) {
			headless.Stop(sessionID)
			_ = os.RemoveAll(filepath.Join(uploadsDir, sessionID))
		}),
	)

	bridge := session.NewLarkReplyBridge(cfg.LarkAppID, cfg.LarkAppSecret, mgr, uploadsDir)
	bridge.SetDefaultStartSessionName(cfg.LarkDefaultSessionName)
	bridge.SetSessionChatPrefix(cfg.LarkSessionChatPrefix)
	bridge.SetIgnoreMessagePrefix(cfg.LarkIgnoreMessagePrefix)
	bridge.SetStartPresets(cfg.SessionStartPresets)
	bridge.SetNamePresets(cfg.SessionNamePresets)
	bridge.SetCustomShortcuts(cfg.LarkCustomShortcuts)
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

type startupOptions struct {
	Port      string
	ConfigDir string
	Version   bool
}

func parseStartupOptions(args []string) (startupOptions, error) {
	var opts startupOptions
	fs := flag.NewFlagSet("easy_terminal", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.Port, "port", "", "HTTP listen port")
	fs.StringVar(&opts.Port, "p", "", "HTTP listen port")
	fs.StringVar(&opts.ConfigDir, "config-dir", "", "config file directory")
	fs.BoolVar(&opts.Version, "version", false, "print version")
	fs.BoolVar(&opts.Version, "v", false, "print version")
	if err := fs.Parse(args); err != nil {
		return startupOptions{}, err
	}
	opts.ConfigDir = strings.TrimSpace(opts.ConfigDir)
	if fs.NArg() > 0 {
		return startupOptions{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func loadConfig(path string) Config {
	cfg := defaultConfig()
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	cfg.Port = env("PORT", cfg.Port)
	cfg.LarkAppID = env("LARK_APP_ID", cfg.LarkAppID)
	cfg.LarkAppSecret = env("LARK_APP_SECRET", cfg.LarkAppSecret)
	cfg.LarkNotifyReceiveID = env("LARK_NOTIFY_RECEIVE_ID", cfg.LarkNotifyReceiveID)
	cfg.LarkDefaultSessionName = env("LARK_DEFAULT_SESSION_NAME", cfg.LarkDefaultSessionName)
	cfg.LarkSessionChatPrefix = env("LARK_SESSION_CHAT_PREFIX", cfg.LarkSessionChatPrefix)
	cfg.LarkIgnoreMessagePrefix = env("LARK_IGNORE_MESSAGE_PREFIX", cfg.LarkIgnoreMessagePrefix)
	cfg.SessionPreStartCommand = env("SESSION_PRE_START_COMMAND", cfg.SessionPreStartCommand)
	if v := os.Getenv("LARK_MENTION_ENABLED"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.LarkMentionEnabled = parsed
		}
	}
	if cfg.FastWaitingTransitionMs <= 0 {
		cfg.FastWaitingTransitionMs = defaultFastWaitingTransitionMs
	}
	if cfg.ConservativeWaitingTransitionMs <= 0 {
		cfg.ConservativeWaitingTransitionMs = defaultConservativeWaitingTransitionMs
	}
	if cfg.LarkAutoRefreshIntervalMs <= 0 {
		cfg.LarkAutoRefreshIntervalMs = defaultLarkAutoRefreshIntervalMs
	}
	if cfg.LarkNotifyMaxLines <= 0 {
		cfg.LarkNotifyMaxLines = defaultLarkNotifyMaxLines
	}
	cfg.LarkSessionChatPrefix = normalizeLarkSessionChatPrefix(cfg.LarkSessionChatPrefix)
	session.SetLarkNotifyMaxLines(cfg.LarkNotifyMaxLines)
	if err := session.SetLarkNotifyDropLineRules(cfg.LarkNotifyDropLineRules.Rules()); err != nil {
		log.Printf("invalid lark_notify_drop_line_patterns: %v", err)
	}
	return cfg
}

func defaultConfig() Config {
	return Config{
		Port:                            "8080",
		LarkMentionEnabled:              true,
		LarkDefaultSessionName:          defaultLarkDefaultSessionName,
		LarkSessionChatPrefix:           defaultLarkSessionChatPrefix,
		LarkIgnoreMessagePrefix:         defaultLarkIgnoreMessagePrefix,
		FastWaitingTransitionMs:         defaultFastWaitingTransitionMs,
		ConservativeWaitingTransitionMs: defaultConservativeWaitingTransitionMs,
		LarkAutoRefreshIntervalMs:       defaultLarkAutoRefreshIntervalMs,
		LarkNotifyMaxLines:              defaultLarkNotifyMaxLines,
		LarkNotifyDropLineRules:         defaultLarkNotifyDropLineRules.Rules(),
	}
}

func ensureConfigFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeConfigFile(path, defaultConfig())
}

func defaultConfigPath() string {
	return filepath.Join(defaultConfigDir(), "config.local.json")
}

func configPathFromDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return defaultConfigPath()
	}
	return filepath.Join(dir, "config.local.json")
}

func defaultConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("EASY_TERMINAL_CONFIG_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(defaultDataDir(), "conf")
}

func defaultDBPath() string {
	return dbPathInDataDir(defaultDataDir())
}

func defaultUploadsDir() string {
	return uploadsDirInDataDir(defaultDataDir())
}

func defaultLogDir() string {
	return logDirInDataDir(defaultDataDir())
}

func dbPathInDataDir(dir string) string {
	return filepath.Join(dir, "easy_terminal.db")
}

func uploadsDirInDataDir(dir string) string {
	return filepath.Join(dir, "data", "uploads")
}

func logDirInDataDir(dir string) string {
	return filepath.Join(dir, "log")
}

func dataDirFromConfigDir(dir string) string {
	if dir := strings.TrimSpace(dir); dir != "" {
		return dir
	}
	return defaultDataDir()
}

func defaultDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("EASY_TERMINAL_HOME")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("EASY_TERMINAL_CONFIG_DIR")); dir != "" {
		return dir
	}
	if dir, err := os.UserHomeDir(); err == nil && dir != "" {
		return filepath.Join(dir, ".easy_terminal")
	}
	return ".easy_terminal"
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
	oldCfg := *s.cfg
	cfg := *s.cfg
	if req.FastWaitingTransitionMs <= 0 || req.ConservativeWaitingTransitionMs <= 0 || req.LarkAutoRefreshIntervalMs <= 0 || req.LarkNotifyMaxLines <= 0 {
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
	cfg.LarkSessionChatPrefix = normalizeLarkSessionChatPrefix(req.LarkSessionChatPrefix)
	cfg.LarkIgnoreMessagePrefix = strings.TrimSpace(req.LarkIgnoreMessagePrefix)
	cfg.FastWaitingTransitionMs = req.FastWaitingTransitionMs
	cfg.ConservativeWaitingTransitionMs = req.ConservativeWaitingTransitionMs
	cfg.LarkAutoRefreshIntervalMs = req.LarkAutoRefreshIntervalMs
	cfg.LarkNotifyMaxLines = req.LarkNotifyMaxLines
	cfg.LarkNotifyDropLineRules = req.LarkNotifyDropLineRules
	cfg.SessionPreStartCommand = req.SessionPreStartCommand
	cfg.SessionStartPresets = req.SessionStartPresets
	cfg.SessionNamePresets = req.SessionNamePresets
	cfg.LarkCustomShortcuts = req.LarkCustomShortcuts
	cfg.OnboardingCompleted = req.OnboardingCompleted
	reconnectLark := oldCfg.LarkAppID != cfg.LarkAppID || oldCfg.LarkAppSecret != cfg.LarkAppSecret
	if err := applyRuntimeConfig(cfg, s.manager, s.bridge, reconnectLark); err != nil {
		return httpapi.RuntimeConfig{}, err
	}
	if err := writeConfigFile(s.path, cfg); err != nil {
		return httpapi.RuntimeConfig{}, err
	}
	*s.cfg = cfg
	return runtimeConfigFromConfig(cfg), nil
}

func applyRuntimeConfig(cfg Config, manager *session.Manager, bridge *session.LarkReplyBridge, reconnectLark bool) error {
	manager.SetWaitingTransitionDelays(time.Duration(cfg.FastWaitingTransitionMs)*time.Millisecond, time.Duration(cfg.ConservativeWaitingTransitionMs)*time.Millisecond)
	manager.SetAutoRefreshInterval(time.Duration(cfg.LarkAutoRefreshIntervalMs) * time.Millisecond)
	manager.SetPreStartCommand(cfg.SessionPreStartCommand)
	notifier := session.NewLarkAppNotifier(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkNotifyReceiveID, cfg.LarkMentionEnabled)
	notifier.SetCustomShortcuts(cfg.LarkCustomShortcuts)
	manager.SetNotifier(notifier)
	session.SetLarkNotifyMaxLines(cfg.LarkNotifyMaxLines)
	if err := session.SetLarkNotifyDropLineRules(cfg.LarkNotifyDropLineRules.Rules()); err != nil {
		return err
	}
	if bridge != nil {
		if reconnectLark {
			bridge.Stop()
			bridge.SetAppCredentials(cfg.LarkAppID, cfg.LarkAppSecret)
		}
		bridge.SetDefaultStartSessionName(cfg.LarkDefaultSessionName)
		bridge.SetSessionChatPrefix(cfg.LarkSessionChatPrefix)
		bridge.SetIgnoreMessagePrefix(cfg.LarkIgnoreMessagePrefix)
		bridge.SetStartPresets(cfg.SessionStartPresets)
		bridge.SetNamePresets(cfg.SessionNamePresets)
		bridge.SetCustomShortcuts(cfg.LarkCustomShortcuts)
		if reconnectLark && bridge.Available() {
			go func() {
				if err := bridge.Start(context.Background()); err != nil {
					log.Printf("lark reply bridge stopped: %v", err)
				}
			}()
		}
	}
	return nil
}

func runtimeConfigFromConfig(cfg Config) httpapi.RuntimeConfig {
	return httpapi.RuntimeConfig{
		FastWaitingTransitionMs:         cfg.FastWaitingTransitionMs,
		ConservativeWaitingTransitionMs: cfg.ConservativeWaitingTransitionMs,
		LarkAutoRefreshIntervalMs:       cfg.LarkAutoRefreshIntervalMs,
		LarkNotifyMaxLines:              cfg.LarkNotifyMaxLines,
		LarkNotifyDropLineRules:         cfg.LarkNotifyDropLineRules.Rules(),
		SessionPreStartCommand:          cfg.SessionPreStartCommand,
		LarkAppID:                       cfg.LarkAppID,
		LarkAppSecret:                   cfg.LarkAppSecret,
		LarkNotifyReceiveID:             cfg.LarkNotifyReceiveID,
		LarkMentionEnabled:              cfg.LarkMentionEnabled,
		LarkDefaultSessionName:          cfg.LarkDefaultSessionName,
		LarkSessionChatPrefix:           cfg.LarkSessionChatPrefix,
		LarkIgnoreMessagePrefix:         cfg.LarkIgnoreMessagePrefix,
		SessionStartPresets:             cfg.SessionStartPresets,
		SessionNamePresets:              cfg.SessionNamePresets,
		LarkCustomShortcuts:             cfg.LarkCustomShortcuts,
		OnboardingCompleted:             cfg.OnboardingCompleted,
	}
}

func normalizeLarkSessionChatPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return defaultLarkSessionChatPrefix
	}
	return prefix
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
		m.mu.Unlock()
		return
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
	pageURL := "http://localhost:" + m.port + "/?session=" + url.QueryEscape(sessionID) + "&headless=1"
	cmd := exec.Command(chrome, headlessChromeArgs(profile, pageURL)...)
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

func (m *headlessBrowserManager) Stop(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	sess := m.sessions[sessionID]
	if sess != nil {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
	if sess == nil || sess.cmd == nil || sess.cmd.Process == nil || sess.cmd.ProcessState != nil {
		return
	}
	log.Printf("headless browser stopped (pid=%d, session=%s)", sess.cmd.Process.Pid, sessionID)
	_ = sess.cmd.Process.Kill()
}

func headlessChromeArgs(profile, pageURL string) []string {
	return []string{
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-dev-shm-usage",
		"--window-size=1440,1000",
		"--force-device-scale-factor=1",
		"--hide-scrollbars",
		"--user-data-dir=" + profile,
		pageURL,
	}
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
