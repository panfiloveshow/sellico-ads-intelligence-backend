package service

import (
	"context"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/llm"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAIExecutionFixture(t *testing.T) *ozonFixture {
	t.Helper()
	pool := testdb.New(t)
	db := &testutil.TestDB{Pool: pool, Queries: sqlcgen.New(pool)}
	ws := newOzonWorkspace(t, db, "ai-execution")
	return &ozonFixture{db: db, workspaceID: ws, cabinetID: seedOzonCabinet(t, db, ws, fullOzonCreds())}
}

// The screenshot's failed CPO switch must never stop the original campaign
// merely because the pause is individually valid. The whole plan is checked
// before any external write.
func TestAIExecution_UnavailableCPOPreventsPartialCampaignPlan(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 36154132, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 5467997633, 5)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 4, 300, 1000)
	id := seedAIStrategy(t, fx, 3)
	_, err := fx.db.Pool.Exec(ctx, `INSERT INTO strategy_bindings(strategy_id,ozon_campaign_id) VALUES($1,$2)`, id, c.ID)
	require.NoError(t, err)
	row, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(id))
	require.NoError(t, err)
	client := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{submitProposalsResponse("Предлагаю остановить кампанию и перейти на CPO без потери заказов", []map[string]any{
		{"action_type": "campaign_pause", "target": map[string]any{"ozon_campaign_id": 36154132}, "rationale": "CPO вместо CPC"},
		{"action_type": "cpo_enable", "target": map[string]any{"sku": 5467997633}, "rationale": "CPO"},
	})}}
	perf := &fakePerfClient{}
	mgr := newAIManagerWithPerf(fx.db, client, perf, perf)
	require.NoError(t, mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(row), domain.AIRunTriggerManual))
	assert.Empty(t, perf.deactivateCalls, "must not execute the surviving fragment of an invalid plan")
	assert.Zero(t, perf.enableCalls)
	current, err := fx.db.Queries.GetOzonCampaignByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "CAMPAIGN_STATE_RUNNING", current.State.String)
	runs, _, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 10, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.NotContains(t, runs[0].Summary, "без потери заказов", "execution summary must not promise an unexecuted effect")
}

func TestAIExecution_RuntimeFailureStopsDependentPause(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	budget := int64(10000)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 321, "CAMPAIGN_STATE_RUNNING", nil, &budget)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 4, 300, 1000)
	id := seedAIStrategy(t, fx, 3)
	row, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(id))
	require.NoError(t, err)
	client := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{submitProposalsResponse("Всё выполнено, продажи сохранены", []map[string]any{
		{"action_type": "campaign_pause", "target": map[string]any{"ozon_campaign_id": 321}},
		{"action_type": "budget_change", "target": map[string]any{"ozon_campaign_id": 321}, "new_value": 8000},
	})}}
	perf := &fakePerfClient{writeErr: assertAnErr}
	mgr := newAIManagerWithPerf(fx.db, client, perf, perf)
	require.NoError(t, mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(row), domain.AIRunTriggerManual))
	assert.Equal(t, []int64{321}, perf.budgetCalls)
	assert.Empty(t, perf.deactivateCalls)
	runs, _, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 10, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Contains(t, runs[0].Summary, "Выполнено: 0.")
	assert.Contains(t, runs[0].Summary, "Ошибок: 1")
	assert.NotContains(t, runs[0].Summary, "продажи сохранены")
}

func TestAIExecution_RepeatedPauseDoesNotCountAsChange(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 322, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 4, 300, 1000)
	id := seedAIStrategy(t, fx, 3)
	row, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(id))
	require.NoError(t, err)
	response := submitProposalsResponse("Остановлено", []map[string]any{{"action_type": "campaign_pause", "target": map[string]any{"ozon_campaign_id": 322}}})
	client := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{response, response}}
	perf := &fakePerfClient{}
	mgr := newAIManagerWithPerf(fx.db, client, perf, perf)
	for i := 0; i < 2; i++ {
		require.NoError(t, mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(row), domain.AIRunTriggerManual))
	}
	assert.Equal(t, []int64{322}, perf.deactivateCalls)
	runs, _, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 10, 0)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Contains(t, runs[0].Summary, "Выполнено: 0.")
	event, err := fx.db.Queries.GetLastOzonAIStateEvent(ctx, uuidToPgtype(fx.cabinetID), c.ID, 322)
	require.NoError(t, err)
	assert.Equal(t, domain.BidSourceAI, event.Source)
}

func TestAIExecution_LifecycleCooldownUsesCanonicalCampaignTarget(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 323, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 4, 300, 1000)
	id := seedAIStrategy(t, fx, 3)
	row, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(id))
	require.NoError(t, err)
	client := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{submitProposalsResponse("Пауза", []map[string]any{{"action_type": "campaign_pause", "target": map[string]any{"ozon_campaign_id": 323}}})}}
	mgr := newAIManager(fx.db, client)
	require.NoError(t, mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(row), domain.AIRunTriggerManual))
	p := aiProposal{ActionType: domain.AIActionCampaignActivate, Target: domain.AIDecisionTarget{OzonCampaignID: 323, SKU: 999}}
	assert.NotEmpty(t, mgr.decisionCooldownReason(ctx, fx.cabinetID, p, rowParams(row), time.Now().UTC()))
}

func rowParams(row sqlcgen.Strategy) domain.StrategyParams {
	return strategyFromSqlc(row).Params.Merged()
}

func TestAIExecution_ManualStopOverridesHistoricalAIPause(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 324, "CAMPAIGN_STATE_RUNNING", nil, nil)
	mgr := newAIManager(fx.db, &fakeLLM{})
	require.NoError(t, mgr.actions.SetCampaignStateWithSource(ctx, fx.workspaceID, uuidFromPgtype(c.ID), false, domain.BidSourceAI))
	require.NoError(t, mgr.actions.ActivateCampaign(ctx, fx.workspaceID, uuidFromPgtype(c.ID)))
	require.NoError(t, mgr.actions.DeactivateCampaign(ctx, fx.workspaceID, uuidFromPgtype(c.ID)))
	p := aiProposal{ActionType: domain.AIActionCampaignActivate, Target: domain.AIDecisionTarget{OzonCampaignID: 324}}
	reason := mgr.activationObservationReason(ctx, fx.cabinetID, c, p, &aiCabinetData{}, domain.StrategyParams{}.Merged(), time.Now().UTC())
	assert.Contains(t, reason, "Последняя подтверждённая остановка не принадлежит ИИ")
}

func TestAIExecution_UnverifiedCPOBidUnavailableEvenWithProduct(t *testing.T) {
	fx := newAIExecutionFixture(t)
	mgr := newAIManager(fx.db, &fakeLLM{})
	value := 200.0
	data := &aiCabinetData{cpoBySKU: map[int64]domain.OzonCPOProduct{5: {SKU: 5}}}
	p := aiProposal{ActionType: domain.AIActionCPOBid, Target: domain.AIDecisionTarget{SKU: 5}, NewValue: &value}
	assert.Equal(t, aiCPOBidUnavailableReason, mgr.evaluateProposal(context.Background(), fx.cabinetID, domain.StrategyParams{}.Merged(), p, data))
	assert.NotContains(t, string(aiToolsForContext(data)[0].Function.Parameters), `"cpo_bid"`)
	assert.Contains(t, string(aiToolsForContext(data)[0].Function.Parameters), `"cpo_enable"`)
	assert.NotContains(t, string(aiToolsForContext(&aiCabinetData{})[0].Function.Parameters), `"cpo_enable"`)
}

func TestAIExecution_CPOEnableRequiresMeasuredAffordableCost(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	params := domain.StrategyParams{MaxBid: 500, TargetACoS: 15}.Merged()
	cases := []struct {
		name    string
		mutate  func(*aiCabinetData)
		allowed bool
	}{
		{"measured affordable cost", func(*aiCabinetData) {}, true},
		{"unmeasured turnover", func(d *aiCabinetData) { d.totalDRR.Status = totalDRRStatusNoData }, false},
		{"unknown margin", func(d *aiCabinetData) { e := d.economicsBySKU[5]; e.MarginPct = nil; d.economicsBySKU[5] = e }, false},
		{"cost above margin headroom", func(d *aiCabinetData) { p := d.cpoBySKU[5]; p.BidPriceRub = ptr(200); d.cpoBySKU[5] = p }, false},
		{"cost at ceiling", func(d *aiCabinetData) { p := d.cpoBySKU[5]; p.BidPriceRub = ptr(50); d.cpoBySKU[5] = p }, false},
		{"unconfirmed cost", func(d *aiCabinetData) { p := d.cpoBySKU[5]; p.BidPriceRub = nil; d.cpoBySKU[5] = p }, false},
		{"stale CPO", func(d *aiCabinetData) {
			p := d.cpoBySKU[5]
			p.UpdatedAt = time.Now().Add(-72 * time.Hour)
			d.cpoBySKU[5] = p
		}, false},
		{"no stock", func(d *aiCabinetData) { d.stockBySKU[5] = 0 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &aiCabinetData{
				cpoBySKU:       map[int64]domain.OzonCPOProduct{5: {SKU: 5, BidPriceRub: ptr(40), UpdatedAt: time.Now()}},
				economicsBySKU: map[int64]aiPackEconomics{5: {SKU: 5, PriceRub: ptr(1000), MarginPct: ptr(40), CommissionFBOPct: ptr(15)}},
				stockBySKU:     map[int64]int64{5: 100}, totalDRRCeiling: ptr(10),
			}
			d.totalDRR.Status = totalDRRStatusOK
			d.totalDRR.Value = 5
			tc.mutate(d)
			reason := (&OzonAIManagerService{}).cpoEconomicsGuardReason(aiProposal{ActionType: domain.AIActionCPOEnable, Target: domain.AIDecisionTarget{SKU: 5}}, d, params)
			if tc.allowed {
				assert.Empty(t, reason)
			} else {
				assert.NotEmpty(t, reason)
			}
		})
	}
}

func TestAIExecution_RestartNeedsObservationAndConfirmedRepair(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 325, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 5, 100)
	mgr := newAIManager(fx.db, &fakeLLM{})
	require.NoError(t, mgr.actions.SetCampaignStateWithSource(ctx, fx.workspaceID, uuidFromPgtype(c.ID), false, domain.BidSourceAI))
	p := aiProposal{ActionType: domain.AIActionCampaignActivate, Target: domain.AIDecisionTarget{OzonCampaignID: 325}}
	params := domain.StrategyParams{}.Merged()
	d := &aiCabinetData{statsByCampaign: map[int64][]aiPackDay{325: {{time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), int64(1000), int64(100), 300.0, int64(4), 1000.0}}}}
	assert.Contains(t, mgr.activationObservationReason(ctx, fx.cabinetID, c, p, d, params, time.Now().UTC()), "идёт наблюдение")
	_, err := fx.db.Pool.Exec(ctx, `UPDATE audit_logs SET created_at=now()-interval '30 days' WHERE entity_id=$1`, c.ID)
	require.NoError(t, err)
	assert.Contains(t, mgr.activationObservationReason(ctx, fx.cabinetID, c, p, d, params, time.Now().UTC()), "не подтверждено изменение")
	_, err = mgr.actions.SetProductBidsWithSource(ctx, fx.workspaceID, uuidFromPgtype(c.ID), []OzonBidInput{{SKU: 5, BidRub: 90}}, domain.BidSourceManual, "verified repair")
	require.NoError(t, err)
	assert.Empty(t, mgr.activationObservationReason(ctx, fx.cabinetID, c, p, d, params, time.Now().UTC()))
}

func TestAIExecution_CPOEnableUsesLiveFeeInsteadOfRefreshedMirror(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ptr := func(v float64) *float64 { return &v }
	params := domain.StrategyParams{MaxBid: 500, TargetACoS: 15}.Merged()
	for _, tc := range []struct {
		name           string
		fee            float64
		missing        bool
		alreadyEnabled bool
		allowed        bool
	}{
		{"live affordable", 40, false, false, true},
		{"live fee increased", 200, false, false, false},
		{"no fee in API", 0, false, false, false},
		{"SKU absent", 40, true, false, false},
		{"already enabled", 40, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			perf := &fakePerfClient{}
			if !tc.missing {
				perf.cpoProducts = []ozon.CPOProduct{{SKU: 5, BidPriceRub: tc.fee, Enabled: tc.alreadyEnabled}}
			}
			mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
			d := &aiCabinetData{cpoBySKU: map[int64]domain.OzonCPOProduct{5: {SKU: 5, BidPriceRub: ptr(40), UpdatedAt: time.Now()}}, economicsBySKU: map[int64]aiPackEconomics{5: {SKU: 5, PriceRub: ptr(1000), MarginPct: ptr(40), CommissionFBOPct: ptr(15)}}, stockBySKU: map[int64]int64{5: 100}, totalDRRCeiling: ptr(10)}
			d.totalDRR.Status = totalDRRStatusOK
			d.totalDRR.Value = 5
			p := aiProposal{ActionType: domain.AIActionCPOEnable, Target: domain.AIDecisionTarget{SKU: 5}}
			reason := mgr.liveCPOEnableGuard(context.Background(), fx.workspaceID, fx.cabinetID, p, d, params)
			if tc.allowed {
				assert.Empty(t, reason)
			} else {
				assert.NotEmpty(t, reason)
			}
			assert.Zero(t, perf.enableCalls)
		})
	}
}
