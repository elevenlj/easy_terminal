package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxOutputBytes = 512 * 1024
	maxRoundBytes  = 64 * 1024
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
	mu                sync.RWMutex
	store             Store
	launcher          Launcher
	notifier          WaitingNotifier
	idCounter         atomic.Int64
	idleTimeout       time.Duration
	notifyIdleTimeout time.Duration
	sessions          map[string]*RuntimeSession
	onBrowserNeeded   func(string)
}

type ManagerOption func(*Manager)

func NewManager(store Store, launcher Launcher, opts ...ManagerOption) *Manager {
	m := &Manager{
		store:             store,
		launcher:          launcher,
		idleTimeout:       2 * time.Second,
		notifyIdleTimeout: 5 * time.Second,
		sessions:          make(map[string]*RuntimeSession),
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

func WithIdleTimeout(d time.Duration) ManagerOption {
	return func(m *Manager) {
		if d > 0 {
			m.idleTimeout = d
		}
	}
}

func WithNotifyIdleTimeout(d time.Duration) ManagerOption {
	return func(m *Manager) {
		if d > 0 {
			m.notifyIdleTimeout = d
		}
	}
}

func WithBrowserNeeded(fn func(string)) ManagerOption {
	return func(m *Manager) { m.onBrowserNeeded = fn }
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
		subscribers: make(map[chan []byte]struct{}),
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
	rt.resetIdleTimerLocked()
	go rt.streamOutput()
	go rt.waitForExit()
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
	mu              sync.Mutex
	manager         *Manager
	session         Session
	terminal        Terminal
	process         Waiter
	output          []byte
	roundReply      []byte
	visibleSnapshot string
	lastInputText   string
	awaitingReply   bool
	subscribers     map[chan []byte]struct{}
	nextSeq         int64
	stateVersion    int64
	notifyVersion   int64
	idleTimer       *time.Timer
	notifyIdleTimer *time.Timer
}

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

func (rt *RuntimeSession) Subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 64)
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
	rt.MarkInputActivity(data)
	_, err := rt.terminal.Write([]byte(data))
	return err
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
	rt.mu.Unlock()
}

func (rt *RuntimeSession) MarkInputActivity(data string) {
	rt.mu.Lock()
	if strings.TrimSpace(data) != "" {
		rt.lastInputText = strings.TrimSpace(strings.ReplaceAll(data, "\r", "\n"))
	}
	rt.awaitingReply = true
	rt.roundReply = nil
	rt.session.Status = StatusRunning
	rt.session.UpdatedAt = time.Now().UTC()
	rt.stateVersion++
	rt.notifyVersion++
	rt.stopNotifyTimerLocked()
	rt.resetIdleTimerLocked()
	s := rt.session
	rt.mu.Unlock()
	_ = rt.manager.persist(context.Background(), s)
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
	if renderable {
		rt.awaitingReply = false
		rt.session.Status = StatusRunning
		rt.resetIdleTimerLocked()
	}
	for ch := range rt.subscribers {
		select {
		case ch <- cp:
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
}

func (rt *RuntimeSession) Close() {
	rt.mu.Lock()
	rt.stopIdleTimerLocked()
	rt.stopNotifyTimerLocked()
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
	rt.stopIdleTimerLocked()
	rt.stopNotifyTimerLocked()
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

func (rt *RuntimeSession) resetIdleTimerLocked() {
	rt.stopIdleTimerLocked()
	version := rt.stateVersion
	rt.idleTimer = time.AfterFunc(rt.manager.idleTimeout, func() {
		rt.markWaitingIfActive(version)
	})
}

func (rt *RuntimeSession) markWaitingIfActive(version int64) {
	rt.mu.Lock()
	if rt.session.Status != StatusRunning || !rt.session.Live || rt.stateVersion != version || rt.awaitingReply {
		rt.mu.Unlock()
		return
	}
	rt.session.Status = StatusWaiting
	rt.session.UpdatedAt = time.Now().UTC()
	rt.notifyVersion++
	notifyVersion := rt.notifyVersion
	s := rt.session
	if rt.session.NotifyOnWaiting {
		rt.stopNotifyTimerLocked()
		rt.notifyIdleTimer = time.AfterFunc(rt.manager.notifyIdleTimeout, func() {
			rt.notifyIfStillWaiting(notifyVersion)
		})
	}
	rt.mu.Unlock()
	_ = rt.manager.persist(context.Background(), s)
}

func (rt *RuntimeSession) notifyIfStillWaiting(version int64) {
	time.Sleep(150 * time.Millisecond)
	rt.mu.Lock()
	if rt.session.Status != StatusWaiting || !rt.session.Live || rt.notifyVersion != version || rt.manager.notifier == nil || !rt.manager.notifier.Available() {
		rt.mu.Unlock()
		return
	}
	sessionID := rt.session.ID
	if len(rt.subscribers) == 0 && rt.manager.onBrowserNeeded != nil {
		go rt.manager.onBrowserNeeded(sessionID)
	}
	content := PickNotifyContent(rt.visibleSnapshot, rt.roundReply, rt.lastInputText)
	n := WaitingNotification{SessionID: sessionID, Name: rt.session.Name, Content: content}
	rt.mu.Unlock()
	_ = rt.manager.notifier.NotifyWaiting(n)
	defaultLarkMessageRegistry.rememberLatest(n.SessionID)
}

func (rt *RuntimeSession) stopIdleTimerLocked() {
	if rt.idleTimer != nil {
		rt.idleTimer.Stop()
		rt.idleTimer = nil
	}
}

func (rt *RuntimeSession) stopNotifyTimerLocked() {
	if rt.notifyIdleTimer != nil {
		rt.notifyIdleTimer.Stop()
		rt.notifyIdleTimer = nil
	}
}
