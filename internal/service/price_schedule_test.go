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

func TestBuildScheduleIntents_BelowFloorKeepsRelativeStep(t *testing.T) {
	entryID, cabinetID := uuid.New(), uuid.New()
	prices := map[int64]domain.ProductPrice{
		184010772: {WBProductID: 184010772, PriceRub: 2132, DiscountPercent: 75},
	}
	// floor far above the current effective 533 ₽ — the July incident shape
	floors := map[int64]int64{184010772: 4248}
	up := domain.ManualPriceAdjustment{Type: domain.PriceAdjustDeltaPercent, Value: 1}

	intents := buildScheduleIntents(entryID, cabinetID, domain.PriceScopeAll, nil, up, prices, floors)
	require.Len(t, intents, 1)
	// +1% stays +1% (2132 → 2153); the old floor-lift shipped 16992 here
	assert.Equal(t, int64(2153), intents[0].NewPriceRub)
	assert.Equal(t, 75, intents[0].NewDiscount)
	assert.Equal(t, domain.PriceSourceSchedule, intents[0].Source)
	require.NotNil(t, intents[0].ScheduleEntryID)
	assert.Equal(t, entryID, *intents[0].ScheduleEntryID)

	// a downward step while already below the floor is refused entirely
	down := domain.ManualPriceAdjustment{Type: domain.PriceAdjustDeltaPercent, Value: -1}
	assert.Empty(t, buildScheduleIntents(entryID, cabinetID, domain.PriceScopeAll, nil, down, prices, floors))
}

func TestBuildScheduleIntents_FloorClampAndScope(t *testing.T) {
	entryID, cabinetID := uuid.New(), uuid.New()
	prices := map[int64]domain.ProductPrice{
		101: {WBProductID: 101, PriceRub: 3000, DiscountPercent: 0},
		102: {WBProductID: 102, PriceRub: 3000, DiscountPercent: 0},
	}
	floors := map[int64]int64{101: 2549, 102: 2549}
	target := domain.ManualPriceAdjustment{Type: domain.PriceAdjustTargetRub, Value: 2000}

	// list scope only touches the listed product; crossing the floor clamps to it
	intents := buildScheduleIntents(entryID, cabinetID, domain.PriceScopeList, []int64{101}, target, prices, floors)
	require.Len(t, intents, 1)
	assert.Equal(t, int64(101), intents[0].NmID)
	assert.Equal(t, int64(2549), intents[0].NewPriceRub)
}

func TestBuildScheduleIntents_DeepDropIsStepped(t *testing.T) {
	entryID, cabinetID := uuid.New(), uuid.New()
	// the live disaster shape: base 89598 at 75% discount (effective 22400),
	// scheduled straight back to base 9196 (effective 2299) — a 9.7x drop
	prices := map[int64]domain.ProductPrice{
		184010773: {WBProductID: 184010773, PriceRub: 89598, DiscountPercent: 75},
	}
	target := domain.ManualPriceAdjustment{Type: domain.PriceAdjustTargetRub, Value: 9196}

	intents := buildScheduleIntents(entryID, cabinetID, domain.PriceScopeAll, nil, target, prices, nil)
	require.Len(t, intents, 1)
	// shipped step is capped just under 3x: effective 22400 → 7467, base 29868
	assert.Equal(t, int64(29868), intents[0].NewPriceRub)
	assert.Contains(t, intents[0].Reason, "quarantine-safe step")
	// and the shipped step must clear the final quarantine guard
	require.NoError(t, checkQuarantineRisk(intents[0]))
	require.NoError(t, checkRaiseAnomaly(intents[0]))
}
