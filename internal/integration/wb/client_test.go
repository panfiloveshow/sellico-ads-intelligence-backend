package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withFastWBRetryTiming(t *testing.T) {
	t.Helper()
	origBaseBackoff := baseBackoff
	origRetryAfterUnit := retryAfterUnit
	origDefaultRetryAfter := defaultRetryAfter
	baseBackoff = time.Millisecond
	retryAfterUnit = time.Millisecond
	defaultRetryAfter = 10 * time.Millisecond
	t.Cleanup(func() {
		baseBackoff = origBaseBackoff
		retryAfterUnit = origRetryAfterUnit
		defaultRetryAfter = origDefaultRetryAfter
	})
}

func newTestClient(baseURL string) *Client {
	cfg := &config.Config{
		WBAPIBaseURL:   baseURL,
		WBAPIRateLimit: 100, // high limit for tests
	}
	logger := zerolog.Nop()
	client := NewClient(cfg, logger)
	client.contentURL = baseURL
	client.commonURL = baseURL
	client.feedbacksURL = baseURL
	client.fullStatsInterBatchDelay = 0
	client.normQueryInterBatchDelay = 0
	return client
}

func TestDoRequest_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, body, err := client.doRequest(context.Background(), http.MethodGet, "/test", "test-token", nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"ok":true}`, string(body))
}

func TestDoRequest_Retry5xx(t *testing.T) {
	withFastWBRetryTiming(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, body, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	assert.Equal(t, int32(3), attempts.Load())
}

func TestDoRequest_5xxExhausted(t *testing.T) {
	withFastWBRetryTiming(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, _, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "all 3 attempts exhausted")
	assert.Equal(t, int32(3), attempts.Load())
}

func TestDoRequest_WriteNotRetriedOn5xx(t *testing.T) {
	withFastWBRetryTiming(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	// A POST (non-idempotent write) must not be auto-retried on 5xx: WB may have
	// applied it before failing, so a retry could duplicate the effect.
	_, _, err := client.doRequest(context.Background(), http.MethodPost, "/test", "token", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server error (500)")
	assert.Equal(t, int32(1), attempts.Load())
}

func TestDoRequest_WriteStillRetriedOn429(t *testing.T) {
	withFastWBRetryTiming(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	// 429 means the write was rejected before processing, so retrying it is safe.
	resp, _, err := client.doRequest(context.Background(), http.MethodPost, "/test", "token", nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), attempts.Load())
}

func TestDoRequest_ClientErrorsDoNotOpenBreaker(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	// Far more than the breaker's 5-failure threshold: benign 4xx must never open it,
	// so every call must still reach the server (no circuit-breaker lockout).
	const calls = 8
	for i := 0; i < calls; i++ {
		_, _, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "client error (400)")
	}
	assert.Equal(t, int32(calls), attempts.Load())
}

func TestDoRequest_429WithRetryAfter(t *testing.T) {
	withFastWBRetryTiming(t)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, _, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), attempts.Load())
}

func TestDoRequest_4xxNonRetryable(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, body, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrWBAPIError))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(body), "bad request")
	// Should NOT retry on 4xx (except 429)
	assert.Equal(t, int32(1), attempts.Load())
}

func TestDoRequest_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, _, err := client.doRequest(ctx, http.MethodGet, "/test", "token", nil)
	require.Error(t, err)
}

func TestDoRequest_RateLimiterPerToken(t *testing.T) {
	client := newTestClient("http://localhost")

	lim1 := client.limiterForToken("token-a")
	lim2 := client.limiterForToken("token-b")
	lim1Again := client.limiterForToken("token-a")

	assert.NotSame(t, lim1, lim2, "different tokens should have different limiters")
	assert.Same(t, lim1, lim1Again, "same token should return same limiter")
}

func TestPricesRateLimiterIsSharedPerTokenAndUsesWBBucket(t *testing.T) {
	client := newTestClient("http://localhost")

	lim1 := client.priceLimiterForToken("token-a")
	lim2 := client.priceLimiterForToken("token-b")
	lim1Again := client.priceLimiterForToken("token-a")

	assert.NotSame(t, lim1, lim2)
	assert.Same(t, lim1, lim1Again)
	assert.InDelta(t, 10.0/6.0, float64(lim1.Limit()), 0.001)
	assert.Equal(t, 1, lim1.Burst())
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected time.Duration
	}{
		{"empty header", "", defaultRetryAfter},
		{"valid seconds", "5", 5 * time.Second},
		{"invalid string", "abc", defaultRetryAfter},
		{"zero", "0", defaultRetryAfter},
		{"negative", "-1", defaultRetryAfter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseRetryAfter(tt.header)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBackoffDuration(t *testing.T) {
	assert.Equal(t, 1*time.Second, backoffDuration(1)) // 3^0 * 1s = 1s
	assert.Equal(t, 3*time.Second, backoffDuration(2)) // 3^1 * 1s = 3s
	assert.Equal(t, 9*time.Second, backoffDuration(3)) // 3^2 * 1s = 9s
}

func TestNewClient_DefaultValues(t *testing.T) {
	cfg := &config.Config{
		WBAPIBaseURL:   "https://custom-api.example.com",
		WBAPIRateLimit: 5,
	}
	logger := zerolog.Nop()
	client := NewClient(cfg, logger)

	assert.Equal(t, "https://custom-api.example.com", client.baseURL)
	assert.Equal(t, 5, client.rateLimit)
	assert.NotNil(t, client.limiters)
	assert.NotNil(t, client.priceLimiters)
}
