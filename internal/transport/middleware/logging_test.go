package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLog redirects zerolog output to a buffer for the duration of fn,
// then returns the parsed JSON log entry.
func captureLog(t *testing.T, fn func()) map[string]interface{} {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Logger
	log.Logger = zerolog.New(&buf).With().Timestamp().Logger()
	defer func() { log.Logger = orig }()

	fn()

	entry := make(map[string]interface{})
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	return entry
}

func TestLogging_SuccessfulRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	rec := httptest.NewRecorder()

	entry := captureLog(t, func() {
		Logging(handler).ServeHTTP(rec, req)
	})

	assert.Equal(t, "GET", entry["method"])
	assert.Equal(t, "/api/v1/test", entry["path"])
	assert.Equal(t, float64(200), entry["status"])
	assert.Equal(t, "info", entry["level"])
	assert.Contains(t, entry, "duration")
}

func TestLogging_4xxRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()

	entry := captureLog(t, func() {
		Logging(handler).ServeHTTP(rec, req)
	})

	assert.Equal(t, float64(404), entry["status"])
	assert.Equal(t, "warn", entry["level"])
}

func TestLogging_5xxRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	req := httptest.NewRequest(http.MethodPost, "/fail", nil)
	rec := httptest.NewRecorder()

	entry := captureLog(t, func() {
		Logging(handler).ServeHTTP(rec, req)
	})

	assert.Equal(t, float64(500), entry["status"])
	assert.Equal(t, "error", entry["level"])
}

func TestLogging_IncludesRequestID(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	reqID := "test-request-id-123"
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), RequestIDKey, reqID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	entry := captureLog(t, func() {
		Logging(handler).ServeHTTP(rec, req)
	})

	assert.Equal(t, reqID, entry["request_id"])
}

func TestLogging_IncludesUserID(t *testing.T) {
	userID := uuid.New()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	entry := captureLog(t, func() {
		Logging(handler).ServeHTTP(rec, req)
	})

	assert.Equal(t, userID.String(), entry["user_id"])
}

func TestLogging_IncludesWorkspaceID(t *testing.T) {
	wsID := uuid.New()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), WorkspaceIDKey, wsID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	entry := captureLog(t, func() {
		Logging(handler).ServeHTTP(rec, req)
	})

	assert.Equal(t, wsID.String(), entry["workspace_id"])
}

func TestLogging_OmitsAbsentContextFields(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	entry := captureLog(t, func() {
		Logging(handler).ServeHTTP(rec, req)
	})

	_, hasRequestID := entry["request_id"]
	_, hasUserID := entry["user_id"]
	_, hasWorkspaceID := entry["workspace_id"]
	assert.False(t, hasRequestID)
	assert.False(t, hasUserID)
	assert.False(t, hasWorkspaceID)
}
