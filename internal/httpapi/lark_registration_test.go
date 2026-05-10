package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
