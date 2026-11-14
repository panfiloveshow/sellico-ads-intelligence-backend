package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagerProductBidOzonKeepsExactMicroRublesAndDoesNotGuessNumericMinimumUnit(t *testing.T) {
	for _, raw := range []string{`"5000001"`, `5`} {
		t.Run(raw, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/client/token":
					writeToken(w, "test-token", 1800)
				case "/api/client/campaign":
					_, _ = w.Write([]byte(`{"list":[{"id":"101","title":"Native campaign","state":"CAMPAIGN_STATE_RUNNING","advObjectType":"SKU","productAutopilotStrategy":"TARGET_BIDS","placement":"PLACEMENT_SEARCH","weeklyBudget":"100000000"}]}`))
				case "/api/client/campaign/101/v2/products":
					_, _ = w.Write([]byte(`{"products":[{"sku":"202","bid":"10000001"}]}`))
				case "/api/client/min/sku":
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, []any{"202"}, body["skus"])
					require.Equal(t, "CPC", body["paymentType"])
					_, _ = w.Write([]byte(`{"minBids":[{"sku":"202","bid":` + raw + `}]}`))
				default:
					t.Errorf("Unexpected native endpoint %s", r.URL.Path)
					w.WriteHeader(400)
				}
			}))
			defer srv.Close()
			result, err := newTestPerfClient(srv.URL).ReadManagerProductBid(context.Background(), testCreds, 101, 202)
			require.NoError(t, err)
			require.Equal(t, "10.000001", result.Bid.CurrentBidRub)
			require.Nil(t, result.Bid.MaximumBidRub)
			if raw == `5` {
				require.Nil(t, result.Bid.MinimumBidRub)
				require.Equal(t, "5", *result.Bid.UnverifiedMinimumRaw)
				require.Equal(t, "unverified_numeric_unit", result.Bid.MinimumSource)
			} else {
				require.Equal(t, "5.000001", *result.Bid.MinimumBidRub)
				require.Nil(t, result.Bid.UnverifiedMinimumRaw)
			}
		})
	}
}

func TestManagerProductBidExactOzonBodyPreservesBeyondIEEEIntegerPrecision(t *testing.T) {
	body, err := exactManagerProductBidBody(101, 202, "9007199254.740993")
	require.NoError(t, err)
	require.JSONEq(t, `{"bids":[{"sku":"202","bid":"9007199254740993"}]}`, string(body))
	for _, bad := range []string{"0", "-1", "1.0000001", "1e2", "NaN", "01", "9999999999999"} {
		_, err = exactManagerProductBidBody(101, 202, bad)
		require.Error(t, err, bad)
	}
}
