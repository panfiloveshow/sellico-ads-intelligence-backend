package service

import (
	"fmt"
	"math"

	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// PriceEngine computes repricer decisions from product economics, stock, sales
// velocity and ad signals. The decision functions are pure (see *_test.go).
type PriceEngine struct {
	logger zerolog.Logger
}

// NewPriceEngine creates a PriceEngine.
func NewPriceEngine(logger zerolog.Logger) *PriceEngine {
	return &PriceEngine{logger: logger.With().Str("component", "price_engine").Logger()}
}

// PriceEngineInputs is everything the engine needs to decide one product's price.
// All *Rub values are integer rubles.
type PriceEngineInputs struct {
	Current                   domain.ProductPrice
	Economics                 domain.ProductEconomics
	CommissionFallbackPercent *float64 // from wb_commission_tariffs when economics.CommissionPercent is nil
	Stock                     int64
	StockKnown                bool
	SalesUnitsPerDay          float64
	// Ad signals (price_ad_linked).
	DRR                *float64
	HasActiveCampaigns bool
}

// PriceDecision is the engine's verdict for one product.
type PriceDecision struct {
	ShouldChange       bool
	NewPriceRub        int64  // base price to send to WB, integer rubles
	NewDiscountPercent int    // kept equal to current discount in v1
	TargetEffectiveRub int64  // clamped target effective (discounted) price
	MinPriceRub        int64  // margin floor used, 0 if unknown
	Direction          string // up|down|none
	Reason             string
	SkipReason         string
}

func skip(reason string) PriceDecision { return PriceDecision{Direction: "none", SkipReason: reason} }
func noChange(reason string, floor int64) PriceDecision {
	return PriceDecision{Direction: "none", Reason: reason, MinPriceRub: floor}
}

// ComputeMinEffectivePrice returns the lowest effective (discounted) price, in
// integer rubles, at which the product still hits its target margin:
//
//	floor = ceil( (cost + logistics + other) / (1 - (commission% + tax% + margin%)/100) )
//
// Returns (0, reason) when it cannot be computed. commissionFallback (from WB
// commission tariffs) is used only when economics.CommissionPercent is nil.
func ComputeMinEffectivePrice(econ domain.ProductEconomics, commissionFallback *float64) (int64, string) {
	if econ.CostPrice == nil {
		return 0, "missing_cost_price"
	}
	cost := *econ.CostPrice
	if econ.LogisticsCost != nil {
		cost += *econ.LogisticsCost
	}
	if econ.OtherCosts != nil {
		cost += *econ.OtherCosts
	}
	if cost < 0 {
		return 0, "negative_cost"
	}

	commission := 0.0
	switch {
	case econ.CommissionPercent != nil:
		commission = *econ.CommissionPercent
	case commissionFallback != nil:
		commission = *commissionFallback
	default:
		return 0, "missing_commission_percent"
	}
	tax := 0.0
	if econ.TaxRatePercent != nil {
		tax = *econ.TaxRatePercent
	}
	margin := 0.0
	if econ.TargetMarginPercent != nil {
		margin = *econ.TargetMarginPercent
	}

	denom := 1 - (commission+tax+margin)/100
	if denom < 0.05 {
		// commission+tax+margin ≥ 95% — no achievable price, refuse rather than
		// return an absurd floor.
		return 0, "economics_percentages_invalid"
	}
	return int64(math.Ceil(float64(cost) / denom)), ""
}

// basePriceForTarget converts a target effective price to the base price WB needs,
// keeping the discount constant: base = ceil(target / (1 - discount/100)).
// discount is clamped to [0, 95].
func basePriceForTarget(targetEffectiveRub int64, discountPercent int) int64 {
	if discountPercent < 0 {
		discountPercent = 0
	}
	if discountPercent > 95 {
		discountPercent = 95
	}
	if discountPercent == 0 {
		return targetEffectiveRub
	}
	return int64(math.Ceil(float64(targetEffectiveRub) * 100 / float64(100-discountPercent)))
}

// effectiveMinFloor combines the margin floor with an optional hard MinPriceRub override.
func effectiveMinFloor(marginFloor int64, params domain.StrategyParams) int64 {
	floor := marginFloor
	if params.MinPriceRub != nil && *params.MinPriceRub > floor {
		floor = *params.MinPriceRub
	}
	return floor
}

// resolveFloor computes the per-product effective price floor for a strategy.
// Priority: (1) margin floor from product economics (most precise); (2) an
// explicit MinPriceRub override; (3) a RELATIVE floor — current price minus
// MaxDiscountPercent — so strategies protect every product at its own level
// without per-product config or economics. With none, the product is skipped.
func resolveFloor(in PriceEngineInputs, params domain.StrategyParams) (int64, string) {
	marginFloor, reason := ComputeMinEffectivePrice(in.Economics, in.CommissionFallbackPercent)
	if reason == "" {
		return effectiveMinFloor(marginFloor, params), ""
	}
	if params.MinPriceRub != nil && *params.MinPriceRub > 0 {
		return *params.MinPriceRub, ""
	}
	if current := in.Current.EffectivePriceRub(); current > 0 && params.MaxDiscountPercent > 0 {
		return int64(math.Round(float64(current) * (1 - params.MaxDiscountPercent/100))), ""
	}
	return 0, reason
}

// DecideMarginFloor raises a product priced below its margin floor back up to it.
func DecideMarginFloor(in PriceEngineInputs, params domain.StrategyParams) PriceDecision {
	floor, reason := resolveFloor(in, params)
	if reason != "" {
		return skip(reason)
	}
	current := in.Current.EffectivePriceRub()
	if current <= 0 {
		return skip("invalid_current_price")
	}
	if current >= floor {
		return noChange("above_margin_floor", floor)
	}
	// Raise to floor. Margin protection bypasses the step cap (selling below cost
	// is worse than a large corrective raise); raising never triggers quarantine.
	return PriceDecision{
		ShouldChange:       true,
		NewPriceRub:        basePriceForTarget(floor, in.Current.DiscountPercent),
		NewDiscountPercent: in.Current.DiscountPercent,
		TargetEffectiveRub: floor,
		MinPriceRub:        floor,
		Direction:          "up",
		Reason:             fmt.Sprintf("below margin floor %d, raising to floor", floor),
	}
}

// DecideInventoryDemand nudges price down on overstock+slow sales and up on
// low-stock+fast sales, always clamped to the margin floor / max price.
func DecideInventoryDemand(in PriceEngineInputs, params domain.StrategyParams) PriceDecision {
	p := params.MergedPriceParams()
	floor, reason := resolveFloor(in, p)
	if reason != "" {
		return skip(reason)
	}
	current := in.Current.EffectivePriceRub()
	if current <= 0 {
		return skip("invalid_current_price")
	}
	if !in.StockKnown {
		return skip("stock_unknown")
	}

	daysOfStock := math.Inf(1)
	if in.SalesUnitsPerDay > 0 {
		daysOfStock = float64(in.Stock) / in.SalesUnitsPerDay
	}
	slow := in.SalesUnitsPerDay <= p.SlowVelocityPerDay

	switch {
	case daysOfStock > float64(p.OverstockDays) && slow:
		target := int64(math.Round(float64(current) * (1 - p.StepPercent/100)))
		if target < floor {
			target = floor
		}
		if target >= current {
			return noChange("already_at_floor", floor)
		}
		return priceMove(in, "down", target, floor, fmt.Sprintf("overstock %.0f days, stepping down %.0f%%", daysOfStock, p.StepPercent))

	case daysOfStock < float64(p.LowStockDays) && in.SalesUnitsPerDay > 0:
		if params.MaxPriceRub == nil {
			return skip("max_price_required_for_increase")
		}
		target := int64(math.Round(float64(current) * (1 + p.StepPercent/100)))
		if target > *params.MaxPriceRub {
			target = *params.MaxPriceRub
		}
		if target <= current {
			return noChange("already_at_max", floor)
		}
		return priceMove(in, "up", target, floor, fmt.Sprintf("low stock %.0f days, stepping up %.0f%%", daysOfStock, p.StepPercent))

	default:
		return noChange("inventory_balanced", floor)
	}
}

// DecideAdLinked lowers price when the product's ad DRR exceeds its allowed DRR
// (a cheaper price lifts ad-traffic conversion and pulls DRR back down).
func DecideAdLinked(in PriceEngineInputs, params domain.StrategyParams) PriceDecision {
	p := params.MergedPriceParams()
	floor, reason := resolveFloor(in, p)
	if reason != "" {
		return skip(reason)
	}
	current := in.Current.EffectivePriceRub()
	if current <= 0 {
		return skip("invalid_current_price")
	}
	if !in.HasActiveCampaigns {
		return skip("no_active_campaigns")
	}
	// DRR ceiling: strategy param first, product economics as fallback.
	maxDRR := p.MaxAllowedDRRPercent
	if maxDRR == nil {
		maxDRR = in.Economics.MaxAllowedDRR
	}
	if in.DRR == nil || maxDRR == nil {
		return skip("missing_ad_data")
	}
	if *in.DRR <= *maxDRR {
		return noChange("drr_within_limit", floor)
	}
	// DRR too high: step down toward floor to improve ad-traffic conversion.
	target := int64(math.Round(float64(current) * (1 - p.StepPercent/100)))
	if target < floor {
		target = floor
	}
	if target >= current {
		return noChange("already_at_floor", floor)
	}
	return priceMove(in, "down", target, floor, fmt.Sprintf("DRR %.1f%% over allowed %.1f%%, stepping down", *in.DRR, *maxDRR))
}

// DecidePeakHours does demand-driven time-of-day pricing PER PRODUCT, in percent
// of each product's own current price — so it works across any price tier with
// no per-product config. The target sits between current×(1−dead%) at a dead
// hour and current×(1+peak%) at a demand peak, interpolated by the current
// hour's order intensity (0..1 from the heatmap). Clamped to the product floor,
// per-run move capped to step%, 1% dead-band to avoid churn.
func DecidePeakHours(in PriceEngineInputs, params domain.StrategyParams, intensity float64) PriceDecision {
	p := params.MergedPriceParams()
	uplift := p.PeakUpliftPercent
	dead := p.DeadDiscountPercent
	current := in.Current.EffectivePriceRub()
	if current <= 0 {
		return skip("invalid_current_price")
	}
	if intensity < 0 {
		intensity = 0
	}
	if intensity > 1 {
		intensity = 1
	}
	floor, reason := resolveFloor(in, p)
	if reason != "" {
		return skip(reason)
	}

	// Band relative to each product's current price.
	factor := 1 + (uplift/100)*intensity - (dead/100)*(1-intensity)
	target := int64(math.Round(float64(current) * factor))
	ceiling := int64(math.Round(float64(current) * (1 + uplift/100)))
	if target > ceiling {
		target = ceiling
	}
	if target < floor {
		target = floor
	}

	// Cap the per-run move to step%.
	step := p.StepPercent / 100
	if target > current {
		if capped := int64(math.Round(float64(current) * (1 + step))); target > capped {
			target = capped
		}
	} else if target < current {
		if capped := int64(math.Round(float64(current) * (1 - step))); target < capped {
			target = capped
		}
		if target < floor {
			target = floor
		}
	}

	// Dead-band: ignore sub-1% (or sub-1₽) nudges.
	diff := target - current
	if diff < 0 {
		diff = -diff
	}
	if diff == 0 || float64(diff) < math.Max(1, float64(current)*0.01) {
		return noChange("near_demand_target", floor)
	}

	dir := "up"
	if target < current {
		dir = "down"
	}
	return priceMove(in, dir, target, floor, fmt.Sprintf("demand %.0f%%, %s to %d₽ (band −%.0f%%..+%.0f%%)", intensity*100, dir, target, dead, uplift))
}

// priceMove builds a change decision for a target effective price.
func priceMove(in PriceEngineInputs, direction string, targetEffective, floor int64, reason string) PriceDecision {
	return PriceDecision{
		ShouldChange:       true,
		NewPriceRub:        basePriceForTarget(targetEffective, in.Current.DiscountPercent),
		NewDiscountPercent: in.Current.DiscountPercent,
		TargetEffectiveRub: targetEffective,
		MinPriceRub:        floor,
		Direction:          direction,
		Reason:             reason,
	}
}
