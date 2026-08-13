package service

import (
	"context"
	"encoding/json"
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

// seedUnderspendingCampaign seeds a campaign whose own ДРР (10%) sits below
// the 15% target, so the engine wants to RAISE the bid — the only situation in
// which the total-ДРР ceiling has anything to say.
func seedUnderspendingCampaign(t *testing.T, fx *ozonFixture, ozonID, sku int64) pgtype.UUID {
	t.Helper()
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, ozonID, ozonCampaignStateRunning, nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, sku, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 2000, 100, 5, 100, 1000)
	return c.ID
}

// TestOzonStrategy_TotalDRRCeilingBlocksIncrease and its control below are a
// pair: the control proves the engine genuinely wants to raise this bid, so
// the blocked case cannot pass vacuously.
func TestOzonStrategy_TotalDRRCeilingBlocksIncrease(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-strategy-total-drr")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{minBids: map[int64]float64{503: 1}}
	svc := newStrategyService(fx.db, perf)

	campaignID := seedUnderspendingCampaign(t, fx, 9700, 503)
	// Cabinet-wide: 100 ₽ spent against 1000 ₽ of total turnover → 10%.
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 503, time.Now().UTC().AddDate(0, 0, -1), 4, 1000)

	params := cpcTargetDRRParams(3)
	ceiling := 5.0
	params.MaxTotalDRRPercent = &ceiling

	strategyID := seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonCPCTargetDRR, params, true)
	seedOzonStrategyBinding(t, fx, strategyID, campaignID)

	applied, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 0, applied)
	assert.Equal(t, 0, perf.bidCalls, "ceiling reached: nothing may be written")

	changes, _, err := newCampaignActions(fx.db, perf).ListBidChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	assert.Empty(t, changes, "a blocked increase must not be recorded as a bid change")
}

func TestOzonStrategy_TotalDRRCeilingUnsetAllowsIncrease(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-strategy-total-drr-off")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{minBids: map[int64]float64{504: 1}}
	svc := newStrategyService(fx.db, perf)

	campaignID := seedUnderspendingCampaign(t, fx, 9701, 504)
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 504, time.Now().UTC().AddDate(0, 0, -1), 4, 1000)

	strategyID := seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonCPCTargetDRR, cpcTargetDRRParams(3), true) // no ceiling
	seedOzonStrategyBinding(t, fx, strategyID, campaignID)

	applied, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)
	assert.Equal(t, 1, applied)
	assert.Equal(t, 1, perf.bidCalls)
}

// TestOzonAttributedTurnover_NoDoubleCounting is the load-bearing property of
// the per-campaign split: two campaigns advertising the same SKU must together
// be attributed exactly that SKU's turnover, never twice.
func TestOzonAttributedTurnover_NoDoubleCounting(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-attribution")
	defer cleanup()
	ctx := context.Background()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)

	// One SKU, two campaigns, spend split 3:1.
	a := seedOzonCampaign(t, fx.db, fx.cabinetID, 9800, ozonCampaignStateRunning, nil, nil)
	b := seedOzonCampaign(t, fx.db, fx.cabinetID, 9801, ozonCampaignStateRunning, nil, nil)
	seedOzonCampaignProduct(t, fx.db, a.ID, 601, 20)
	seedOzonCampaignProduct(t, fx.db, b.ID, 601, 20)
	seedOzonCampaignStat(t, fx.db, a.ID, yesterday, 1000, 100, 5, 300, 500)
	seedOzonCampaignStat(t, fx.db, b.ID, yesterday, 1000, 100, 5, 100, 200)
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 601, yesterday, 10, 4000)

	rows := loadCampaignAttributedTurnover(ctx, fx.db.Queries, ozonTestLogger(), fx.cabinetID,
		time.Now().UTC().AddDate(0, 0, -7))
	require.Len(t, rows, 2)

	got := map[uuid.UUID]float64{}
	var sum float64
	for id, row := range rows {
		v := pgNumericToFloat(row.RevenueRub)
		got[id] = v
		sum += v
		assert.True(t, row.RevenueShared, "both campaigns share the SKU")
	}
	assert.InDelta(t, 4000, sum, 0.01, "the SKU's turnover must be split, not duplicated")
	assert.InDelta(t, 3000, got[uuidFromPgtype(a.ID)], 0.01) // 300/400 of 4000
	assert.InDelta(t, 1000, got[uuidFromPgtype(b.ID)], 0.01) // 100/400 of 4000
}

// A SKU nobody advertises contributes nothing, and a lone campaign keeps the
// whole turnover of its SKUs.
func TestOzonAttributedTurnover_SoleCampaignKeepsAll(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-attribution-solo")
	defer cleanup()
	ctx := context.Background()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)

	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 9810, ozonCampaignStateRunning, nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 602, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, yesterday, 1000, 100, 5, 250, 900)
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 602, yesterday, 10, 4000)
	// 603 sells but is not in any campaign — must not reach any denominator.
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 603, yesterday, 10, 9999)

	rows := loadCampaignAttributedTurnover(ctx, fx.db.Queries, ozonTestLogger(), fx.cabinetID,
		time.Now().UTC().AddDate(0, 0, -7))
	require.Len(t, rows, 1)
	row := rows[uuidFromPgtype(c.ID)]
	assert.InDelta(t, 4000, pgNumericToFloat(row.RevenueRub), 0.01)
	assert.False(t, row.RevenueShared)
}

// The second target must be able to override a campaign-ДРР increase: the
// campaign looks cheap on its own attributed revenue but expensive against the
// turnover it actually moves.
func TestOzonStrategy_TotalDRRTargetOverridesIncrease(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-strategy-total-target")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{minBids: map[int64]float64{505: 1}}
	svc := newStrategyService(fx.db, perf)

	campaignID := seedUnderspendingCampaign(t, fx, 9702, 505) // campaign ДРР 10% vs target 15% → raise
	// Attributed turnover of just 400 ₽ against 100 ₽ spend → total ДРР 25%,
	// far above the 5% second target, so the lower bid must win.
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 505, time.Now().UTC().AddDate(0, 0, -1), 2, 400)

	params := cpcTargetDRRParams(3)
	params.TargetTotalDRRPercent = 5

	strategyID := seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID,
		domain.StrategyTypeOzonCPCTargetDRR, params, true)
	seedOzonStrategyBinding(t, fx, strategyID, campaignID)

	_, err := svc.RunForWorkspace(ctx, fx.workspaceID)
	require.NoError(t, err)

	changes, _, err := newCampaignActions(fx.db, perf).ListBidChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	require.NotNil(t, changes[0].NewBidRub)
	require.NotNil(t, changes[0].OldBidRub)
	assert.Less(t, *changes[0].NewBidRub, *changes[0].OldBidRub,
		"campaign ДРР alone wanted a raise; the total-ДРР target must turn it into a cut")
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
		{OfferID: "", CostPrice: 50},     // skipped (no offer)
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

// TestStrategyDeleteIsAuditedAndRestorable — удаление стратегии должно
// оставлять в журнале снимок, достаточный для её воссоздания.
//
// Поводом стал реальный случай: стратегия ozon_ai_autopilot исчезла, ИИ
// замолчал, и двое суток ушло на выяснение причины — потому что операции со
// стратегиями не журналировались вообще.
func TestStrategyDeleteIsAuditedAndRestorable(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "strategy-audit")
	defer cleanup()
	ctx := context.Background()
	svc := NewStrategyService(fx.db.Queries)
	actor := uuid.New()

	created, err := svc.Create(ctx, fx.workspaceID, actor, domain.Strategy{
		SellerCabinetID: fx.cabinetID,
		Name:            "ИИ-автопилот",
		Type:            domain.StrategyTypeOzonAIAutopilot,
		IsActive:        true,
		Params:          domain.StrategyParams{TargetACoS: 15, AutomationLevel: 1, MinBid: 5, MaxBid: 500},
	})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(ctx, fx.workspaceID, created.ID, actor))

	var action, entityType string
	var userID pgtype.UUID
	var meta []byte
	err = fx.db.Pool.QueryRow(ctx,
		`SELECT action, entity_type, user_id, metadata FROM audit_logs
		  WHERE entity_id = $1 AND action = 'delete_strategy'`, uuidToPgtype(created.ID)).
		Scan(&action, &entityType, &userID, &meta)
	require.NoError(t, err, "удаление обязано попасть в журнал")
	assert.Equal(t, "strategy", entityType)
	assert.Equal(t, actor, uuidFromPgtype(userID), "должно быть видно, КТО удалил")

	// Снимка должно хватать, чтобы создать стратегию заново.
	var snapshot struct {
		Name     string                `json:"name"`
		Type     string                `json:"type"`
		IsActive bool                  `json:"is_active"`
		Params   domain.StrategyParams `json:"params"`
	}
	require.NoError(t, json.Unmarshal(meta, &snapshot))
	assert.Equal(t, "ИИ-автопилот", snapshot.Name)
	assert.Equal(t, domain.StrategyTypeOzonAIAutopilot, snapshot.Type)
	assert.True(t, snapshot.IsActive)
	assert.EqualValues(t, 15, snapshot.Params.TargetACoS)
	assert.EqualValues(t, 1, snapshot.Params.AutomationLevel)
}

// Изменение уровня автоматизации меняет поведение молча — в журнале обязано
// остаться прежнее значение.
func TestStrategyUpdateRecordsPreviousLevel(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "strategy-audit-upd")
	defer cleanup()
	ctx := context.Background()
	svc := NewStrategyService(fx.db.Queries)
	actor := uuid.New()

	created, err := svc.Create(ctx, fx.workspaceID, actor, domain.Strategy{
		SellerCabinetID: fx.cabinetID,
		Name:            "тест",
		Type:            domain.StrategyTypeOzonCPCTargetDRR,
		IsActive:        true,
		Params:          domain.StrategyParams{TargetACoS: 15, AutomationLevel: 1, MinBid: 5, MaxBid: 500},
	})
	require.NoError(t, err)

	_, err = svc.Update(ctx, fx.workspaceID, created.ID, actor, domain.Strategy{
		SellerCabinetID: fx.cabinetID,
		Name:            "тест",
		Type:            domain.StrategyTypeOzonCPCTargetDRR,
		IsActive:        true,
		Params:          domain.StrategyParams{TargetACoS: 15, AutomationLevel: 3, MinBid: 5, MaxBid: 500},
	})
	require.NoError(t, err)

	var meta []byte
	require.NoError(t, fx.db.Pool.QueryRow(ctx,
		`SELECT metadata FROM audit_logs WHERE entity_id = $1 AND action = 'update_strategy'`,
		uuidToPgtype(created.ID)).Scan(&meta))

	var record struct {
		WasAutomationLevel int `json:"was_automation_level"`
		Params             struct {
			AutomationLevel int `json:"automation_level"`
		} `json:"params"`
	}
	require.NoError(t, json.Unmarshal(meta, &record))
	assert.Equal(t, 1, record.WasAutomationLevel, "прежний уровень «Тень» обязан остаться в журнале")
	assert.Equal(t, 3, record.Params.AutomationLevel)
}
