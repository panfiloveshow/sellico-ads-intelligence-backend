package service

import (
	"context"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestAIReadinessRegression_UsesOnlyCompleteDisjointObservations(t *testing.T) {
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	seedAIStrategy(t, fx, 1)
	applied := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	legacyCampaign := seedOzonCampaign(t, fx.db, fx.cabinetID, 9000, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedImpactRegressionWeeks(t, fx, legacyCampaign, applied)
	legacy := seedImpactRegressionDecision(t, fx, domain.AIActionBudgetChange,
		domain.AIDecisionTarget{OzonCampaignID: 9000}, applied)
	_, err := fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET outcome_status='evaluated',
	 evaluated_at=$2,drr_before=20,drr_after=30,spend_before_rub=140,
	 spend_after_rub=210,revenue_before_rub=700,revenue_after_rub=700 WHERE id=$1`,
		legacy.ID, applied.AddDate(0, 0, 4))
	require.NoError(t, err)
	readiness, err := mgr.GetReadiness(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	require.NotNil(t, readiness)
	require.Nil(t, readiness.ProjectedDRRDelta, "a legacy partial comparison must not promise a projected improvement")

	for i := int64(1); i <= 2; i++ {
		campaign := seedOzonCampaign(t, fx.db, fx.cabinetID, 9000+i, "CAMPAIGN_STATE_RUNNING", nil, nil)
		seedImpactRegressionWeeks(t, fx, campaign, applied)
		count := 8
		if i == 2 {
			count = 1
			_, err = fx.db.Pool.Exec(ctx, `UPDATE ozon_campaign_stats SET spend_rub=10 WHERE campaign_id=$1 AND date>$2::date`, campaign.ID, applied)
			require.NoError(t, err)
		}
		for j := 0; j < count; j++ {
			d := seedImpactRegressionDecision(t, fx, domain.AIActionBidChange,
				domain.AIDecisionTarget{OzonCampaignID: 9000 + i, SKU: int64(j + 1)}, applied)
			status, evalErr := mgr.evaluateDecisionImpact(ctx, d)
			require.NoError(t, evalErr)
			require.Equal(t, domain.AIOutcomeEvaluated, status)
		}
	}
	for i := 0; i < 20; i++ {
		seedImpactRegressionDecision(t, fx, domain.AIActionBudgetChange,
			domain.AIDecisionTarget{OzonCampaignID: 9001}, time.Now().UTC().Add(-time.Duration(i)*time.Hour))
	}
	readiness, err = mgr.GetReadiness(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	require.NotNil(t, readiness.ProjectedDRRDelta)
	require.InDelta(t, 0, *readiness.ProjectedDRRDelta, .001,
		"one +10 and one -10 observation balance; duplicate SKU decisions and pending rows must not change their weight")
	require.EqualValues(t, 30, readiness.DecisionsTotal, "the other existing readiness criteria retain their decision counts")
}
