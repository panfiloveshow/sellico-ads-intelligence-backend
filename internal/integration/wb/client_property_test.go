package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 19: WB_Client — retry и rate-limiting
// Проверяет: Требования 16.1, 16.5, 16.6

// TestProperty_WBClient_5xxAlwaysRetries verifies that any HTTP 5xx response
// triggers retry logic and the server receives exactly maxRetries attempts.
func TestProperty_WBClient_5xxAlwaysRetries(t *testing.T) {
	withFastWBRetryTiming(t)
	rapid.Check(t, func(t *rapid.T) {
		statusCode := rapid.IntRange(500, 599).Draw(t, "status_code")

		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(statusCode)
		}))
		defer server.Close()

		client := newTestClient(server.URL)
		_, _, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "all 3 attempts exhausted")
		assert.Equal(t, int32(maxRetries), attempts.Load(),
			"5xx status %d must trigger exactly %d attempts", statusCode, maxRetries)
	})
}

// TestProperty_WBClient_4xxNeverRetries verifies that any non-429 4xx response
// is returned immediately without retries.
func TestProperty_WBClient_4xxNeverRetries(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// 4xx codes excluding 429 (rate limit)
		statusCode := rapid.OneOf(
			rapid.IntRange(400, 428),
			rapid.IntRange(430, 499),
		).Draw(t, "status_code")

		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"error":"client error"}`))
		}))
		defer server.Close()

		client := newTestClient(server.URL)
		resp, _, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

		assert.Error(t, err)
		assert.Equal(t, statusCode, resp.StatusCode)
		assert.Equal(t, int32(1), attempts.Load(),
			"4xx status %d must NOT trigger retries", statusCode)
	})
}

// TestProperty_WBClient_429TriggersRetry verifies that HTTP 429 responses
// trigger retry behavior and the request is eventually retried.
func TestProperty_WBClient_429TriggersRetry(t *testing.T) {
	withFastWBRetryTiming(t)
	rapid.Check(t, func(t *rapid.T) {
		retryAfterSec := rapid.IntRange(1, 3).Draw(t, "retry_after_seconds")

		retryAfterStr := strconv.Itoa(retryAfterSec)

		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			if n == 1 {
				w.Header().Set("Retry-After", retryAfterStr)
				w.WriteHeader(http.StatusTooManyRequests)
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
		assert.Contains(t, string(body), `"ok":true`)
		assert.Equal(t, int32(2), attempts.Load(),
			"429 with Retry-After=%d must trigger exactly one retry", retryAfterSec)
	})
}

// TestProperty_WBClient_5xxRecovery verifies that if the server returns 5xx
// for a random number of attempts (< maxRetries) then succeeds, the client
// returns success with the correct total attempt count.
func TestProperty_WBClient_5xxRecovery(t *testing.T) {
	withFastWBRetryTiming(t)
	rapid.Check(t, func(t *rapid.T) {
		failCount := rapid.IntRange(1, maxRetries-1).Draw(t, "fail_count")
		statusCode := rapid.IntRange(500, 599).Draw(t, "status_code")

		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			if int(n) <= failCount {
				w.WriteHeader(statusCode)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"recovered":true}`))
		}))
		defer server.Close()

		client := newTestClient(server.URL)
		resp, body, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(body), `"recovered":true`)
		assert.Equal(t, int32(failCount+1), attempts.Load(),
			"expected %d failures + 1 success = %d total attempts", failCount, failCount+1)
	})
}

// TestProperty_WBClient_RateLimiterIsolation verifies that different tokens
// always get distinct rate limiters, and the same token always returns the
// same limiter instance.
func TestProperty_WBClient_RateLimiterIsolation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		tokenA := rapid.StringMatching(`[a-zA-Z0-9]{8,32}`).Draw(t, "token_a")
		tokenB := rapid.StringMatching(`[a-zA-Z0-9]{8,32}`).Draw(t, "token_b")

		client := newTestClient("http://localhost")

		limA1 := client.limiterForToken(tokenA)
		limA2 := client.limiterForToken(tokenA)
		limB := client.limiterForToken(tokenB)

		// Same token → same limiter instance
		assert.Same(t, limA1, limA2,
			"same token %q must return identical limiter", tokenA)

		// Different tokens → different limiter instances (unless tokens collide)
		if tokenA != tokenB {
			assert.NotSame(t, limA1, limB,
				"different tokens %q and %q must have separate limiters", tokenA, tokenB)
		}
	})
}

// TestProperty_WBClient_SuccessNeverRetries verifies that any 2xx response
// is returned immediately without additional attempts.
func TestProperty_WBClient_SuccessNeverRetries(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		statusCode := rapid.IntRange(200, 299).Draw(t, "status_code")

		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"ok":true}`))
		}))
		defer server.Close()

		client := newTestClient(server.URL)
		resp, _, err := client.doRequest(context.Background(), http.MethodGet, "/test", "token", nil)

		require.NoError(t, err)
		assert.Equal(t, statusCode, resp.StatusCode)
		assert.Equal(t, int32(1), attempts.Load(),
			"2xx status %d must not trigger retries", statusCode)
	})
}

// TestProperty_WBClient_ContextCancelStopsRetries verifies that cancelling
// the context during 5xx retries stops the retry loop promptly.
func TestProperty_WBClient_ContextCancelStopsRetries(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		statusCode := rapid.IntRange(500, 599).Draw(t, "status_code")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(statusCode)
		}))
		defer server.Close()

		client := newTestClient(server.URL)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		_, _, err := client.doRequest(ctx, http.MethodGet, "/test", "token", nil)
		assert.Error(t, err, "cancelled context must produce an error")
	})
}
