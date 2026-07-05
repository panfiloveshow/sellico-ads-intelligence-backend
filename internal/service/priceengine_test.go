package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func ptrI64(v int64) *int64     { return &v }
func ptrF64(v float64) *float64 { return &v }

func econ(cost, logistics, other int64, commission, tax, margin, maxDRR float64) domain.ProductEconomics {
	e := domain.ProductEconomics{
		CostPrice:         ptrI64(cost),
		LogisticsCost:     ptrI64(logistics),
		OtherCosts:        ptrI64(other),
		CommissionPercent: ptrF64(commission),
		TaxRatePercent:    ptrF64(tax),
	}
	if margin > 0 {
		e.TargetMarginPercent = ptrF64(margin)
	}
	if maxDRR > 0 {
		e.MaxAllowedDRR = ptrF64(maxDRR)
	}
	return e
}

func TestComputeMinEffectivePrice(t *testing.T) {
	tests := []struct {
		name      string
		econ      domain.ProductEconomics
		fallback  *float64
		wantFloor int64
		wantSkip  string
	}{
		{
			name: "cost 690, 30% commission+tax+margin -> ceil(690/0.7)=986",
			// cost 600+80+10=690; commission 15 + tax 5 + margin 10 = 30% -> denom 0.7
			econ:      econ(600, 80, 10, 15, 5, 10, 0),
			wantFloor: 986,
		},
		{
			name:     "missing cost price",
			econ:     domain.ProductEconomics{CommissionPercent: ptrF64(15)},
			wantSkip: "missing_cost_price",
		},
		{
			name:     "missing commission and no fallback",
			econ:     domain.ProductEconomics{CostPrice: ptrI64(500)},
			wantSkip: "missing_commission_percent",
		},
		{
			name:      "commission fallback used when nil",
			econ:      domain.ProductEconomics{CostPrice: ptrI64(500)},
			fallback:  ptrF64(20),
			wantFloor: 625, // 500/0.8
		},
		{
			name:     "percentages sum >= 95 rejected",
			econ:     econ(500, 0, 0, 60, 20, 20, 0),
			wantSkip: "economics_percentages_invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			floor, skip := ComputeMinEffectivePrice(tt.econ, tt.fallback)
			assert.Equal(t, tt.wantSkip, skip)
			if tt.wantSkip == "" {
				assert.Equal(t, tt.wantFloor, floor)
			}
		})
	}
}

func TestBasePriceForTarget(t *testing.T) {
	assert.Equal(t, int64(1000), basePriceForTarget(1000, 0))
	// target 700 effective at 30% discount -> base ceil(700*100/70)=1000
	assert.Equal(t, int64(1000), basePriceForTarget(700, 30))
	// rounding up
	assert.Equal(t, int64(1429), basePriceForTarget(1000, 30)) // 1000*100/70 = 1428.57 -> 1429
	// clamp discount
	assert.Equal(t, basePriceForTarget(500, 95), basePriceForTarget(500, 120))
}

func price(base int64, discount int, discounted int64) domain.ProductPrice {
	p := domain.ProductPrice{PriceRub: base, DiscountPercent: discount}
	if discounted > 0 {
		p.DiscountedPriceRub = ptrI64(discounted)
	}
	return p
}

func TestDecideMarginFloor(t *testing.T) {
	e := econ(600, 80, 10, 15, 5, 10, 0) // floor 986

	t.Run("below floor raises to floor", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(900, 0, 900), Economics: e}
		d := DecideMarginFloor(in, domain.StrategyParams{})
		require.True(t, d.ShouldChange)
		assert.Equal(t, "up", d.Direction)
		assert.Equal(t, int64(986), d.TargetEffectiveRub)
		assert.Equal(t, int64(986), d.NewPriceRub) // 0% discount
	})

	t.Run("above floor no change", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(1200, 0, 1200), Economics: e}
		d := DecideMarginFloor(in, domain.StrategyParams{})
		assert.False(t, d.ShouldChange)
		assert.Equal(t, "none", d.Direction)
	})

	t.Run("keeps discount, moves base", func(t *testing.T) {
		// discounted 900 at 30% discount, floor 986 -> base ceil(986*100/70)=1409
		in := PriceEngineInputs{Current: price(1286, 30, 900), Economics: e}
		d := DecideMarginFloor(in, domain.StrategyParams{})
		require.True(t, d.ShouldChange)
		assert.Equal(t, 30, d.NewDiscountPercent)
		assert.Equal(t, int64(1409), d.NewPriceRub)
	})

	t.Run("skips on missing economics", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(900, 0, 900), Economics: domain.ProductEconomics{}}
		d := DecideMarginFloor(in, domain.StrategyParams{})
		assert.False(t, d.ShouldChange)
		assert.Equal(t, "missing_cost_price", d.SkipReason)
	})

	t.Run("MinPriceRub override raises the floor", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(1000, 0, 1000), Economics: e}
		d := DecideMarginFloor(in, domain.StrategyParams{MinPriceRub: ptrI64(1100)})
		require.True(t, d.ShouldChange)
		assert.Equal(t, int64(1100), d.TargetEffectiveRub)
	})
}

func TestDecideInventoryDemand(t *testing.T) {
	e := econ(600, 80, 10, 15, 5, 10, 0) // floor 986

	t.Run("overstock slow steps down by step%", func(t *testing.T) {
		in := PriceEngineInputs{
			Current:          price(2000, 0, 2000),
			Economics:        e,
			Stock:            1000,
			StockKnown:       true,
			SalesUnitsPerDay: 1, // 1000 days of stock, slow
		}
		params := domain.StrategyParams{StepPercent: 5, SlowVelocityPerDay: 2}
		d := DecideInventoryDemand(in, params)
		require.True(t, d.ShouldChange)
		assert.Equal(t, "down", d.Direction)
		assert.Equal(t, int64(1900), d.TargetEffectiveRub) // 2000*0.95
	})

	t.Run("down move clamped to floor", func(t *testing.T) {
		in := PriceEngineInputs{
			Current:          price(1000, 0, 1000),
			Economics:        e,
			Stock:            1000,
			StockKnown:       true,
			SalesUnitsPerDay: 0.5,
		}
		params := domain.StrategyParams{StepPercent: 5, SlowVelocityPerDay: 2}
		d := DecideInventoryDemand(in, params)
		require.True(t, d.ShouldChange)
		assert.Equal(t, int64(986), d.TargetEffectiveRub) // 950 < floor 986 -> clamp
	})

	t.Run("low stock fast steps up but needs max price", func(t *testing.T) {
		in := PriceEngineInputs{
			Current:          price(1000, 0, 1000),
			Economics:        e,
			Stock:            5,
			StockKnown:       true,
			SalesUnitsPerDay: 2, // 2.5 days
		}
		// no max price -> skip
		d := DecideInventoryDemand(in, domain.StrategyParams{StepPercent: 5})
		assert.False(t, d.ShouldChange)
		assert.Equal(t, "max_price_required_for_increase", d.SkipReason)

		// with max price -> step up clamped
		d2 := DecideInventoryDemand(in, domain.StrategyParams{StepPercent: 5, MaxPriceRub: ptrI64(1030)})
		require.True(t, d2.ShouldChange)
		assert.Equal(t, "up", d2.Direction)
		assert.Equal(t, int64(1030), d2.TargetEffectiveRub) // 1050 clamped to 1030
	})

	t.Run("balanced inventory no change", func(t *testing.T) {
		in := PriceEngineInputs{
			Current:          price(1000, 0, 1000),
			Economics:        e,
			Stock:            30,
			StockKnown:       true,
			SalesUnitsPerDay: 1, // 30 days: between low(14) and overstock(60)
		}
		d := DecideInventoryDemand(in, domain.StrategyParams{})
		assert.False(t, d.ShouldChange)
		assert.Equal(t, "none", d.Direction)
	})

	t.Run("unknown stock skips", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(1000, 0, 1000), Economics: e, StockKnown: false}
		d := DecideInventoryDemand(in, domain.StrategyParams{})
		assert.Equal(t, "stock_unknown", d.SkipReason)
	})
}

func TestDecideAdLinked(t *testing.T) {
	e := econ(600, 80, 10, 15, 5, 10, 20) // floor 986, maxDRR 20

	t.Run("high DRR steps down", func(t *testing.T) {
		in := PriceEngineInputs{
			Current:            price(2000, 0, 2000),
			Economics:          e,
			HasActiveCampaigns: true,
			DRR:                ptrF64(35),
		}
		d := DecideAdLinked(in, domain.StrategyParams{StepPercent: 5})
		require.True(t, d.ShouldChange)
		assert.Equal(t, "down", d.Direction)
		assert.Equal(t, int64(1900), d.TargetEffectiveRub)
	})

	t.Run("DRR within limit no change", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(2000, 0, 2000), Economics: e, HasActiveCampaigns: true, DRR: ptrF64(15)}
		d := DecideAdLinked(in, domain.StrategyParams{})
		assert.False(t, d.ShouldChange)
	})

	t.Run("no active campaigns skips", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(2000, 0, 2000), Economics: e, DRR: ptrF64(35)}
		d := DecideAdLinked(in, domain.StrategyParams{})
		assert.Equal(t, "no_active_campaigns", d.SkipReason)
	})

	t.Run("missing ad data skips", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(2000, 0, 2000), Economics: e, HasActiveCampaigns: true}
		d := DecideAdLinked(in, domain.StrategyParams{})
		assert.Equal(t, "missing_ad_data", d.SkipReason)
	})
}

// A down step of ≤10% can never trigger WB quarantine (which needs the new
// discounted price ≥3x below the old). This pins that invariant.
func TestStepDownNeverTriggersQuarantine(t *testing.T) {
	e := econ(1, 0, 0, 0, 0, 0, 0) // floor ~1, so clamp doesn't interfere
	for _, step := range []float64{1, 3, 5, 10} {
		in := PriceEngineInputs{
			Current:          price(10000, 0, 10000),
			Economics:        e,
			Stock:            10000,
			StockKnown:       true,
			SalesUnitsPerDay: 0.1,
		}
		d := DecideInventoryDemand(in, domain.StrategyParams{StepPercent: step, SlowVelocityPerDay: 1})
		require.True(t, d.ShouldChange)
		// new effective must stay well above old/3
		assert.Greater(t, d.TargetEffectiveRub, int64(10000)/3)
	}
}

// Without product economics, an explicit MinPriceRub on the strategy acts as
// the floor (strategies work out of the box); with neither, skip.
func TestResolveFloor_MinPriceFallback(t *testing.T) {
	minPrice := int64(700)

	t.Run("no economics + min_price_rub → floor works", func(t *testing.T) {
		in := PriceEngineInputs{
			Current:          price(2000, 0, 2000),
			Economics:        domain.ProductEconomics{}, // cost price not filled in
			Stock:            1000,
			StockKnown:       true,
			SalesUnitsPerDay: 0.1,
		}
		params := domain.StrategyParams{StepPercent: 5, SlowVelocityPerDay: 1, MinPriceRub: &minPrice}
		d := DecideInventoryDemand(in, params)
		require.True(t, d.ShouldChange)
		assert.Equal(t, "down", d.Direction)
		assert.Equal(t, int64(1900), d.TargetEffectiveRub)
		assert.Equal(t, minPrice, d.MinPriceRub)
	})

	t.Run("no economics, no min → relative floor (default 30% below current)", func(t *testing.T) {
		// Overstock + slow with no economics: the relative floor (current×0.7)
		// lets the strategy still act instead of skipping.
		in := PriceEngineInputs{Current: price(2000, 0, 2000), Economics: domain.ProductEconomics{}, StockKnown: true, Stock: 1000, SalesUnitsPerDay: 0.1}
		d := DecideInventoryDemand(in, domain.StrategyParams{SlowVelocityPerDay: 1})
		require.True(t, d.ShouldChange)
		assert.Equal(t, "down", d.Direction)
		assert.Equal(t, int64(1400), d.MinPriceRub) // 2000 × (1 − 0.30)
	})

	t.Run("no economics, no min, no relative floor → skip", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(0, 0, 0), Economics: domain.ProductEconomics{}, StockKnown: true}
		d := DecideInventoryDemand(in, domain.StrategyParams{})
		assert.False(t, d.ShouldChange)
	})

	t.Run("margin floor raises to explicit min price", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(500, 0, 500), Economics: domain.ProductEconomics{}}
		d := DecideMarginFloor(in, domain.StrategyParams{MinPriceRub: &minPrice})
		require.True(t, d.ShouldChange)
		assert.Equal(t, "up", d.Direction)
		assert.Equal(t, minPrice, d.TargetEffectiveRub)
	})

	t.Run("ad linked uses strategy DRR ceiling without economics", func(t *testing.T) {
		drr := 25.0
		maxDRR := 15.0
		in := PriceEngineInputs{
			Current:            price(2000, 0, 2000),
			Economics:          domain.ProductEconomics{},
			HasActiveCampaigns: true,
			DRR:                &drr,
		}
		params := domain.StrategyParams{StepPercent: 5, MinPriceRub: &minPrice, MaxAllowedDRRPercent: &maxDRR}
		d := DecideAdLinked(in, params)
		require.True(t, d.ShouldChange)
		assert.Equal(t, "down", d.Direction)
	})
}

func TestDecidePeakHours(t *testing.T) {
	// Percentage band per product: +10% peak, −20% dead, step cap 10%, relative
	// floor 30% below current (no economics).
	params := domain.StrategyParams{StepPercent: 10, PeakUpliftPercent: 10, DeadDiscountPercent: 20, MaxDiscountPercent: 30}

	t.Run("peak → up by uplift on the product's own price", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(1000, 0, 1000), Economics: domain.ProductEconomics{}}
		d := DecidePeakHours(in, params, 1.0) // 1000 × 1.10 = 1100
		require.True(t, d.ShouldChange)
		assert.Equal(t, "up", d.Direction)
		assert.Equal(t, int64(1100), d.TargetEffectiveRub)
	})

	t.Run("same % scales with a cheaper product (300₽)", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(300, 0, 300), Economics: domain.ProductEconomics{}}
		d := DecidePeakHours(in, params, 1.0) // 300 × 1.10 = 330
		require.True(t, d.ShouldChange)
		assert.Equal(t, int64(330), d.TargetEffectiveRub)
	})

	t.Run("dead hour → down, capped by step and relative floor", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(1000, 0, 1000), Economics: domain.ProductEconomics{}}
		d := DecidePeakHours(in, params, 0.0) // 1000×0.8=800 → step −10% → 900 (floor 700)
		require.True(t, d.ShouldChange)
		assert.Equal(t, "down", d.Direction)
		assert.Equal(t, int64(900), d.TargetEffectiveRub)
	})

	t.Run("neutral intensity within dead-band → no change", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(1000, 0, 1000), Economics: domain.ProductEconomics{}}
		d := DecidePeakHours(in, params, 2.0/3.0) // factor ≈ 1 → target ≈ current
		assert.False(t, d.ShouldChange)
		assert.Equal(t, "near_demand_target", d.Reason)
	})

	t.Run("bare strategy uses defaults; default step 3% caps the move", func(t *testing.T) {
		in := PriceEngineInputs{Current: price(1000, 0, 1000), Economics: domain.ProductEconomics{}}
		d := DecidePeakHours(in, domain.StrategyParams{}, 1.0) // +8% band but step 3% → 1030
		require.True(t, d.ShouldChange)
		assert.Equal(t, int64(1030), d.TargetEffectiveRub)
	})
}
