package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// A read may retry; an uncorrelated price write must not consume a second
// vendor attempt after a response which can conceal an accepted mutation.
func TestUpdatePricesDoesNotRetryUncertainMutation(t *testing.T) {
	for _, mode := range []string{"server_error", "rate_limit", "lost_response"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v1/product/import/prices", r.URL.Path)
				var body struct {
					Prices []struct {
						ProductID int64  `json:"product_id"`
						Price     string `json:"price"`
					} `json:"prices"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Len(t, body.Prices, 1)
				require.Equal(t, int64(101), body.Prices[0].ProductID)
				require.Equal(t, "100.01", body.Prices[0].Price)
				if calls.Add(1) == 1 {
					switch mode {
					case "server_error":
						w.WriteHeader(http.StatusServiceUnavailable)
					case "rate_limit":
						w.Header().Set("Retry-After", "0")
						w.WriteHeader(http.StatusTooManyRequests)
					case "lost_response":
						conn, _, err := w.(http.Hijacker).Hijack()
						require.NoError(t, err)
						_ = conn.Close()
					}
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"product_id": 101, "updated": true}}})
			}))
			defer srv.Close()

			results, err := newTestSellerClient(srv.URL).UpdatePrices(context.Background(), testCreds, []PriceUpdate{{ProductID: 101, PriceRub: 100.01}})
			require.Error(t, err)
			require.Empty(t, results)
			require.Equal(t, int32(1), calls.Load(), "an unknown write cannot be replayed by the HTTP read retry loop")
		})
	}
}

func TestUpdatePricesDoesNotForwardSellerCredentialsThroughRedirect(t *testing.T) {
	var escaped atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"product_id": 101, "updated": true}}})
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/unexpected", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	_, err := newTestSellerClient(origin.URL).UpdatePrices(context.Background(), testCreds, []PriceUpdate{{ProductID: 101, PriceRub: 100.01}})
	require.Error(t, err)
	require.Zero(t, escaped.Load(), "API keys and price payloads must not leave the configured seller origin")
}

func TestUpdatePricesMalformedReceiptCannotEstablishRejection(t *testing.T) {
	for _, body := range []string{
		`{"result":[{"product_id":101,"errors":[{"code":"PRICE_ERROR","message":"invalid"}]}]}`,
		`{"result":[{"product_id":101,"updated":false,"errors":[{}]}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			rows, err := newTestSellerClient(srv.URL).UpdatePrices(context.Background(), testCreds, []PriceUpdate{{ProductID: 101, PriceRub: 100.01}})
			require.Error(t, err)
			require.Empty(t, rows)
			require.Equal(t, int32(1), calls.Load())
		})
	}
}
