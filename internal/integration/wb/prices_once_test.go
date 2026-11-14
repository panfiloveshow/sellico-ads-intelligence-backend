package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestWBPriceUploadDoesNotRetryRateLimitedMutation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":77},"error":false}`))
	}))
	defer srv.Close()
	c := NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop())
	_, _, err := c.UploadPriceTask(context.Background(), "test-price-token", []PriceUpdateItem{{NmID: 101, Price: 1000}})
	require.Error(t, err)
	require.True(t, IsPriceUploadOutcomeUnknown(err))
	require.Equal(t, int32(1), calls.Load())
}

func TestWBPriceUploadDoesNotFollowRedirect(t *testing.T) {
	var escaped atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped.Add(1)
		_, _ = w.Write([]byte(`{"data":{"id":77},"error":false}`))
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/other-price-writer", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	c := NewClient(&config.Config{WBAPIBaseURL: origin.URL, WBAPIRateLimit: 10000}, zerolog.Nop())
	_, _, err := c.UploadPriceTask(context.Background(), "test-price-token", []PriceUpdateItem{{NmID: 101, Price: 1000}})
	require.Error(t, err)
	require.True(t, IsPriceUploadOutcomeUnknown(err))
	require.Zero(t, escaped.Load(), "redirects must not replay a price payload or forward the seller token")
}

func TestWBPriceOversizedReceiptStaysUnknown(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"data":{"id":77},"error":false,"padding":"` + strings.Repeat("x", 2<<20) + `"}`))
	}))
	defer srv.Close()
	c := NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop())
	id, _, err := c.UploadPriceTask(context.Background(), "test-price-token", []PriceUpdateItem{{NmID: 101, Price: 1000}})
	require.Error(t, err)
	require.True(t, IsPriceUploadOutcomeUnknown(err))
	require.Zero(t, id)
	require.Equal(t, int32(1), calls.Load())
}
