package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestValidateStrategyRolloutUpdateRequiresShadowValidation(t *testing.T) {
	state, err := validateStrategyRolloutUpdate(StrategyRolloutUpdate{DesiredMode: "live"})
	require.NoError(t, err)
	require.Equal(t, domain.RolloutShadowValidating, state)

	state, err = validateStrategyRolloutUpdate(StrategyRolloutUpdate{DesiredMode: "shadow", ManualHold: true})
	require.NoError(t, err)
	require.Equal(t, domain.RolloutManualHold, state)

	_, err = validateStrategyRolloutUpdate(StrategyRolloutUpdate{DesiredMode: "automatic"})
	require.Error(t, err)
}

func TestCampaignDailyLimitRejectsUnsafeEnabledValueBeforeDatabase(t *testing.T) {
	service := &CampaignActionService{}
	_, err := service.UpdateDailyLimit(context.Background(), uuid.New(), uuid.New(), 0, true)
	require.ErrorContains(t, err, "daily_limit must be greater than 0")
}

func TestStrategyEvaluationWireEnumsStayStable(t *testing.T) {
	require.ElementsMatch(t, []string{"blocked", "ready_no_change", "would_apply", "claimed", "applied", "failed", "unknown"}, []string{
		domain.FactBlocked, domain.FactReadyNoChange, domain.FactWouldApply, domain.FactClaimed,
		domain.FactApplied, domain.FactFailed, domain.FactUnknown,
	})
}

func TestClusterEvaluationCompletionNeverPromotesAllBlockedTargets(t *testing.T) {
	recorder := &bindingEvaluationRecorder{}
	require.Equal(t, "blocked", clusterEvaluationCompletion(recorder, 0))

	recorder.evaluatedTarget()
	require.Equal(t, "ready_no_change", clusterEvaluationCompletion(recorder, 0))

	recorder.proposedTarget()
	require.Equal(t, "shadow_decision", clusterEvaluationCompletion(recorder, 0))
	require.Equal(t, "applied", clusterEvaluationCompletion(recorder, 1))
}
