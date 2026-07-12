package service

import (
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

func TestApplyAdjustment(t *testing.T) {
	assert.Equal(t, int64(900), applyAdjustment(1000, domain.ManualPriceAdjustment{Type: domain.PriceAdjustPercent, Value: -10}))
	assert.Equal(t, int64(1100), applyAdjustment(1000, domain.ManualPriceAdjustment{Type: domain.PriceAdjustPercent, Value: 10}))
	assert.Equal(t, int64(950), applyAdjustment(1000, domain.ManualPriceAdjustment{Type: domain.PriceAdjustAbsolute, Value: -50}))
	assert.Equal(t, int64(777), applyAdjustment(1000, domain.ManualPriceAdjustment{Type: domain.PriceAdjustTargetRub, Value: 777}))
}

func TestEffectiveOfAndClampDiscount(t *testing.T) {
	assert.Equal(t, int64(700), effectiveOf(1000, 30))
	assert.Equal(t, int64(1000), effectiveOf(1000, 0))
	assert.Equal(t, 0, clampDiscount(-5))
	assert.Equal(t, 95, clampDiscount(120))
	assert.Equal(t, 30, clampDiscount(30))
}

func TestInverseDeltaPercentRestoresBase(t *testing.T) {
	for _, v := range []float64{-20, -10, 10, 25} {
		inv := inverseDeltaPercent(v)
		// applying v then inv should return to ~1.0
		got := (1 + v/100) * (1 + inv/100)
		assert.InDelta(t, 1.0, got, 1e-9)
	}
}

func TestApplyAdjustmentDeltaPercent(t *testing.T) {
	// schedules use delta_percent — must behave like percent
	assert.Equal(t, int64(800), applyAdjustment(1000, domain.ManualPriceAdjustment{Type: domain.PriceAdjustDeltaPercent, Value: -20}))
}

func TestValidateManualPriceBulkRequest_StrictModesAndScopes(t *testing.T) {
	target := int64(900)
	cabinetID := uuid.New()
	validAdjustment := &domain.ManualPriceAdjustment{Type: domain.PriceAdjustPercent, Value: -10}

	tests := []struct {
		name    string
		req     domain.ManualPriceBulkRequest
		wantErr bool
	}{
		{name: "items only", req: domain.ManualPriceBulkRequest{Items: []domain.ManualPriceBulkItem{{WBProductID: 1, TargetPriceRub: &target}}}},
		{name: "all scope", req: domain.ManualPriceBulkRequest{Scope: &domain.ManualPriceBulkScope{All: true}, Adjustment: validAdjustment}},
		{name: "cabinet scope", req: domain.ManualPriceBulkRequest{Scope: &domain.ManualPriceBulkScope{SellerCabinetID: &cabinetID}, Adjustment: validAdjustment}},
		{name: "empty request", req: domain.ManualPriceBulkRequest{}, wantErr: true},
		{name: "items and scope", req: domain.ManualPriceBulkRequest{Items: []domain.ManualPriceBulkItem{{WBProductID: 1, TargetPriceRub: &target}}, Scope: &domain.ManualPriceBulkScope{All: true}}, wantErr: true},
		{name: "items and adjustment", req: domain.ManualPriceBulkRequest{Items: []domain.ManualPriceBulkItem{{WBProductID: 1, TargetPriceRub: &target}}, Adjustment: validAdjustment}, wantErr: true},
		{name: "empty scope", req: domain.ManualPriceBulkRequest{Scope: &domain.ManualPriceBulkScope{}, Adjustment: validAdjustment}, wantErr: true},
		{name: "ambiguous scope", req: domain.ManualPriceBulkRequest{Scope: &domain.ManualPriceBulkScope{All: true, SellerCabinetID: &cabinetID}, Adjustment: validAdjustment}, wantErr: true},
		{name: "duplicate item", req: domain.ManualPriceBulkRequest{Items: []domain.ManualPriceBulkItem{{WBProductID: 1, TargetPriceRub: &target}, {WBProductID: 1, TargetPriceRub: &target}}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateManualPriceBulkRequest(tt.req)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, apperror.Is(err, apperror.ErrValidation))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidatePriceAdjustmentBounds(t *testing.T) {
	valid := []domain.ManualPriceAdjustment{
		{Type: domain.PriceAdjustPercent, Value: -95},
		{Type: domain.PriceAdjustPercent, Value: 95},
		{Type: domain.PriceAdjustAbsolute, Value: -1_000_000_000},
		{Type: domain.PriceAdjustTargetRub, Value: 1_000_000_000},
	}
	for _, adj := range valid {
		require.NoError(t, validatePriceAdjustment(adj, false))
	}

	invalid := []domain.ManualPriceAdjustment{
		{Type: domain.PriceAdjustPercent, Value: 0},
		{Type: domain.PriceAdjustPercent, Value: -95.01},
		{Type: domain.PriceAdjustAbsolute, Value: 1_000_000_001},
		{Type: domain.PriceAdjustTargetRub, Value: 0},
		{Type: domain.PriceAdjustTargetRub, Value: math.Inf(1)},
	}
	for _, adj := range invalid {
		require.Error(t, validatePriceAdjustment(adj, false))
	}
}

func TestApplyAdjustmentRejectsUnsafeResult(t *testing.T) {
	assert.Zero(t, applyAdjustment(900_000_000, domain.ManualPriceAdjustment{Type: domain.PriceAdjustPercent, Value: 95}))
	assert.Zero(t, applyAdjustment(100, domain.ManualPriceAdjustment{Type: domain.PriceAdjustAbsolute, Value: -100}))
}
