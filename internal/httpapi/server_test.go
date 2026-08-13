package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easy_terminal/internal/session"
)

func TestEmbeddedStaticAssetsDisableStaleBrowserCaching(t *testing.T) {
	server := NewServer(nil, "")

	for _, path := range []string{"/", "/app.js", "/app.css"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)

			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
			}
			if got, want := recorder.Header().Get("Cache-Control"), "no-store, max-age=0, must-revalidate"; got != want {
				t.Fatalf("GET %s Cache-Control = %q, want %q", path, got, want)
			}
			if got, want := recorder.Header().Get("Pragma"), "no-cache"; got != want {
				t.Fatalf("GET %s Pragma = %q, want %q", path, got, want)
			}
			if got, want := recorder.Header().Get("Expires"), "0"; got != want {
				t.Fatalf("GET %s Expires = %q, want %q", path, got, want)
			}
		})
	}
}

func TestAgentStopHookAcceptsLastAssistantMessage(t *testing.T) {
	terminal := newWSBridgeTestTerminal()
	manager := session.NewManager(nil, wsBridgeTestLauncher{terminal: terminal})
	sess, err := manager.CreateSession(context.Background(), "hook-content")
	if err != nil {
		t.Fatal(err)
	}
	rt, ok := manager.GetRuntime(sess.ID)
	if !ok {
		t.Fatal("runtime session missing")
	}
	defer rt.Close()
	rt.RecordShellCommandForRecovery("codex --dangerously-bypass-approvals-and-sandbox")

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sess.ID+"/hook/turn-ended", strings.NewReader(`{
		"session_id":"019f5153-6e7f-7742-9f61-3ffe1530d61c",
		"last_assistant_message":"本轮 Hook 最终回复"
	}`))
	req.Header.Set("X-Easy-Terminal-Hook-Token", sess.RecoveryKey)
	rec := httptest.NewRecorder()
	NewServer(manager, "").Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("hook status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rt.CachedCurrentRoundContent(); got != "本轮 Hook 最终回复" {
		t.Fatalf("hook content = %q", got)
	}
}
