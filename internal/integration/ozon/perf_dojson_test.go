package ozon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
)

// --- constructors ---

func TestConstructors(t *testing.T) {
	cfg := &config.Config{
		OzonSellerAPIBaseURL: "https://api-seller.ozon.ru",
		OzonPerfAPIBaseURL:   "https://api-performance.ozon.ru",
	}
	sc := NewSellerClient(cfg, zerolog.Nop())
	require.NotNil(t, sc)
	assert.Equal(t, "https://api-seller.ozon.ru", sc.baseURL)
	assert.NotNil(t, sc.limiters)
	assert.NotNil(t, sc.analyticsLimiters)

	pc := NewPerfClient(cfg, zerolog.Nop())
	require.NotNil(t, pc)
	assert.Equal(t, "https://api-performance.ozon.ru", pc.baseURL)
	assert.NotNil(t, pc.tokens)
}

// --- doJSON retry semantics (exercised via ActivateCampaign, a POST) ---

func TestDoJSON_401InvalidatesTokenAndRetries(t *testing.T) {
	var tokenCalls, dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			n := atomic.AddInt32(&tokenCalls, 1)
			writeToken(w, "tok-"+itoa(n), 1800)
			return
		}
		if atomic.AddInt32(&dataCalls, 1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	require.NoError(t, c.ActivateCampaign(context.Background(), testCreds, 1))
	assert.Equal(t, int32(2), atomic.LoadInt32(&tokenCalls), "401 drops the cached token")
	assert.Equal(t, int32(2), atomic.LoadInt32(&dataCalls))
}

func TestDoJSON_429HonorsRetryAfterThenSucceeds(t *testing.T) {
	var dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		if atomic.AddInt32(&dataCalls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	start := time.Now()
	require.NoError(t, c.ActivateCampaign(context.Background(), testCreds, 1))
	assert.GreaterOrEqual(t, time.Since(start), time.Second)
	assert.Equal(t, int32(2), atomic.LoadInt32(&dataCalls))
}

func TestDoJSON_5xxRetriesThenSucceeds(t *testing.T) {
	var dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		if atomic.AddInt32(&dataCalls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	require.NoError(t, c.DeactivateCampaign(context.Background(), testCreds, 1))
	assert.Equal(t, int32(2), atomic.LoadInt32(&dataCalls))
}

func TestDoJSON_429ExhaustsRetries(t *testing.T) {
	var dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		atomic.AddInt32(&dataCalls, 1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	err := c.ActivateCampaign(context.Background(), testCreds, 1)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.Equal(t, int32(maxRetries), atomic.LoadInt32(&dataCalls))
}

func TestDoJSON_ClientErrorNoRetry(t *testing.T) {
	var dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		atomic.AddInt32(&dataCalls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	err := c.ActivateCampaign(context.Background(), testCreds, 1)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&dataCalls))
}

// --- doGet retry branches not covered elsewhere (via ListCampaigns, a GET) ---

func TestDoGet_5xxRetriesThenSucceeds(t *testing.T) {
	var dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		if atomic.AddInt32(&dataCalls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"list":[]}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	out, err := c.ListCampaigns(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Equal(t, int32(2), atomic.LoadInt32(&dataCalls))
}

func TestDoGet_NetworkErrorExhausts(t *testing.T) {
	// Port 1 refuses connections → transport error on every attempt, backoff
	// between them, then the wrapped error is returned.
	c := newTestPerfClient("http://127.0.0.1:1")
	c.tokens[testCreds.PerfClientID] = cachedToken{token: "tok", expiresAt: time.Now().Add(time.Hour)}
	_, err := c.ListCampaigns(context.Background(), testCreds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "attempts exhausted")
}

func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
