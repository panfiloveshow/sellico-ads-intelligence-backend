package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

func TestOzonSync_ResolveOzonCabinet(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-resolve")
	defer cleanup()
	svc := newSyncService(fx.db)
	ctx := context.Background()

	t.Run("owned cabinet resolves", func(t *testing.T) {
		cab, err := svc.ResolveOzonCabinet(ctx, fx.workspaceID, fx.cabinetID)
		require.NoError(t, err)
		assert.Equal(t, fx.cabinetID, cab.ID)
	})

	t.Run("foreign workspace is NotFound", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-resolve-other")
		_, err := svc.ResolveOzonCabinet(ctx, other, fx.cabinetID)
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})

	t.Run("missing cabinet is NotFound", func(t *testing.T) {
		_, err := svc.ResolveOzonCabinet(ctx, fx.workspaceID, uuid.New())
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})

	t.Run("WB cabinet is rejected", func(t *testing.T) {
		wb := seedWBCabinet(t, fx.db, fx.workspaceID)
		_, err := svc.ResolveOzonCabinet(ctx, fx.workspaceID, wb)
		require.Error(t, err)
	})
}

func TestOzonSync_ListCampaignsWithStats(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-list-campaigns")
	defer cleanup()
	svc := newSyncService(fx.db)
	ctx := context.Background()

	daily := int64(500)
	c1 := seedOzonCampaign(t, fx.db, fx.cabinetID, 1001, "CAMPAIGN_STATE_RUNNING", &daily, nil)
	c2 := seedOzonCampaign(t, fx.db, fx.cabinetID, 1002, "CAMPAIGN_STATE_INACTIVE", nil, nil)
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	seedOzonCampaignStat(t, fx.db, c1.ID, yesterday, 1000, 100, 10, 200, 1000) // DRR = 20%
	_ = c2

	items, total, err := svc.ListCampaignsWithStats(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)

	byID := map[int64]domain.OzonCampaignWithStats{}
	for _, it := range items {
		byID[it.OzonCampaignID] = it
	}
	got := byID[1001]
	assert.EqualValues(t, 1000, got.StatsViews)
	assert.EqualValues(t, 200, got.StatsSpend)
	assert.EqualValues(t, 1000, got.StatsRevenue)
	assert.InDelta(t, 20.0, got.DRR, 0.001)

	t.Run("tenancy gate", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-list-campaigns-other")
		_, _, err := svc.ListCampaignsWithStats(ctx, other, fx.cabinetID, 50, 0)
		require.Error(t, err)
	})
}

func TestOzonSync_GetCampaignAndStats(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-get-campaign")
	defer cleanup()
	svc := newSyncService(fx.db)
	ctx := context.Background()

	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 2001, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 555, 12.5)
	seedOzonProduct(t, fx.db, fx.cabinetID, 999, 555, "ART-1", "Product One")
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -2), 10, 1, 0, 5, 0)

	campaign, products, err := svc.GetCampaign(ctx, fx.workspaceID, uuid.UUID(c.ID.Bytes))
	require.NoError(t, err)
	assert.EqualValues(t, 2001, campaign.OzonCampaignID)
	require.Len(t, products, 1)
	assert.EqualValues(t, 555, products[0].SKU)
	assert.Equal(t, "Product One", products[0].Name)
	assert.Equal(t, "ART-1", products[0].OfferID)

	stats, err := svc.ListCampaignStats(ctx, fx.workspaceID, uuid.UUID(c.ID.Bytes),
		time.Now().UTC().AddDate(0, 0, -7), time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, stats, 1)

	t.Run("foreign workspace hides campaign", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-get-campaign-other")
		_, _, err := svc.GetCampaign(ctx, other, uuid.UUID(c.ID.Bytes))
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})

	t.Run("missing campaign is NotFound", func(t *testing.T) {
		_, _, err := svc.GetCampaign(ctx, fx.workspaceID, uuid.New())
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})
}

func TestOzonSync_ListPrices(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-list-prices")
	defer cleanup()
	svc := newSyncService(fx.db)
	ctx := context.Background()

	// Row with Ozon's own net_price -> floor from the row economics.
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 10, OfferID: "ART-10", Name: "Ten", PriceRub: 1000,
		NetPriceRub: 400, CommissionFBOPct: 20, AcquiringPct: 15,
	})
	// Row without net_price -> economics fallback provides the floor.
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 11, OfferID: "ART-11", Name: "Eleven", PriceRub: 800,
		CommissionFBOPct: 20, AcquiringPct: 10,
	})
	seedOzonEconomics(t, fx.db, fx.cabinetID, "ART-11", 11, 300, 50)

	items, total, err := svc.ListPrices(ctx, fx.workspaceID, fx.cabinetID, "", 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)

	byOffer := map[string]domain.OzonProductPrice{}
	for _, it := range items {
		byOffer[it.OfferID] = it
	}
	require.NotNil(t, byOffer["ART-10"].FloorRub)
	assert.Greater(t, *byOffer["ART-10"].FloorRub, 0.0)
	// Economics fallback floor: (300+50+10) / (1-0.20) = 450
	require.NotNil(t, byOffer["ART-11"].FloorRub)
	assert.InDelta(t, 450.0, *byOffer["ART-11"].FloorRub, 0.5)

	t.Run("search filter narrows", func(t *testing.T) {
		items, total, err := svc.ListPrices(ctx, fx.workspaceID, fx.cabinetID, "ART-10", 50, 0)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, items, 1)
		assert.Equal(t, "ART-10", items[0].OfferID)
	})

	t.Run("tenancy gate", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-list-prices-other")
		_, _, err := svc.ListPrices(ctx, other, fx.cabinetID, "", 50, 0)
		require.Error(t, err)
	})
}

func TestOzonSync_ListSearchQueries(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-search-queries")
	defer cleanup()
	svc := newSyncService(fx.db)
	ctx := context.Background()

	seedOzonProduct(t, fx.db, fx.cabinetID, 900, 42, "ART-42", "Widget")
	today := time.Now().UTC()
	err := fx.db.Pool.QueryRow(ctx, `SELECT 1`).Scan(new(int))
	require.NoError(t, err)
	// Upsert two days of the same query for SKU 42.
	for i := 0; i < 2; i++ {
		err := fx.db.Queries.UpsertOzonSearchQuery(ctx, ozonSearchQueryRow(fx.cabinetID, 42, "widget red", today.AddDate(0, 0, -i), 500, 50, 5, 100))
		require.NoError(t, err)
	}

	rows, total, err := svc.ListSearchQueries(ctx, fx.workspaceID, fx.cabinetID, nil, "", 30, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, "widget red", rows[0].Query)
	assert.EqualValues(t, 1000, rows[0].Views) // aggregated over 2 days
	assert.Equal(t, "Widget", rows[0].ProductName)
	assert.InDelta(t, 10.0, rows[0].CTR, 0.01) // 100/1000*100

	t.Run("sku filter", func(t *testing.T) {
		sku := int64(42)
		_, total, err := svc.ListSearchQueries(ctx, fx.workspaceID, fx.cabinetID, &sku, "", 30, 50, 0)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
	})

	t.Run("tenancy gate", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-search-queries-other")
		_, _, err := svc.ListSearchQueries(ctx, other, fx.cabinetID, nil, "", 30, 50, 0)
		require.Error(t, err)
	})
}

func TestOzonSync_ClientFreeHelpers(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sync-helpers")
	defer cleanup()
	svc := newSyncService(fx.db)
	ctx := context.Background()

	ids, err := svc.ListOzonCabinetIDs(ctx)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	assert.Equal(t, fx.cabinetID, ids[0])

	cab, err := svc.loadOzonCabinet(ctx, fx.cabinetID)
	require.NoError(t, err)
	creds, err := svc.credentials(*cab)
	require.NoError(t, err)
	assert.True(t, creds.HasSellerAPI())
	assert.True(t, creds.HasPerformanceAPI())
}

func TestOzonSync_PricesOnlyCabinetSkipsPerformanceSteps(t *testing.T) {
	db, cleanup := newOzonTestDB(t)
	defer cleanup()
	svc := newSyncService(db)
	ctx := context.Background()

	ws := newOzonWorkspace(t, db, "ozon-prices-only")
	cabID := seedOzonCabinet(t, db, ws, pricesOnlyOzonCreds())
	cab, err := svc.loadOzonCabinet(ctx, cabID)
	require.NoError(t, err)

	// Performance-gated steps refuse without perf creds (no client call).
	require.Error(t, svc.SyncCampaigns(ctx, *cab))
	require.Error(t, svc.SyncStats(ctx, *cab))
	require.Error(t, svc.SyncPhrases(ctx, *cab))
}
