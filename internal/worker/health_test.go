package worker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestWorkerHealthServerExposesPrometheusMetrics(t *testing.T) {
	server := NewHealthServer(0, zerolog.Nop())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	server.server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", recorder.Code)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("expected Prometheus content type, got %q", contentType)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "go_gc_duration_seconds") {
		t.Fatalf("expected Go collector metrics, got %q", body)
	}
}
