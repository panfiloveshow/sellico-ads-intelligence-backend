package service

import (
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/stretchr/testify/require"
)

func TestValidateStrategyForSaveRejectsInvalidTrustAndCostLimits(t *testing.T) {
	err := validateStrategyForSave(domain.Strategy{
		Name: "Autopilot",
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			MaxCPC:          -1,
			MaxCPO:          1000,
			AutomationLevel: 3,
		},
	})
	require.Error(t, err)
	require.True(t, apperror.Is(err, apperror.ErrValidation))
	require.Contains(t, err.Error(), "max_cpc")

	err = validateStrategyForSave(domain.Strategy{
		Name: "Autopilot",
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			MaxCPC:          50,
			MaxCPO:          1000,
			AutomationLevel: 5,
		},
	})
	require.Error(t, err)
	require.True(t, apperror.Is(err, apperror.ErrValidation))
	require.Contains(t, err.Error(), "automation_level")
}

func TestValidateStrategyForSaveAllowsSafeDefaultsAndSemiAutoLevel(t *testing.T) {
	err := validateStrategyForSave(domain.Strategy{
		Name: "Semi-auto guard",
		Type: domain.StrategyTypeACoS,
		Params: domain.StrategyParams{
			TargetACoS:      25,
			AutomationLevel: 2,
			MaxCPC:          50,
			MaxCPO:          1500,
		},
	})

	require.NoError(t, err)
}

func TestValidateStrategyForSaveRejectsUnsupportedRecommendationStrategy(t *testing.T) {
	err := validateStrategyForSave(domain.Strategy{
		Name: "Auto recommendations",
		Type: domain.StrategyTypeRecommendation,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not executable")
}

func TestValidateStrategyForSaveRejectsInvalidTypeSpecificParams(t *testing.T) {
	tests := []domain.Strategy{
		{Name: "ACoS", Type: domain.StrategyTypeACoS, Params: domain.StrategyParams{TargetACoS: -1}},
		{Name: "ROAS", Type: domain.StrategyTypeROAS, Params: domain.StrategyParams{TargetROAS: 0}},
		{Name: "Anti", Type: domain.StrategyTypeAntiSliv, Params: domain.StrategyParams{MaxACoS: 0}},
		{Name: "Day", Type: domain.StrategyTypeDayparting, Params: domain.StrategyParams{HourlyMultipliers: map[string]float64{"24": 1.2}}},
		{Name: "Day TZ", Type: domain.StrategyTypeDayparting, Params: domain.StrategyParams{Timezone: "not/a-timezone"}},
		{Name: "Search", Type: domain.StrategyTypeSearchPlaybook, Params: domain.StrategyParams{TargetPosition: 101}},
	}
	for _, strategy := range tests {
		require.Error(t, validateStrategyForSave(strategy), strategy.Name)
	}
}
