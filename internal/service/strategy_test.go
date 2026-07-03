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
			AutomationLevel: 2,
			MaxCPC:          50,
			MaxCPO:          1500,
		},
	})

	require.NoError(t, err)
}
