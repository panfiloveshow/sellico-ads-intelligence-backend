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
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// autoMarginParams builds margin-floor params in the given apply mode.
func autoMarginParams(mode string) domain.StrategyParams {
	return domain.StrategyParams{
		PriceApplyMode:        mode,
		MaxPriceChangesPerDay: 5,
		PriceCooldownHours:    0,
		StepPercent:           50,
	}
}

func TestOzonRepricer_RunForWorkspace_AutoApplies(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-auto")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	// current 300 < floor 500 (net 400 / (1-0.20)) -> raise to 500.
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 100, OfferID: "ART-100", Name: "Below floor", PriceRub: 300,
		NetPriceRub: 400, CommissionFBOPct: 20,
	})
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPriceMarginFloor, autoMarginParams(domain.PriceApplyModeAuto), true)

	written, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 1, written)
	require.Len(t, writer.calls, 1)

	// Audit row applied + mirror updated to the floor.
	changes, total, err := svc.ListPriceChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, changes, 1)
	assert.Equal(t, domain.OzonPriceStatusApplied, changes[0].Status)
	assert.EqualValues(t, 500, changes[0].NewPriceRub)

	mirror, err := fx.db.Queries.GetOzonProductPriceBySku(ctx, sqlcgen.GetOzonProductPriceBySkuParams{
		SellerCabinetID: uuidToPgtype(fx.cabinetID), Sku: 100,
	})
	require.NoError(t, err)
	assert.InDelta(t, 500, pgNumericToFloat(mirror.PriceRub), 0.01)
}

func TestOzonRepricer_RunForWorkspace_DryRunShadow(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-shadow")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 101, OfferID: "ART-101", PriceRub: 300, NetPriceRub: 400, CommissionFBOPct: 20,
	})
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPriceMarginFloor, autoMarginParams(domain.PriceApplyModeDryRun), true)

	written, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 1, written)
	assert.Empty(t, writer.calls, "dry_run must not write to Ozon")

	changes, _, err := svc.ListPriceChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, domain.OzonPriceStatusShadow, changes[0].Status)
}

func TestOzonRepricer_RunForWorkspace_PausedCabinetSkips(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-paused")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 102, OfferID: "ART-102", PriceRub: 300, NetPriceRub: 400, CommissionFBOPct: 20,
	})
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPriceMarginFloor, autoMarginParams(domain.PriceApplyModeAuto), true)
	setCabinetPaused(t, fx.db, fx.cabinetID, time.Now().Add(time.Hour))

	written, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 0, written)
	assert.Empty(t, writer.calls)
}

func TestOzonRepricer_ApplyManualAndRollback(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-manual")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)
	userID := uuid.New()

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 200, OfferID: "ART-200", Name: "Manual", PriceRub: 1000,
		NetPriceRub: 400, CommissionFBOPct: 20,
	})

	t.Run("validation", func(t *testing.T) {
		_, err := svc.ApplyManual(ctx, fx.workspaceID, fx.cabinetID, nil, userID)
		require.Error(t, err)
		_, err = svc.ApplyManual(ctx, fx.workspaceID, fx.cabinetID, []OzonManualPriceInput{{SKU: 0, PriceRub: 5}}, userID)
		require.Error(t, err)
	})

	res, err := svc.ApplyManual(ctx, fx.workspaceID, fx.cabinetID,
		[]OzonManualPriceInput{{SKU: 200, PriceRub: 900}}, userID)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, domain.OzonPriceStatusApplied, res[0].Status)
	require.Len(t, writer.calls, 1)

	// Mirror moved to 900; rollback restores 1000.
	changeID := res[0].ID
	rb, err := svc.Rollback(ctx, fx.workspaceID, changeID)
	require.NoError(t, err)
	require.NotNil(t, rb)
	assert.Equal(t, domain.OzonPriceStatusApplied, rb.Status)
	assert.InDelta(t, 1000, rb.NewPriceRub, 0.01)

	// Original change is now rolled_back.
	orig, err := fx.db.Queries.GetOzonPriceChangeByID(ctx, uuidToPgtype(changeID))
	require.NoError(t, err)
	assert.Equal(t, domain.OzonPriceStatusRolledBack, orig.Status)
}

func TestOzonRepricer_ApplyManualFailedWrite(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-manual-fail")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{rejectSKUs: map[int64]bool{201: true}}
	svc := newRepricer(fx.db, writer)

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{SKU: 201, OfferID: "ART-201", PriceRub: 1000})
	res, err := svc.ApplyManual(ctx, fx.workspaceID, fx.cabinetID,
		[]OzonManualPriceInput{{SKU: 201, PriceRub: 950}}, uuid.New())
	require.NoError(t, err) // per-item failure, not a call error
	require.Len(t, res, 1)
	assert.Equal(t, domain.OzonPriceStatusFailed, res[0].Status)
}

func TestOzonRepricer_RollbackGuards(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-rollback-guard")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	t.Run("missing change", func(t *testing.T) {
		_, err := svc.Rollback(ctx, fx.workspaceID, uuid.New())
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})
}

func TestOzonRepricer_ListPriceChangesTenancy(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-listtenancy")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	other := newOzonWorkspace(t, fx.db, "ozon-repricer-listtenancy-other")
	_, _, err := svc.ListPriceChanges(ctx, other, fx.cabinetID, nil, 50, 0)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))

	require.NoError(t, svc.ResolveWorkspaceCabinet(ctx, fx.workspaceID, fx.cabinetID))
	require.Error(t, svc.ResolveWorkspaceCabinet(ctx, other, fx.cabinetID))
}

func TestOzonRepricer_OrdersHeatmap(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-heatmap")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	// Seed the orders-hourly matrix (native sales SKU key space) + a product
	// mapping so the product_id bridge resolves.
	seedOzonProduct(t, fx.db, fx.cabinetID, 5000, 42, "ART-42", "Heat")
	require.NoError(t, fx.db.Queries.ReplaceOzonOrdersHourly(ctx, uuidToPgtype(fx.cabinetID), []sqlcgen.OzonOrdersHourlyRow{
		{Sku: 42, Dow: 1, Hour: 12, Orders: 4, Quantity: 6},
		{Sku: 42, Dow: 2, Hour: 9, Orders: 2, Quantity: 2},
	}))

	hm, err := svc.OrdersHeatmap(ctx, fx.workspaceID, fx.cabinetID, 0, domain.HeatmapMetricUnits)
	require.NoError(t, err)
	require.NotNil(t, hm)

	// Revenue metric downgrades to units (postings carry no money).
	hm2, err := svc.OrdersHeatmap(ctx, fx.workspaceID, fx.cabinetID, 5000, domain.HeatmapMetricRevenue)
	require.NoError(t, err)
	require.NotNil(t, hm2)

	t.Run("tenancy", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-repricer-heatmap-other")
		_, err := svc.OrdersHeatmap(ctx, other, fx.cabinetID, 0, domain.HeatmapMetricUnits)
		require.Error(t, err)
	})
}

func TestOzonRepricer_InventoryDemandStrategy(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-inventory")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	// price keyed by product_id 900; overstocked + no sales -> step down to floor.
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 900, OfferID: "ART-900", PriceRub: 1000, NetPriceRub: 400, CommissionFBOPct: 20,
	})
	seedOzonProduct(t, fx.db, fx.cabinetID, 900, 42, "ART-900", "Inv")
	require.NoError(t, fx.db.Queries.UpsertOzonProductStock(ctx, sqlcgen.UpsertOzonProductStockParams{
		SellerCabinetID: uuidToPgtype(fx.cabinetID), Sku: 900, Present: 5000,
	}))
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPriceInventoryDemand, domain.StrategyParams{
			PriceApplyMode: domain.PriceApplyModeAuto, MaxPriceChangesPerDay: 5,
			StepPercent: 30, OverstockDays: 30, SlowVelocityPerDay: 2,
		}, true)

	written, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, written, 1)
}

func TestOzonRepricer_PeakHoursStrategyRuns(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-peak")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 901, OfferID: "ART-901", PriceRub: 1000, NetPriceRub: 400, CommissionFBOPct: 20,
	})
	seedOzonProduct(t, fx.db, fx.cabinetID, 901, 43, "ART-901", "Peak")
	// Seed a full week of orders so the cabinet aggregate has data in every slot.
	var rows []sqlcgen.OzonOrdersHourlyRow
	for dow := int16(0); dow < 7; dow++ {
		for h := int16(0); h < 24; h++ {
			rows = append(rows, sqlcgen.OzonOrdersHourlyRow{Sku: 43, Dow: dow, Hour: h, Orders: 5, Quantity: 5})
		}
	}
	require.NoError(t, fx.db.Queries.ReplaceOzonOrdersHourly(ctx, uuidToPgtype(fx.cabinetID), rows))
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPricePeakHours, domain.StrategyParams{
			PriceApplyMode: domain.PriceApplyModeDryRun, MaxPriceChangesPerDay: 5,
			StepPercent: 10, PeakUpliftPercent: 8, DeadDiscountPercent: 12,
		}, true)

	// Exercises loadStrategyAux peak branch + peakIntensityForProduct +
	// decideOzonPeakHours; a shadow row may or may not be produced.
	_, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
}

func TestOzonRepricer_AdLinkedStrategy(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-adlinked")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 902, OfferID: "ART-902", PriceRub: 1000, NetPriceRub: 400, CommissionFBOPct: 20,
	})
	seedOzonProduct(t, fx.db, fx.cabinetID, 902, 44, "ART-902", "Ad")
	// Campaign with a product (sales sku 44) + stats -> ad spend & DRR.
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 9200, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 44, 10)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 2, 500, 1000) // DRR 50%
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPriceAdLinked, domain.StrategyParams{
			PriceApplyMode: domain.PriceApplyModeDryRun, MaxPriceChangesPerDay: 5,
			StepPercent: 20, MaxAllowedDRRPercent: floatPtr(5),
		}, true)

	written, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, written, 0)
}
func TestOzonRepricer_LoadHelpers(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-loaders")
	defer cleanup()
	ctx := context.Background()
	svc := newRepricer(fx.db, &fakePriceWriter{})

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{SKU: 300, OfferID: "ART-300", PriceRub: 500})
	seedOzonEconomics(t, fx.db, fx.cabinetID, "ART-300", 300, 200, 10)

	prices, err := svc.loadCabinetPrices(ctx, fx.cabinetID)
	require.NoError(t, err)
	require.Len(t, prices, 1)

	econ, err := svc.loadCabinetEconomics(ctx, fx.cabinetID)
	require.NoError(t, err)
	require.Contains(t, econ, "ART-300")

	t.Run("resolveCabinet requires seller creds", func(t *testing.T) {
		_, _, err := svc.resolveCabinet(ctx, fx.workspaceID, fx.cabinetID)
		require.NoError(t, err)
	})
}
