package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// perf400Server returns a token, then 400 on any data path (no retry path).
func perf400Server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad"}`))
	}))
}

// TestReadMethods_PropagateAPIError exercises the error-return branch of each
// read method (the `if err != nil { return nil, err }` after the transport
// call), confirming a client error surfaces as *APIError.
func TestReadMethods_PropagateAPIError(t *testing.T) {
	srv := perf400Server(t)
	defer srv.Close()
	c := newTestPerfClient(srv.URL)
	ctx := context.Background()

	assertAPIErr := func(err error) {
		require.Error(t, err)
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	}

	_, err := c.ListCampaigns(ctx, testCreds)
	assertAPIErr(err)
	_, err = c.ListCampaignProducts(ctx, testCreds, 1)
	assertAPIErr(err)
	_, err = c.DailyStats(ctx, testCreds, mustDate(2026, 8, 1), mustDate(2026, 8, 1), nil)
	assertAPIErr(err)
	_, err = c.GetCompetitiveBids(ctx, testCreds, 1, []int64{1})
	assertAPIErr(err)
	_, err = c.GetMinSKUBids(ctx, testCreds, []int64{1}, "CPC")
	assertAPIErr(err)
	_, err = c.GetCPOMinBids(ctx, testCreds, []int64{1})
	assertAPIErr(err)
	_, err = c.GetBidLimits(ctx, testCreds)
	assertAPIErr(err)
	_, err = c.ListSearchPromoProducts(ctx, testCreds)
	assertAPIErr(err)
	err = c.EnableSearchPromo(ctx, testCreds, []int64{1})
	assertAPIErr(err)
	err = c.SetSearchPromoBids(ctx, testCreds, []CPOBid{{SKU: 1, BidRub: 1}})
	assertAPIErr(err)
	err = c.SetCampaignProductBids(ctx, testCreds, 1, []ProductBid{{SKU: 1, BidRub: 1}})
	assertAPIErr(err)
	err = c.UpdateCampaign(ctx, testCreds, 1, CampaignPatch{DailyBudgetRub: ptrInt64(5)})
	assertAPIErr(err)
	err = c.ActivateCampaign(ctx, testCreds, 1)
	assertAPIErr(err)
	// Phrases submit fails on the first doJSON.
	_, err = c.GetPhrasesReport(ctx, testCreds, []int64{1}, mustDate(2026, 8, 1), mustDate(2026, 8, 1))
	require.Error(t, err)
}

// TestGetCompetitiveBids_SkipsUnparseableBid covers the per-row skip branch.
func TestGetCompetitiveBids_SkipsUnparseableBid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"bids": []map[string]any{
			{"sku": 111, "bid": "25500000"},
			{"sku": 222, "bid": "not-a-number"}, // skipped
		}})
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	out, err := c.GetCompetitiveBids(context.Background(), testCreds, 1, []int64{111, 222})
	require.NoError(t, err)
	require.Len(t, out, 1, "unparseable bid row skipped")
	assert.Equal(t, int64(111), out[0].SKU)
}

func ptrInt64(v int64) *int64 { return &v }
