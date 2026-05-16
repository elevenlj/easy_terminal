package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	maxOutputBytes                       = 512 * 1024
	maxRoundBytes                        = 64 * 1024
	defaultFastWaitingTransition         = 300 * time.Millisecond
	defaultConservativeWaitingTransition = 700 * time.Millisecond
	defaultAutoRefreshInterval           = 5 * time.Second
	defaultNotificationUpdateCoalesce    = 0
	defaultNotifyRetryDelay              = time.Second
	defaultNotifySnapshotTimeout         = 1200 * time.Millisecond
	defaultNotifySnapshotDeadline        = 2500 * time.Millisecond
	defaultInputBaselineSnapshotDeadline = 2500 * time.Millisecond
	defaultStartupPresetSettleDelay      = 2 * time.Second
	defaultNotificationSendAttempts      = 3
	defaultNotificationSendRetryDelay    = 120 * time.Millisecond
)

var errNotificationMessageDisabled = errors.New("notification message is disabled")

type startupNotifyMode int

const (
	startupNotifyNormal startupNotifyMode = iota
	startupNotifySuppress
	startupNotifySettling
	startupNotifyFinal
)

type Store interface {
	CreateSession(context.Context, Session) error
	UpdateSession(context.Context, Session) error
	ListSessions(context.Context) ([]Session, error)
	GetSession(context.Context, string) (Session, bool, error)
	DeleteSession(context.Context, string) error
	AppendOutput(context.Context, string, int64, []byte) error
	Output(context.Context, string) ([]byte, error)
	DeleteAllSessions(context.Context) error
	ListQuickCommands(context.Context) ([]QuickCommand, error)
	CreateQuickCommand(context.Context, QuickCommand) error
	DeleteQuickCommand(context.Context, string) error
}

type Manager struct {
	mu                  sync.RWMutex
	store               Store
	launcher            Launcher
	notifier            WaitingNotifier
	idCounter           atomic.Int64
	fastWaiting         time.Duration
	conservativeWaiting time.Duration
	autoRefreshInterval time.Duration
	updateCoalesce      time.Duration
	preStartCommand     string
	sessions            map[string]*RuntimeSession
	onBrowserNeeded     func(string)
	onBrowserActive     func(string)
	onNotificationSent  func(string)
	onSessionEnded      func(string)
}

type ManagerOption func(*Manager)

func NewManager(store Store, launcher Launcher, opts ...ManagerOption) *Manager {
	m := &Manager{
		store:               store,
		launcher:            launcher,
		fastWaiting:         defaultFastWaitingTransition,
		conservativeWaiting: defaultConservativeWaitingTransition,
		autoRefreshInterval: defaultAutoRefreshInterval,
		updateCoalesce:      defaultNotificationUpdateCoalesce,
		sessions:            make(map[string]*RuntimeSession),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.launcher == nil {
		m.launcher = ShellLauncher{}
	}
	return m
}

func WithNotifier(n WaitingNotifier) ManagerOption {
	return func(m *Manager) { m.notifier = n }
}

func (m *Manager) SetNotifier(n WaitingNotifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifier = n
}

func WithWaitingTransitionDelays(fast, conservative time.Duration) ManagerOption {
	return func(m *Manager) {
		if fast > 0 {
			m.fastWaiting = fast
		}
		if conservative > 0 {
			m.conservativeWaiting = conservative
		}
	}
}

func WithNotificationUpdateCoalesce(delay time.Duration) ManagerOption {
	return func(m *Manager) {
		if delay >= 0 {
			m.updateCoalesce = delay
		}
	}
}

func WithAutoRefreshInterval(interval time.Duration) ManagerOption {
	return func(m *Manager) {
		if interval > 0 {
			m.autoRefreshInterval = interval
		}
	}
}

func WithBrowserNeeded(fn func(string)) ManagerOption {
	return func(m *Manager) { m.onBrowserNeeded = fn }
}

func WithBrowserActive(fn func(string)) ManagerOption {
	return func(m *Manager) { m.onBrowserActive = fn }
}

func WithSessionEnded(fn func(string)) ManagerOption {
	return func(m *Manager) { m.onSessionEnded = fn }
}

func WithPreStartCommand(command string) ManagerOption {
	return func(m *Manager) { m.preStartCommand = strings.TrimSpace(command) }
}

func (m *Manager) SetWaitingTransitionDelays(fast, conservative time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fast > 0 {
		m.fastWaiting = fast
	}
	if conservative > 0 {
		m.conservativeWaiting = conservative
	}
}

func (m *Manager) SetAutoRefreshInterval(interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if interval > 0 {
		m.autoRefreshInterval = interval
	}
}

func (m *Manager) autoRefreshDelay() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.autoRefreshInterval <= 0 {
		return defaultAutoRefreshInterval
	}
	return m.autoRefreshInterval
}

func (m *Manager) SetPreStartCommand(command string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.preStartCommand = strings.TrimSpace(command)
}

func (m *Manager) EnsureBrowser(sessionID string) {
	if m.onBrowserNeeded == nil || sessionID == "" {
		return
	}
	go m.onBrowserNeeded(sessionID)
}

func (m *Manager) BrowserActive(sessionID string) {
	if m.onBrowserActive == nil || sessionID == "" {
		return
	}
	go m.onBrowserActive(sessionID)
}

func (m *Manager) SetNotificationSentHook(fn func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onNotificationSent = fn
}

func (m *Manager) notificationSent(sessionID string) {
	m.mu.RLock()
	fn := m.onNotificationSent
	m.mu.RUnlock()
	if fn != nil && sessionID != "" {
		go fn(sessionID)
	}
}

func (m *Manager) sessionEnded(sessionID string) {
	m.mu.RLock()
	fn := m.onSessionEnded
	m.mu.RUnlock()
	if fn != nil && sessionID != "" {
		fn(sessionID)
	}
}

func (m *Manager) CreateSession(ctx context.Context, name string) (Session, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Session{}, errors.New("session name is required")
	}
	now := time.Now().UTC()
	id, err := m.nextSessionID(ctx)
	if err != nil {
		return Session{}, err
	}
	sess := Session{ID: id, Name: name, Status: StatusRunning, CreatedAt: now, UpdatedAt: now, Live: true}
	handle, err := m.launcher.Launch(context.Background())
	if err != nil {
		code := 1
		sess.Status = StatusFailed
		sess.Live = false
		sess.ExitCode = &code
		return sess, err
	}
	rt := &RuntimeSession{
		manager:     m,
		session:     sess,
		terminal:    handle.Terminal(),
		process:     handle.Process(),
		subscribers: make(map[chan RuntimeEvent]runtimeSubscriber),
	}
	if m.store != nil {
		if err := m.store.CreateSession(ctx, sess); err != nil {
			_ = handle.Terminal().Close()
			return Session{}, err
		}
	}
	m.mu.Lock()
	m.sessions[id] = rt
	m.mu.Unlock()
	go rt.streamOutput()
	go rt.waitForExit()
	rt.runPreStartCommand()
	sess.NotificationsAvailable = m.notifier != nil && m.notifier.Available()
	return sess, nil
}

func (m *Manager) nextSessionID(ctx context.Context) (string, error) {
	for {
		id := fmt.Sprintf("sess-%d", m.idCounter.Add(1))
		if m.store == nil {
			return id, nil
		}
		_, exists, err := m.store.GetSession(ctx, id)
		if err != nil {
			return "", err
		}
		if !exists {
			return id, nil
		}
	}
}

func (m *Manager) ListSessions(ctx context.Context) ([]Session, error) {
	var list []Session
	var err error
	if m.store != nil {
		list, err = m.store.ListSessions(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		m.mu.RLock()
		for _, rt := range m.sessions {
			list = append(list, rt.Snapshot())
		}
		m.mu.RUnlock()
	}
	active := list[:0]
	for _, s := range list {
		if s.Live && s.Status != StatusExited && s.Status != StatusFailed {
			active = append(active, s)
		}
	}
	list = active
	available := m.notifier != nil && m.notifier.Available()
	for i := range list {
		list[i].NotificationsAvailable = available
	}
	return list, nil
}

func (m *Manager) GetRuntime(id string) (*RuntimeSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rt, ok := m.sessions[id]
	return rt, ok
}

func (m *Manager) GetSession(ctx context.Context, id string) (Session, bool, error) {
	if rt, ok := m.GetRuntime(id); ok {
		s := rt.Snapshot()
		s.NotificationsAvailable = m.notifier != nil && m.notifier.Available()
		return s, true, nil
	}
	if m.store == nil {
		return Session{}, false, nil
	}
	s, ok, err := m.store.GetSession(ctx, id)
	if ok && (!s.Live || s.Status == StatusExited || s.Status == StatusFailed) {
		return Session{}, false, nil
	}
	s.NotificationsAvailable = m.notifier != nil && m.notifier.Available()
	return s, ok, err
}

func (m *Manager) UpdateNotifyOnWaiting(ctx context.Context, id string, enabled bool) (Session, bool, error) {
	rt, ok := m.GetRuntime(id)
	if !ok {
		s, exists, err := m.GetSession(ctx, id)
		if err != nil || !exists {
			return Session{}, exists, err
		}
		s.NotifyOnWaiting = enabled
		s.UpdatedAt = time.Now().UTC()
		if m.store != nil {
			err = m.store.UpdateSession(ctx, s)
		}
		return s, true, err
	}
	rt.mu.Lock()
	rt.session.NotifyOnWaiting = enabled
	rt.session.UpdatedAt = time.Now().UTC()
	s := rt.session
	rt.mu.Unlock()
	err := m.persist(ctx, s)
	s.NotificationsAvailable = m.notifier != nil && m.notifier.Available()
	return s, true, err
}

func (m *Manager) BindLarkChat(ctx context.Context, id string, chatID string) (Session, bool, error) {
	chatID = strings.TrimSpace(chatID)
	rt, ok := m.GetRuntime(id)
	if !ok {
		s, exists, err := m.GetSession(ctx, id)
		if err != nil || !exists {
			return Session{}, exists, err
		}
		s.LarkChatID = chatID
		s.UpdatedAt = time.Now().UTC()
		if m.store != nil {
			err = m.store.UpdateSession(ctx, s)
		}
		if err == nil && chatID != "" {
			defaultLarkMessageRegistry.rememberChat(chatID, id)
		}
		return s, true, err
	}
	rt.mu.Lock()
	rt.session.LarkChatID = chatID
	if chatID != "" {
		rt.requireLarkChat = false
	}
	rt.session.UpdatedAt = time.Now().UTC()
	s := rt.session
	rt.mu.Unlock()
	if m.store != nil {
		if err := m.store.UpdateSession(ctx, s); err != nil {
			return s, true, err
		}
	}
	if chatID != "" {
		defaultLarkMessageRegistry.rememberChat(chatID, id)
	}
	return s, true, nil
}

func (m *Manager) FindSessionByLarkChatID(ctx context.Context, chatID string) (Session, bool, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return Session{}, false, nil
	}
	m.mu.RLock()
	for _, rt := range m.sessions {
		s := rt.Snapshot()
		if s.LarkChatID == chatID && s.Live && s.Status != StatusExited && s.Status != StatusFailed {
			defaultLarkMessageRegistry.rememberChat(chatID, s.ID)
			m.mu.RUnlock()
			return s, true, nil
		}
	}
	m.mu.RUnlock()
	if m.store == nil {
		return Session{}, false, nil
	}
	list, err := m.store.ListSessions(ctx)
	if err != nil {
		return Session{}, false, err
	}
	for _, s := range list {
		if s.LarkChatID == chatID && s.Live && s.Status != StatusExited && s.Status != StatusFailed {
			defaultLarkMessageRegistry.rememberChat(chatID, s.ID)
			return s, true, nil
		}
	}
	return Session{}, false, nil
}

func (m *Manager) LatestLarkChatID() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var latest Session
	for _, rt := range m.sessions {
		s := rt.Snapshot()
		if strings.TrimSpace(s.LarkChatID) == "" {
			continue
		}
		if latest.ID == "" || s.CreatedAt.After(latest.CreatedAt) {
			latest = s
		}
	}
	return latest.LarkChatID
}

func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	m.mu.Lock()
	rt, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
		rt.Close()
	}
	if m.store != nil {
		return m.store.DeleteSession(ctx, id)
	}
	return nil
}

func (m *Manager) Output(ctx context.Context, id string) ([]byte, bool, error) {
	if rt, ok := m.GetRuntime(id); ok {
		return rt.OutputSnapshot(), true, nil
	}
	if m.store == nil {
		return nil, false, nil
	}
	if _, ok, err := m.store.GetSession(ctx, id); err != nil || !ok {
		return nil, ok, err
	}
	out, err := m.store.Output(ctx, id)
	return out, true, err
}

func (m *Manager) ListQuickCommands(ctx context.Context) ([]QuickCommand, error) {
	return m.store.ListQuickCommands(ctx)
}

func (m *Manager) CreateQuickCommand(ctx context.Context, name, text string) (QuickCommand, error) {
	qc := QuickCommand{ID: fmt.Sprintf("qc-%d", time.Now().UnixNano()), Name: strings.TrimSpace(name), Text: text, CreatedAt: time.Now().UTC()}
	if qc.Name == "" || strings.TrimSpace(qc.Text) == "" {
		return QuickCommand{}, errors.New("name and text are required")
	}
	return qc, m.store.CreateQuickCommand(ctx, qc)
}

func (m *Manager) DeleteQuickCommand(ctx context.Context, id string) error {
	return m.store.DeleteQuickCommand(ctx, id)
}

func (m *Manager) persist(ctx context.Context, sess Session) error {
	if m.store == nil {
		return nil
	}
	return m.store.UpdateSession(ctx, sess)
}

type RuntimeSession struct {
	mu                          sync.Mutex
	notificationPatchMu         sync.Mutex
	manager                     *Manager
	session                     Session
	terminal                    Terminal
	process                     Waiter
	output                      []byte
	roundReply                  []byte
	visibleSnapshot             string
	visibleSnapshotSource       string
	visibleSnapshotVersion      int64
	snapshotAtRoundStart        string
	snapshotAtRoundVersion      int64
	snapshotAtRoundStartSet     bool
	lastInputText               string
	inputLineBuffer             string
	lastNotifiedRoundHash       string
	lastNotifiedMessageID       string
	lastNotifiedContent         string
	lastNotifiedVisibleSnapshot string
	notificationMentionOpenID   string
	notificationUpdateNo        int
	notificationRunning         bool
	notificationWindowInputText string
	frozenNotificationMessages  map[string]struct{}
	suppressRunningMarker       bool
	requireLarkChat             bool
	notificationPatchVersion    int64
	autoRefreshEnabled          bool
	autoRefreshMessageID        string
	autoRefreshStop             chan struct{}
	startupNotifyMode           startupNotifyMode
	subscribers                 map[chan RuntimeEvent]runtimeSubscriber
	snapshotWaiters             []chan struct{}
	nextSeq                     int64
	stateVersion                int64
	notifyVersion               int64
	notifyRetryTimer            *time.Timer
	notifyStableTimer           *time.Timer
	startupNotifyTimer          *time.Timer
}

type RuntimeEvent struct {
	Type string
	Data []byte
}

type runtimeSubscriber struct {
	Headless bool
}

const (
	RuntimeEventOutput          = "output"
	RuntimeEventSnapshotRequest = "snapshot_request"
)

func (rt *RuntimeSession) Snapshot() Session {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.session
}

func (rt *RuntimeSession) OutputSnapshot() []byte {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	cp := make([]byte, len(rt.output))
	copy(cp, rt.output)
	return cp
}

func (rt *RuntimeSession) Subscribe() (chan RuntimeEvent, func()) {
	return rt.SubscribeWithMode(false)
}

func (rt *RuntimeSession) SubscribeWithMode(headless bool) (chan RuntimeEvent, func()) {
	ch := make(chan RuntimeEvent, 64)
	rt.mu.Lock()
	if rt.subscribers == nil {
		rt.subscribers = make(map[chan RuntimeEvent]runtimeSubscriber)
	}
	rt.subscribers[ch] = runtimeSubscriber{Headless: headless}
	needsSnapshot := len(rt.snapshotWaiters) > 0
	sessionID := rt.session.ID
	rt.mu.Unlock()
	if !headless && rt.manager != nil {
		rt.manager.BrowserActive(sessionID)
	}
	if needsSnapshot {
		select {
		case ch <- RuntimeEvent{Type: RuntimeEventSnapshotRequest}:
		default:
		}
	}
	cancel := func() {
		rt.mu.Lock()
		if _, ok := rt.subscribers[ch]; ok {
			delete(rt.subscribers, ch)
			close(ch)
		}
		rt.mu.Unlock()
	}
	return ch, cancel
}

func (rt *RuntimeSession) WriteInput(data string) error {
	if data == "" {
		return nil
	}
	if inputChangesSessionState(data) {
		rt.MarkInputActivity(data)
	}
	_, err := rt.terminal.Write([]byte(data))
	return err
}

func (rt *RuntimeSession) SuppressStartupNotifications() {
	rt.mu.Lock()
	rt.startupNotifyMode = startupNotifySuppress
	rt.mu.Unlock()
}

func (rt *RuntimeSession) FinishStartupNotifications() {
	rt.finishStartupNotificationsAfter(defaultStartupPresetSettleDelay)
}

func (rt *RuntimeSession) finishStartupNotificationsAfter(delay time.Duration) {
	rt.mu.Lock()
	if rt.startupNotifyMode == startupNotifySuppress {
		rt.startupNotifyMode = startupNotifySettling
		rt.scheduleStartupNotifyFinalLocked(delay)
	}
	rt.mu.Unlock()
}

func (rt *RuntimeSession) runPreStartCommand() {
	command := strings.TrimSpace(rt.manager.preStartCommand)
	if command == "" {
		return
	}
	if !strings.HasSuffix(command, "\r") && !strings.HasSuffix(command, "\n") {
		command += "\r"
	}
	if _, err := rt.terminal.Write([]byte(command)); err != nil {
		log.Printf("pre-start command failed session=%s: %v", rt.session.ID, err)
	}
}

func (rt *RuntimeSession) Resize(cols, rows uint16) error {
	if cols < 80 || rows < 20 {
		return nil
	}
	return rt.terminal.Resize(cols, rows)
}

func (rt *RuntimeSession) SetVisibleSnapshot(data string) {
	rt.SetVisibleSnapshotWithSource(data, "legacy")
}

func (rt *RuntimeSession) SetVisibleSnapshotWithSource(data string, source string) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "unknown"
	}
	rt.mu.Lock()
	if isHeadlessSnapshotSource(source) && rt.hasRealSubscriberLocked() {
		sessionID := rt.session.ID
		rt.mu.Unlock()
		log.Printf("visible snapshot ignored session=%s source=%s reason=real_browser_active len=%d", sessionID, source, len(data))
		return
	}
	rt.visibleSnapshot = data
	rt.visibleSnapshotSource = source
	rt.visibleSnapshotVersion++
	version := rt.visibleSnapshotVersion
	sessionID := rt.session.ID
	waiters := rt.snapshotWaiters
	rt.snapshotWaiters = nil
	rt.mu.Unlock()
	log.Printf("visible snapshot updated session=%s source=%s version=%d len=%d lines=%d waiters=%d", sessionID, source, version, len(data), countLogLines(data), len(waiters))
	for _, ch := range waiters {
		close(ch)
	}
}

func isHeadlessSnapshotSource(source string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "headless:")
}

func (rt *RuntimeSession) RequireLarkChatForNotifications() {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.requireLarkChat = true
	rt.mu.Unlock()
}

func (rt *RuntimeSession) SetNotificationMentionOpenID(openID string) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.notificationMentionOpenID = strings.TrimSpace(openID)
	rt.mu.Unlock()
}

func (rt *RuntimeSession) NotificationMentionOpenID() string {
	if rt == nil {
		return ""
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.notificationMentionOpenID
}

func (rt *RuntimeSession) CurrentRoundContent() string {
	rt.RequestFreshSnapshot(800 * time.Millisecond)
	return rt.CachedCurrentRoundContent()
}

func (rt *RuntimeSession) CachedCurrentRoundContent() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.currentNotifyContentLocked()
}

func (rt *RuntimeSession) CurrentVisibleContent() string {
	rt.RequestFreshSnapshot(800 * time.Millisecond)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return pickNotifyContentWithWindow(rt.visibleSnapshot, "", rt.roundReply, rt.lastInputText, rt.notificationWindowInputText)
}

func (rt *RuntimeSession) previousNotifySnapshotLocked() string {
	if rt.snapshotAtRoundStartSet {
		return rt.snapshotAtRoundStart
	}
	return rt.lastNotifiedVisibleSnapshot
}

func (rt *RuntimeSession) currentNotifyContentLocked() string {
	return pickNotifyContentWithWindow(rt.visibleSnapshot, rt.previousNotifySnapshotLocked(), rt.roundReply, rt.lastInputText, rt.notificationWindowInputText)
}

func (rt *RuntimeSession) NotificationMessageFrozen(messageID string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.notificationMessageFrozenLocked(messageID)
}

func (rt *RuntimeSession) ValidateNotificationAction(messageID string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.validateNotificationActionLocked(messageID)
}

func (rt *RuntimeSession) validateNotificationActionLocked(messageID string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	if rt.notificationMessageFrozenLocked(messageID) {
		return errNotificationMessageDisabled
	}
	if rt.lastNotifiedMessageID != "" && rt.lastNotifiedMessageID != messageID {
		return errNotificationMessageDisabled
	}
	return nil
}

func (rt *RuntimeSession) notificationMessageFrozenLocked(messageID string) bool {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" || len(rt.frozenNotificationMessages) == 0 {
		return false
	}
	_, ok := rt.frozenNotificationMessages[messageID]
	return ok
}

func (rt *RuntimeSession) freezeNotificationMessageLocked(messageID string) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return
	}
	if rt.frozenNotificationMessages == nil {
		rt.frozenNotificationMessages = make(map[string]struct{})
	}
	rt.frozenNotificationMessages[messageID] = struct{}{}
	if rt.autoRefreshMessageID == messageID {
		rt.autoRefreshMessageID = ""
	}
}

func (rt *RuntimeSession) disabledNotificationLocked(messageID string) (WaitingNotification, bool) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" || rt.manager == nil || rt.manager.notifier == nil || !rt.manager.notifier.Available() {
		return WaitingNotification{}, false
	}
	content := strings.TrimSpace(rt.lastNotifiedContent)
	if content == "" {
		content = RunningNotificationPlaceholder
	}
	return WaitingNotification{
		SessionID:          rt.session.ID,
		Name:               rt.session.Name,
		Content:            content,
		MessageID:          messageID,
		ChatID:             rt.session.LarkChatID,
		MentionOpenID:      rt.notificationMentionOpenID,
		UpdateNo:           rt.notificationUpdateNo,
		Running:            false,
		Disabled:           true,
		AutoRefreshEnabled: false,
		SuppressUpdateTip:  true,
	}, true
}

func (rt *RuntimeSession) RequestFreshSnapshot(timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	rt.mu.Lock()
	if !rt.session.Live {
		rt.mu.Unlock()
		return false
	}
	before := rt.visibleSnapshotVersion
	sessionID := rt.session.ID
	hasSubscribers := len(rt.subscribers) > 0
	hasRealSubscribers := rt.hasRealSubscriberLocked()
	needsBrowser := !hasSubscribers && rt.manager != nil && rt.manager.onBrowserNeeded != nil
	if !hasSubscribers && !needsBrowser {
		rt.mu.Unlock()
		return false
	}
	waiter := make(chan struct{})
	rt.snapshotWaiters = append(rt.snapshotWaiters, waiter)
	for ch, sub := range rt.subscribers {
		if hasRealSubscribers && sub.Headless {
			continue
		}
		select {
		case ch <- RuntimeEvent{Type: RuntimeEventSnapshotRequest}:
		default:
		}
	}
	rt.mu.Unlock()
	if needsBrowser {
		rt.manager.EnsureBrowser(sessionID)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-waiter:
	case <-timer.C:
	}
	rt.mu.Lock()
	fresh := rt.visibleSnapshotVersion > before
	subscriberCount := len(rt.subscribers)
	realSubscriberCount := rt.realSubscriberCountLocked()
	rt.mu.Unlock()
	log.Printf("snapshot request finished session=%s fresh=%v subscribers=%d real_subscribers=%d needed_browser=%v timeout=%s", sessionID, fresh, subscriberCount, realSubscriberCount, needsBrowser, timeout)
	return fresh
}

func (rt *RuntimeSession) hasRealSubscriberLocked() bool {
	return rt.realSubscriberCountLocked() > 0
}

func (rt *RuntimeSession) realSubscriberCountLocked() int {
	count := 0
	for _, sub := range rt.subscribers {
		if !sub.Headless {
			count++
		}
	}
	return count
}

func (rt *RuntimeSession) MarkInputActivity(data string) {
	rt.mu.Lock()
	previousInput := rt.lastInputText
	submitted := rt.recordInputLocked(data)
	disabledNote, disabledOK := rt.markInputActivityLocked(submitted, previousInput)
	s := rt.session
	rt.mu.Unlock()
	if disabledOK {
		go rt.updateDisabledNotification(disabledNote)
	}
	_ = rt.manager.persist(context.Background(), s)
}

func (rt *RuntimeSession) MarkStructuredInputActivity(text string) {
	rt.mu.Lock()
	previousInput := rt.lastInputText
	if cleaned := strings.TrimSpace(cleanInputForRecord(text)); cleaned != "" {
		rt.lastInputText = cleaned
	}
	rt.inputLineBuffer = ""
	disabledNote, disabledOK := rt.markInputActivityLocked(true, previousInput)
	s := rt.session
	rt.mu.Unlock()
	if disabledOK {
		go rt.updateDisabledNotification(disabledNote)
	}
	_ = rt.manager.persist(context.Background(), s)
}

func (rt *RuntimeSession) PrepareInputSnapshotBaseline() bool {
	return rt.prepareInputSnapshotBaseline(defaultInputBaselineSnapshotDeadline)
}

func (rt *RuntimeSession) prepareInputSnapshotBaseline(deadline time.Duration) bool {
	if deadline <= 0 {
		return false
	}
	expires := time.Now().Add(deadline)
	fresh := rt.RequestFreshSnapshot(minDuration(defaultNotifySnapshotTimeout, deadline))
	remaining := time.Until(expires)
	if remaining <= 0 {
		return fresh
	}
	secondTimeout := minDuration(600*time.Millisecond, remaining)
	if secondTimeout > 0 && rt.RequestFreshSnapshot(secondTimeout) {
		fresh = true
	}
	return fresh
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (rt *RuntimeSession) markInputActivityLocked(submitted bool, previousInput string) (WaitingNotification, bool) {
	var disabledNote WaitingNotification
	disabledOK := false
	if submitted {
		overlapRunningCard := rt.notificationRunning && rt.lastNotifiedMessageID != ""
		if overlapRunningCard {
			disabledNote, disabledOK = rt.disabledNotificationLocked(rt.lastNotifiedMessageID)
			rt.freezeNotificationMessageLocked(rt.lastNotifiedMessageID)
		}
		if overlapRunningCard && rt.snapshotAtRoundStartSet {
			if strings.TrimSpace(rt.notificationWindowInputText) == "" {
				rt.notificationWindowInputText = strings.TrimSpace(previousInput)
			}
			if strings.TrimSpace(rt.notificationWindowInputText) == "" {
				rt.notificationWindowInputText = strings.TrimSpace(rt.lastInputText)
			}
		} else {
			rt.snapshotAtRoundStart = rt.visibleSnapshot
			rt.snapshotAtRoundVersion = rt.visibleSnapshotVersion
			rt.snapshotAtRoundStartSet = true
			rt.notificationWindowInputText = ""
		}
		rt.roundReply = nil
		rt.lastNotifiedRoundHash = ""
		rt.lastNotifiedMessageID = ""
		rt.lastNotifiedContent = ""
		rt.notificationUpdateNo = 0
		rt.notificationRunning = false
		rt.suppressRunningMarker = false
	}
	rt.session.Status = StatusRunning
	rt.startupNotifyMode = startupNotifyNormal
	rt.session.UpdatedAt = time.Now().UTC()
	rt.stateVersion++
	rt.notifyVersion++
	rt.stopNotifyTimerLocked()
	rt.stopNotifyStableTimerLocked()
	rt.stopStartupNotifyTimerLocked()
	return disabledNote, disabledOK
}

func (rt *RuntimeSession) NotifyInputRunning() {
	rt.NotifyInputRunningOnMessage("")
}

func (rt *RuntimeSession) NotifyInputRunningOnMessage(messageID string) {
	source := "input"
	if strings.TrimSpace(messageID) != "" {
		source = "card_shortcut"
	}
	if rt == nil || rt.manager == nil || rt.manager.notifier == nil || !rt.manager.notifier.Available() {
		return
	}
	rt.mu.Lock()
	if !rt.session.Live || !rt.session.NotifyOnWaiting {
		rt.mu.Unlock()
		return
	}
	if rt.requireLarkChat && strings.TrimSpace(rt.session.LarkChatID) == "" {
		rt.mu.Unlock()
		return
	}
	messageID = strings.TrimSpace(messageID)
	if messageID != "" && rt.notificationMessageFrozenLocked(messageID) {
		log.Printf("lark card write skipped source=%s action=running_anchor session=%s message=%s reason=frozen_message", source, rt.session.ID, messageID)
		messageID = ""
	}
	if rt.lastNotifiedMessageID != "" && rt.notificationMessageFrozenLocked(rt.lastNotifiedMessageID) {
		rt.lastNotifiedMessageID = ""
	}
	if messageID != "" {
		rt.lastNotifiedMessageID = messageID
	}
	if rt.lastNotifiedMessageID != "" && rt.notificationRunning {
		rt.mu.Unlock()
		return
	}
	if rt.lastNotifiedMessageID != "" {
		rt.mu.Unlock()
		log.Printf("lark card write skipped source=%s action=running_patch session=%s message=%s reason=existing_card", source, rt.session.ID, rt.lastNotifiedMessageID)
		return
	}
	content := RunningNotificationPlaceholder
	rt.notificationPatchVersion++
	patchVersion := rt.notificationPatchVersion
	n := WaitingNotification{
		SessionID:           rt.session.ID,
		Name:                rt.session.Name,
		Content:             content,
		MessageID:           rt.lastNotifiedMessageID,
		ChatID:              rt.session.LarkChatID,
		MentionOpenID:       rt.notificationMentionOpenID,
		UpdateNo:            rt.notificationUpdateNo,
		Running:             true,
		AutoRefreshEnabled:  rt.autoRefreshEnabled,
		NotificationVersion: patchVersion,
	}
	rt.notificationRunning = true
	rt.mu.Unlock()
	log.Printf("lark card write queued source=%s action=running_create session=%s message=%s running=%v placeholder=%v update_no=%d content_len=%d", source, n.SessionID, n.MessageID, n.Running, n.Content == RunningNotificationPlaceholder, n.UpdateNo, len(n.Content))

	rt.notificationPatchMu.Lock()
	rt.mu.Lock()
	if rt.notificationPatchVersion != n.NotificationVersion {
		rt.mu.Unlock()
		rt.notificationPatchMu.Unlock()
		log.Printf("running notification send skipped session=%s message=%s reason=stale_patch", n.SessionID, n.MessageID)
		return
	}
	rt.mu.Unlock()
	result, err := rt.notifyWaitingWithRetry(n)
	rt.notificationPatchMu.Unlock()
	if err != nil {
		log.Printf("running notification send failed session=%s message=%s: %v", n.SessionID, n.MessageID, err)
		rt.mu.Lock()
		if rt.notificationPatchVersion == n.NotificationVersion && rt.lastNotifiedMessageID == n.MessageID {
			rt.notificationRunning = false
			if n.MessageID == "" {
				rt.lastNotifiedContent = ""
			}
		}
		rt.mu.Unlock()
		return
	}
	rt.mu.Lock()
	if rt.notificationPatchVersion == n.NotificationVersion {
		if result.MessageID != "" {
			rt.lastNotifiedMessageID = result.MessageID
			rt.bindAutoRefreshMessageLocked(result.MessageID)
		}
		rt.lastNotifiedContent = n.Content
		rt.notificationRunning = true
	}
	rt.mu.Unlock()
	defaultLarkMessageRegistry.rememberLatest(n.SessionID)
}

func (rt *RuntimeSession) RefreshNotificationMessage(messageID string, preserveUpdateNo ...int) error {
	if err := rt.refreshNotificationMessage(messageID, true, preserveUpdateNo...); err != nil {
		return err
	}
	rt.scheduleAutoRefreshOnce(messageID)
	return nil
}

func (rt *RuntimeSession) AutoRefreshNotificationMessage(messageID string, preserveUpdateNo ...int) error {
	return rt.refreshNotificationMessage(messageID, false, preserveUpdateNo...)
}

func (rt *RuntimeSession) refreshNotificationMessage(messageID string, suppressUpdateTip bool, preserveUpdateNo ...int) error {
	if rt == nil || rt.manager == nil || rt.manager.notifier == nil || !rt.manager.notifier.Available() {
		return errors.New("lark notifier is not configured")
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		rt.mu.Lock()
		messageID = rt.lastNotifiedMessageID
		rt.mu.Unlock()
	}
	if messageID == "" {
		return errors.New("notification message is not available")
	}
	rt.mu.Lock()
	if rt.notificationMessageFrozenLocked(messageID) {
		rt.mu.Unlock()
		return errors.New("notification message is frozen")
	}
	rt.mu.Unlock()
	content := strings.TrimSpace(rt.CurrentRoundContent())
	hasSnapshotContent := content != ""
	if !hasSnapshotContent {
		content = strings.TrimSpace(rt.CurrentVisibleContent())
		hasSnapshotContent = content != ""
	}
	if !hasSnapshotContent {
		content = "当前轮暂无内容"
	}
	contentHash := notifyContentHash(content)
	rt.mu.Lock()
	if !rt.session.Live || !rt.session.NotifyOnWaiting {
		rt.mu.Unlock()
		return errors.New("notification is not enabled")
	}
	if rt.notificationMessageFrozenLocked(messageID) {
		rt.mu.Unlock()
		return errors.New("notification message is frozen")
	}
	running := rt.session.Status == StatusRunning
	updateNo := rt.notificationUpdateNo
	if len(preserveUpdateNo) > 0 && preserveUpdateNo[0] > 0 {
		updateNo = preserveUpdateNo[0]
	}
	rt.notificationPatchVersion++
	patchVersion := rt.notificationPatchVersion
	n := WaitingNotification{
		SessionID:           rt.session.ID,
		Name:                rt.session.Name,
		Content:             content,
		MessageID:           messageID,
		ChatID:              rt.session.LarkChatID,
		MentionOpenID:       rt.notificationMentionOpenID,
		UpdateNo:            updateNo,
		Running:             running,
		AutoRefreshEnabled:  rt.autoRefreshEnabled,
		SuppressUpdateTip:   suppressUpdateTip,
		NotificationVersion: patchVersion,
	}
	rt.notificationRunning = n.Running
	rt.mu.Unlock()
	source := "auto_refresh"
	if suppressUpdateTip {
		source = "manual_refresh"
	}
	log.Printf("lark card write queued source=%s action=patch session=%s message=%s running=%v placeholder=%v update_no=%d content_len=%d", source, n.SessionID, n.MessageID, n.Running, n.Content == RunningNotificationPlaceholder, n.UpdateNo, len(n.Content))

	rt.notificationPatchMu.Lock()
	rt.mu.Lock()
	if rt.notificationMessageFrozenLocked(messageID) {
		rt.mu.Unlock()
		rt.notificationPatchMu.Unlock()
		return errors.New("notification message is frozen")
	}
	rt.mu.Unlock()
	result, err := rt.notifyWaitingWithRetry(n)
	rt.notificationPatchMu.Unlock()
	if err != nil {
		return err
	}
	rt.mu.Lock()
	if rt.notificationPatchVersion == n.NotificationVersion {
		if result.MessageID != "" {
			rt.lastNotifiedMessageID = result.MessageID
			rt.bindAutoRefreshMessageLocked(result.MessageID)
		} else {
			rt.lastNotifiedMessageID = messageID
			rt.bindAutoRefreshMessageLocked(messageID)
		}
		rt.lastNotifiedContent = content
		rt.lastNotifiedRoundHash = contentHash
		if hasSnapshotContent {
			rt.lastNotifiedVisibleSnapshot = rt.visibleSnapshot
		}
		if result.Updated {
			rt.notificationUpdateNo = n.UpdateNo
		}
		rt.notificationRunning = n.Running
	}
	rt.mu.Unlock()
	defaultLarkMessageRegistry.remember(rt.session.ID, messageID)
	defaultLarkMessageRegistry.rememberLatest(rt.session.ID)
	return nil
}

func (rt *RuntimeSession) bindAutoRefreshMessageLocked(messageID string) {
	messageID = strings.TrimSpace(messageID)
	if rt.autoRefreshEnabled && messageID != "" {
		rt.autoRefreshMessageID = messageID
	}
}

func (rt *RuntimeSession) ToggleAutoRefresh(messageID string) (bool, error) {
	if rt == nil {
		return false, errors.New("session is not available")
	}
	messageID = strings.TrimSpace(messageID)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.session.Live || !rt.session.NotifyOnWaiting {
		return false, errors.New("notification is not enabled")
	}
	if messageID == "" {
		messageID = rt.lastNotifiedMessageID
	}
	if messageID == "" {
		return false, errors.New("notification message is not available")
	}
	if rt.notificationMessageFrozenLocked(messageID) {
		return false, errors.New("notification message is frozen")
	}
	if rt.autoRefreshEnabled {
		rt.stopAutoRefreshLocked()
		return false, nil
	}
	rt.autoRefreshEnabled = true
	rt.autoRefreshMessageID = messageID
	stop := make(chan struct{})
	rt.autoRefreshStop = stop
	go rt.autoRefreshLoop(stop)
	return true, nil
}

func (rt *RuntimeSession) stopAutoRefreshLocked() {
	if rt.autoRefreshStop != nil {
		close(rt.autoRefreshStop)
		rt.autoRefreshStop = nil
	}
	rt.autoRefreshEnabled = false
	rt.autoRefreshMessageID = ""
}

func (rt *RuntimeSession) autoRefreshLoop(stop <-chan struct{}) {
	timer := time.NewTimer(rt.manager.autoRefreshDelay())
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
		}
		rt.mu.Lock()
		if !rt.autoRefreshEnabled || rt.autoRefreshStop != stop {
			rt.mu.Unlock()
			return
		}
		if !rt.session.Live || rt.session.Status == StatusExited || rt.session.Status == StatusFailed {
			rt.stopAutoRefreshLocked()
			rt.mu.Unlock()
			return
		}
		messageID := rt.autoRefreshMessageID
		updateNo := rt.notificationUpdateNo
		running := rt.session.Status == StatusRunning
		sessionID := rt.session.ID
		rt.mu.Unlock()
		if running && messageID != "" {
			if err := rt.AutoRefreshNotificationMessage(messageID, updateNo); err != nil {
				log.Printf("lark card auto refresh failed session=%s message=%s: %v", sessionID, messageID, err)
			}
		}
		timer.Reset(rt.manager.autoRefreshDelay())
	}
}

func (rt *RuntimeSession) scheduleAutoRefreshOnce(messageID string) {
	if rt == nil || rt.manager == nil {
		return
	}
	messageID = strings.TrimSpace(messageID)
	rt.mu.Lock()
	if !rt.autoRefreshEnabled || rt.autoRefreshStop == nil {
		rt.mu.Unlock()
		return
	}
	if messageID == "" {
		messageID = rt.autoRefreshMessageID
	}
	if messageID == "" {
		rt.mu.Unlock()
		return
	}
	delay := rt.manager.autoRefreshDelay()
	sessionID := rt.session.ID
	rt.mu.Unlock()
	time.AfterFunc(delay, func() {
		rt.mu.Lock()
		if !rt.autoRefreshEnabled || rt.autoRefreshMessageID != messageID || !rt.session.Live || rt.session.Status == StatusExited || rt.session.Status == StatusFailed {
			rt.mu.Unlock()
			return
		}
		updateNo := rt.notificationUpdateNo
		rt.mu.Unlock()
		if err := rt.AutoRefreshNotificationMessage(messageID, updateNo); err != nil {
			log.Printf("lark card auto refresh after manual refresh failed session=%s message=%s: %v", sessionID, messageID, err)
		}
	})
}

func (rt *RuntimeSession) recordInputLocked(data string) bool {
	cleaned := cleanInputForRecord(data)
	submitted := false
	for _, r := range cleaned {
		switch r {
		case '\r', '\n':
			submitted = true
			if text := strings.TrimSpace(rt.inputLineBuffer); text != "" {
				rt.lastInputText = text
			}
			rt.inputLineBuffer = ""
		case '\b', 0x7f:
			rs := []rune(rt.inputLineBuffer)
			if len(rs) > 0 {
				rt.inputLineBuffer = string(rs[:len(rs)-1])
			}
		default:
			if r >= 0x20 && r != 0x1b {
				rt.inputLineBuffer += string(r)
			}
		}
	}
	if !submitted {
		if text := strings.TrimSpace(rt.inputLineBuffer); text != "" {
			rt.lastInputText = text
		}
	}
	return submitted
}

func inputChangesSessionState(data string) bool {
	for _, r := range cleanInputForRecord(data) {
		switch r {
		case '\r', '\n':
			return true
		case '\b', 0x7f:
			continue
		default:
			if r >= 0x20 && r != 0x1b && !unicode.IsSpace(r) {
				return true
			}
		}
	}
	return false
}

func cleanInputForRecord(data string) string {
	runes := []rune(data)
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 0x1b {
			i = skipInputEscape(runes, i)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func skipInputEscape(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return i
	}
	switch runes[i+1] {
	case '[':
		j := i + 2
		for j < len(runes) {
			r := runes[j]
			if r >= 0x40 && r <= 0x7e {
				return j
			}
			j++
		}
		return len(runes) - 1
	case 'O':
		if i+2 < len(runes) {
			return i + 2
		}
		return i + 1
	default:
		return i + 1
	}
}

func (rt *RuntimeSession) HandleOutput(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	cp := append([]byte(nil), chunk...)
	renderable := HasRenderableContent(cp)
	rt.mu.Lock()
	rt.output = append(rt.output, cp...)
	if len(rt.output) > maxOutputBytes {
		rt.output = rt.output[len(rt.output)-maxOutputBytes:]
	}
	rt.roundReply = append(rt.roundReply, cp...)
	if len(rt.roundReply) > maxRoundBytes {
		rt.roundReply = rt.roundReply[len(rt.roundReply)-maxRoundBytes:]
	}
	rt.session.HistorySize += int64(len(cp))
	rt.session.UpdatedAt = time.Now().UTC()
	var runningNote WaitingNotification
	markRunning := false
	if renderable {
		previousStatus := rt.session.Status
		rt.session.Status = StatusRunning
		rt.stateVersion++
		rt.notifyVersion++
		if previousStatus != StatusRunning {
			log.Printf("session status transition source=terminal_output session=%s from=%s to=%s notify_version=%d card_message=%s notification_running=%v suppress_running_marker=%v",
				rt.session.ID, previousStatus, rt.session.Status, rt.notifyVersion, rt.lastNotifiedMessageID, rt.notificationRunning, rt.suppressRunningMarker)
		}
		if previousStatus == StatusWaiting {
			runningNote, markRunning = rt.markNotificationRunningLocked()
		}
		if rt.startupNotifyMode == startupNotifySettling {
			rt.scheduleStartupNotifyFinalLocked(defaultStartupPresetSettleDelay)
		}
		rt.resetNotifyStableTimerLocked()
	}
	for ch := range rt.subscribers {
		select {
		case ch <- RuntimeEvent{Type: RuntimeEventOutput, Data: cp}:
		default:
		}
	}
	seq := rt.nextSeq
	rt.nextSeq++
	s := rt.session
	rt.mu.Unlock()
	if rt.manager.store != nil {
		_ = rt.manager.store.AppendOutput(context.Background(), s.ID, seq, cp)
		_ = rt.manager.store.UpdateSession(context.Background(), s)
	}
	if markRunning {
		go rt.updateNotificationRunning(runningNote, true)
	}
}

func (rt *RuntimeSession) Close() {
	rt.mu.Lock()
	rt.stopAutoRefreshLocked()
	rt.stopNotifyTimerLocked()
	rt.stopNotifyStableTimerLocked()
	rt.stopStartupNotifyTimerLocked()
	for ch := range rt.subscribers {
		close(ch)
		delete(rt.subscribers, ch)
	}
	rt.mu.Unlock()
	_ = rt.terminal.Close()
}

func (rt *RuntimeSession) streamOutput() {
	buf := make([]byte, 8192)
	for {
		n, err := rt.terminal.Read(buf)
		if n > 0 {
			rt.HandleOutput(buf[:n])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				rt.HandleOutput([]byte("\r\n[terminal closed]\r\n"))
			}
			return
		}
	}
}

func (rt *RuntimeSession) waitForExit() {
	err := rt.process.Wait()
	code := 0
	status := StatusExited
	if err != nil {
		status = StatusFailed
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	rt.markTerminal(status, code)
}

func (rt *RuntimeSession) markTerminal(status string, code int) {
	rt.mu.Lock()
	rt.stopAutoRefreshLocked()
	rt.stopNotifyTimerLocked()
	rt.stopNotifyStableTimerLocked()
	rt.stopStartupNotifyTimerLocked()
	rt.session.Status = status
	rt.session.Live = false
	rt.session.ExitCode = &code
	rt.session.UpdatedAt = time.Now().UTC()
	s := rt.session
	for ch := range rt.subscribers {
		close(ch)
		delete(rt.subscribers, ch)
	}
	rt.mu.Unlock()
	rt.manager.mu.Lock()
	delete(rt.manager.sessions, s.ID)
	rt.manager.mu.Unlock()
	_ = rt.terminal.Close()
	if rt.manager.store != nil {
		_ = rt.manager.store.DeleteSession(context.Background(), s.ID)
	}
	rt.manager.sessionEnded(s.ID)
}

func (rt *RuntimeSession) notifyStableDelayLocked() time.Duration {
	if notifyContentNeedsConservativeDelayWithWindow(rt.visibleSnapshot, rt.previousNotifySnapshotLocked(), rt.lastInputText, rt.notificationWindowInputText) {
		return rt.manager.conservativeWaiting
	}
	return rt.manager.fastWaiting
}

func (rt *RuntimeSession) resetNotifyStableTimerLocked() {
	rt.stopNotifyStableTimerLocked()
	if !rt.session.Live {
		return
	}
	version := rt.stateVersion
	delay := rt.notifyStableDelayLocked()
	rt.notifyStableTimer = time.AfterFunc(delay, func() {
		rt.notifyAfterStable(version)
	})
}

func (rt *RuntimeSession) notifyAfterStable(version int64) {
	rt.mu.Lock()
	if !rt.session.Live || rt.stateVersion != version || rt.session.Status == StatusExited || rt.session.Status == StatusFailed {
		rt.mu.Unlock()
		return
	}
	if rt.session.Status == StatusRunning {
		rt.session.Status = StatusWaiting
		rt.session.UpdatedAt = time.Now().UTC()
		rt.notifyVersion++
		s := rt.session
		notifyVersion := rt.notifyVersion
		rt.mu.Unlock()
		_ = rt.manager.persist(context.Background(), s)
		rt.notifyIfStillWaiting(notifyVersion)
		return
	}
	notifyVersion := rt.notifyVersion
	rt.mu.Unlock()
	rt.notifyIfStillWaiting(notifyVersion)
}

func (rt *RuntimeSession) notifyIfStillWaiting(version int64) {
	time.Sleep(100 * time.Millisecond)
	rt.mu.Lock()
	if rt.session.Status != StatusWaiting || !rt.session.Live || !rt.session.NotifyOnWaiting || rt.notifyVersion != version || rt.manager.notifier == nil || !rt.manager.notifier.Available() {
		rt.mu.Unlock()
		return
	}
	rt.mu.Unlock()
	rt.RequestFreshSnapshot(defaultNotifySnapshotTimeout)
	deadline := time.Now().Add(defaultNotifySnapshotDeadline)
	for time.Now().Before(deadline) {
		rt.mu.Lock()
		needsMoreSnapshot := rt.notifyContentNeedsMoreSnapshotLocked()
		done := rt.session.Status != StatusWaiting || !rt.session.Live || !rt.session.NotifyOnWaiting || rt.notifyVersion != version
		rt.mu.Unlock()
		if done {
			return
		}
		if !needsMoreSnapshot {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	rt.mu.Lock()
	if rt.session.Status != StatusWaiting || !rt.session.Live || !rt.session.NotifyOnWaiting || rt.notifyVersion != version {
		rt.mu.Unlock()
		return
	}
	n, contentHash, ok := rt.waitingNotificationLocked()
	if !ok {
		_, _, _, reason := rt.waitingNotificationCandidateLocked()
		log.Printf("waiting notification not ready session=%s version=%d reason=%s status=%s live=%v notify_version=%d last_input=%q visible_len=%d round_len=%d",
			rt.session.ID, version, reason, rt.session.Status, rt.session.Live, rt.notifyVersion, rt.lastInputText, len(rt.visibleSnapshot), len(rt.roundReply))
		var waitingNote WaitingNotification
		clearRunning := false
		if reason == "duplicate_hash" && rt.notificationRunning {
			waitingNote, clearRunning = rt.markNotificationWaitingLocked()
		}
		if reason != "duplicate_hash" {
			rt.rescheduleNotifyRetryLocked(version)
		}
		rt.mu.Unlock()
		if clearRunning {
			rt.updateNotificationRunning(waitingNote, false)
		}
		return
	}
	if rt.startupNotifyMode == startupNotifySuppress || rt.startupNotifyMode == startupNotifySettling {
		mode := rt.startupNotifyMode
		rt.mu.Unlock()
		log.Printf("waiting notification suppressed during startup presets session=%s version=%d mode=%d hash=%s", n.SessionID, version, mode, shortNotifyHash(contentHash))
		return
	}
	if rt.startupNotifyMode == startupNotifyFinal {
		rt.startupNotifyMode = startupNotifyNormal
	}
	if rt.lastNotifiedMessageID != "" {
		n.MessageID = rt.lastNotifiedMessageID
		n.UpdateNo = rt.notificationUpdateNo + 1
		n.Running = false
	}
	n.AutoRefreshEnabled = rt.autoRefreshEnabled
	rt.notificationPatchVersion++
	n.NotificationVersion = rt.notificationPatchVersion
	notifiedVisibleSnapshot := rt.visibleSnapshot
	roundInput := rt.lastInputText
	roundSnapshotVersion := rt.snapshotAtRoundVersion
	updateCoalesce := time.Duration(0)
	if n.MessageID != "" {
		updateCoalesce = rt.manager.updateCoalesce
	}
	rt.mu.Unlock()
	if updateCoalesce > 0 && !rt.waitForNotificationUpdateCoalesce(version, updateCoalesce) {
		log.Printf("waiting notification update coalesced session=%s version=%d hash=%s delay=%s",
			n.SessionID, version, shortNotifyHash(contentHash), updateCoalesce)
		return
	}
	action := "create"
	if n.MessageID != "" {
		action = "patch"
	}
	log.Printf("lark card write queued source=waiting action=%s session=%s message=%s running=%v placeholder=%v update_no=%d version=%d hash=%s snapshot_source=%s content_len=%d content_lines=%d preview=%q",
		action, n.SessionID, n.MessageID, n.Running, n.Content == RunningNotificationPlaceholder, n.UpdateNo, version, shortNotifyHash(contentHash), n.SnapshotSource, len(n.Content), countLogLines(n.Content), previewLogText(n.Content, 160))
	rt.notificationPatchMu.Lock()
	rt.mu.Lock()
	if rt.session.Status != StatusWaiting || !rt.session.Live || !rt.session.NotifyOnWaiting || rt.notifyVersion != version {
		currentVersion := rt.notifyVersion
		currentStatus := rt.session.Status
		rt.mu.Unlock()
		rt.notificationPatchMu.Unlock()
		log.Printf("waiting notification send skipped session=%s version=%d current_version=%d status=%s reason=stale_before_send",
			n.SessionID, version, currentVersion, currentStatus)
		return
	}
	if rt.notificationPatchVersion != n.NotificationVersion {
		currentPatchVersion := rt.notificationPatchVersion
		rt.mu.Unlock()
		rt.notificationPatchMu.Unlock()
		log.Printf("waiting notification send skipped session=%s version=%d current_patch_version=%d note_patch_version=%d reason=stale_patch",
			n.SessionID, version, currentPatchVersion, n.NotificationVersion)
		return
	}
	rt.mu.Unlock()
	result, err := rt.notifyWaitingWithRetry(n)
	rt.notificationPatchMu.Unlock()
	if err != nil {
		log.Printf("waiting notification send failed session=%s version=%d hash=%s: %v", n.SessionID, version, shortNotifyHash(contentHash), err)
		return
	}
	log.Printf("waiting notification sent session=%s version=%d hash=%s", n.SessionID, version, shortNotifyHash(contentHash))
	rt.mu.Lock()
	sameRound := rt.lastInputText == roundInput && rt.snapshotAtRoundVersion == roundSnapshotVersion
	if rt.session.Status == StatusWaiting && rt.session.Live && rt.session.NotifyOnWaiting && rt.notifyVersion == version {
		if rt.notificationPatchVersion == n.NotificationVersion {
			rt.lastNotifiedRoundHash = contentHash
			if result.MessageID != "" {
				rt.lastNotifiedMessageID = result.MessageID
				rt.bindAutoRefreshMessageLocked(result.MessageID)
			}
			rt.lastNotifiedContent = n.Content
			rt.lastNotifiedVisibleSnapshot = notifiedVisibleSnapshot
			if result.Updated {
				rt.notificationUpdateNo = n.UpdateNo
			}
			rt.notificationRunning = n.Running
		}
	} else if result.MessageID != "" && !result.Updated && sameRound && rt.lastNotifiedMessageID == "" {
		rt.lastNotifiedMessageID = result.MessageID
		rt.bindAutoRefreshMessageLocked(result.MessageID)
		rt.lastNotifiedContent = n.Content
		rt.lastNotifiedVisibleSnapshot = notifiedVisibleSnapshot
		rt.notificationUpdateNo = n.UpdateNo
		rt.notificationRunning = n.Running
	}
	rt.mu.Unlock()
	defaultLarkMessageRegistry.rememberLatest(n.SessionID)
	rt.manager.notificationSent(n.SessionID)
}

func (rt *RuntimeSession) waitForNotificationUpdateCoalesce(version int64, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.session.Status == StatusWaiting &&
		rt.session.Live &&
		rt.session.NotifyOnWaiting &&
		rt.notifyVersion == version
}

func (rt *RuntimeSession) waitingNotificationLocked() (WaitingNotification, string, bool) {
	n, contentHash, ok, _ := rt.waitingNotificationCandidateLocked()
	return n, contentHash, ok
}

func (rt *RuntimeSession) markNotificationRunningLocked() (WaitingNotification, bool) {
	content := strings.TrimSpace(rt.lastNotifiedContent)
	if content == "" {
		content = strings.TrimSpace(rt.currentNotifyContentLocked())
	}
	switch {
	case rt.manager.notifier == nil:
		log.Printf("waiting notification running marker skipped session=%s reason=no_notifier", rt.session.ID)
		return WaitingNotification{}, false
	case rt.requireLarkChat && strings.TrimSpace(rt.session.LarkChatID) == "":
		log.Printf("waiting notification running marker skipped session=%s reason=waiting_for_lark_chat", rt.session.ID)
		return WaitingNotification{}, false
	case rt.lastNotifiedMessageID == "":
		log.Printf("waiting notification running marker skipped session=%s reason=no_message_id", rt.session.ID)
		return WaitingNotification{}, false
	case content == "":
		log.Printf("waiting notification running marker skipped session=%s message=%s reason=no_content", rt.session.ID, rt.lastNotifiedMessageID)
		return WaitingNotification{}, false
	}
	if _, ok := rt.manager.notifier.(WaitingRunningNotifier); !ok {
		log.Printf("waiting notification running marker skipped session=%s message=%s reason=notifier_unsupported", rt.session.ID, rt.lastNotifiedMessageID)
		return WaitingNotification{}, false
	}
	rt.notificationPatchVersion++
	rt.lastNotifiedContent = content
	rt.notificationRunning = true
	return WaitingNotification{
		SessionID:           rt.session.ID,
		Name:                rt.session.Name,
		Content:             content,
		MessageID:           rt.lastNotifiedMessageID,
		ChatID:              rt.session.LarkChatID,
		MentionOpenID:       rt.notificationMentionOpenID,
		UpdateNo:            rt.notificationUpdateNo,
		Running:             true,
		AutoRefreshEnabled:  rt.autoRefreshEnabled,
		NotificationVersion: rt.notificationPatchVersion,
	}, true
}

func (rt *RuntimeSession) markNotificationWaitingLocked() (WaitingNotification, bool) {
	switch {
	case rt.manager.notifier == nil:
		return WaitingNotification{}, false
	case rt.lastNotifiedMessageID == "":
		return WaitingNotification{}, false
	case rt.lastNotifiedContent == "":
		return WaitingNotification{}, false
	}
	if _, ok := rt.manager.notifier.(WaitingRunningNotifier); !ok {
		return WaitingNotification{}, false
	}
	rt.notificationPatchVersion++
	rt.notificationRunning = false
	return WaitingNotification{
		SessionID:           rt.session.ID,
		Name:                rt.session.Name,
		Content:             rt.lastNotifiedContent,
		MessageID:           rt.lastNotifiedMessageID,
		ChatID:              rt.session.LarkChatID,
		MentionOpenID:       rt.notificationMentionOpenID,
		UpdateNo:            rt.notificationUpdateNo,
		Running:             false,
		AutoRefreshEnabled:  rt.autoRefreshEnabled,
		NotificationVersion: rt.notificationPatchVersion,
	}, true
}

func (rt *RuntimeSession) updateNotificationRunning(note WaitingNotification, running bool) {
	notifier, ok := rt.manager.notifier.(WaitingRunningNotifier)
	if !ok {
		return
	}
	rt.notificationPatchMu.Lock()
	defer rt.notificationPatchMu.Unlock()
	rt.mu.Lock()
	currentMessageID := rt.lastNotifiedMessageID
	if rt.notificationMessageFrozenLocked(note.MessageID) {
		rt.mu.Unlock()
		log.Printf("waiting notification running marker skipped session=%s message=%s running=%v reason=frozen_message",
			note.SessionID, note.MessageID, running)
		return
	}
	if currentMessageID != "" && currentMessageID != note.MessageID {
		rt.mu.Unlock()
		log.Printf("waiting notification running marker skipped session=%s message=%s current_message=%s running=%v reason=stale_message",
			note.SessionID, note.MessageID, currentMessageID, running)
		return
	}
	if note.NotificationVersion > 0 && rt.notificationPatchVersion != note.NotificationVersion {
		currentPatchVersion := rt.notificationPatchVersion
		rt.mu.Unlock()
		log.Printf("waiting notification running marker skipped session=%s message=%s current_patch_version=%d note_patch_version=%d running=%v reason=stale_patch",
			note.SessionID, note.MessageID, currentPatchVersion, note.NotificationVersion, running)
		return
	}
	rt.mu.Unlock()
	if err := rt.updateWaitingRunningWithRetry(notifier, note, running); err != nil {
		log.Printf("waiting notification running marker failed session=%s message=%s running=%v: %v", note.SessionID, note.MessageID, running, err)
		if running {
			rt.mu.Lock()
			if rt.lastNotifiedMessageID == note.MessageID && rt.notificationPatchVersion == note.NotificationVersion {
				rt.notificationRunning = false
			}
			rt.mu.Unlock()
		}
		return
	}
	log.Printf("waiting notification running marker updated session=%s message=%s running=%v", note.SessionID, note.MessageID, running)
}

func (rt *RuntimeSession) updateDisabledNotification(note WaitingNotification) {
	if rt == nil || rt.manager == nil || rt.manager.notifier == nil || !rt.manager.notifier.Available() {
		return
	}
	note.MessageID = strings.TrimSpace(note.MessageID)
	if note.MessageID == "" {
		return
	}
	note.Running = false
	note.Disabled = true
	note.AutoRefreshEnabled = false
	note.SuppressUpdateTip = true
	rt.notificationPatchMu.Lock()
	defer rt.notificationPatchMu.Unlock()
	rt.mu.Lock()
	if !rt.notificationMessageFrozenLocked(note.MessageID) {
		rt.mu.Unlock()
		return
	}
	rt.mu.Unlock()
	if _, err := rt.notifyWaitingWithRetry(note); err != nil {
		log.Printf("disabled notification update failed session=%s message=%s: %v", note.SessionID, note.MessageID, err)
		return
	}
	log.Printf("disabled notification updated session=%s message=%s", note.SessionID, note.MessageID)
}

func (rt *RuntimeSession) notifyWaitingWithRetry(note WaitingNotification) (WaitingNotificationResult, error) {
	if rt == nil || rt.manager == nil || rt.manager.notifier == nil {
		return WaitingNotificationResult{}, errors.New("lark notifier is not configured")
	}
	var lastErr error
	for attempt := 1; attempt <= defaultNotificationSendAttempts; attempt++ {
		result, err := rt.manager.notifier.NotifyWaiting(note)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt < defaultNotificationSendAttempts {
			time.Sleep(time.Duration(attempt) * defaultNotificationSendRetryDelay)
		}
	}
	return WaitingNotificationResult{}, lastErr
}

func (rt *RuntimeSession) updateWaitingRunningWithRetry(notifier WaitingRunningNotifier, note WaitingNotification, running bool) error {
	var lastErr error
	for attempt := 1; attempt <= defaultNotificationSendAttempts; attempt++ {
		if err := notifier.UpdateWaitingRunning(note, running); err != nil {
			lastErr = err
			if attempt < defaultNotificationSendAttempts {
				time.Sleep(time.Duration(attempt) * defaultNotificationSendRetryDelay)
			}
			continue
		}
		return nil
	}
	return lastErr
}

func (rt *RuntimeSession) waitingNotificationCandidateLocked() (WaitingNotification, string, bool, string) {
	if rt.requireLarkChat && strings.TrimSpace(rt.session.LarkChatID) == "" {
		return WaitingNotification{}, "", false, "waiting_for_lark_chat"
	}
	if rt.visibleSnapshotStaleForCurrentRoundLocked() {
		return WaitingNotification{}, "", false, "stale_visible_snapshot"
	}
	if notifyContentNeedsMoreSnapshotWithWindow(rt.visibleSnapshot, rt.previousNotifySnapshotLocked(), rt.roundReply, rt.lastInputText, rt.notificationWindowInputText) {
		return WaitingNotification{}, "", false, "needs_more_snapshot"
	}
	content := rt.currentNotifyContentLocked()
	content = strings.TrimSpace(content)
	if content == "" {
		return WaitingNotification{}, "", false, "empty_content"
	}
	contentHash := notifyContentHash(content)
	if contentHash == rt.lastNotifiedRoundHash {
		return WaitingNotification{}, "", false, "duplicate_hash"
	}
	return WaitingNotification{SessionID: rt.session.ID, Name: rt.session.Name, Content: content, ChatID: rt.session.LarkChatID, MentionOpenID: rt.notificationMentionOpenID, SnapshotSource: rt.visibleSnapshotSource}, contentHash, true, "ready"
}

func (rt *RuntimeSession) notifyContentNeedsMoreSnapshotLocked() bool {
	if rt.visibleSnapshotStaleForCurrentRoundLocked() {
		return true
	}
	return notifyContentNeedsMoreSnapshotWithWindow(rt.visibleSnapshot, rt.previousNotifySnapshotLocked(), rt.roundReply, rt.lastInputText, rt.notificationWindowInputText)
}

func (rt *RuntimeSession) visibleSnapshotStaleForCurrentRoundLocked() bool {
	if strings.TrimSpace(rt.lastInputText) == "" || strings.TrimSpace(rt.visibleSnapshot) == "" {
		return false
	}
	if rt.visibleSnapshotVersion <= rt.snapshotAtRoundVersion {
		return true
	}
	return normalizeSnapshotText(rt.visibleSnapshot) == normalizeSnapshotText(rt.snapshotAtRoundStart)
}

func (rt *RuntimeSession) rescheduleNotifyRetryLocked(version int64) {
	if rt.session.Status != StatusWaiting || !rt.session.Live || rt.notifyVersion != version || !rt.session.NotifyOnWaiting {
		return
	}
	rt.stopNotifyTimerLocked()
	rt.notifyRetryTimer = time.AfterFunc(defaultNotifyRetryDelay, func() {
		rt.notifyIfStillWaiting(version)
	})
}

func notifyContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func shortNotifyHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func previewLogText(text string, max int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

func countLogLines(text string) int {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func (rt *RuntimeSession) stopNotifyTimerLocked() {
	if rt.notifyRetryTimer != nil {
		rt.notifyRetryTimer.Stop()
		rt.notifyRetryTimer = nil
	}
}

func (rt *RuntimeSession) stopNotifyStableTimerLocked() {
	if rt.notifyStableTimer != nil {
		rt.notifyStableTimer.Stop()
		rt.notifyStableTimer = nil
	}
}

func (rt *RuntimeSession) stopStartupNotifyTimerLocked() {
	if rt.startupNotifyTimer != nil {
		rt.startupNotifyTimer.Stop()
		rt.startupNotifyTimer = nil
	}
}

func (rt *RuntimeSession) scheduleStartupNotifyFinalLocked(delay time.Duration) {
	if delay <= 0 {
		delay = defaultStartupPresetSettleDelay
	}
	rt.stopStartupNotifyTimerLocked()
	rt.startupNotifyTimer = time.AfterFunc(delay, func() {
		rt.mu.Lock()
		if rt.startupNotifyMode != startupNotifySettling || !rt.session.Live {
			rt.mu.Unlock()
			return
		}
		rt.startupNotifyMode = startupNotifyFinal
		version := rt.notifyVersion
		waiting := rt.session.Status == StatusWaiting
		rt.mu.Unlock()
		if waiting {
			rt.notifyIfStillWaiting(version)
		}
	})
}
