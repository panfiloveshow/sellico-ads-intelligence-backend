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

func TestManagerCampaignBudgetOzonReadsExactMoneyAndDoesNotInventPeriodOrLimits(t *testing.T) {
	for _, kind := range []string{"weekly", "daily", "unlimited", "ambiguous"} {
		t.Run(kind, func(t *testing.T) {
			var writes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/client/token" {
					writeToken(w, "test-token", 1800)
					return
				}
				require.Equal(t, "GET", r.Method)
				if r.Method != "GET" {
					writes.Add(1)
				}
				row := map[string]any{"id": "101", "title": "Native budget campaign", "state": "CAMPAIGN_STATE_RUNNING", "advObjectType": "SKU", "dailyBudget": "0", "weeklyBudget": "100000001"}
				if kind == "daily" {
					row["dailyBudget"], row["weeklyBudget"] = "100000001", "0"
				}
				if kind == "unlimited" {
					row["weeklyBudget"] = "0"
				}
				if kind == "ambiguous" {
					row["dailyBudget"] = "200000000"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"list": []any{row}})
			}))
			defer srv.Close()
			period := "weekly"
			if kind == "daily" {
				period = "daily"
			}
			view, err := newTestPerfClient(srv.URL).ReadManagerCampaignBudget(context.Background(), testCreds, 101, period)
			require.NoError(t, err)
			require.Equal(t, period, view.Budget.Period)
			require.Equal(t, kind == "weekly" || kind == "daily", view.Budget.PeriodConfirmed)
			require.Nil(t, view.Budget.MinimumAmountRub)
			require.Nil(t, view.Budget.MaximumAmountRub)
			require.Nil(t, view.Budget.SpentTodayRub)
			require.Nil(t, view.Budget.CarryOverEnabled)
			if kind == "unlimited" {
				require.False(t, view.Budget.Enabled)
				require.Equal(t, "0", *view.Budget.AmountRub)
			} else {
				require.Equal(t, "100.000001", *view.Budget.AmountRub)
			}
			require.Equal(t, int32(0), writes.Load())
		})
	}
}

func TestManagerCampaignBudgetOzonExactPatchContainsOnlySelectedPeriod(t *testing.T) {
	for _, period := range []string{"daily", "weekly"} {
		body, err := exactManagerCampaignBudgetBody(101, period, "9007199254.74")
		require.NoError(t, err)
		require.JSONEq(t, `{"`+period+`Budget":"9007199254740000"}`, string(body))
		body, err = exactManagerCampaignBudgetBody(101, period, "0")
		require.NoError(t, err)
		require.JSONEq(t, `{"`+period+`Budget":"0"}`, string(body))
	}
	for _, value := range []string{"-1", "NaN", "1e3", "01", "0.001", "9999999999999"} {
		_, err := exactManagerCampaignBudgetBody(101, "weekly", value)
		require.Error(t, err, value)
	}
	_, err := exactManagerCampaignBudgetBody(101, "total", "100")
	require.Error(t, err)
}

func TestManagerCampaignBudgetOzonRejectsForeignTypeAndUnconfirmedAmount(t *testing.T) {
	for _, kind := range []string{"foreign", "non_sku", "missing", "number", "negative", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/client/token" {
					writeToken(w, "test-token", 1800)
					return
				}
				row := map[string]any{"id": "101", "title": "Native campaign", "state": "CAMPAIGN_STATE_RUNNING", "advObjectType": "SKU", "weeklyBudget": "100000001"}
				if kind == "foreign" {
					row["id"] = "102"
				}
				if kind == "non_sku" {
					row["advObjectType"] = "BANNER"
				}
				if kind == "missing" {
					delete(row, "weeklyBudget")
				}
				if kind == "number" {
					row["weeklyBudget"] = 100000001
				}
				if kind == "negative" {
					row["weeklyBudget"] = "-1"
				}
				rows := []any{row}
				if kind == "duplicate" {
					rows = append(rows, row)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"list": rows})
			}))
			defer srv.Close()
			_, err := newTestPerfClient(srv.URL).ReadManagerCampaignBudget(context.Background(), testCreds, 101, "weekly")
			require.Error(t, err)
		})
	}
}

func TestManagerCampaignBudgetOzonOneNativePatchWithoutRedirectOrRetry(t *testing.T) {
	for _, status := range []int{302, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls, destination atomic.Int32
			dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destination.Add(1); w.WriteHeader(200) }))
			defer dst.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/client/token" {
					writeToken(w, "test-token", 1800)
					return
				}
				require.Equal(t, "PATCH", r.Method)
				require.Equal(t, "/api/client/campaign/101", r.URL.Path)
				var body map[string]string
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, map[string]string{"weeklyBudget": "12340000"}, body)
				calls.Add(1)
				w.Header().Set("Location", dst.URL)
				w.WriteHeader(status)
			}))
			defer srv.Close()
			err := newTestPerfClient(srv.URL).SetExactCampaignBudget(context.Background(), testCreds, 101, "weekly", "12.34")
			require.Error(t, err)
			require.Equal(t, int32(1), calls.Load())
			require.Equal(t, int32(0), destination.Load())
		})
	}
}
