package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectedPriceSnapshotUsesExactCentsAndDoesNotInventMissingRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v5/product/info/prices", r.URL.Path)
		var input struct {
			Filter struct {
				ProductID []string `json:"product_id"`
			} `json:"filter"`
			Limit int `json:"limit"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
		require.Equal(t, []string{"101", "102"}, input.Filter.ProductID)
		require.Equal(t, 1000, input.Limit)
		_, _ = w.Write([]byte(`{"items":[{"product_id":"101","offer_id":"TEST","price":{"currency_code":"RUB","price":"100.0100","old_price":120.25,"min_price":null}}]}`))
	}))
	defer server.Close()
	result, err := newTestSellerClient(server.URL).ReadPriceSnapshots(context.Background(), testCreds, []int64{101, 102})
	require.NoError(t, err)
	require.Equal(t, int64(10001), result[101].PriceCents)
	require.Equal(t, int64(12025), *result[101].OldPriceCents)
	require.Nil(t, result[101].MinPriceCents)
	_, missing := result[102]
	require.False(t, missing)
}

func TestSelectedPriceSnapshotsRejectForeignDuplicateInexactAndUnknownMoney(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"items":[{"product_id":999,"price":{"currency_code":"RUB","price":"100"}}]}`,
		`{"items":[{"product_id":101,"price":{"currency_code":"USD","price":"100"}}]}`,
		`{"items":[{"product_id":101,"price":{"currency_code":"RUB","price":"100.001"}}]}`,
		`{"items":[{"product_id":101,"price":{"currency_code":"RUB","price":"NaN"}}]}`,
		`{"items":[{"product_id":101,"price":{"currency_code":"RUB","price":"100"}},{"product_id":101,"price":{"currency_code":"RUB","price":"100"}}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(raw)) }))
			defer server.Close()
			_, err := newTestSellerClient(server.URL).ReadPriceSnapshots(context.Background(), testCreds, []int64{101})
			require.Error(t, err)
		})
	}
}

func TestSelectedPriceSnapshotPromotionConditionsAreObservedAndNeverDefaulted(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mutate        func(map[string]any, map[string]any)
		known, simple bool
	}{
		{"complete_simple", func(map[string]any, map[string]any) {}, true, true},
		{"auto_action", func(p, m map[string]any) { p["auto_action_enabled"] = true }, true, false},
		{"auto_add", func(p, m map[string]any) { p["auto_add_to_ozon_actions_list_enabled"] = true }, true, false},
		{"marketing_price", func(p, m map[string]any) { p["marketing_seller_price"] = "99" }, true, false},
		{"action_list", func(p, m map[string]any) { m["actions"] = []any{map[string]any{"title": "Observed action"}} }, true, false},
		{"action_flag", func(p, m map[string]any) { m["ozon_actions_exist"] = true }, true, false},
		{"missing_auto", func(p, m map[string]any) { delete(p, "auto_action_enabled") }, false, false},
		{"missing_add", func(p, m map[string]any) { delete(p, "auto_add_to_ozon_actions_list_enabled") }, false, false},
		{"missing_marketing", func(p, m map[string]any) { delete(p, "marketing_seller_price") }, false, false},
		{"inexact_marketing", func(p, m map[string]any) { p["marketing_seller_price"] = "100.001" }, false, false},
		{"missing_actions", func(p, m map[string]any) { delete(m, "actions") }, false, false},
		{"null_actions", func(p, m map[string]any) { m["actions"] = nil }, false, false},
		{"object_actions", func(p, m map[string]any) { m["actions"] = map[string]any{} }, false, false},
		{"missing_action_flag", func(p, m map[string]any) { delete(m, "ozon_actions_exist") }, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := map[string]any{"currency_code": "RUB", "price": "100.00", "old_price": "120.00", "min_price": "90.00", "marketing_seller_price": "100.00", "auto_action_enabled": false, "auto_add_to_ozon_actions_list_enabled": false}
			m := map[string]any{"actions": []any{}, "ozon_actions_exist": false}
			tc.mutate(p, m)
			raw, err := json.Marshal(map[string]any{"items": []any{map[string]any{"product_id": 101, "offer_id": "TEST", "price": p, "marketing_actions": m}}})
			require.NoError(t, err)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
			defer server.Close()
			rows, err := newTestSellerClient(server.URL).ReadPriceSnapshots(context.Background(), testCreds, []int64{101})
			require.NoError(t, err)
			require.Equal(t, int64(10000), rows[101].PriceCents)
			require.Equal(t, tc.known, rows[101].Conditions != nil)
			require.Equal(t, tc.simple, rows[101].HasSimpleConditions())
		})
	}
}
