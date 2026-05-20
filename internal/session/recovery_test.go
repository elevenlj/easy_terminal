package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoveryTracksCDChain(t *testing.T) {
	manager := NewManager(nil, nil)
	rt := &RuntimeSession{
		manager: manager,
		session: Session{ID: "sess-1", Name: "A", LastMode: SessionModeShell, LastCWD: "/tmp"},
	}

	rt.MarkStructuredInputActivity("cd project && cd ../other")

	if got := rt.Snapshot().LastCWD; got != "/tmp/other" {
		t.Fatalf("LastCWD = %q, want /tmp/other", got)
	}
}

func TestRecoveryRecordsCodexResumeCommandWithFlags(t *testing.T) {
	base := filepath.Join(t.TempDir(), "sessions")
	manager := NewManager(nil, nil, WithRecoveryBaseDir(base))
	rt := &RuntimeSession{
		manager: manager,
		session: Session{ID: "sess-1", Name: "A", RecoveryKey: "rk", LastMode: SessionModeShell, LastCWD: "/tmp"},
	}

	rt.MarkStructuredInputActivity("codex --dangerously-bypass-approvals-and-sandbox -m gpt-5.5")
	s := rt.Snapshot()

	if s.LastMode != SessionModeAgent || s.LastAgentKind != "codex" {
		t.Fatalf("agent state = mode %q kind %q", s.LastMode, s.LastAgentKind)
	}
	if !strings.Contains(s.LastAgentResumeCommand, "resume") || !strings.Contains(s.LastAgentResumeCommand, "--last") {
		t.Fatalf("resume command = %q, want codex resume --last", s.LastAgentResumeCommand)
	}
	if !strings.Contains(s.LastAgentResumeCommand, "--dangerously-bypass-approvals-and-sandbox") || !strings.Contains(s.LastAgentResumeCommand, "gpt-5.5") {
		t.Fatalf("resume command did not preserve flags: %q", s.LastAgentResumeCommand)
	}
	if !strings.HasSuffix(s.LastAgentHome, filepath.Join("rk", "codex_home")) {
		t.Fatalf("LastAgentHome = %q", s.LastAgentHome)
	}
}

func TestRecoveryBaseDirIsAbsolute(t *testing.T) {
	manager := NewManager(nil, nil, WithRecoveryBaseDir(filepath.Join("conf", "data", "sessions")))
	got := manager.sessionCodexHome(Session{RecoveryKey: "rk"})

	if !filepath.IsAbs(got) {
		t.Fatalf("codex home = %q, want absolute path", got)
	}
	if !strings.HasSuffix(got, filepath.Join("conf", "data", "sessions", "rk", "codex_home")) {
		t.Fatalf("codex home = %q, want recovery path suffix", got)
	}
}

func TestRecoveryDoesNotDuplicateCodexResume(t *testing.T) {
	manager := NewManager(nil, nil)
	rt := &RuntimeSession{
		manager: manager,
		session: Session{ID: "sess-1", Name: "A", LastMode: SessionModeShell, LastCWD: "/tmp"},
	}

	rt.MarkStructuredInputActivity("codex resume 019e440b-54a7-7200-8ca0-fe9e9e87d4be --dangerously-bypass-approvals-and-sandbox")
	got := rt.Snapshot().LastAgentResumeCommand

	if strings.Count(got, "resume") != 1 {
		t.Fatalf("resume command = %q, want single resume", got)
	}
	if strings.Contains(got, "--last") {
		t.Fatalf("explicit resume command should not add --last: %q", got)
	}
}

func TestRecoverRuntimeRestoresAgentCommand(t *testing.T) {
	launcher := &recordingLauncher{}
	st := newMemoryStore()
	manager := NewManager(st, launcher)
	now := time.Now().UTC()
	sess := Session{
		ID:                     "sess-1",
		Name:                   "A",
		Status:                 StatusWaiting,
		CreatedAt:              now,
		UpdatedAt:              now,
		Live:                   true,
		LastMode:               SessionModeAgent,
		LastCWD:                "/tmp/project",
		LastAgentKind:          "codex",
		LastAgentResumeCommand: "codex resume --last --dangerously-bypass-approvals-and-sandbox",
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatal(err)
	}

	rt, _, ok, err := manager.RecoverRuntime(context.Background(), sess.ID)
	if err != nil || !ok || rt == nil {
		t.Fatalf("RecoverRuntime ok=%v err=%v rt=%v", ok, err, rt)
	}

	writes := launcher.terminals[0].writes()
	if !strings.Contains(writes, "cd '/tmp/project'\r") || !strings.Contains(writes, "codex resume --last --dangerously-bypass-approvals-and-sandbox\r") {
		t.Fatalf("recovery writes = %q", writes)
	}
}

type memoryStore struct {
	sessions map[string]Session
}

func newMemoryStore() *memoryStore {
	return &memoryStore{sessions: make(map[string]Session)}
}

func (s *memoryStore) CreateSession(_ context.Context, sess Session) error {
	s.sessions[sess.ID] = sess
	return nil
}

func (s *memoryStore) UpdateSession(_ context.Context, sess Session) error {
	s.sessions[sess.ID] = sess
	return nil
}

func (s *memoryStore) ListSessions(context.Context) ([]Session, error) {
	out := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	return out, nil
}

func (s *memoryStore) GetSession(_ context.Context, id string) (Session, bool, error) {
	sess, ok := s.sessions[id]
	return sess, ok, nil
}

func (s *memoryStore) DeleteSession(_ context.Context, id string) error {
	delete(s.sessions, id)
	return nil
}

func (s *memoryStore) AppendOutput(context.Context, string, int64, []byte) error { return nil }
func (s *memoryStore) Output(context.Context, string) ([]byte, error)            { return nil, nil }
func (s *memoryStore) DeleteAllSessions(context.Context) error                   { return nil }
func (s *memoryStore) ListQuickCommands(context.Context) ([]QuickCommand, error) { return nil, nil }
func (s *memoryStore) CreateQuickCommand(context.Context, QuickCommand) error    { return nil }
func (s *memoryStore) DeleteQuickCommand(context.Context, string) error          { return nil }
