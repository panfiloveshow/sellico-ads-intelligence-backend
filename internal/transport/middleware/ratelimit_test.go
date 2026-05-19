package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestRateLimit_UsesAuthenticatedUserID(t *testing.T) {
	userID := uuid.New()
	calls := 0
	handler := RateLimit(RateLimitConfig{RequestsPerSecond: 1, Burst: 1})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodGet, "/test", nil)
	first.RemoteAddr = "127.0.0.1:10001"
	first = first.WithContext(context.WithValue(first.Context(), UserIDKey, userID))
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)

	second := httptest.NewRequest(http.MethodGet, "/test", nil)
	second.RemoteAddr = "127.0.0.1:10002"
	second = second.WithContext(context.WithValue(second.Context(), UserIDKey, userID))
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)

	assert.Equal(t, http.StatusOK, firstRec.Code)
	assert.Equal(t, http.StatusTooManyRequests, secondRec.Code)
	assert.Equal(t, 1, calls)
}

func TestRateLimit_NormalizesRemoteAddrPort(t *testing.T) {
	calls := 0
	handler := RateLimit(RateLimitConfig{RequestsPerSecond: 1, Burst: 1})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodGet, "/test", nil)
	first.RemoteAddr = "127.0.0.1:10001"
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)

	second := httptest.NewRequest(http.MethodGet, "/test", nil)
	second.RemoteAddr = "127.0.0.1:10002"
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)

	assert.Equal(t, http.StatusOK, firstRec.Code)
	assert.Equal(t, http.StatusTooManyRequests, secondRec.Code)
	assert.Equal(t, 1, calls)
}
