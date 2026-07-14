package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestValidateStrategyInput_RejectsInvalidValues(t *testing.T) {
	errs := validateStrategyInput(domain.Strategy{
		Type: "bad",
		Params: domain.StrategyParams{
			MinBid:              200,
			MaxBid:              100,
			MaxCPC:              -1,
			MaxCPO:              -1,
			AutomationLevel:     5,
			MaxChangePercent:    101,
			LookbackDays:        -1,
			MinClicks:           -1,
			MinStockForIncrease: -1,
			CooldownMinutes:     -1,
			MaxChangesPerDay:    -1,
			MaxDataAgeHours:     -1,
		},
	})

	assert.Equal(t, "is required", errs["name"])
	assert.Equal(t, "is required", errs["seller_cabinet_id"])
	assert.Contains(t, errs["type"], "must be one of")
	assert.Equal(t, "must be less than or equal to max_bid", errs["params.min_bid"])
	assert.Equal(t, "must be non-negative", errs["params.max_cpc"])
	assert.Equal(t, "must be non-negative", errs["params.max_cpo"])
	assert.Equal(t, "must be between 1 and 4", errs["params.automation_level"])
	assert.Equal(t, "must be between 0 and 100", errs["params.max_change_percent"])
	assert.Equal(t, "must be non-negative", errs["params.lookback_days"])
	assert.Equal(t, "must be non-negative", errs["params.min_clicks"])
	assert.Equal(t, "must be non-negative", errs["params.min_stock_for_increase"])
	assert.Equal(t, "must be non-negative", errs["params.cooldown_minutes"])
	assert.Equal(t, "must be non-negative", errs["params.max_changes_per_day"])
	assert.Equal(t, "must be non-negative", errs["params.max_data_age_hours"])
}

func TestValidateStrategyInput_AcceptsValidValues(t *testing.T) {
	errs := validateStrategyInput(domain.Strategy{
		SellerCabinetID: uuid.New(),
		Name:            "ACoS guard",
		Type:            domain.StrategyTypeACoS,
		Params: domain.StrategyParams{
			TargetACoS:          25,
			MinBid:              100,
			MaxBid:              500,
			MaxCPC:              50,
			MaxCPO:              1500,
			AutomationLevel:     2,
			MaxChangePercent:    20,
			LookbackDays:        7,
			MinClicks:           10,
			MinStockForIncrease: 3,
			CooldownMinutes:     120,
			MaxChangesPerDay:    3,
			MaxDataAgeHours:     36,
		},
	})

	assert.Empty(t, errs)
}
