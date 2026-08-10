package ozon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseCPOOrdersReport_RealSample parses the exact row shape a live
// cabinet returned on 2026-08-10: every value a string, money/percent with
// Russian decimal commas, dates DD.MM.YYYY.
func TestParseCPOOrdersReport_RealSample(t *testing.T) {
	body := []byte(`{"rows":[
		{"date":"04.08.2026","order_id":"37245177478","order_number":"0125476150-1011",
		 "sku":"2011312046","adv_sku":"2011312046","vendor_code":"A61","name":"Товар А61",
		 "quantity":"1","price":"752,00","sale_price":"752,00","bid":"5,00",
		 "abs_bid":"37,60","adv_money_spent":"37,60"}
	]}`)
	rows, err := parseCPOOrdersReport(body)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC), row.Date)
	assert.Equal(t, "37245177478", row.OrderID)
	assert.Equal(t, "0125476150-1011", row.OrderNumber)
	assert.Equal(t, int64(2011312046), row.SKU)
	assert.Equal(t, int64(2011312046), row.AdvSKU)
	assert.Equal(t, "A61", row.VendorCode)
	assert.Equal(t, "Товар А61", row.Name)
	assert.Equal(t, 1, row.Quantity)
	assert.InDelta(t, 752.0, row.PriceRub, 1e-9)
	assert.InDelta(t, 752.0, row.SalePriceRub, 1e-9)
	assert.InDelta(t, 5.0, row.BidPct, 1e-9)
	assert.InDelta(t, 37.60, row.BidRub, 1e-9)
	assert.InDelta(t, 37.60, row.SpendRub, 1e-9)
}

// TestParseCPOOrdersReport_SeparatorsAndSkips covers thousand separators
// (regular and non-breaking spaces), rows without an order_id (summary lines)
// and rows with an unparseable date — both skipped, the rest kept.
func TestParseCPOOrdersReport_SeparatorsAndSkips(t *testing.T) {
	body := []byte(`{"rows":[
		{"date":"31.12.2026","order_id":"111","sku":"5","quantity":"2",
		 "price":"1 752,50","sale_price":"1` + " " + `700,00","bid":"7,00","abs_bid":"119,00","adv_money_spent":"238,00"},
		{"date":"01.08.2026","order_id":"","name":"Всего"},
		{"date":"мусор","order_id":"222","sku":"6"}
	]}`)
	rows, err := parseCPOOrdersReport(body)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), row.Date)
	assert.Equal(t, 2, row.Quantity)
	assert.InDelta(t, 1752.50, row.PriceRub, 1e-9)
	assert.InDelta(t, 1700.0, row.SalePriceRub, 1e-9)
	assert.InDelta(t, 7.0, row.BidPct, 1e-9)
	assert.InDelta(t, 119.0, row.BidRub, 1e-9)
	assert.InDelta(t, 238.0, row.SpendRub, 1e-9)
}

func TestParseCPOOrdersReport_EmptyAndInvalid(t *testing.T) {
	rows, err := parseCPOOrdersReport([]byte(`{"rows":[]}`))
	require.NoError(t, err)
	assert.Empty(t, rows)

	_, err = parseCPOOrdersReport([]byte(`not json`))
	require.Error(t, err)
}

// TestGetAllSKUPromoOrders_FullAsyncFlow exercises submit (timeBounds query
// params, /json variant) → poll → download → parse against a fake server.
func TestGetAllSKUPromoOrders_FullAsyncFlow(t *testing.T) {
	var submitQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/client/token":
			writeToken(w, "tok-1", 1800)
		case "/api/client/statistics/all_sku_promo/orders/generate/json":
			submitQuery = r.URL.RawQuery
			w.Write([]byte(`{"UUID":"orders-uuid-1"}`))
		case "/api/client/statistics/orders-uuid-1":
			w.Write([]byte(`{"state":"OK"}`))
		case "/api/client/statistics/report":
			require.Equal(t, "orders-uuid-1", r.URL.Query().Get("UUID"))
			w.Write([]byte(`{"rows":[{"date":"04.08.2026","order_id":"37245177478","sku":"2011312046","quantity":"1","sale_price":"752,00","bid":"5,00","abs_bid":"37,60","adv_money_spent":"37,60"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	from := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	rows, err := c.GetAllSKUPromoOrders(context.Background(), testCreds, from, to)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "37245177478", rows[0].OrderID)
	assert.InDelta(t, 37.60, rows[0].SpendRub, 1e-9)
	assert.Contains(t, submitQuery, "timeBounds.from=2026-08-03T00%3A00%3A00Z")
	assert.Contains(t, submitQuery, "timeBounds.to=2026-08-10T00%3A00%3A00Z")
}

// TestGetAllSKUPromoOrders_SubmitNoUUID surfaces a clear error when the
// submit endpoint answers without a report UUID.
func TestGetAllSKUPromoOrders_SubmitNoUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	_, err := c.GetAllSKUPromoOrders(context.Background(), testCreds, time.Now().Add(-24*time.Hour), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no UUID")
}
