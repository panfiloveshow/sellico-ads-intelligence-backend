package ozon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func newTestPerfClient(baseURL string) *PerfClient {
	return &PerfClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     zerolog.Nop(),
		// Effectively unlimited so tests never sleep on the limiter.
		limiters: newLimiterPool(rate.Limit(10000), 10000),
		tokens:   make(map[string]cachedToken),
	}
}

var testCreds = Credentials{
	ClientID:         "seller-1",
	APIKey:           "key",
	PerfClientID:     "perf-client-1",
	PerfClientSecret: "secret",
}

func writeToken(w http.ResponseWriter, token string, expiresIn int) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d}`, token, expiresIn)
}

// --- token cache ---

func TestPerfToken_CachedAcrossCalls(t *testing.T) {
	var tokenCalls, dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/client/token":
			atomic.AddInt32(&tokenCalls, 1)
			writeToken(w, "tok-1", 1800)
		default:
			atomic.AddInt32(&dataCalls, 1)
			assert.Equal(t, "Bearer tok-1", r.Header.Get("Authorization"))
			fmt.Fprint(w, `{"list":[]}`)
		}
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	for i := 0; i < 3; i++ {
		_, err := c.ListCampaigns(context.Background(), testCreds)
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), atomic.LoadInt32(&tokenCalls), "token must be requested once and cached")
	assert.Equal(t, int32(3), atomic.LoadInt32(&dataCalls))
}

func TestPerfToken_SingleflightUnderConcurrency(t *testing.T) {
	var tokenCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/client/token", r.URL.Path)
		atomic.AddInt32(&tokenCalls, 1)
		time.Sleep(50 * time.Millisecond) // widen the race window
		writeToken(w, "tok-shared", 1800)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	const goroutines = 16
	var wg sync.WaitGroup
	tokens := make([]string, goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tokens[idx], errs[idx] = c.token(context.Background(), testCreds)
		}(i)
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		require.NoError(t, errs[i])
		assert.Equal(t, "tok-shared", tokens[i])
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&tokenCalls), "concurrent callers must share one token request")
}

func TestPerfToken_ExpiredEntryRefreshes(t *testing.T) {
	var tokenCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		writeToken(w, "tok-fresh", 1800)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	c.tokens[testCreds.PerfClientID] = cachedToken{token: "tok-stale", expiresAt: time.Now().Add(-time.Minute)}

	token, err := c.token(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, "tok-fresh", token)
	assert.Equal(t, int32(1), atomic.LoadInt32(&tokenCalls))
}

func TestPerfToken_ShortTTLStillCachesPositive(t *testing.T) {
	// expires_in below the early-refresh window must not produce an
	// already-expired cache entry (regression: ttl <= 300s bumps to 360s).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "tok-short", 60)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	_, err := c.token(context.Background(), testCreds)
	require.NoError(t, err)

	cached, ok := c.tokens[testCreds.PerfClientID]
	require.True(t, ok)
	assert.True(t, cached.expiresAt.After(time.Now()), "short TTL must still cache a token valid for some time")
}

func TestPerfToken_UpstreamErrorSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"invalid_client"}`)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	_, err := c.token(context.Background(), testCreds)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
}

func TestPerfToken_MissingAccessTokenErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"expires_in":1800}`)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	_, err := c.token(context.Background(), testCreds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing access_token")
}

// --- doGet retry semantics ---

func TestPerfDoGet_401InvalidatesTokenAndRetries(t *testing.T) {
	var tokenCalls, dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			n := atomic.AddInt32(&tokenCalls, 1)
			writeToken(w, fmt.Sprintf("tok-%d", n), 1800)
			return
		}
		if atomic.AddInt32(&dataCalls, 1) == 1 {
			assert.Equal(t, "Bearer tok-1", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		assert.Equal(t, "Bearer tok-2", r.Header.Get("Authorization"), "retry must carry the re-issued token")
		fmt.Fprint(w, `{"list":[]}`)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	_, err := c.ListCampaigns(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&tokenCalls), "401 must drop the cached token and re-request")
	assert.Equal(t, int32(2), atomic.LoadInt32(&dataCalls))
}

func TestPerfDoGet_429HonorsRetryAfter(t *testing.T) {
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
		fmt.Fprint(w, `{"list":[]}`)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	start := time.Now()
	_, err := c.ListCampaigns(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&dataCalls), "429 must be retried")
	assert.GreaterOrEqual(t, time.Since(start), time.Second, "retry must wait out Retry-After")
}

func TestPerfDoGet_429ExhaustsRetries(t *testing.T) {
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
	_, err := c.ListCampaigns(context.Background(), testCreds)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	assert.Equal(t, time.Second, apiErr.RetryAfter)
	assert.Equal(t, int32(maxRetries), atomic.LoadInt32(&dataCalls))
}

func TestPerfDoGet_ClientErrorDoesNotRetry(t *testing.T) {
	var dataCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		atomic.AddInt32(&dataCalls, 1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"bad campaign id"}`)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	_, err := c.ListCampaigns(context.Background(), testCreds)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&dataCalls), "4xx (except 401/429) must not be retried")
}

// --- competitive bids chunking ---

func TestGetCompetitiveBids_ChunksOver200SKUs(t *testing.T) {
	const total = 450 // 200 + 200 + 50
	var chunkSizes []int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/campaign/77/products/bids/competitive", r.URL.Path)
		skus := r.URL.Query()["skus"]
		mu.Lock()
		chunkSizes = append(chunkSizes, len(skus))
		mu.Unlock()

		type bid struct {
			SKU string `json:"sku"`
			Bid string `json:"bid"`
		}
		bids := make([]bid, 0, len(skus))
		for _, sku := range skus {
			// 25.5 rub in micro-rubles.
			bids = append(bids, bid{SKU: sku, Bid: "25500000"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"bids": bids})
	}))
	defer srv.Close()

	skus := make([]int64, total)
	for i := range skus {
		skus[i] = int64(i + 1)
	}

	c := newTestPerfClient(srv.URL)
	out, err := c.GetCompetitiveBids(context.Background(), testCreds, 77, skus)
	require.NoError(t, err)

	assert.Equal(t, []int{200, 200, 50}, chunkSizes, "requests must be chunked at 200 SKUs")
	require.Len(t, out, total, "all chunks must be merged")
	assert.Equal(t, int64(1), out[0].SKU)
	assert.InDelta(t, 25.5, out[0].BidRub, 1e-9, "micro-rub bid must convert to rubles")
	assert.Equal(t, int64(total), out[total-1].SKU)
}

func TestGetCompetitiveBids_EmptySKUsMakesNoRequests(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeToken(w, "tok-1", 1800)
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	out, err := c.GetCompetitiveBids(context.Background(), testCreds, 77, nil)
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Zero(t, atomic.LoadInt32(&calls))
}

// --- campaign list paging ---

func TestListCampaigns_PagesUntilShortPage(t *testing.T) {
	pageSizes := map[string]int{"1": perfPageSize, "2": 3}
	var pagesSeen []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		page := r.URL.Query().Get("page")
		mu.Lock()
		pagesSeen = append(pagesSeen, page)
		mu.Unlock()
		n := pageSizes[page]
		list := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			list = append(list, map[string]any{"id": fmt.Sprintf("%s%03d", page, i), "title": "c", "state": "CAMPAIGN_STATE_RUNNING"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"list": list})
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	campaigns, err := c.ListCampaigns(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, pagesSeen)
	assert.Len(t, campaigns, perfPageSize+3)
}

// --- daily stats parsing ---

func TestParseDailyStatsJSON(t *testing.T) {
	t.Run("empty rows", func(t *testing.T) {
		rows, err := parseDailyStatsJSON([]byte(`{"rows":[]}`))
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("campaignId fallback and spend fallback", func(t *testing.T) {
		body := `{"rows":[
			{"campaignId":"42","date":"2026-08-01","views":"100","clicks":5,"spend":"12.5","orders":1,"ordersMoney":"999.99"},
			{"id":43,"date":"2026-08-01","views":10,"clicks":0,"moneySpent":7,"orders":0,"ordersMoney":0}
		]}`
		rows, err := parseDailyStatsJSON([]byte(body))
		require.NoError(t, err)
		require.Len(t, rows, 2)
		assert.Equal(t, int64(42), rows[0].CampaignID, "campaignId is used when id is absent")
		assert.InDelta(t, 12.5, rows[0].SpendRub, 1e-9, "spend is used when moneySpent is absent")
		assert.InDelta(t, 999.99, rows[0].RevenueRub, 1e-9)
		assert.Equal(t, int64(43), rows[1].CampaignID)
		assert.InDelta(t, 7.0, rows[1].SpendRub, 1e-9)
	})

	t.Run("bad date rows are skipped", func(t *testing.T) {
		body := `{"rows":[
			{"id":1,"date":"08/01/2026","views":1},
			{"id":1,"date":"2026-08-01","views":2}
		]}`
		rows, err := parseDailyStatsJSON([]byte(body))
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, int64(2), rows[0].Views)
	})

	t.Run("duplicate dates are preserved for upsert dedup", func(t *testing.T) {
		body := `{"rows":[
			{"id":1,"date":"2026-08-01","views":1},
			{"id":1,"date":"2026-08-01","views":2}
		]}`
		rows, err := parseDailyStatsJSON([]byte(body))
		require.NoError(t, err)
		assert.Len(t, rows, 2, "parser must not dedup; the DB upsert keyed by (campaign,date) does")
	})
}

func TestParseDailyStatsCSV(t *testing.T) {
	t.Run("empty body", func(t *testing.T) {
		rows, err := parseDailyStatsCSV(nil)
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("header only", func(t *testing.T) {
		rows, err := parseDailyStatsCSV([]byte("id;date;views;clicks"))
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("semicolon with russian headers and comma decimals", func(t *testing.T) {
		body := "ID;День;Показы;Клики;Расход;Заказы;Выручка\n" +
			"42;2026-08-01;1 200;45;123,50;3;9 999,90\n" +
			"42;bad-date;1;1;1;1;1\n"
		rows, err := parseDailyStatsCSV([]byte(body))
		require.NoError(t, err)
		require.Len(t, rows, 1, "bad-date row must be skipped")
		row := rows[0]
		assert.Equal(t, int64(42), row.CampaignID)
		assert.Equal(t, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), row.Date)
		assert.Equal(t, int64(1200), row.Views, "space thousands separator")
		assert.Equal(t, int64(45), row.Clicks)
		assert.InDelta(t, 123.5, row.SpendRub, 1e-9, "comma decimal separator")
		assert.Equal(t, int64(3), row.Orders)
		assert.InDelta(t, 9999.9, row.RevenueRub, 1e-9)
	})

	t.Run("english headers with semicolons", func(t *testing.T) {
		body := "id;date;views;clicks;moneySpent;orders;ordersMoney\n" +
			"7;2026-08-02;10;2;5.5;1;100\n"
		rows, err := parseDailyStatsCSV([]byte(body))
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, int64(7), rows[0].CampaignID)
		assert.InDelta(t, 5.5, rows[0].SpendRub, 1e-9)
	})
}

// --- retry helpers ---

func TestParseRetryAfter(t *testing.T) {
	assert.Equal(t, defaultRetryAfter, parseRetryAfter(""))
	assert.Equal(t, 5*time.Second, parseRetryAfter("5"))
	assert.Equal(t, defaultRetryAfter, parseRetryAfter("0"))
	assert.Equal(t, defaultRetryAfter, parseRetryAfter("garbage"))

	// HTTP-date in the future yields a positive delay ≤ that horizon.
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(future)
	assert.Greater(t, d, 60*time.Second)
	assert.LessOrEqual(t, d, 90*time.Second)

	// HTTP-date in the past falls back to the default.
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	assert.Equal(t, defaultRetryAfter, parseRetryAfter(past))
}

func TestBackoffDuration(t *testing.T) {
	assert.Equal(t, 1*time.Second, backoffDuration(1))
	assert.Equal(t, 3*time.Second, backoffDuration(2))
	assert.Equal(t, 9*time.Second, backoffDuration(3))
}
