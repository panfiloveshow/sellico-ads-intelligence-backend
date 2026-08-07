package ozon

import (
	"context"
	"encoding/json"
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

// newTestSellerClient builds a SellerClient pointed at a test server with
// effectively-unlimited limiters so tests never sleep on rate control.
func newTestSellerClient(baseURL string) *SellerClient {
	return &SellerClient{
		baseURL:           baseURL,
		httpClient:        &http.Client{Timeout: 10 * time.Second},
		logger:            zerolog.Nop(),
		limiters:          newLimiterPool(rate.Limit(10000), 10000),
		analyticsLimiters: newLimiterPool(rate.Limit(10000), 10000),
	}
}

// --- ListProducts pagination ---

func TestListProducts_PagesUntilShortOrNoLastID(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v3/product/list", r.URL.Path)
		var req struct {
			LastID string `json:"last_id"`
			Limit  int    `json:"limit"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			// Full page + last_id → the client must ask for more.
			assert.Equal(t, "", req.LastID)
			items := make([]map[string]any, sellerPageSize)
			for i := range items {
				items[i] = map[string]any{"product_id": i + 1, "offer_id": "off"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"items": items, "last_id": "cursor-2", "total": sellerPageSize + 2},
			})
			return
		}
		// Second page: carries the cursor, returns a short page → stop.
		assert.Equal(t, "cursor-2", req.LastID)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"items":   []map[string]any{{"product_id": "9001", "offer_id": "a"}, {"product_id": 9002, "offer_id": "b"}},
				"last_id": "cursor-3",
			},
		})
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	out, err := c.ListProducts(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	require.Len(t, out, sellerPageSize+2)
	assert.Equal(t, int64(1), out[0].ProductID)
	assert.Equal(t, int64(9001), out[sellerPageSize].ProductID, "string product_id parsed via flexInt64")
}

func TestListProducts_StopsWhenLastIDEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Full page but empty last_id → the client must stop after one page.
		items := make([]map[string]any, sellerPageSize)
		for i := range items {
			items[i] = map[string]any{"product_id": i + 1, "offer_id": "x"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"items": items, "last_id": ""}})
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	out, err := c.ListProducts(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Len(t, out, sellerPageSize)
}

// --- ListProductPrices ---

func TestListProductPrices_ParsesPricesIndexesAndSkipsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v5/product/info/prices", r.URL.Path)
		// price as string, VAT fraction, commissions, and both competitor
		// min-price spellings (min_price vs minimal_price).
		body := `{
			"items": [
				{
					"product_id": "111", "offer_id": "A",
					"price": {"price": "1990.0000", "old_price": "2490.0", "min_price": 1500, "net_price": "1000", "marketing_seller_price": "1890", "vat": "0.2"},
					"commissions": {"sales_percent_fbo": 15.5, "sales_percent_fbs": "17.0"},
					"acquiring": 1.5,
					"price_indexes": {
						"color_index": "GREEN",
						"ozon_index_data": {"min_price": "1800.0"},
						"external_index_data": {"minimal_price": "1750.5"},
						"self_marketplaces_index_data": {"min_price": 0, "minimal_price": 0}
					}
				},
				{"product_id": 222, "offer_id": "B", "price": {"price": 0}}
			],
			"cursor": "",
			"total": 2
		}`
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	out, err := c.ListProductPrices(context.Background(), testCreds)
	require.NoError(t, err)
	require.Len(t, out, 1, "zero-price row must be skipped")
	row := out[0]
	assert.Equal(t, int64(111), row.ProductID)
	assert.InDelta(t, 1990, row.PriceRub, 1e-9)
	assert.InDelta(t, 2490, row.OldPriceRub, 1e-9)
	assert.InDelta(t, 1500, row.MinPriceRub, 1e-9)
	assert.InDelta(t, 1000, row.NetPriceRub, 1e-9)
	assert.InDelta(t, 1890, row.MarketingSellerPriceRub, 1e-9)
	assert.InDelta(t, 20, row.VATPct, 1e-9, "vat fraction scaled to percent")
	assert.Equal(t, "GREEN", row.ColorIndex)
	assert.InDelta(t, 15.5, row.CommissionFBOPct, 1e-9)
	assert.InDelta(t, 17, row.CommissionFBSPct, 1e-9)
	assert.InDelta(t, 1.5, row.AcquiringPct, 1e-9)
	assert.InDelta(t, 1800, row.OzonIndexMinPriceRub, 1e-9, "min_price used")
	assert.InDelta(t, 1750.5, row.ExternalIndexMinPriceRub, 1e-9, "minimal_price fallback used")
	assert.Zero(t, row.SelfIndexMinPriceRub)
}

func TestListProductPrices_CursorPagination(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Cursor string `json:"cursor"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			assert.Empty(t, req.Cursor)
			items := make([]map[string]any, sellerPageSize)
			for i := range items {
				items[i] = map[string]any{"product_id": i + 1, "price": map[string]any{"price": 100}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "cursor": "next"})
			return
		}
		assert.Equal(t, "next", req.Cursor)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":  []map[string]any{{"product_id": 5000, "price": map[string]any{"price": 200}}},
			"cursor": "",
		})
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	out, err := c.ListProductPrices(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	assert.Len(t, out, sellerPageSize+1)
}

// --- GetProductInfoList ---

func TestGetProductInfoList_TopLevelAndResultShapesWithChunking(t *testing.T) {
	var chunkLens []int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v3/product/info/list", r.URL.Path)
		var req struct {
			ProductID []int64 `json:"product_id"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		mu.Lock()
		chunkLens = append(chunkLens, len(req.ProductID))
		call := len(chunkLens)
		mu.Unlock()

		if call == 1 {
			// Top-level items shape.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"id": req.ProductID[0], "name": "N1", "offer_id": "O1", "sku": 555},
					{"id": 0, "name": "dropped"}, // product id 0 → dropped
				},
			})
			return
		}
		// Wrapped result.items shape.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"items": []map[string]any{
				{"id": req.ProductID[0], "name": "N2", "offer_id": "O2", "sources": []map[string]any{{"sku": 777}}},
			}},
		})
	}))
	defer srv.Close()

	ids := make([]int64, productInfoChunkSize+1) // forces 2 chunks
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	c := newTestSellerClient(srv.URL)
	out, err := c.GetProductInfoList(context.Background(), testCreds, ids)
	require.NoError(t, err)
	assert.Equal(t, []int{productInfoChunkSize, 1}, chunkLens, "chunked at 1000 ids")
	require.Len(t, out, 2, "the id=0 item is dropped")
	assert.Equal(t, int64(555), out[0].SKU)
	assert.Equal(t, int64(777), out[1].SKU, "sales sku from sources")
}

// --- UpdatePrices ---

func TestUpdatePrices_ChunksAndPerItemResults(t *testing.T) {
	var chunkLens []int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/product/import/prices", r.URL.Path)
		var req struct {
			Prices []struct {
				ProductID    int64  `json:"product_id"`
				Price        string `json:"price"`
				OldPrice     string `json:"old_price"`
				MinPrice     string `json:"min_price"`
				CurrencyCode string `json:"currency_code"`
			} `json:"prices"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		mu.Lock()
		chunkLens = append(chunkLens, len(req.Prices))
		mu.Unlock()
		// First item of first chunk carries old/min; assert string formatting.
		if req.Prices[0].ProductID == 1 {
			assert.Equal(t, "19.90", req.Prices[0].Price)
			assert.Equal(t, "29.00", req.Prices[0].OldPrice)
			assert.Equal(t, "9.50", req.Prices[0].MinPrice)
			assert.Equal(t, "RUB", req.Prices[0].CurrencyCode)
		}
		results := make([]map[string]any, 0, len(req.Prices))
		for i, p := range req.Prices {
			if i == 1 {
				results = append(results, map[string]any{
					"product_id": p.ProductID, "updated": false,
					"errors": []map[string]any{{"code": "E1", "message": "bad price"}},
				})
				continue
			}
			results = append(results, map[string]any{"product_id": p.ProductID, "updated": true})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": results})
	}))
	defer srv.Close()

	old := 29.0
	min := 9.5
	items := make([]PriceUpdate, updatePricesChunkSize+2)
	items[0] = PriceUpdate{ProductID: 1, PriceRub: 19.9, OldPriceRub: &old, MinPriceRub: &min}
	items[1] = PriceUpdate{ProductID: 2, PriceRub: 5}
	for i := 2; i < len(items); i++ {
		items[i] = PriceUpdate{ProductID: int64(i + 1), PriceRub: 1}
	}

	c := newTestSellerClient(srv.URL)
	out, err := c.UpdatePrices(context.Background(), testCreds, items)
	require.NoError(t, err)
	assert.Equal(t, []int{updatePricesChunkSize, 2}, chunkLens, "chunked at 1000 items")
	require.Len(t, out, updatePricesChunkSize+2)
	assert.True(t, out[0].Updated)
	assert.False(t, out[1].Updated)
	require.Len(t, out[1].Errors, 1)
	assert.Contains(t, out[1].Errors[0], "E1: bad price")
}

func TestUpdatePrices_TransportErrorReturnsPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	out, err := c.UpdatePrices(context.Background(), testCreds, []PriceUpdate{{ProductID: 1, PriceRub: 5}})
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Empty(t, out)
}

// --- GetProductStocks ---

func TestGetProductStocks_SumsSchemesBothWrappers(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v4/product/info/stocks", r.URL.Path)
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			// Top-level items/cursor shape; present/reserved summed across schemes.
			items := make([]map[string]any, sellerPageSize)
			for i := range items {
				items[i] = map[string]any{
					"product_id": i + 1, "offer_id": "o",
					"stocks": []map[string]any{
						{"type": "fbo", "present": 3, "reserved": 1},
						{"type": "fbs", "present": "2", "reserved": "1"},
					},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "cursor": "c2"})
			return
		}
		// Wrapped result shape with last_id + a product_id=0 row (dropped).
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"items": []map[string]any{
					{"product_id": 9000, "stocks": []map[string]any{{"present": 10, "reserved": 4}}},
					{"product_id": 0, "stocks": []map[string]any{{"present": 99}}},
				},
				"last_id": "",
			},
		})
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	out, err := c.GetProductStocks(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	require.Len(t, out, sellerPageSize+1, "product_id=0 dropped")
	assert.Equal(t, int64(5), out[0].Present, "3+2 summed across schemes")
	assert.Equal(t, int64(2), out[0].Reserved)
	assert.Equal(t, int64(9000), out[sellerPageSize].ProductID)
}

// --- GetAnalyticsSalesDaily ---

func TestGetAnalyticsSalesDaily_OffsetPaginationAndSkips(t *testing.T) {
	var offsets []int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/analytics/data", r.URL.Path)
		var req struct {
			Offset int `json:"offset"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		mu.Lock()
		offsets = append(offsets, req.Offset)
		call := len(offsets)
		mu.Unlock()

		if call == 1 {
			data := make([]map[string]any, analyticsPageSize)
			for i := range data {
				data[i] = map[string]any{
					"dimensions": []map[string]any{{"id": "1000"}, {"id": "2026-08-01"}},
					"metrics":    []any{5, 4990.5},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"data": data}})
			return
		}
		// Short second page with skip-worthy rows.
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"data": []map[string]any{
			{"dimensions": []map[string]any{{"id": "2000"}, {"id": "2026-08-02"}}, "metrics": []any{2, 100}},
			{"dimensions": []map[string]any{{"id": "0"}, {"id": "2026-08-02"}}, "metrics": []any{2, 100}},  // sku 0 skipped
			{"dimensions": []map[string]any{{"id": "3000"}, {"id": "bad-date"}}, "metrics": []any{2, 100}}, // bad date skipped
			{"dimensions": []map[string]any{{"id": "4000"}}, "metrics": []any{2, 100}},                     // too few dims skipped
		}}})
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	out, err := c.GetAnalyticsSalesDaily(context.Background(), testCreds, from, from.AddDate(0, 0, 1))
	require.NoError(t, err)
	assert.Equal(t, []int{0, analyticsPageSize}, offsets)
	require.Len(t, out, analyticsPageSize+1)
	assert.Equal(t, int64(1000), out[0].SKU)
	assert.Equal(t, int64(5), out[0].OrderedUnits)
	assert.InDelta(t, 4990.5, out[0].RevenueRub, 1e-9)
	assert.Equal(t, int64(2000), out[analyticsPageSize].SKU)
}

// --- ListPostings ---

func TestListPostings_MergesFBOAndFBS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/posting/fbo/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{
				{"created_at": "2026-08-01T10:00:00Z", "products": []map[string]any{{"sku": 111, "quantity": 2}}},
			}})
		case "/v3/posting/fbs/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"postings": []map[string]any{
				{"in_process_at": "2026-08-01T11:00:00Z", "products": []map[string]any{{"sku": 222, "quantity": 1}}},
			}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	out, err := c.ListPostings(context.Background(), testCreds, from, from.AddDate(0, 0, 1))
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, int64(111), out[0].SKU)
	assert.Equal(t, int64(222), out[1].SKU)
}

func TestListPostings_FBOErrorFailsWhole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad range"}`))
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	_, err := c.ListPostings(context.Background(), testCreds, from, from.AddDate(0, 0, 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "posting/fbo/list")
}

// --- do() transport branches ---

func TestSellerDo_ClientErrorNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// Long body to exercise the 300-byte snippet truncation.
		w.WriteHeader(http.StatusBadRequest)
		long := make([]byte, 0, 400)
		for i := 0; i < 400; i++ {
			long = append(long, 'x')
		}
		w.Write(long)
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	_, err := c.ListProducts(context.Background(), testCreds)
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "4xx must not retry")
}

func TestSellerDo_5xxRetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"items": []any{}, "last_id": ""}})
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	out, err := c.ListProducts(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "5xx retried once then ok")
}

func TestSellerDo_429HonorsRetryAfterThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"items": []any{}, "last_id": ""}})
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	start := time.Now()
	_, err := c.ListProducts(context.Background(), testCreds)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), time.Second)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

func TestSellerDo_DecodeErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	_, err := c.ListProducts(context.Background(), testCreds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode product/list")
}

func TestSellerDo_NetworkErrorExhausts(t *testing.T) {
	// Port 1 refuses connections → transport error every attempt, backoff
	// between them, then the wrapped error is returned.
	c := newTestSellerClient("http://127.0.0.1:1")
	_, err := c.ListProducts(context.Background(), testCreds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "attempts exhausted")
}

func TestSleepWithContext_CancelReturnsErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleepWithContext(ctx, time.Hour)
	require.Error(t, err)
}

func TestSellerDo_ContextCancelledDuringLimiter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	c := newTestSellerClient(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ListProducts(ctx, testCreds)
	require.Error(t, err)
}
