package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

func TestOzonReadService_ListCampaignsWithStats(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	campaignA := testdb.OzonCampaign(t, env.pool, env.cabinetID, 201, "Кампания A", "CAMPAIGN_STATE_RUNNING")
	testdb.OzonCampaign(t, env.pool, env.cabinetID, 202, "Кампания B", "CAMPAIGN_STATE_INACTIVE")

	now := time.Now().UTC()
	testdb.OzonCampaignStat(t, env.pool, campaignA, now.AddDate(0, 0, -1), 100, 10, 50, 1, 500)
	testdb.OzonCampaignStat(t, env.pool, campaignA, now.AddDate(0, 0, -3), 200, 20, 100, 2, 1000)
	// Outside the 7-day list window — must not pollute the aggregate.
	testdb.OzonCampaignStat(t, env.pool, campaignA, now.AddDate(0, 0, -20), 9999, 999, 9999, 99, 99999)

	items, total, err := env.syncSvc.ListCampaignsWithStats(ctx, env.workspaceID, env.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)

	// Ordered by ozon_campaign_id DESC: B (202) first, A (201) second.
	assert.Equal(t, int64(202), items[0].OzonCampaignID)
	assert.Zero(t, items[0].StatsViews)
	assert.Zero(t, items[0].DRR)

	campaignAStats := items[1]
	assert.Equal(t, int64(201), campaignAStats.OzonCampaignID)
	assert.Equal(t, int64(300), campaignAStats.StatsViews)
	assert.Equal(t, int64(30), campaignAStats.StatsClicks)
	assert.InDelta(t, 150.0, campaignAStats.StatsSpend, 0.001)
	assert.Equal(t, int64(3), campaignAStats.StatsOrders)
	assert.InDelta(t, 1500.0, campaignAStats.StatsRevenue, 0.001)
	assert.InDelta(t, 10.0, campaignAStats.DRR, 0.001) // 150 / 1500 * 100

	// Tenancy: a foreign workspace sees nothing, not even the count.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, _, err = env.syncSvc.ListCampaignsWithStats(ctx, otherWorkspace, env.cabinetID, 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestOzonReadService_GetCPOOverview_PromoRunning(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	promo := testdb.OzonCampaignTyped(t, env.pool, env.cabinetID, 25626134, "Оплата за заказ - все товары", "ALL_SKU_PROMO", "CAMPAIGN_STATE_RUNNING")
	// A non-promo campaign whose stats must NOT leak into the aggregate.
	other := testdb.OzonCampaign(t, env.pool, env.cabinetID, 999, "Обычная", "CAMPAIGN_STATE_RUNNING")

	now := time.Now().UTC()
	testdb.OzonCampaignStat(t, env.pool, promo, now.AddDate(0, 0, -1), 100, 10, 50, 1, 500)
	testdb.OzonCampaignStat(t, env.pool, promo, now.AddDate(0, 0, -3), 200, 20, 100, 2, 1000)
	testdb.OzonCampaignStat(t, env.pool, promo, now.AddDate(0, 0, -20), 9999, 999, 9999, 99, 99999) // outside window
	testdb.OzonCampaignStat(t, env.pool, other, now.AddDate(0, 0, -1), 7, 7, 7, 7, 7)               // non-promo

	testdb.OzonCPOProduct(t, env.pool, env.cabinetID, 555, true, 5)
	testdb.OzonCPOProduct(t, env.pool, env.cabinetID, 777, true, 0)

	ov, err := env.syncSvc.GetCPOOverview(ctx, env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.True(t, ov.Enabled)
	require.NotNil(t, ov.PromoCampaignID)
	assert.Equal(t, int64(25626134), *ov.PromoCampaignID)
	assert.Equal(t, "Оплата за заказ - все товары", ov.PromoCampaignTitle)
	assert.Equal(t, int64(2), ov.ProductsCount)
	assert.Nil(t, ov.RatePct) // display-only, not derivable yet
	assert.Equal(t, int64(300), ov.Stats7d.Views)
	assert.Equal(t, int64(30), ov.Stats7d.Clicks)
	assert.InDelta(t, 150.0, ov.Stats7d.SpendRub, 0.001)
	assert.Equal(t, int64(3), ov.Stats7d.Orders)
	assert.InDelta(t, 1500.0, ov.Stats7d.RevenueRub, 0.001)
	assert.InDelta(t, 10.0, ov.Stats7d.DRR, 0.001) // 150 / 1500 * 100

	// Tenancy: a foreign workspace is rejected.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, err = env.syncSvc.GetCPOOverview(ctx, otherWorkspace, env.cabinetID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestOzonReadService_GetCPOOverview_NoPromo(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	// Only a non-promo campaign exists.
	testdb.OzonCampaign(t, env.pool, env.cabinetID, 42, "Обычная", "CAMPAIGN_STATE_RUNNING")

	ov, err := env.syncSvc.GetCPOOverview(ctx, env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.False(t, ov.Enabled)
	assert.Nil(t, ov.PromoCampaignID)
	assert.Empty(t, ov.PromoCampaignTitle)
	assert.Zero(t, ov.ProductsCount)
	assert.Nil(t, ov.RatePct)
	assert.Zero(t, ov.Stats7d.Views)
	assert.Zero(t, ov.Stats7d.DRR)
}

func TestOzonReadService_GetCPOOverview_PromoNotRunning(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	testdb.OzonCampaignTyped(t, env.pool, env.cabinetID, 25626134, "Промо (пауза)", "SEARCH_PROMO", "CAMPAIGN_STATE_INACTIVE")

	ov, err := env.syncSvc.GetCPOOverview(ctx, env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.False(t, ov.Enabled) // promo exists but not RUNNING
	require.NotNil(t, ov.PromoCampaignID)
	assert.Equal(t, int64(25626134), *ov.PromoCampaignID)
	assert.Equal(t, "Промо (пауза)", ov.PromoCampaignTitle)
}

func TestOzonReadService_GetCampaign_EnrichesProductNames(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 301, "Кампания", "CAMPAIGN_STATE_RUNNING")
	testdb.OzonCampaignProduct(t, env.pool, campaignID, 555, 12.5)
	testdb.OzonCampaignProduct(t, env.pool, campaignID, 777, 9)
	// Only SKU 555 has a catalog mapping.
	testdb.OzonProduct(t, env.pool, env.cabinetID, 9001, 555, "ART-1", "Товар первый")

	campaign, products, err := env.syncSvc.GetCampaign(ctx, env.workspaceID, campaignID)
	require.NoError(t, err)
	assert.Equal(t, int64(301), campaign.OzonCampaignID)
	require.Len(t, products, 2)

	bySKU := map[int64]int{}
	for i, product := range products {
		bySKU[product.SKU] = i
	}
	enriched := products[bySKU[555]]
	assert.Equal(t, "Товар первый", enriched.Name)
	assert.Equal(t, "ART-1", enriched.OfferID)
	require.NotNil(t, enriched.BidRub)
	assert.InDelta(t, 12.5, *enriched.BidRub, 0.001)

	plain := products[bySKU[777]]
	assert.Empty(t, plain.Name)
	assert.Empty(t, plain.OfferID)

	// Tenancy: campaign of a foreign workspace reads as not found.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, _, err = env.syncSvc.GetCampaign(ctx, otherWorkspace, campaignID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))

	_, _, err = env.syncSvc.GetCampaign(ctx, env.workspaceID, uuid.New())
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestOzonReadService_ListCampaignStats_RangeFilter(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	campaignID := testdb.OzonCampaign(t, env.pool, env.cabinetID, 401, "Кампания", "CAMPAIGN_STATE_RUNNING")
	now := time.Now().UTC()
	testdb.OzonCampaignStat(t, env.pool, campaignID, now.AddDate(0, 0, -1), 100, 10, 50, 1, 500)
	testdb.OzonCampaignStat(t, env.pool, campaignID, now.AddDate(0, 0, -5), 200, 20, 100, 2, 1000)
	testdb.OzonCampaignStat(t, env.pool, campaignID, now.AddDate(0, 0, -30), 300, 30, 150, 3, 1500)

	stats, err := env.syncSvc.ListCampaignStats(ctx, env.workspaceID, campaignID, now.AddDate(0, 0, -7), now)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	// Ordered by date ascending.
	assert.True(t, stats[0].Date.Before(stats[1].Date))
	assert.Equal(t, int64(200), stats[0].Views)
	assert.InDelta(t, 50.0, stats[1].SpendRub, 0.001)
}

func TestOzonReadService_ListPrices_FloorFallbacks(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	// (a) Ozon's own net_price drives the floor: (900+10)/(1-0.20) = 1137.5.
	testdb.OzonProductPrice(t, env.pool, env.cabinetID, testdb.OzonPriceRow{
		SKU: 9001, OfferID: "ART-1", Name: "Товар первый", PriceRub: 1990,
		NetPriceRub: 900, CommissionFBOPct: 20, CommissionFBSPct: 10, AcquiringPct: 10,
	})
	// (b) No net_price → Sellico economics fallback: cost 500+50, same
	// commissions → (550+10)/(1-0.20) = 700.
	testdb.OzonProductPrice(t, env.pool, env.cabinetID, testdb.OzonPriceRow{
		SKU: 9002, OfferID: "ART-2", Name: "Товар второй", PriceRub: 990,
		CommissionFBOPct: 20, CommissionFBSPct: 10, AcquiringPct: 10,
	})
	testdb.OzonProductEconomic(t, env.pool, env.cabinetID, "ART-2", 500, 50)
	// (c) No net_price and no economics row → no floor at all.
	testdb.OzonProductPrice(t, env.pool, env.cabinetID, testdb.OzonPriceRow{
		SKU: 9003, OfferID: "ART-3", Name: "Товар третий", PriceRub: 490,
		CommissionFBOPct: 20, CommissionFBSPct: 10, AcquiringPct: 10,
	})

	prices, total, err := env.syncSvc.ListPrices(ctx, env.workspaceID, env.cabinetID, "", 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, prices, 3)

	byOffer := map[string]int{}
	for i, price := range prices {
		byOffer[price.OfferID] = i
	}

	ownFloor := prices[byOffer["ART-1"]]
	require.NotNil(t, ownFloor.FloorRub)
	assert.InDelta(t, 1137.5, *ownFloor.FloorRub, 0.001)

	economicsFloor := prices[byOffer["ART-2"]]
	require.NotNil(t, economicsFloor.FloorRub, "floor must fall back to Sellico unit economics")
	assert.InDelta(t, 700.0, *economicsFloor.FloorRub, 0.001)

	noFloor := prices[byOffer["ART-3"]]
	assert.Nil(t, noFloor.FloorRub)

	// Search narrows by offer_id substring and by exact SKU text.
	filtered, filteredTotal, err := env.syncSvc.ListPrices(ctx, env.workspaceID, env.cabinetID, "ART-2", 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), filteredTotal)
	require.Len(t, filtered, 1)
	assert.Equal(t, int64(9002), filtered[0].SKU)

	bySKU, bySKUTotal, err := env.syncSvc.ListPrices(ctx, env.workspaceID, env.cabinetID, "9003", 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), bySKUTotal)
	require.Len(t, bySKU, 1)
	assert.Equal(t, "ART-3", bySKU[0].OfferID)

	// Tenancy gate.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, _, err = env.syncSvc.ListPrices(ctx, otherWorkspace, env.cabinetID, "", 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestOzonReadService_ListSearchQueries_AggregatesAndFilters(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()
	now := time.Now().UTC()
	position3, position5 := 3.0, 5.0

	// One query split across two SKUs and two days.
	testdb.OzonSearchQuery(t, env.pool, env.cabinetID, 555, "чехол для iphone", now.AddDate(0, 0, -1), 100, 10, 1, 50, &position3)
	testdb.OzonSearchQuery(t, env.pool, env.cabinetID, 777, "чехол для iphone", now.AddDate(0, 0, -2), 300, 15, 2, 70, &position5)
	// A second query.
	testdb.OzonSearchQuery(t, env.pool, env.cabinetID, 555, "case magsafe", now.AddDate(0, 0, -1), 50, 5, 0, 20, nil)
	// Outside the 30-day window — excluded.
	testdb.OzonSearchQuery(t, env.pool, env.cabinetID, 555, "чехол для iphone", now.AddDate(0, 0, -40), 9999, 999, 99, 999, nil)
	// Product name enrichment joins by the query's top SKU (777 wins on views).
	testdb.OzonProduct(t, env.pool, env.cabinetID, 9002, 777, "ART-2", "Товар второй")

	stats, total, err := env.syncSvc.ListSearchQueries(ctx, env.workspaceID, env.cabinetID, nil, "", 30, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, stats, 2)

	// Ordered by views DESC: the aggregated query comes first.
	topQuery := stats[0]
	assert.Equal(t, "чехол для iphone", topQuery.Query)
	assert.Equal(t, int64(400), topQuery.Views)
	assert.Equal(t, int64(25), topQuery.Clicks)
	assert.Equal(t, int64(3), topQuery.Orders)
	assert.InDelta(t, 120.0, topQuery.SpendRub, 0.001)
	assert.InDelta(t, 6.25, topQuery.CTR, 0.001) // 25/400*100
	assert.Equal(t, int64(777), topQuery.SKU)
	assert.Equal(t, "Товар второй", topQuery.ProductName)
	require.NotNil(t, topQuery.AvgPosition)
	assert.InDelta(t, 4.5, *topQuery.AvgPosition, 0.001) // (3*100+5*300)/400

	secondQuery := stats[1]
	assert.Equal(t, "case magsafe", secondQuery.Query)
	assert.Nil(t, secondQuery.AvgPosition)
	assert.Empty(t, secondQuery.ProductName)

	// SKU filter: only rows of SKU 555 aggregate.
	sku := int64(555)
	skuStats, skuTotal, err := env.syncSvc.ListSearchQueries(ctx, env.workspaceID, env.cabinetID, &sku, "", 30, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), skuTotal)
	for _, stat := range skuStats {
		if stat.Query == "чехол для iphone" {
			assert.Equal(t, int64(100), stat.Views)
		}
	}

	// Substring filter.
	filtered, filteredTotal, err := env.syncSvc.ListSearchQueries(ctx, env.workspaceID, env.cabinetID, nil, "case", 30, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), filteredTotal)
	require.Len(t, filtered, 1)
	assert.Equal(t, "case magsafe", filtered[0].Query)

	// Tenancy gate.
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, _, err = env.syncSvc.ListSearchQueries(ctx, otherWorkspace, env.cabinetID, nil, "", 30, 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestOzonReadService_ResolveOzonCabinet_Gates(t *testing.T) {
	t.Parallel()
	env := newOzonTestEnv(t, testdb.OzonCredentials())
	ctx := context.Background()

	cabinet, err := env.syncSvc.ResolveOzonCabinet(ctx, env.workspaceID, env.cabinetID)
	require.NoError(t, err)
	assert.Equal(t, env.cabinetID, cabinet.ID)

	// Foreign workspace → not found (no existence leak).
	otherWorkspace := testdb.Workspace(t, env.pool)
	_, err = env.syncSvc.ResolveOzonCabinet(ctx, otherWorkspace, env.cabinetID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))

	// WB cabinet → validation error.
	wbCabinetID := testdb.WBCabinet(t, env.pool, env.workspaceID)
	_, err = env.syncSvc.ResolveOzonCabinet(ctx, env.workspaceID, wbCabinetID)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrValidation))
}
