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
	defaultNotificationUpdateCoalesce    = 15 * time.Second
	defaultNotifyRetryDelay              = time.Second
	defaultStartupPresetSettleDelay      = 2 * time.Second
)

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
	MarkAllNonTerminalSessionsExited(context.Context) error
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
	updateCoalesce      time.Duration
	preStartCommand     string
	sessions            map[string]*RuntimeSession
	onBrowserNeeded     func(string)
	onNotificationSent  func(string)
}

type ManagerOption func(*Manager)

func NewManager(store Store, launcher Launcher, opts ...ManagerOption) *Manager {
	m := &Manager{
		store:               store,
		launcher:            launcher,
		fastWaiting:         defaultFastWaitingTransition,
		conservativeWaiting: defaultConservativeWaitingTransition,
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

func WithBrowserNeeded(fn func(string)) ManagerOption {
	return func(m *Manager) { m.onBrowserNeeded = fn }
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
		if m.store != nil {
			_ = m.store.CreateSession(ctx, sess)
		}
		return sess, err
	}
	rt := &RuntimeSession{
		manager:     m,
		session:     sess,
		terminal:    handle.Terminal(),
		process:     handle.Process(),
		subscribers: make(map[chan RuntimeEvent]struct{}),
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
		if s.LarkChatID == chatID {
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
		if s.LarkChatID == chatID {
			if s.Live && s.Status != StatusExited && s.Status != StatusFailed {
				defaultLarkMessageRegistry.rememberChat(chatID, s.ID)
			}
			return s, true, nil
		}
	}
	return Session{}, false, nil
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

func (m *Manager) MarkFinished(ctx context.Context, id string) (bool, error) {
	rt, ok := m.GetRuntime(id)
	if !ok {
		return false, nil
	}
	rt.markTerminal(StatusExited, 0)
	return true, nil
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
	mu                     sync.Mutex
	notificationPatchMu    sync.Mutex
	manager                *Manager
	session                Session
	terminal               Terminal
	process                Waiter
	output                 []byte
	roundReply             []byte
	visibleSnapshot        string
	visibleSnapshotVersion int64
	snapshotAtRoundStart   string
	snapshotAtRoundVersion int64
	lastInputText          string
	inputLineBuffer        string
	lastNotifiedRoundHash  string
	lastNotifiedMessageID  string
	lastNotifiedContent    string
	notificationUpdateNo   int
	notificationRunning    bool
	startupNotifyMode      startupNotifyMode
	subscribers            map[chan RuntimeEvent]struct{}
	snapshotWaiters        []chan struct{}
	nextSeq                int64
	stateVersion           int64
	notifyVersion          int64
	notifyRetryTimer       *time.Timer
	notifyStableTimer      *time.Timer
	startupNotifyTimer     *time.Timer
}

type RuntimeEvent struct {
	Type string
	Data []byte
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
	ch := make(chan RuntimeEvent, 64)
	rt.mu.Lock()
	rt.subscribers[ch] = struct{}{}
	rt.mu.Unlock()
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
	rt.mu.Lock()
	rt.visibleSnapshot = data
	rt.visibleSnapshotVersion++
	waiters := rt.snapshotWaiters
	rt.snapshotWaiters = nil
	rt.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

func (rt *RuntimeSession) CurrentRoundContent() string {
	rt.RequestFreshSnapshot(800 * time.Millisecond)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return PickNotifyContent(rt.visibleSnapshot, rt.snapshotAtRoundStart, rt.roundReply, rt.lastInputText)
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
	needsBrowser := len(rt.subscribers) == 0 && rt.manager.onBrowserNeeded != nil
	if len(rt.subscribers) == 0 && !needsBrowser {
		rt.mu.Unlock()
		return false
	}
	waiter := make(chan struct{})
	rt.snapshotWaiters = append(rt.snapshotWaiters, waiter)
	for ch := range rt.subscribers {
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
	rt.mu.Unlock()
	log.Printf("snapshot request finished session=%s fresh=%v subscribers=%d needed_browser=%v timeout=%s", sessionID, fresh, subscriberCount, needsBrowser, timeout)
	return fresh
}

func (rt *RuntimeSession) MarkInputActivity(data string) {
	rt.mu.Lock()
	submitted := rt.recordInputLocked(data)
	if submitted {
		rt.snapshotAtRoundStart = rt.visibleSnapshot
		rt.snapshotAtRoundVersion = rt.visibleSnapshotVersion
		rt.roundReply = nil
		rt.lastNotifiedRoundHash = ""
		rt.lastNotifiedMessageID = ""
		rt.lastNotifiedContent = ""
		rt.notificationUpdateNo = 0
		rt.notificationRunning = false
	}
	rt.session.Status = StatusRunning
	rt.startupNotifyMode = startupNotifyNormal
	rt.session.UpdatedAt = time.Now().UTC()
	rt.stateVersion++
	rt.notifyVersion++
	rt.stopNotifyTimerLocked()
	rt.stopNotifyStableTimerLocked()
	rt.stopStartupNotifyTimerLocked()
	s := rt.session
	rt.mu.Unlock()
	_ = rt.manager.persist(context.Background(), s)
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
		rt.session.Status = StatusRunning
		rt.stateVersion++
		rt.notifyVersion++
		if rt.lastNotifiedMessageID != "" && rt.lastNotifiedContent != "" && !rt.notificationRunning {
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
		rt.updateNotificationRunning(runningNote, true)
	}
}

func (rt *RuntimeSession) Close() {
	rt.mu.Lock()
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
	_ = rt.manager.persist(context.Background(), s)
	rt.manager.mu.Lock()
	delete(rt.manager.sessions, s.ID)
	rt.manager.mu.Unlock()
	_ = rt.terminal.Close()
}

func (rt *RuntimeSession) notifyStableDelayLocked() time.Duration {
	if NotifyContentNeedsConservativeDelay(rt.visibleSnapshot, rt.snapshotAtRoundStart, rt.lastInputText) {
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
	rt.RequestFreshSnapshot(4 * time.Second)
	deadline := time.Now().Add(8 * time.Second)
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
	log.Printf("waiting notification sending session=%s name=%q version=%d hash=%s content_len=%d preview=%q",
		n.SessionID, n.Name, version, shortNotifyHash(contentHash), len(n.Content), previewLogText(n.Content, 160))
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
	rt.mu.Unlock()
	result, err := rt.manager.notifier.NotifyWaiting(n)
	rt.notificationPatchMu.Unlock()
	if err != nil {
		log.Printf("waiting notification send failed session=%s version=%d hash=%s: %v", n.SessionID, version, shortNotifyHash(contentHash), err)
		return
	}
	log.Printf("waiting notification sent session=%s version=%d hash=%s", n.SessionID, version, shortNotifyHash(contentHash))
	rt.mu.Lock()
	sameRound := rt.lastInputText == roundInput && rt.snapshotAtRoundVersion == roundSnapshotVersion
	if rt.session.Status == StatusWaiting && rt.session.Live && rt.session.NotifyOnWaiting && rt.notifyVersion == version {
		rt.lastNotifiedRoundHash = contentHash
		if result.MessageID != "" {
			rt.lastNotifiedMessageID = result.MessageID
		}
		rt.lastNotifiedContent = n.Content
		if result.Updated {
			rt.notificationUpdateNo = n.UpdateNo
		}
		rt.notificationRunning = n.Running
	} else if result.MessageID != "" && !result.Updated && sameRound && rt.lastNotifiedMessageID == "" {
		rt.lastNotifiedMessageID = result.MessageID
		rt.lastNotifiedContent = n.Content
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
	switch {
	case rt.manager.notifier == nil:
		log.Printf("waiting notification running marker skipped session=%s reason=no_notifier", rt.session.ID)
		return WaitingNotification{}, false
	case rt.lastNotifiedMessageID == "":
		log.Printf("waiting notification running marker skipped session=%s reason=no_message_id", rt.session.ID)
		return WaitingNotification{}, false
	case rt.lastNotifiedContent == "":
		log.Printf("waiting notification running marker skipped session=%s message=%s reason=no_content", rt.session.ID, rt.lastNotifiedMessageID)
		return WaitingNotification{}, false
	case rt.notificationRunning:
		log.Printf("waiting notification running marker skipped session=%s message=%s reason=already_running", rt.session.ID, rt.lastNotifiedMessageID)
		return WaitingNotification{}, false
	}
	if _, ok := rt.manager.notifier.(WaitingRunningNotifier); !ok {
		log.Printf("waiting notification running marker skipped session=%s message=%s reason=notifier_unsupported", rt.session.ID, rt.lastNotifiedMessageID)
		return WaitingNotification{}, false
	}
	rt.notificationRunning = true
	return WaitingNotification{
		SessionID: rt.session.ID,
		Name:      rt.session.Name,
		Content:   rt.lastNotifiedContent,
		MessageID: rt.lastNotifiedMessageID,
		ChatID:    rt.session.LarkChatID,
		UpdateNo:  rt.notificationUpdateNo,
		Running:   true,
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
	rt.notificationRunning = false
	return WaitingNotification{
		SessionID: rt.session.ID,
		Name:      rt.session.Name,
		Content:   rt.lastNotifiedContent,
		MessageID: rt.lastNotifiedMessageID,
		ChatID:    rt.session.LarkChatID,
		UpdateNo:  rt.notificationUpdateNo,
		Running:   false,
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
	if currentMessageID != "" && currentMessageID != note.MessageID {
		rt.mu.Unlock()
		log.Printf("waiting notification running marker skipped session=%s message=%s current_message=%s running=%v reason=stale_message",
			note.SessionID, note.MessageID, currentMessageID, running)
		return
	}
	rt.mu.Unlock()
	if err := notifier.UpdateWaitingRunning(note, running); err != nil {
		log.Printf("waiting notification running marker failed session=%s message=%s running=%v: %v", note.SessionID, note.MessageID, running, err)
		if running {
			rt.mu.Lock()
			if rt.lastNotifiedMessageID == note.MessageID {
				rt.notificationRunning = false
			}
			rt.mu.Unlock()
		}
		return
	}
	log.Printf("waiting notification running marker updated session=%s message=%s running=%v", note.SessionID, note.MessageID, running)
}

func (rt *RuntimeSession) waitingNotificationCandidateLocked() (WaitingNotification, string, bool, string) {
	if rt.visibleSnapshotStaleForCurrentRoundLocked() {
		return WaitingNotification{}, "", false, "stale_visible_snapshot"
	}
	if NotifyContentNeedsMoreSnapshot(rt.visibleSnapshot, rt.snapshotAtRoundStart, rt.roundReply, rt.lastInputText) {
		return WaitingNotification{}, "", false, "needs_more_snapshot"
	}
	content := PickNotifyContent(rt.visibleSnapshot, rt.snapshotAtRoundStart, rt.roundReply, rt.lastInputText)
	content = strings.TrimSpace(content)
	if content == "" {
		return WaitingNotification{}, "", false, "empty_content"
	}
	contentHash := notifyContentHash(content)
	if contentHash == rt.lastNotifiedRoundHash {
		return WaitingNotification{}, "", false, "duplicate_hash"
	}
	return WaitingNotification{SessionID: rt.session.ID, Name: rt.session.Name, Content: content, ChatID: rt.session.LarkChatID}, contentHash, true, "ready"
}

func (rt *RuntimeSession) notifyContentNeedsMoreSnapshotLocked() bool {
	if rt.visibleSnapshotStaleForCurrentRoundLocked() {
		return true
	}
	return NotifyContentNeedsMoreSnapshot(rt.visibleSnapshot, rt.snapshotAtRoundStart, rt.roundReply, rt.lastInputText)
}

func (rt *RuntimeSession) visibleSnapshotStaleForCurrentRoundLocked() bool {
	if strings.TrimSpace(rt.lastInputText) == "" || strings.TrimSpace(rt.visibleSnapshot) == "" {
		return false
	}
	if currentRoundReplyText(rt.roundReply, rt.lastInputText) != "" {
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
