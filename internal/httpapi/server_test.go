package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
