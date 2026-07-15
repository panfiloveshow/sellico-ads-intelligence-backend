package sellico

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestListWBUnitEconomicsIncludesSPPAndCustomerPrice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer service-token", r.Header.Get("Authorization"))
		require.Equal(t, "17", r.URL.Query().Get("integration_id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"source":"sellico-products-unit-economics","items":[{"nm_id":184010772,"cost_price":700,"commission_percent":24.5,"tax_percent":6,"spp_percent":40.12,"customer_price":947,"logistics_cost":120,"other_costs":55,"max_allowed_drr":19.6,"calculated_at":"2026-07-15T09:45:00Z","ready":true}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	rows, err := client.ListWBUnitEconomics(context.Background(), "service-token", "/products/unit-economics/export", "17")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].SppPercent)
	require.Equal(t, 40.12, *rows[0].SppPercent)
	require.NotNil(t, rows[0].CustomerPrice)
	require.Equal(t, 947.0, *rows[0].CustomerPrice)
	require.Equal(t, 120.0, *rows[0].LogisticsCost)
	require.Equal(t, 55.0, *rows[0].OtherCosts)
	require.Equal(t, 19.6, *rows[0].MaxAllowedDRR)
	require.Equal(t, "sellico-products-unit-economics", rows[0].Source)
	require.True(t, rows[0].Ready)
	require.NotNil(t, rows[0].CalculatedAt)
}
