package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

// fakeCPOOrdersReport wires the full async report flow into the fake Ozon API:
// submit → poll (OK immediately) → download with the given rows payload.
func fakeCPOOrdersReport(env *ozonTestEnv, rowsJSON string) {
	env.fake.setRaw("/api/client/statistics/all_sku_promo/orders/generate/json", `{"UUID":"cpo-orders-uuid"}`)
	env.fake.setRaw("/api/client/statistics/cpo-orders-uuid", `{"state":"OK"}`)
	env.fake.setRaw("/api/client/statistics/report", rowsJSON)
}

func TestOzonSync_SyncCPOOrders_UpsertIdempotent(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	testdb.OzonCampaignTyped(t, env.pool, env.cabinetID, 25626134, "Оплата за заказ", "ALL_SKU_PROMO", "CAMPAIGN_STATE_RUNNING")
	// Real report shape: strings everywhere, Russian decimals, DD.MM.YYYY.
	fakeCPOOrdersReport(env, `{"rows":[
		{"date":"04.08.2026","order_id":"37245177478","order_number":"0125476150-1011",
		 "sku":"2011312046","adv_sku":"2011312046","vendor_code":"A61","name":"Товар А61",
		 "quantity":"1","price":"752,00","sale_price":"752,00","bid":"5,00","abs_bid":"37,60","adv_money_spent":"37,60"},
		{"date":"05.08.2026","order_id":"37245177479","sku":"2011312047","quantity":"2",
		 "price":"1 000,00","sale_price":"900,00","bid":"5,00","abs_bid":"90,00","adv_money_spent":"90,00"}
	]}`)

	require.NoError(t, env.syncSvc.SyncCPOOrders(ctx, env.cabinet(t)))
	assert.Equal(t, 2, countRows(t, env.pool, `SELECT COUNT(*) FROM ozon_cpo_orders WHERE seller_cabinet_id = $1`, env.cabinetID))

	// Second sync of the same window: upserts, no duplicates, values updated.
	fakeCPOOrdersReport(env, `{"rows":[
		{"date":"04.08.2026","order_id":"37245177478","order_number":"0125476150-1011",
		 "sku":"2011312046","quantity":"1","sale_price":"700,00","bid":"5,00","abs_bid":"35,00","adv_money_spent":"35,00"}
	]}`)
	require.NoError(t, env.syncSvc.SyncCPOOrders(ctx, env.cabinet(t)))
	assert.Equal(t, 2, countRows(t, env.pool, `SELECT COUNT(*) FROM ozon_cpo_orders WHERE seller_cabinet_id = $1`, env.cabinetID))
	assert.Equal(t, 1, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_cpo_orders WHERE seller_cabinet_id = $1 AND order_id = '37245177478' AND sale_price_rub = 700.00 AND spend_rub = 35.00`,
		env.cabinetID))
}

func TestOzonSync_SyncCPOOrders_SkipsWhenPromoNotRunning(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	// Promo exists but paused — the report budget must not be spent.
	testdb.OzonCampaignTyped(t, env.pool, env.cabinetID, 25626134, "Промо (пауза)", "ALL_SKU_PROMO", "CAMPAIGN_STATE_INACTIVE")
	require.NoError(t, env.syncSvc.SyncCPOOrders(ctx, env.cabinet(t)))
	assert.Zero(t, env.fake.callCount("/api/client/statistics/all_sku_promo/orders/generate/json"))
	assert.Zero(t, countRows(t, env.pool, `SELECT COUNT(*) FROM ozon_cpo_orders WHERE seller_cabinet_id = $1`, env.cabinetID))
}

func TestOzonSync_SyncCPOOrders_PricesOnlySkips(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentialsSellerOnly())
	err := env.syncSvc.SyncCPOOrders(context.Background(), env.cabinet(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "performance credentials missing")
}

func TestOzonReadService_GetCPOOverview_OrdersAggregates(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	now := time.Now().UTC()
	// Two lines of the same order (multi-SKU order counts once) + one more
	// order inside the window + one outside it.
	testdb.OzonCPOOrder(t, env.pool, env.cabinetID, now.AddDate(0, 0, -1), "order-1", 111, 1, 752.00, 5, 37.60)
	testdb.OzonCPOOrder(t, env.pool, env.cabinetID, now.AddDate(0, 0, -1), "order-1", 112, 2, 100.00, 5, 10.00)
	testdb.OzonCPOOrder(t, env.pool, env.cabinetID, now.AddDate(0, 0, -3), "order-2", 111, 1, 500.00, 7, 35.00)
	testdb.OzonCPOOrder(t, env.pool, env.cabinetID, now.AddDate(0, 0, -20), "order-old", 111, 9, 9999.00, 9, 999.00)

	ov, err := env.syncSvc.GetCPOOverview(ctx, env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), ov.OrdersCount7d) // distinct order_id in window
	assert.Equal(t, int64(4), ov.SoldUnits7d)   // 1 + 2 + 1
	// 752*1 + 100*2 + 500*1 = 1452
	assert.InDelta(t, 1452.0, ov.PromoRevenue7d, 0.001)
	assert.InDelta(t, 82.60, ov.PromoSpend7d, 0.001) // 37.60 + 10 + 35
	require.NotNil(t, ov.AvgBidPct7d)
	assert.InDelta(t, (5.0+5.0+7.0)/3, *ov.AvgBidPct7d, 0.001)

	// Existing campaign-stats aggregate is untouched (different source).
	assert.Zero(t, ov.Stats7d.Views)
}

func TestOzonReadService_GetCPOOverview_NoOrders(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())

	ov, err := env.syncSvc.GetCPOOverview(context.Background(), env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.Zero(t, ov.OrdersCount7d)
	assert.Zero(t, ov.SoldUnits7d)
	assert.Zero(t, ov.PromoRevenue7d)
	assert.Zero(t, ov.PromoSpend7d)
	assert.Nil(t, ov.AvgBidPct7d)
}

func TestOzonSyncService_ListCPOOrders(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	now := time.Now().UTC()
	testdb.OzonCPOOrder(t, env.pool, env.cabinetID, now.AddDate(0, 0, -1), "order-new", 111, 1, 752.00, 5, 37.60)
	testdb.OzonCPOOrder(t, env.pool, env.cabinetID, now.AddDate(0, 0, -5), "order-mid", 112, 2, 300.00, 5, 30.00)
	testdb.OzonCPOOrder(t, env.pool, env.cabinetID, now.AddDate(0, 0, -40), "order-old", 113, 1, 100.00, 5, 5.00)

	// Default window (7 days): newest first, the 40-day-old row filtered out.
	items, total, err := env.syncSvc.ListCPOOrders(ctx, env.workspaceID, env.cabinetID, 0, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)
	assert.Equal(t, "order-new", items[0].OrderID)
	assert.Equal(t, "order-mid", items[1].OrderID)
	assert.Equal(t, int64(111), items[0].SKU)
	assert.Equal(t, int32(1), items[0].Quantity)
	require.NotNil(t, items[0].SalePriceRub)
	assert.InDelta(t, 752.0, *items[0].SalePriceRub, 0.001)
	require.NotNil(t, items[0].BidPct)
	assert.InDelta(t, 5.0, *items[0].BidPct, 0.001)
	require.NotNil(t, items[0].SpendRub)
	assert.InDelta(t, 37.60, *items[0].SpendRub, 0.001)

	// Wider window picks up the old order; pagination slices.
	items, total, err = env.syncSvc.ListCPOOrders(ctx, env.workspaceID, env.cabinetID, 90, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, items, 2)

	// Tenancy: a foreign workspace is rejected.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, _, err = env.syncSvc.ListCPOOrders(ctx, otherWorkspace, env.cabinetID, 0, 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}
