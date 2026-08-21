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
	if list[0].LarkMentionModeEnabled {
		t.Fatalf("mention mode should default to disabled: %#v", list[0])
	}
	list[0].LarkMentionModeEnabled = true
	list[0].LarkAlertModeEnabled = true
	list[0].LarkAlertCursorMillis = 1234
	list[0].LarkThreadID = "omt_alert"
	list[0].LarkTopicRootID = "om_root"
	list[0].LarkAlertSourceMessageID = "om_alert"
	list[0].LarkAlertSourceContent = "告警内容"
	list[0].LarkAlertParentSessionID = "sess-parent"
	if err := st.UpdateSession(context.Background(), list[0]); err != nil {
		t.Fatal(err)
	}
	updated, ok, err := st.GetSession(context.Background(), sess.ID)
	if err != nil || !ok {
		t.Fatalf("GetSession ok=%v err=%v", ok, err)
	}
	if !updated.LarkMentionModeEnabled {
		t.Fatalf("mention mode should persist: %#v", updated)
	}
	if !updated.LarkAlertModeEnabled || updated.LarkAlertCursorMillis != 1234 || updated.LarkThreadID != "omt_alert" || updated.LarkTopicRootID != "om_root" || updated.LarkAlertSourceMessageID != "om_alert" || updated.LarkAlertSourceContent != "告警内容" || updated.LarkAlertParentSessionID != "sess-parent" {
		t.Fatalf("alert session fields should persist: %#v", updated)
	}
}
