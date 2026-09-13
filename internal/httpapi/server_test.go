package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzIsPublicAndReturnsRequestID(t *testing.T) {
	server := New(nil, []string{"http://localhost:3000"}, nil, "")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("X-Request-Id"); got == "" {
		t.Fatal("X-Request-Id header is missing")
	}
	if got := recorder.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("response body = %q", got)
	}
}
