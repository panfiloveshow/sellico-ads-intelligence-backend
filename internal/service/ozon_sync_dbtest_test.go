package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

// seedOzonHappyPath registers live-format responses for the full SyncCabinet
// pipeline: 2 campaigns (101 SKU / 102 BANNER), per-SKU bids in micro-ruble
// strings, a CSV daily-stats export with Russian unit-suffixed headers,
// product catalog/prices/stocks on the Seller side.
func seedOzonHappyPath(env *ozonTestEnv) (day1, day2 string) {
	day1 = time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	day2 = time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")

	env.fake.setJSON("/api/client/campaign", map[string]any{
		"list": []map[string]any{
			{
				"id": "101", "title": "Кампания SKU", "state": "CAMPAIGN_STATE_RUNNING",
				"advObjectType": "SKU", "fromDate": "2026-08-01", "toDate": "",
				"dailyBudget": "5000000000", "weeklyBudget": "",
				"placement": []string{"PLACEMENT_SEARCH"}, "productAutopilotStrategy": "",
			},
			{
				"id": 102, "title": "Баннеры", "state": "CAMPAIGN_STATE_INACTIVE",
				"advObjectType": "BANNER", "fromDate": "", "toDate": "",
				"dailyBudget": "", "weeklyBudget": "7000000000",
				"placement": "PLACEMENT_TOP_PROMOTION", "productAutopilotStrategy": "TOP",
			},
		},
	})
	env.fake.setJSON("/api/client/campaign/101/v2/products", map[string]any{
		"products": []map[string]any{
			{"sku": "555", "bid": "12500000"},
			{"sku": 777, "bid": "9000000"},
		},
	})
	// Campaign 102 has no products endpoint payload → "{}" → empty list.

	env.fake.setRaw("/api/client/statistics/daily", strings.Join([]string{
		"ID;День;Показы;Клики;Расход, ₽;Заказы, шт.;Заказы, ₽",
		fmt.Sprintf("101;%s;1200;80;1 234,56;5;10 000,00", day1),
		fmt.Sprintf("101;%s;900;40;500,00;2;3 000,00", day2),
		fmt.Sprintf("102;%s;100;5;50,10;0;0,00", day1),
	}, "\n"))

	env.fake.setJSON("/v3/product/list", map[string]any{
		"result": map[string]any{
			"items": []map[string]any{
				{"product_id": 9001, "offer_id": "ART-1"},
				{"product_id": "9002", "offer_id": "ART-2"},
			},
			"last_id": "", "total": 2,
		},
	})
	env.fake.setJSON("/v3/product/info/list", map[string]any{
		"items": []map[string]any{
			{"id": 9001, "name": "Товар первый", "offer_id": "ART-1", "sku": 555, "primary_image": "https://img/1.jpg"},
			{"id": 9002, "name": "Товар второй", "offer_id": "ART-2", "sources": []map[string]any{{"sku": 777}}, "primary_image": []string{"https://img/2.jpg"}},
		},
	})
	env.fake.setJSON("/v5/product/info/prices", map[string]any{
		"items": []map[string]any{
			{
				"product_id": 9001, "offer_id": "ART-1",
				"price": map[string]any{
					"price": 1990, "old_price": 2490, "min_price": 1490,
					"net_price": 900, "marketing_seller_price": 1890, "vat": "0.2",
				},
				"commissions": map[string]any{"sales_percent_fbo": 18.5, "sales_percent_fbs": 19},
				"acquiring":   1.5,
				"price_indexes": map[string]any{
					"color_index":                  "GREEN",
					"ozon_index_data":              map[string]any{"min_price": 1800},
					"external_index_data":          map[string]any{"minimal_price": 1750},
					"self_marketplaces_index_data": map[string]any{},
				},
			},
			{
				// Live API has shipped money as strings too — flexFloat path.
				"product_id": "9002", "offer_id": "ART-2",
				"price":         map[string]any{"price": "990.00"},
				"commissions":   map[string]any{},
				"price_indexes": map[string]any{},
			},
		},
		"cursor": "", "total": 2,
	})
	env.fake.setJSON("/v4/product/info/stocks", map[string]any{
		"items": []map[string]any{
			{
				"product_id": 9001, "offer_id": "ART-1",
				"stocks": []map[string]any{
					{"type": "fbo", "present": 10, "reserved": 2},
					{"type": "fbs", "present": "5", "reserved": "1"},
				},
			},
		},
		"cursor": "",
	})
	return day1, day2
}

func TestOzonSyncService_SyncCabinet_FullPipeline(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	day1, _ := seedOzonHappyPath(env)
	ctx := context.Background()

	require.NoError(t, env.syncSvc.SyncCabinet(ctx, env.cabinetID))

	// Campaigns mirrored with converted budgets and parsed dates.
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_campaigns WHERE seller_cabinet_id = $1`, env.cabinetID))
	var (
		title, state, placement string
		dailyBudget             int64
		fromDate                time.Time
	)
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT title, state, placement, daily_budget_rub, from_date
		 FROM ozon_campaigns WHERE seller_cabinet_id = $1 AND ozon_campaign_id = 101`,
		env.cabinetID).Scan(&title, &state, &placement, &dailyBudget, &fromDate))
	assert.Equal(t, "Кампания SKU", title)
	assert.Equal(t, "CAMPAIGN_STATE_RUNNING", state)
	assert.Equal(t, "PLACEMENT_SEARCH", placement)
	assert.Equal(t, int64(5000), dailyBudget) // 5000000000 micro → 5000 rub
	assert.Equal(t, "2026-08-01", fromDate.Format("2006-01-02"))

	var weeklyBudget int64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT weekly_budget_rub FROM ozon_campaigns
		 WHERE seller_cabinet_id = $1 AND ozon_campaign_id = 102`,
		env.cabinetID).Scan(&weeklyBudget))
	assert.Equal(t, int64(7000), weeklyBudget)

	// Per-SKU bids converted from micro-ruble strings.
	var bid555, bid777 float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT p.bid_rub FROM ozon_campaign_products p
		 JOIN ozon_campaigns c ON c.id = p.campaign_id
		 WHERE c.ozon_campaign_id = 101 AND p.sku = 555`).Scan(&bid555))
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT p.bid_rub FROM ozon_campaign_products p
		 JOIN ozon_campaigns c ON c.id = p.campaign_id
		 WHERE c.ozon_campaign_id = 101 AND p.sku = 777`).Scan(&bid777))
	assert.InDelta(t, 12.5, bid555, 0.001)
	assert.InDelta(t, 9.0, bid777, 0.001)

	// Stats parsed from the live CSV export (Russian headers with units,
	// space-grouped thousands, comma decimals).
	assert.Equal(t, 3, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_campaign_stats s
		 JOIN ozon_campaigns c ON c.id = s.campaign_id
		 WHERE c.seller_cabinet_id = $1`, env.cabinetID))
	var views, clicks, orders int64
	var spend, revenue float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT s.views, s.clicks, s.spend_rub, s.orders, s.revenue_rub
		 FROM ozon_campaign_stats s JOIN ozon_campaigns c ON c.id = s.campaign_id
		 WHERE c.ozon_campaign_id = 101 AND s.date = $1`, day1).
		Scan(&views, &clicks, &spend, &orders, &revenue))
	assert.Equal(t, int64(1200), views)
	assert.Equal(t, int64(80), clicks)
	assert.InDelta(t, 1234.56, spend, 0.001)
	assert.Equal(t, int64(5), orders)
	assert.InDelta(t, 10000.0, revenue, 0.001)

	// Product catalog mapping: top-level sku and sources[].sku both land.
	var sku9002 int64
	var name9002 string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT sku, name FROM ozon_products WHERE seller_cabinet_id = $1 AND product_id = 9002`,
		env.cabinetID).Scan(&sku9002, &name9002))
	assert.Equal(t, int64(777), sku9002)
	assert.Equal(t, "Товар второй", name9002)

	// Prices mirrored with name enrichment from ozon_products.
	var priceName, colorIndex string
	var priceRub, netPrice, commFBO, ozonIdx, extIdx float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT name, color_index, price_rub, net_price_rub, commission_fbo_pct,
		        ozon_index_min_price_rub, external_index_min_price_rub
		 FROM ozon_product_prices WHERE seller_cabinet_id = $1 AND sku = 9001`,
		env.cabinetID).Scan(&priceName, &colorIndex, &priceRub, &netPrice, &commFBO, &ozonIdx, &extIdx))
	assert.Equal(t, "Товар первый", priceName)
	assert.Equal(t, "GREEN", colorIndex)
	assert.InDelta(t, 1990.0, priceRub, 0.001)
	assert.InDelta(t, 900.0, netPrice, 0.001)
	assert.InDelta(t, 18.5, commFBO, 0.001)
	assert.InDelta(t, 1800.0, ozonIdx, 0.001)
	assert.InDelta(t, 1750.0, extIdx, 0.001) // minimal_price alias parsed

	var price9002 float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT price_rub FROM ozon_product_prices WHERE seller_cabinet_id = $1 AND sku = 9002`,
		env.cabinetID).Scan(&price9002))
	assert.InDelta(t, 990.0, price9002, 0.001)

	// Stocks summed across fulfillment schemes.
	var present, reserved int32
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT present, reserved FROM ozon_product_stocks WHERE seller_cabinet_id = $1 AND sku = 9001`,
		env.cabinetID).Scan(&present, &reserved))
	assert.Equal(t, int32(15), present)
	assert.Equal(t, int32(3), reserved)

	// Sync state touched everywhere, no error recorded.
	var campaignsAt, statsAt, pricesAt *time.Time
	var lastError *string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT campaigns_synced_at, stats_synced_at, prices_synced_at, last_error
		 FROM ozon_sync_states WHERE seller_cabinet_id = $1`, env.cabinetID).
		Scan(&campaignsAt, &statsAt, &pricesAt, &lastError))
	assert.NotNil(t, campaignsAt)
	assert.NotNil(t, statsAt)
	assert.NotNil(t, pricesAt)
	assert.Nil(t, lastError)
}

func TestOzonSyncService_SyncCabinet_Idempotent(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	seedOzonHappyPath(env)
	ctx := context.Background()

	require.NoError(t, env.syncSvc.SyncCabinet(ctx, env.cabinetID))
	require.NoError(t, env.syncSvc.SyncCabinet(ctx, env.cabinetID))

	// Upserts, not duplicates.
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_campaigns WHERE seller_cabinet_id = $1`, env.cabinetID))
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_campaign_products p
		 JOIN ozon_campaigns c ON c.id = p.campaign_id WHERE c.seller_cabinet_id = $1`, env.cabinetID))
	assert.Equal(t, 3, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_campaign_stats s
		 JOIN ozon_campaigns c ON c.id = s.campaign_id WHERE c.seller_cabinet_id = $1`, env.cabinetID))
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_product_prices WHERE seller_cabinet_id = $1`, env.cabinetID))
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_products WHERE seller_cabinet_id = $1`, env.cabinetID))

	// A changed upstream price lands on the third run (update, not insert).
	env.fake.setJSON("/v5/product/info/prices", map[string]any{
		"items": []map[string]any{
			{
				"product_id": 9001, "offer_id": "ART-1",
				"price":         map[string]any{"price": 2100},
				"commissions":   map[string]any{},
				"price_indexes": map[string]any{},
			},
		},
		"cursor": "",
	})
	require.NoError(t, env.syncSvc.SyncPrices(ctx, env.cabinet(t)))
	var priceRub float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT price_rub FROM ozon_product_prices WHERE seller_cabinet_id = $1 AND sku = 9001`,
		env.cabinetID).Scan(&priceRub))
	assert.InDelta(t, 2100.0, priceRub, 0.001)
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_product_prices WHERE seller_cabinet_id = $1`, env.cabinetID))
}

func TestOzonSyncService_SyncCabinet_PartialFailureRecordsLastError(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	seedOzonHappyPath(env)
	// Seller catalog breaks: products + prices become issues, campaigns/stats
	// still succeed. 400 → the client does not retry (no backoff sleeps).
	env.fake.fail("/v3/product/list", 400)
	ctx := context.Background()

	err := env.syncSvc.SyncCabinet(ctx, env.cabinetID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "products:")
	assert.Contains(t, err.Error(), "prices:")

	// Campaign half of the pipeline still ran.
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_campaigns WHERE seller_cabinet_id = $1`, env.cabinetID))

	var lastError string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT last_error FROM ozon_sync_states WHERE seller_cabinet_id = $1`, env.cabinetID).
		Scan(&lastError))
	assert.Contains(t, lastError, "products:")
	assert.Contains(t, lastError, "prices:")
}

func TestOzonSyncService_SyncCabinet_StocksFailureIsNoteNotError(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	seedOzonHappyPath(env)
	env.fake.fail("/v4/product/info/stocks", 400)
	ctx := context.Background()

	// Stocks are best-effort: the run reports success but records the note.
	require.NoError(t, env.syncSvc.SyncCabinet(ctx, env.cabinetID))

	var lastError string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT last_error FROM ozon_sync_states WHERE seller_cabinet_id = $1`, env.cabinetID).
		Scan(&lastError))
	assert.Contains(t, lastError, "stocks:")
	assert.NotContains(t, lastError, "prices:")
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_product_prices WHERE seller_cabinet_id = $1`, env.cabinetID))
}

func TestOzonSyncService_SyncCabinet_PricesOnlyMode(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentialsSellerOnly())
	seedOzonHappyPath(env)
	ctx := context.Background()

	// No Performance pair → campaigns/stats skipped as an expected note.
	require.NoError(t, env.syncSvc.SyncCabinet(ctx, env.cabinetID))

	assert.Equal(t, 0, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_campaigns WHERE seller_cabinet_id = $1`, env.cabinetID))
	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_product_prices WHERE seller_cabinet_id = $1`, env.cabinetID))

	var campaignsAt *time.Time
	var lastError string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT campaigns_synced_at, last_error FROM ozon_sync_states WHERE seller_cabinet_id = $1`,
		env.cabinetID).Scan(&campaignsAt, &lastError))
	assert.Nil(t, campaignsAt)
	assert.Contains(t, lastError, "performance credentials missing")
	assert.Equal(t, 0, env.fake.callCount("/api/client/campaign"))
}

func TestOzonSyncService_SyncCabinet_TenancyAndMarketplaceGates(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	// Unknown cabinet → not found.
	err := env.syncSvc.SyncCabinet(ctx, uuid.New())
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))

	// WB cabinet must never sync through the Ozon pipeline.
	wbCabinetID := testdb.WBCabinet(t, env.pool, env.workspaceID)
	err = env.syncSvc.SyncCabinet(ctx, wbCabinetID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))
}

func TestOzonSyncService_ListOzonCabinetIDs_FiltersMarketplace(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	testdb.WBCabinet(t, env.pool, env.workspaceID)

	ids, err := env.syncSvc.ListOzonCabinetIDs(context.Background())
	require.NoError(t, err)
	require.Len(t, ids, 1)
	assert.Equal(t, env.cabinetID, ids[0])
}

func TestOzonSyncService_SyncPostings_RewritesHeatmap(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	orderedAt1 := time.Now().UTC().Add(-26 * time.Hour)
	orderedAt2 := time.Now().UTC().Add(-3 * time.Hour)
	env.fake.setJSON("/v2/posting/fbo/list", map[string]any{
		"result": []map[string]any{
			{
				"in_process_at": orderedAt1.Format(time.RFC3339Nano),
				"products":      []map[string]any{{"sku": 555, "quantity": 2}},
			},
		},
	})
	env.fake.setJSON("/v3/posting/fbs/list", map[string]any{
		"result": map[string]any{
			"postings": []map[string]any{
				{
					"created_at": orderedAt2.Format(time.RFC3339),
					"products": []map[string]any{
						{"sku": "555", "quantity": 1},
						{"sku": 777, "quantity": "3"},
					},
				},
			},
		},
	})

	require.NoError(t, env.syncSvc.SyncPostings(ctx, env.cabinet(t)))

	dow1, hour1 := ozonMSKSlot(orderedAt1)
	var orders, quantity int
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT orders, quantity FROM ozon_orders_hourly
		 WHERE seller_cabinet_id = $1 AND sku = 555 AND dow = $2 AND hour = $3`,
		env.cabinetID, dow1, hour1).Scan(&orders, &quantity))
	assert.Equal(t, 1, orders)
	assert.Equal(t, 2, quantity)

	dow2, hour2 := ozonMSKSlot(orderedAt2)
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT orders, quantity FROM ozon_orders_hourly
		 WHERE seller_cabinet_id = $1 AND sku = 777 AND dow = $2 AND hour = $3`,
		env.cabinetID, dow2, hour2).Scan(&orders, &quantity))
	assert.Equal(t, 1, orders)
	assert.Equal(t, 3, quantity)

	// An empty pull clears the matrix — the rewrite is total, never additive.
	env.fake.setJSON("/v2/posting/fbo/list", map[string]any{"result": []map[string]any{}})
	env.fake.setJSON("/v3/posting/fbs/list", map[string]any{"result": map[string]any{"postings": []map[string]any{}}})
	require.NoError(t, env.syncSvc.SyncPostings(ctx, env.cabinet(t)))
	assert.Equal(t, 0, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_orders_hourly WHERE seller_cabinet_id = $1`, env.cabinetID))
}

func TestOzonSyncService_AllCabinetSweeps(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()
	day := time.Now().UTC().AddDate(0, 0, -1)

	env.fake.setJSON("/v2/posting/fbo/list", map[string]any{
		"result": []map[string]any{
			{"created_at": day.Format(time.RFC3339), "products": []map[string]any{{"sku": 555, "quantity": 1}}},
		},
	})
	env.fake.setJSON("/v3/posting/fbs/list", map[string]any{
		"result": map[string]any{"postings": []map[string]any{}},
	})
	env.fake.setJSON("/v1/analytics/data", map[string]any{
		"result": map[string]any{"data": []map[string]any{
			{"dimensions": []map[string]any{{"id": "555"}, {"id": day.Format("2006-01-02")}}, "metrics": []any{1, 990}},
		}},
	})

	require.NoError(t, env.syncSvc.SyncPostingsAllCabinets(ctx))
	require.NoError(t, env.syncSvc.SyncAnalyticsAllCabinets(ctx))

	assert.Equal(t, 1, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_orders_hourly WHERE seller_cabinet_id = $1`, env.cabinetID))
	assert.Equal(t, 1, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_sales_daily WHERE seller_cabinet_id = $1`, env.cabinetID))
}

func TestOzonSyncService_SyncSalesDaily(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()
	day := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")

	env.fake.setJSON("/v1/analytics/data", map[string]any{
		"result": map[string]any{
			"data": []map[string]any{
				{
					"dimensions": []map[string]any{{"id": "555"}, {"id": day}},
					"metrics":    []any{3, 4500},
				},
				{
					"dimensions": []map[string]any{{"id": "777"}, {"id": day}},
					"metrics":    []any{"2", "1500.5"},
				},
				{
					// Unparseable SKU dimension rows are skipped defensively.
					"dimensions": []map[string]any{{"id": "not-a-sku"}, {"id": day}},
					"metrics":    []any{1, 100},
				},
			},
		},
	})

	require.NoError(t, env.syncSvc.SyncSalesDaily(ctx, env.cabinet(t)))

	assert.Equal(t, 2, countRows(t, env.pool,
		`SELECT COUNT(*) FROM ozon_sales_daily WHERE seller_cabinet_id = $1`, env.cabinetID))
	var units int32
	var revenue float64
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT ordered_units, revenue_rub FROM ozon_sales_daily
		 WHERE seller_cabinet_id = $1 AND sku = 777 AND date = $2`,
		env.cabinetID, day).Scan(&units, &revenue))
	assert.Equal(t, int32(2), units)
	assert.InDelta(t, 1500.5, revenue, 0.001)
}
