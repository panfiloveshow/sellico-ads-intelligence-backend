package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecovery_NoPanic(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`OK`))
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	Recovery(handler).ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
}

func TestRecovery_PanicReturns500(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	Recovery(handler).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp envelope.Response
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Errors)
	assert.Equal(t, "INTERNAL_ERROR", resp.Errors[0].Code)
	assert.Equal(t, "internal server error", resp.Errors[0].Message)
}

func TestRecovery_PanicDoesNotLeakDetails(t *testing.T) {
	secretMsg := "secret database password: hunter2"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(secretMsg)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	Recovery(handler).ServeHTTP(rec, req)

	body := rec.Body.String()
	assert.NotContains(t, body, secretMsg)
	assert.NotContains(t, body, "hunter2")
}

func TestRecovery_PanicWithError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(assert.AnError)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	Recovery(handler).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp envelope.Response
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Errors)
	assert.Equal(t, "INTERNAL_ERROR", resp.Errors[0].Code)
}

func TestRecovery_IncludesRequestIDInContext(t *testing.T) {
	// Verify recovery works when request_id is in context (no crash).
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), RequestIDKey, "req-123")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	Recovery(handler).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp envelope.Response
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "INTERNAL_ERROR", resp.Errors[0].Code)
}
