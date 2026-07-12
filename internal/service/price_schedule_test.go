package service

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

func validScheduleInput(now time.Time) domain.PriceScheduleInput {
	return domain.PriceScheduleInput{
		SellerCabinetID: uuid.New(),
		ScopeType:       domain.PriceScopeProduct,
		ProductIDs:      []int64{101},
		AdjustmentType:  domain.PriceAdjustDeltaPercent,
		AdjustmentValue: 10,
		Direction:       domain.PriceDirectionDown,
		ScheduledAt:     now.Add(time.Hour),
	}
}

func TestValidateScheduleInput_StrictScope(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		mutate  func(*domain.PriceScheduleInput)
		wantErr bool
	}{
		{name: "one product"},
		{name: "product requires id", mutate: func(in *domain.PriceScheduleInput) { in.ProductIDs = nil }, wantErr: true},
		{name: "product rejects multiple ids", mutate: func(in *domain.PriceScheduleInput) { in.ProductIDs = []int64{1, 2} }, wantErr: true},
		{name: "all rejects ids", mutate: func(in *domain.PriceScheduleInput) { in.ScopeType = domain.PriceScopeAll }, wantErr: true},
		{name: "all without ids", mutate: func(in *domain.PriceScheduleInput) { in.ScopeType = domain.PriceScopeAll; in.ProductIDs = nil }},
		{name: "list requires ids", mutate: func(in *domain.PriceScheduleInput) { in.ScopeType = domain.PriceScopeList; in.ProductIDs = nil }, wantErr: true},
		{name: "list rejects duplicates", mutate: func(in *domain.PriceScheduleInput) {
			in.ScopeType = domain.PriceScopeList
			in.ProductIDs = []int64{1, 1}
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validScheduleInput(now)
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			err := validateScheduleInput(in, now)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, apperror.Is(err, apperror.ErrValidation))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateScheduleInput_DirectionAndBounds(t *testing.T) {
	now := time.Now().UTC()
	for _, direction := range []string{domain.PriceDirectionUp, domain.PriceDirectionDown} {
		in := validScheduleInput(now)
		in.Direction = direction
		require.NoError(t, validateScheduleInput(in, now))
	}

	invalid := []func(*domain.PriceScheduleInput){
		func(in *domain.PriceScheduleInput) { in.Direction = "" },
		func(in *domain.PriceScheduleInput) { in.Direction = "sideways" },
		func(in *domain.PriceScheduleInput) { in.AdjustmentValue = -10 },
		func(in *domain.PriceScheduleInput) { in.AdjustmentValue = 95.01 },
		func(in *domain.PriceScheduleInput) { in.AdjustmentValue = math.NaN() },
	}
	for _, mutate := range invalid {
		in := validScheduleInput(now)
		mutate(&in)
		require.Error(t, validateScheduleInput(in, now))
	}

	target := validScheduleInput(now)
	target.AdjustmentType = domain.PriceAdjustTargetRub
	target.AdjustmentValue = 900
	target.Direction = ""
	require.NoError(t, validateScheduleInput(target, now))
	target.Direction = domain.PriceDirectionDown
	require.Error(t, validateScheduleInput(target, now))
}

func TestScheduleDirectionAndProductScope(t *testing.T) {
	assert.Equal(t, 10.0, signedScheduleDelta(10, domain.PriceDirectionUp))
	assert.Equal(t, -10.0, signedScheduleDelta(10, domain.PriceDirectionDown))

	targets := map[int64]bool{101: true}
	assert.True(t, scheduleIncludesProduct(domain.PriceScopeProduct, targets, 101))
	assert.False(t, scheduleIncludesProduct(domain.PriceScopeProduct, targets, 202))
	assert.False(t, scheduleIncludesProduct(domain.PriceScopeList, targets, 202))
	assert.True(t, scheduleIncludesProduct(domain.PriceScopeAll, nil, 202))
}

func TestAutoRevertDirectionRestoresMultiplier(t *testing.T) {
	for _, tc := range []struct {
		direction string
		value     float64
	}{
		{direction: domain.PriceDirectionUp, value: 25},
		{direction: domain.PriceDirectionDown, value: 20},
	} {
		primary := signedScheduleDelta(tc.value, tc.direction)
		inverse := inverseDeltaPercent(primary)
		got := (1 + primary/100) * (1 + inverse/100)
		assert.InDelta(t, 1.0, got, 1e-9)
	}
}

func TestAutoRevertRequiresCompletedPrimary(t *testing.T) {
	assert.Equal(t, "execute", autoRevertPrimaryDisposition(domain.PriceScheduleDone))
	assert.Equal(t, "defer", autoRevertPrimaryDisposition(domain.PriceSchedulePlanned))
	assert.Equal(t, "defer", autoRevertPrimaryDisposition(domain.PriceScheduleExecuting))
	assert.Equal(t, "reject", autoRevertPrimaryDisposition(domain.PriceScheduleFailed))
	assert.Equal(t, "reject", autoRevertPrimaryDisposition(domain.PriceScheduleCanceled))
}

func TestScheduleRequiresApplicablePriceChanges(t *testing.T) {
	assert.False(t, hasApplicablePriceChanges(nil))
	assert.True(t, hasApplicablePriceChanges([]priceChangeIntent{{NmID: 101}}))
}
