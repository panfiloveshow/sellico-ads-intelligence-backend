package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestOzon_Constructors(t *testing.T) {
	db, cleanup := newOzonTestDB(t)
	defer cleanup()
	logger := ozonTestLogger()

	require.NotNil(t, NewOzonRepricerService(db.Queries, nil, ozonTestKey(), logger))
	require.NotNil(t, NewOzonCampaignActionsService(db.Queries, nil, ozonTestKey(), logger))
	require.NotNil(t, NewOzonSyncService(db.Queries, nil, nil, ozonTestKey(), logger))
	require.NotNil(t, NewOzonStrategyService(db.Queries, nil, nil, ozonTestKey(), logger))
	require.NotNil(t, NewOzonAIManagerService(db.Queries, nil, nil, nil, ozonTestKey(), logger))
	require.NotNil(t, NewSellicoEconomicsSyncService(db.Queries, nil, "", nil, "", logger))
}

func TestOzonStrategy_RunForWorkspaceNoop(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-strategy-noop")
	defer cleanup()
	ctx := context.Background()
	svc := NewOzonStrategyService(fx.db.Queries, nil, nil, ozonTestKey(), ozonTestLogger())

	// Only a repricer strategy exists — the bid sweep must skip it (no perf call).
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPriceMarginFloor, domain.StrategyParams{}, true)

	applied, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 0, applied)
}

func seedOzonStrategyBinding(t *testing.T, fx *ozonFixture, strategyID uuid.UUID, campaignID pgtype.UUID) {
	t.Helper()
	_, err := fx.db.Queries.CreateOzonStrategyBindingInWorkspace(context.Background(), sqlcgen.CreateOzonStrategyBindingInWorkspaceParams{
		OzonCampaignID: campaignID,
		StrategyID:     uuidToPgtype(strategyID),
		WorkspaceID:    uuidToPgtype(fx.workspaceID),
	})
	require.NoError(t, err)
}

// cpcTargetDRRParams builds params where a high actual DRR forces a bid cut.
func cpcTargetDRRParams(level int) domain.StrategyParams {
	return domain.StrategyParams{
		TargetACoS: 15, AutomationLevel: level, MinClicks: 10, LookbackDays: 7,
		MaxChangePercent: 15, MinBid: 1, MaxBid: 5000, MaxChangesPerDay: 5,
	}
}

func seedRunningCampaignWithBids(t *testing.T, fx *ozonFixture, ozonID int64, sku int64, bid float64) pgtype.UUID {
	t.Helper()
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, ozonID, ozonCampaignStateRunning, nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, sku, bid)
	// High DRR (spend == revenue -> ACoS 100%) with enough clicks.
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 2000, 100, 5, 1000, 1000)
	return c.ID
}

func TestOzonStrategy_ShadowRun(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-strategy-shadow")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{}
	svc := newStrategyService(fx.db, perf)

	campaignID := seedRunningCampaignWithBids(t, fx, 9500, 501, 20)
	strategyID := seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonCPCTargetDRR, cpcTargetDRRParams(1), true) // shadow
	seedOzonStrategyBinding(t, fx, strategyID, campaignID)

	applied, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 0, applied) // shadow: no live application
	assert.Equal(t, 0, perf.bidCalls, "shadow must not call the API")

	// A shadow bid change was recorded.
	changes, total, err := newCampaignActions(fx.db, perf).ListBidChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, int64(1))
	require.NotEmpty(t, changes)
	assert.Equal(t, domain.OzonBidStatusShadow, changes[0].Status)
}

func TestOzonStrategy_LiveRun(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-strategy-live")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{minBids: map[int64]float64{502: 1}}
	svc := newStrategyService(fx.db, perf)

	campaignID := seedRunningCampaignWithBids(t, fx, 9600, 502, 20)
	strategyID := seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonCPCTargetDRR, cpcTargetDRRParams(3), true) // autopilot
	seedOzonStrategyBinding(t, fx, strategyID, campaignID)

	applied, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 1, applied)
	assert.Equal(t, 1, perf.bidCalls)

	changes, _, err := newCampaignActions(fx.db, perf).ListBidChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	assert.Equal(t, domain.OzonBidStatusApplied, changes[0].Status)
}

// fakeSellicoEconomics implements sellicoEconomicsClient.
type fakeSellicoEconomics struct {
	rows []sellico.WBUnitEconomics
	err  error
}

func (f *fakeSellicoEconomics) ListWBUnitEconomics(_ context.Context, _, _, _ string) ([]sellico.WBUnitEconomics, error) {
	return f.rows, f.err
}

func TestSellicoEconomics_ConfiguredAndSyncOzon(t *testing.T) {
	db, cleanup := newOzonTestDB(t)
	defer cleanup()
	ctx := context.Background()
	ws := newOzonWorkspace(t, db, "sellico-econ-ozon")

	// Ozon cabinet with a Sellico integration id.
	enc, err := encryptOzonCredentials(pricesOnlyOzonCreds(), ozonTestKey())
	require.NoError(t, err)
	var cabID uuid.UUID
	err = db.Pool.QueryRow(ctx, `
		INSERT INTO seller_cabinets (workspace_id, name, encrypted_token, marketplace, encrypted_credentials, status, external_integration_id)
		VALUES ($1,'Ozon','tok','ozon',$2,'active','integ-1') RETURNING id`,
		uuidToPgtype(ws), enc).Scan(&cabID)
	require.NoError(t, err)

	client := &fakeSellicoEconomics{rows: []sellico.WBUnitEconomics{
		{OfferID: "ART-1", CostPrice: 100, Source: "sellico"},
		{OfferID: "ART-2", CostPrice: 250},
		{OfferID: "", CostPrice: 50},  // skipped (no offer)
		{OfferID: "ART-3", CostPrice: 0}, // skipped (no cost)
	}}

	t.Run("not configured returns 0", func(t *testing.T) {
		svc := NewSellicoEconomicsSyncService(db.Queries, nil, "", nil, "", ozonTestLogger())
		assert.False(t, svc.Configured())
		n, err := svc.SyncWorkspace(ctx, ws)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})

	svc := NewSellicoEconomicsSyncService(db.Queries, client, "service-token", nil, "/export", ozonTestLogger())
	assert.True(t, svc.Configured())

	imported, err := svc.SyncWorkspace(ctx, ws)
	require.NoError(t, err)
	assert.Equal(t, 2, imported)

	// Mirrored into ozon_product_economics.
	econRows, err := db.Queries.ListOzonProductEconomicsByCabinet(ctx, uuidToPgtype(cabID))
	require.NoError(t, err)
	assert.Len(t, econRows, 2)
}

func TestOzonSync_SyncPhrasesAllCabinetsPricesOnly(t *testing.T) {
	db, cleanup := newOzonTestDB(t)
	defer cleanup()
	ctx := context.Background()
	svc := newSyncService(db)

	ws := newOzonWorkspace(t, db, "ozon-phrases-all")
	seedOzonCabinet(t, db, ws, pricesOnlyOzonCreds())

	// Prices-only cabinet: SyncPhrases fails on the performance gate before any
	// client call, so the sweep returns a joined error without panicking.
	err := svc.SyncPhrasesAllCabinets(ctx)
	require.Error(t, err)
}

func TestOzonSchedule_FailedExecutionMarksEntry(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-sched-fail")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{rejectSKUs: map[int64]bool{710: true}}
	svc := newRepricer(fx.db, writer)

	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{SKU: 710, OfferID: "ART-710", PriceRub: 1000})
	start := time.Now().Add(time.Minute)
	entry, err := svc.CreateSchedule(ctx, fx.workspaceID, fx.cabinetID, domain.OzonPriceScheduleInput{
		SKU: 710, ScheduledPriceRub: 850, StartsAt: start,
	})
	require.NoError(t, err)

	executed, err := svc.ExecuteDueSchedules(ctx, start.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 0, executed)

	fresh, err := fx.db.Queries.GetOzonPriceScheduleEntry(ctx, uuidToPgtype(entry.ID))
	require.NoError(t, err)
	assert.Equal(t, domain.OzonScheduleStatusFailed, fresh.Status)
	assert.True(t, fresh.Error.Valid)
}

func TestOzonRepricer_CompetitorFollowAuto(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-repricer-competitor")
	defer cleanup()
	ctx := context.Background()
	writer := &fakePriceWriter{}
	svc := newRepricer(fx.db, writer)

	// current 1000, floor 500 (net 400/comm 20), competitor min 600 -> step down.
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 810, OfferID: "ART-810", PriceRub: 1000, NetPriceRub: 400, CommissionFBOPct: 20,
		OzonIndexMinRub: 600,
	})
	seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonPriceCompetitorFollow, domain.StrategyParams{
			PriceApplyMode: domain.PriceApplyModeAuto, MaxPriceChangesPerDay: 5,
			StepPercent: 50, UndercutPercent: 2,
		}, true)

	written, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 1, written)
	require.Len(t, writer.calls, 1)

	changes, _, err := svc.ListPriceChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, domain.OzonPriceStatusApplied, changes[0].Status)
	assert.Less(t, changes[0].NewPriceRub, 1000.0)
}
