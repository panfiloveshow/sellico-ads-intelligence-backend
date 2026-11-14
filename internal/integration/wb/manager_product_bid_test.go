package wb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagerProductBidReadsExactWBProductAndMinimum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/advert/v2/adverts":
			require.Equal(t, "GET", r.Method)
			require.Equal(t, "101", r.URL.Query().Get("ids"))
			_, _ = w.Write([]byte(`{"adverts":[{"id":101,"status":9,"type":9,"bid_type":"manual","settings":{"name":"Native campaign","payment_type":"cpm","placements":{"search":true,"recommendations":true}},"nm_settings":[{"nm_id":202,"bids_kopecks":{"search":12345,"recommendations":6789}}]}]}`))
		case "/api/advert/v1/bids/min":
			require.Equal(t, "POST", r.Method)
			var body MinimumBidsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, int64(101), body.AdvertID)
			require.Equal(t, []int64{202}, body.NMIDs)
			require.Equal(t, []string{"search"}, body.PlacementTypes)
			require.Equal(t, "cpm", body.PaymentType)
			_, _ = w.Write([]byte(`{"bids":[{"nm_id":202,"bids":[{"type":"search","value":111}]}]}`))
		default:
			t.Errorf("Unexpected native endpoint %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	result, err := c.ReadManagerProductBid(context.Background(), "token", 101, 202, "search")
	require.NoError(t, err)
	require.Equal(t, "123.45", result.Bid.CurrentBidRub)
	require.Equal(t, "1.11", *result.Bid.MinimumBidRub)
	require.Equal(t, "67.89", result.Bid.PeerBidsRub["recommendations"])
	require.Nil(t, result.Bid.MaximumBidRub)
}

func TestManagerProductBidRejectsWBMissingDisabledAmbiguousOrAutomaticTarget(t *testing.T) {
	for _, bad := range []string{"missing_product", "missing_bid", "disabled", "automatic", "duplicate", "missing_minimum"} {
		t.Run(bad, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/advert/v1/bids/min" {
					_, _ = w.Write([]byte(`{"bids":[]}`))
					return
				}
				nm := int64(202)
				if bad == "missing_product" {
					nm = 303
				}
				bids := map[string]any{"search": 12345}
				if bad == "missing_bid" {
					delete(bids, "search")
				}
				products := []any{map[string]any{"nm_id": nm, "bids_kopecks": bids}}
				if bad == "duplicate" {
					products = append(products, products[0])
				}
				bidType := "manual"
				if bad == "automatic" {
					bidType = "unified"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"adverts": []any{map[string]any{"id": 101, "status": 9, "bid_type": bidType, "settings": map[string]any{"payment_type": "cpm", "placements": map[string]bool{"search": bad != "disabled"}}, "nm_settings": products}}})
			}))
			defer srv.Close()
			_, err := newTestClient(srv.URL).ReadManagerProductBid(context.Background(), "token", 101, 202, "search")
			require.Error(t, err)
		})
	}
}
