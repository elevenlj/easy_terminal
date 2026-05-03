package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"easy_terminal/internal/session"
)

func TestSQLiteSessionLifecycle(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	sess := session.Session{ID: "sess-1", Name: "test", Status: session.StatusRunning, CreatedAt: now, UpdatedAt: now, Live: true}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendOutput(context.Background(), sess.ID, 0, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	out, err := st.Output(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello" {
		t.Fatalf("unexpected output: %q", out)
	}
	list, err := st.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != sess.ID {
		t.Fatalf("unexpected sessions: %#v", list)
	}
}
