package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLarkRegistrationPostFormReturnsOAuthPendingBodyOnHTTP400(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer server.Close()

	client := &larkAppRegistrationClient{httpClient: server.Client()}
	got, err := client.postForm(context.Background(), server.URL, url.Values{"action": {"poll"}})
	if err != nil {
		t.Fatal(err)
	}
	if stringField(got, "error") != "authorization_pending" {
		t.Fatalf("error = %q", stringField(got, "error"))
	}
}

func TestLarkRegistrationBeginFormRequestsMessageAndCardCapabilities(t *testing.T) {
	form := larkRegistrationBeginForm()
	scope := form.Get("scope")
	for _, want := range []string{
		"im:message",
		"im:message:send_as_bot",
		"im:message.p2p_msg:readonly",
		"im:message.group_msg:readonly",
		"im:chat",
		"cardkit:card:write",
	} {
		if !strings.Contains(scope, want) {
			t.Fatalf("scope %q should contain %q", scope, want)
		}
	}
	if form.Get("events") != "im.message.receive_v1" {
		t.Fatalf("events = %q", form.Get("events"))
	}
	if form.Get("callbacks") != "card.action.trigger" {
		t.Fatalf("callbacks = %q", form.Get("callbacks"))
	}
}
