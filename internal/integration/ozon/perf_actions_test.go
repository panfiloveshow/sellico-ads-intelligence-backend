package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// perfActionServer serves a token on /api/client/token and delegates every
// other request to handler, recording the last method/path/body seen.
type recordedReq struct {
	method string
	path   string
	query  map[string][]string
	body   map[string]any
	raw    []byte
}

func newPerfActionServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, rec *recordedReq)) (*httptest.Server, *[]recordedReq, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var reqs []recordedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		rec := recordedReq{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
		if r.Body != nil {
			raw, _ := readAll(r)
			rec.raw = raw
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &rec.body)
			}
		}
		handler(w, r, &rec)
		mu.Lock()
		reqs = append(reqs, rec)
		mu.Unlock()
	}))
	return srv, &reqs, &mu
}

func readAll(r *http.Request) ([]byte, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func TestActivateDeactivateCampaign(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	require.NoError(t, c.ActivateCampaign(context.Background(), testCreds, 42))
	require.NoError(t, c.DeactivateCampaign(context.Background(), testCreds, 42))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *reqs, 2)
	assert.Equal(t, http.MethodPost, (*reqs)[0].method)
	assert.Equal(t, "/api/client/campaign/42/activate", (*reqs)[0].path)
	assert.Equal(t, "/api/client/campaign/42/deactivate", (*reqs)[1].path)
}

func TestUpdateCampaign_PatchFieldsAndMicroRubConversion(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	daily := int64(530)
	weekly := int64(3710)
	from := mustDate(2026, 8, 1)
	to := mustDate(2026, 8, 31)
	err := c.UpdateCampaign(context.Background(), testCreds, 7, CampaignPatch{
		DailyBudgetRub: &daily, WeeklyBudgetRub: &weekly, FromDate: &from, ToDate: &to,
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *reqs, 1)
	req := (*reqs)[0]
	assert.Equal(t, http.MethodPatch, req.method)
	assert.Equal(t, "/api/client/campaign/7", req.path)
	assert.Equal(t, "530000000", req.body["dailyBudget"], "rubles → micro-rubles")
	assert.Equal(t, "3710000000", req.body["weeklyBudget"])
	assert.Equal(t, "2026-08-01", req.body["fromDate"])
	assert.Equal(t, "2026-08-31", req.body["toDate"])
}

func TestUpdateCampaign_EmptyPatchErrors(t *testing.T) {
	c := newTestPerfClient("http://unused")
	err := c.UpdateCampaign(context.Background(), testCreds, 7, CampaignPatch{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty campaign patch")
}

func TestSetCampaignProductBids_PUTWithStringSKUAndMicroRub(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	cir := 12.5
	err := c.SetCampaignProductBids(context.Background(), testCreds, 9, []ProductBid{
		{SKU: 123, BidRub: 25.5, TargetCIR: &cir},
		{SKU: 456, BidRub: 10},
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	req := (*reqs)[0]
	assert.Equal(t, http.MethodPut, req.method)
	assert.Equal(t, "/api/client/campaign/9/products", req.path)
	bids := req.body["bids"].([]any)
	require.Len(t, bids, 2)
	first := bids[0].(map[string]any)
	assert.Equal(t, "123", first["sku"], "uint64 sku sent as string")
	assert.Equal(t, "25500000", first["bid"], "rub → micro-rub")
	assert.Equal(t, "12.5", first["targetCir"])
	second := bids[1].(map[string]any)
	_, hasCIR := second["targetCir"]
	assert.False(t, hasCIR, "omitempty drops nil targetCir")
}

func TestSetCampaignProductBids_EmptyErrors(t *testing.T) {
	c := newTestPerfClient("http://unused")
	err := c.SetCampaignProductBids(context.Background(), testCreds, 9, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no bids")
}

func TestGetMinSKUBids_ChunksAndParsesListKeyVariants(t *testing.T) {
	var chunkSizes []int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/min/sku", r.URL.Path)
		var body struct {
			SKUs        []string `json:"skus"`
			PaymentType string   `json:"paymentType"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		mu.Lock()
		chunkSizes = append(chunkSizes, len(body.SKUs))
		call := len(chunkSizes)
		mu.Unlock()
		assert.Equal(t, "CPC", body.PaymentType)
		// Alternate the response list key across chunks to exercise fallbacks.
		key := "minBids"
		if call == 2 {
			key = "bids"
		}
		rows := make([]map[string]any, 0, len(body.SKUs))
		for _, s := range body.SKUs {
			rows = append(rows, map[string]any{"sku": s, "bid": "5000000"}) // 5 rub
		}
		// One unparseable row to hit the skip branch.
		rows = append(rows, map[string]any{"sku": 999, "bid": "not-a-number"})
		_ = json.NewEncoder(w).Encode(map[string]any{key: rows})
	}))
	defer srv.Close()

	skus := make([]int64, minSKUBidsChunk+5)
	for i := range skus {
		skus[i] = int64(i + 1)
	}
	c := newTestPerfClient(srv.URL)
	out, err := c.GetMinSKUBids(context.Background(), testCreds, skus, "CPC")
	require.NoError(t, err)
	assert.Equal(t, []int{minSKUBidsChunk, 5}, chunkSizes)
	require.Len(t, out, minSKUBidsChunk+5, "unparseable rows skipped")
	assert.InDelta(t, 5.0, out[0].BidRub, 1e-9)
}

func TestParseMinSKUBids_RowsKeyAndEmpty(t *testing.T) {
	rows, err := parseMinSKUBids([]byte(`{"rows":[{"sku":7,"bid":"2500000"}]}`))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(7), rows[0].SKU)
	assert.InDelta(t, 2.5, rows[0].BidRub, 1e-9)

	rows, err = parseMinSKUBids([]byte(`{}`))
	require.NoError(t, err)
	assert.Empty(t, rows)

	_, err = parseMinSKUBids([]byte(`{bad`))
	require.Error(t, err)
}

func TestGetBidLimits_ValidAndInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/limits/list", r.URL.Path)
		w.Write([]byte(`{"limits":[{"min":1}]}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	raw, err := c.GetBidLimits(context.Background(), testCreds)
	require.NoError(t, err)
	assert.JSONEq(t, `{"limits":[{"min":1}]}`, string(raw))

	// Invalid JSON body → error.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.Write([]byte(`not json at all`))
	}))
	defer srv2.Close()
	c2 := newTestPerfClient(srv2.URL)
	_, err = c2.GetBidLimits(context.Background(), testCreds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestListSearchPromoProducts_EnabledResolutionAndPaging(t *testing.T) {
	var pages []int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/campaign/search_promo/v2/products", r.URL.Path)
		var body struct {
			Page int `json:"page"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		mu.Lock()
		pages = append(pages, body.Page)
		mu.Unlock()
		if body.Page == 0 {
			// Full page (0-based): mix of enabled resolution paths.
			products := make([]map[string]any, searchPromoPageSize)
			for i := range products {
				products[i] = map[string]any{"sku": i + 1, "bid": 3.5, "enabled": true}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"products": products})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"products": []map[string]any{
			{"sku": 9001, "bid": 1.0, "isEnabled": true},
			{"sku": 9002, "bid": 2.0, "searchPromoStatus": "SEARCH_PROMO_STATUS_ENABLED"},
			{"sku": 9003, "bid": 2.0, "searchPromoStatus": "DISABLED"},
		}})
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	out, err := c.ListSearchPromoProducts(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1}, pages)
	require.Len(t, out, searchPromoPageSize+3)
	assert.True(t, out[0].Enabled)
	assert.InDelta(t, 3.5, out[0].BidRub, 1e-9)
	assert.True(t, out[searchPromoPageSize].Enabled, "isEnabled path")
	assert.True(t, out[searchPromoPageSize+1].Enabled, "status string path")
	assert.False(t, out[searchPromoPageSize+2].Enabled)
}

// TestListSearchPromoProducts_EnrichedFields covers the real v2 response shape:
// string sku/price/bidPrice, int bid, sourceSku=offer_id, and no per-product
// enabled flag (presence == in promo → Enabled true).
func TestListSearchPromoProducts_EnrichedFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/campaign/search_promo/v2/products", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"products": []map[string]any{
			{
				"sku":             "2011312046",
				"sourceSku":       "A61",
				"title":           "Товар А61",
				"imageUrl":        "https://cdn/x.jpg",
				"price":           "752",
				"bid":             0,
				"bidPrice":        "0",
				"visibilityIndex": "10+",
			},
		}})
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	out, err := c.ListSearchPromoProducts(context.Background(), testCreds)
	require.NoError(t, err)
	require.Len(t, out, 1)
	p := out[0]
	assert.Equal(t, int64(2011312046), p.SKU) // string sku parsed
	assert.Equal(t, "A61", p.OfferID)         // sourceSku → offer_id
	assert.Equal(t, "Товар А61", p.Name)
	assert.Equal(t, "https://cdn/x.jpg", p.ImageURL)
	assert.InDelta(t, 752.0, p.PriceRub, 1e-9) // string price parsed
	assert.InDelta(t, 0.0, p.BidRub, 1e-9)
	assert.InDelta(t, 0.0, p.BidPriceRub, 1e-9) // string bidPrice parsed
	assert.Equal(t, "10+", p.VisibilityIndex)
	assert.True(t, p.Enabled) // no flag in the response → presence == in promo
}

func TestEnableDisableSearchPromo(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	require.NoError(t, c.EnableSearchPromo(context.Background(), testCreds, []int64{111, 222}))
	require.NoError(t, c.DisableSearchPromo(context.Background(), testCreds, []int64{333}))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *reqs, 2)
	assert.Equal(t, "/api/client/search_promo/product/enable", (*reqs)[0].path)
	skus := (*reqs)[0].body["skus"].([]any)
	assert.Equal(t, []any{"111", "222"}, skus)
	assert.Equal(t, "/api/client/search_promo/product/disable", (*reqs)[1].path)

	// Empty skus → error, no request.
	require.Error(t, c.EnableSearchPromo(context.Background(), testCreds, nil))
}

func TestSetSearchPromoBids_RublesFloat(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	err := c.SetSearchPromoBids(context.Background(), testCreds, []CPOBid{{SKU: 5, BidRub: 12.75}})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	req := (*reqs)[0]
	assert.Equal(t, "/api/client/campaign/search_promo/v2/bids/set", req.path)
	bid := req.body["bids"].([]any)[0].(map[string]any)
	assert.Equal(t, "5", bid["sku"])
	assert.InDelta(t, 12.75, bid["bid"].(float64), 1e-9, "CPO bids are plain rubles")

	require.Error(t, c.SetSearchPromoBids(context.Background(), testCreds, nil))
}

func TestGetCPOMinBids_ChunksAndRublesFloat(t *testing.T) {
	var chunkSizes []int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/search_promo/get_cpo_min_bids", r.URL.Path)
		var body struct {
			SKUs []string `json:"skus"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		mu.Lock()
		chunkSizes = append(chunkSizes, len(body.SKUs))
		mu.Unlock()
		rows := make([]map[string]any, 0, len(body.SKUs))
		for _, s := range body.SKUs {
			rows = append(rows, map[string]any{"sku": s, "bid": 4.25})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"bids": rows})
	}))
	defer srv.Close()

	skus := make([]int64, cpoMinBidsChunk+3)
	for i := range skus {
		skus[i] = int64(i + 1)
	}
	c := newTestPerfClient(srv.URL)
	out, err := c.GetCPOMinBids(context.Background(), testCreds, skus)
	require.NoError(t, err)
	assert.Equal(t, []int{cpoMinBidsChunk, 3}, chunkSizes)
	require.Len(t, out, cpoMinBidsChunk+3)
	assert.InDelta(t, 4.25, out[0].BidRub, 1e-9)
}

func TestGetAllSKUPromoRate_ParsesSelectedBidString(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{"selectedBid":"7"}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	rate, err := c.GetAllSKUPromoRate(context.Background(), testCreds)
	require.NoError(t, err)
	assert.Equal(t, 7, rate)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *reqs, 1)
	assert.Equal(t, http.MethodGet, (*reqs)[0].method)
	assert.Equal(t, "/api/client/campaign/all_sku_promo/activate", (*reqs)[0].path)
}

func TestGetAllSKUPromoRate_EmptyOrUnparseableErrors(t *testing.T) {
	// Missing selectedBid → error (so the overview leaves rate_pct null).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newTestPerfClient(srv.URL)
	_, err := c.GetAllSKUPromoRate(context.Background(), testCreds)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no selectedBid")

	// Non-numeric selectedBid → error.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.Write([]byte(`{"selectedBid":"abc"}`))
	}))
	defer srv2.Close()
	c2 := newTestPerfClient(srv2.URL)
	_, err = c2.GetAllSKUPromoRate(context.Background(), testCreds)
	require.Error(t, err)
}

func TestSetAllSKUPromoRate_ValidatesAndSetsBidParam(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)

	// Rejects any rate that is not 5/7/9 — no request is made.
	for _, bad := range []int{0, 4, 6, 8, 10, 100} {
		err := c.SetAllSKUPromoRate(context.Background(), testCreds, bad)
		require.Error(t, err, "rate %d must be rejected", bad)
		assert.Contains(t, err.Error(), "5, 7 or 9")
	}
	mu.Lock()
	assert.Empty(t, *reqs, "invalid rates never hit the API")
	mu.Unlock()

	// Accepts 5/7/9 and sends GET set_bid?bid=<rate>.
	for _, ok := range []int{5, 7, 9} {
		require.NoError(t, c.SetAllSKUPromoRate(context.Background(), testCreds, ok))
	}
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *reqs, 3)
	assert.Equal(t, http.MethodGet, (*reqs)[0].method)
	assert.Equal(t, "/api/client/campaign/all_sku_promo/set_bid", (*reqs)[0].path)
	assert.Equal(t, "5", (*reqs)[0].query["bid"][0])
	assert.Equal(t, "7", (*reqs)[1].query["bid"][0])
	assert.Equal(t, "9", (*reqs)[2].query["bid"][0])
}

func TestDeactivateAllSKUPromo_GETPath(t *testing.T) {
	srv, reqs, mu := newPerfActionServer(t, func(w http.ResponseWriter, r *http.Request, rec *recordedReq) {
		w.Write([]byte(`{}`))
	})
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	require.NoError(t, c.DeactivateAllSKUPromo(context.Background(), testCreds))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, *reqs, 1)
	assert.Equal(t, http.MethodGet, (*reqs)[0].method)
	assert.Equal(t, "/api/client/campaign/all_sku_promo/deactivate", (*reqs)[0].path)
}

// --- ListCampaignProducts + DailyStats (perf_client.go) ---

func TestListCampaignProducts_MicroRubBids(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/campaign/55/v2/products", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"products": []map[string]any{
			{"sku": "123", "bid": "25500000"},
			{"sku": 456, "bid": "bad"}, // unparseable → logged, bid 0
		}})
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	out, err := c.ListCampaignProducts(context.Background(), testCreds, 55)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, int64(123), out[0].SKU)
	assert.InDelta(t, 25.5, out[0].BidRub, 1e-9)
	assert.Zero(t, out[1].BidRub)
}

func TestDailyStats_JSONAndCSVDispatch(t *testing.T) {
	// JSON variant.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		require.Equal(t, "/api/client/statistics/daily", r.URL.Path)
		assert.Contains(t, r.URL.Query()["campaignIds"], "42")
		w.Write([]byte(`{"rows":[{"id":42,"date":"2026-08-01","views":10,"clicks":2,"moneySpent":5.5,"orders":1,"ordersMoney":100}]}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	from := mustDate(2026, 8, 1)
	rows, err := c.DailyStats(context.Background(), testCreds, from, from, []int64{42})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(42), rows[0].CampaignID)

	// CSV variant.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.Write([]byte("id;date;views;clicks;moneySpent;orders;ordersMoney\n42;2026-08-01;10;2;5.5;1;100\n"))
	}))
	defer srv2.Close()
	c2 := newTestPerfClient(srv2.URL)
	rows, err = c2.DailyStats(context.Background(), testCreds, from, from, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.InDelta(t, 5.5, rows[0].SpendRub, 1e-9)
}

func TestValidateCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeToken(w, "tok-1", 1800)
	}))
	defer srv.Close()
	c := newTestPerfClient(srv.URL)
	require.NoError(t, c.ValidateCredentials(context.Background(), testCreds))
}
