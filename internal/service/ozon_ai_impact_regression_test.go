package service

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/stretchr/testify/require"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestAIImpactRegression_RequiresCompleteMaturePairs(t *testing.T) {
	applied := time.Date(2026, 8, 1, 14, 0, 0, 0, time.UTC)
	full := aiImpactWindowTotals{Days: 7}
	tests := []struct {
		name          string
		before, after aiImpactWindowTotals
		now           time.Time
		want          string
	}{
		{"three after days are not a week", full, aiImpactWindowTotals{Days: 3}, applied.AddDate(0, 0, 5), domain.AIOutcomePendingEval},
		{"partial before window is not comparable", aiImpactWindowTotals{Days: 3}, full, applied.AddDate(0, 0, 9), domain.AIOutcomePendingEval},
		{"missing baseline is not zero business activity", aiImpactWindowTotals{}, full, applied.AddDate(0, 0, 15), domain.AIOutcomeNotEvaluable},
		{"last day must finish even when a row exists", full, full, time.Date(2026, 8, 8, 23, 59, 0, 0, time.UTC), domain.AIOutcomePendingEval},
		{"two complete mature weeks", full, full, time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC), domain.AIOutcomeEvaluated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, aiImpactOutcome(tt.before, tt.after, applied, tt.now).Status)
		})
	}
}

func TestAIImpactRegression_OverlappingCampaignMoneyCountedOnce(t *testing.T) {
	applied := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	rows := []aiImpactRow{}
	for i := 0; i < 8; i++ {
		rows = append(rows, aiImpactRow{CampaignID: 42, AppliedAt: applied.AddDate(0, 0, -i), Evaluated: true, HasNumbers: true,
			SpendB: 200, SpendA: 100, RevenueB: 1000, RevenueA: 1200, DRRBefore: f(20), DRRAfter: f(8.33)})
	}
	summary := aiImpactAggregate(rows)
	assert.EqualValues(t, 8, summary.DecisionsEvaluated)
	assert.InDelta(t, -100, summary.SpendDeltaRub, .001)
	assert.InDelta(t, 200, summary.RevenueDeltaRub, .001)
	assert.True(t, summary.LowData, "eight overlapping decisions remain one observed comparison")
	assert.Nil(t, summary.SavedRub)
	assert.Nil(t, summary.ExtraRevenueRub)
	rows = append(rows, aiImpactRow{CampaignID: 42, AppliedAt: applied.AddDate(0, 0, -15), Evaluated: true, HasNumbers: true,
		SpendB: 200, SpendA: 100, RevenueB: 1000, RevenueA: 1200})
	assert.InDelta(t, -200, aiImpactAggregate(rows).SpendDeltaRub, .001, "a disjoint pair may be included")
}

// Only these isolated test fixtures carry synthetic business values.
func seedImpactRegressionDecision(t *testing.T, fx *ozonFixture, action string, target domain.AIDecisionTarget, applied time.Time) sqlcgen.AiDecision {
	t.Helper()
	ctx := context.Background()
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	targetJSON, err := json.Marshal(target)
	require.NoError(t, err)
	d, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: run.WorkspaceID, SellerCabinetID: run.SellerCabinetID,
		ActionType: action, Target: targetJSON, Proposal: []byte(`{"new_value":100}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusApplied,
	})
	require.NoError(t, err)
	_, err = fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET applied_at=$2, created_at=$2 WHERE id=$1`, d.ID, applied)
	require.NoError(t, err)
	d.AppliedAt = pgtype.Timestamptz{Time: applied, Valid: true}
	return d
}

func seedImpactRegressionWeeks(t *testing.T, fx *ozonFixture, campaign sqlcgen.OzonCampaign, applied time.Time) {
	t.Helper()
	for i := 1; i <= 7; i++ {
		seedOzonCampaignStat(t, fx.db, campaign.ID, applied.AddDate(0, 0, -i), 100, 10, 1, 20, 100)
		seedOzonCampaignStat(t, fx.db, campaign.ID, applied.AddDate(0, 0, i), 100, 10, 1, 30, 100)
	}
}

func TestAIImpactRegression_RepairsLegacyPartialAndHidesFeedback(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	campaign := seedOzonCampaign(t, fx.db, fx.cabinetID, 8100, "CAMPAIGN_STATE_RUNNING", nil, nil)
	applied := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	seedImpactRegressionWeeks(t, fx, campaign, applied)
	d := seedImpactRegressionDecision(t, fx, domain.AIActionBudgetChange, domain.AIDecisionTarget{OzonCampaignID: 8100}, applied)
	_, err := fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET outcome_status='evaluated', evaluated_at=$2,
	 spend_before_rub=140, spend_after_rub=90, revenue_before_rub=700,revenue_after_rub=300,drr_before=20,drr_after=30 WHERE id=$1`, d.ID, applied.AddDate(0, 0, 4))
	require.NoError(t, err)
	summary, err := mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.Zero(t, summary.DecisionsEvaluated, "legacy partial evaluation is excluded before the sweep")
	feedback, err := fx.db.Queries.ListAIDecisionFeedback(ctx, uuidToPgtype(fx.cabinetID), 10)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	assert.Equal(t, domain.AIOutcomePendingEval, feedback[0].OutcomeStatus.String)
	assert.False(t, feedback[0].DrrAfter.Valid, "legacy outcomes cannot feed model decisions")
	require.NoError(t, mgr.EvaluateImpactSweep(ctx))
	fresh, err := fx.db.Queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{ID: d.ID, WorkspaceID: uuidToPgtype(fx.workspaceID)})
	require.NoError(t, err)
	assert.Equal(t, domain.AIOutcomeEvaluated, fresh.OutcomeStatus.String)
	assert.InDelta(t, 210, pgNumericToFloat(fresh.SpendAfterRub), .001, "all seven after-days must replace the old three-day total")
	summary, err = mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, summary.DecisionsEvaluated)
	assert.InDelta(t, 70, summary.SpendDeltaRub, .001)
}

func TestAIImpactRegression_ImmatureLegacyResultReturnsToPending(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8101, "CAMPAIGN_STATE_RUNNING", nil, nil)
	applied := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -4)
	for i := 1; i <= 7; i++ {
		seedOzonCampaignStat(t, fx.db, c.ID, applied.AddDate(0, 0, -i), 100, 10, 1, 20, 100)
	}
	for i := 1; i <= 3; i++ {
		seedOzonCampaignStat(t, fx.db, c.ID, applied.AddDate(0, 0, i), 100, 10, 1, 30, 100)
	}
	d := seedImpactRegressionDecision(t, fx, domain.AIActionBudgetChange, domain.AIDecisionTarget{OzonCampaignID: 8101}, applied)
	_, err := fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET outcome_status='evaluated',evaluated_at=now(),spend_before_rub=140,spend_after_rub=90,revenue_before_rub=700,revenue_after_rub=300 WHERE id=$1`, d.ID)
	require.NoError(t, err)
	require.NoError(t, mgr.EvaluateImpactSweep(ctx))
	fresh, err := fx.db.Queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{ID: d.ID, WorkspaceID: uuidToPgtype(fx.workspaceID)})
	require.NoError(t, err)
	assert.Equal(t, domain.AIOutcomePendingEval, fresh.OutcomeStatus.String)
	assert.False(t, fresh.EvaluatedAt.Valid)
	assert.False(t, fresh.SpendAfterRub.Valid)
}

func TestAIImpactRegression_PendingHistoryCannotHideDowngrade(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	strategyID := seedAIStrategy(t, fx, 3)
	applied := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	for i := int64(0); i < 3; i++ {
		c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8200+i, "CAMPAIGN_STATE_RUNNING", nil, nil)
		seedImpactRegressionWeeks(t, fx, c, applied)
		d := seedImpactRegressionDecision(t, fx, domain.AIActionBudgetChange, domain.AIDecisionTarget{OzonCampaignID: 8200 + i}, applied)
		status, err := mgr.evaluateDecisionImpact(ctx, d)
		require.NoError(t, err)
		require.Equal(t, domain.AIOutcomeEvaluated, status)
	}
	for i := 0; i < 20; i++ {
		seedImpactRegressionDecision(t, fx, domain.AIActionBudgetChange, domain.AIDecisionTarget{OzonCampaignID: 8200}, time.Now().UTC().Add(-time.Duration(i)*time.Hour))
	}
	feedback, err := fx.db.Queries.ListAIDecisionFeedback(ctx, uuidToPgtype(fx.cabinetID), 9)
	require.NoError(t, err)
	complete := 0
	for _, d := range feedback {
		if d.OutcomeStatus.String == domain.AIOutcomeEvaluated {
			complete++
		}
	}
	assert.Equal(t, 3, complete)
	mgr.maybeDowngradeAutopilot(ctx, fx.workspaceID, fx.cabinetID)
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)
	assert.Equal(t, 2, strategyFromSqlc(strategy).Params.AutomationLevel)
}

func TestAIImpactRegression_DuplicateSKUsDoNotProduceDowngradeStreak(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	strategyID := seedAIStrategy(t, fx, 3)
	applied := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8300, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedImpactRegressionWeeks(t, fx, c, applied)
	for i := int64(1); i <= 3; i++ {
		d := seedImpactRegressionDecision(t, fx, domain.AIActionBidChange, domain.AIDecisionTarget{OzonCampaignID: 8300, SKU: i}, applied)
		_, err := mgr.evaluateDecisionImpact(ctx, d)
		require.NoError(t, err)
	}
	mgr.maybeDowngradeAutopilot(ctx, fx.workspaceID, fx.cabinetID)
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)
	assert.Equal(t, 3, strategyFromSqlc(strategy).Params.AutomationLevel)
	summary, err := mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.EqualValues(t, 3, summary.DecisionsEvaluated)
	assert.InDelta(t, 70, summary.SpendDeltaRub, .001)
}

func TestAIImpactRegression_MissingCPOSalesStayUnknown(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	applied := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	d := seedImpactRegressionDecision(t, fx, domain.AIActionCPOEnable, domain.AIDecisionTarget{SKU: 101}, applied)
	status, err := mgr.evaluateDecisionImpact(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, domain.AIOutcomePendingEval, status, "absence of sales rows does not establish zero turnover")
	for i := 1; i <= 7; i++ {
		seedOzonSalesDaily(t, fx.db, fx.cabinetID, 101, applied.AddDate(0, 0, -i), 2, 200)
		seedOzonSalesDaily(t, fx.db, fx.cabinetID, 101, applied.AddDate(0, 0, i), 1, 100)
	}
	status, err = mgr.evaluateDecisionImpact(ctx, d)
	require.NoError(t, err)
	assert.Equal(t, domain.AIOutcomeEvaluated, status)
	summary, err := mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, summary.DecisionsEvaluated)
	assert.Zero(t, summary.RevenueDeltaRub, "CPO without measured spend must not enter comparable campaign aggregates")
	assert.True(t, summary.LowData)
}

func TestAIImpactRegression_LaterSyncedPartialTotalsAreNotComplete(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	applied := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8400, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedImpactRegressionWeeks(t, fx, c, applied)
	d := seedImpactRegressionDecision(t, fx, domain.AIActionBudgetChange, domain.AIDecisionTarget{OzonCampaignID: 8400}, applied)
	_, err := fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET outcome_status='evaluated',evaluated_at=now(),spend_before_rub=140,spend_after_rub=90,revenue_before_rub=700,revenue_after_rub=300 WHERE id=$1`, d.ID)
	require.NoError(t, err)
	summary, err := mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.Zero(t, summary.DecisionsEvaluated, "complete rows synced later cannot validate old partial totals")
	require.NoError(t, mgr.EvaluateImpactSweep(ctx))
	summary, err = mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, summary.DecisionsEvaluated)
	assert.InDelta(t, 70, summary.SpendDeltaRub, .001)
}
