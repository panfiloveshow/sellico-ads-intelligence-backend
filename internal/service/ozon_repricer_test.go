package service

import (
	"strings"
	"testing"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestComputeOzonFloor_WithNetPrice(t *testing.T) {
	// net 500₽ + acquiring 25₽, commission max(18, 20)=20%, margin 10% →
	// floor = 525 / (1 − 0.30) = 750.00
	floor, reason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:         500,
		CommissionFBOPct:    18,
		CommissionFBSPct:    20,
		AcquiringRub:        25,
		TargetMarginPercent: 10,
	})
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if floor != 750 {
		t.Fatalf("floor = %v, want 750", floor)
	}
}

func TestComputeOzonFloor_ZeroMargin(t *testing.T) {
	// Without a target margin the floor only covers cost + fees:
	// (1000 + 20) / (1 − 0.15) = 1200.00
	floor, reason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:      1000,
		CommissionFBOPct: 15,
		AcquiringRub:     20,
	})
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if floor != 1200 {
		t.Fatalf("floor = %v, want 1200", floor)
	}
}

func TestComputeOzonFloor_MissingCommission(t *testing.T) {
	_, reason := computeOzonFloor(ozonFloorInputs{NetPriceRub: 500})
	if reason != "missing_commission" {
		t.Fatalf("reason = %q, want missing_commission", reason)
	}
}

func TestComputeOzonFloor_PercentagesTooHigh(t *testing.T) {
	// commission 80% + margin 20% = 100% ≥ 95% cap → refuse.
	_, reason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:         500,
		CommissionFBOPct:    80,
		TargetMarginPercent: 20,
	})
	if reason != "economics_percentages_invalid" {
		t.Fatalf("reason = %q, want economics_percentages_invalid", reason)
	}
}

func TestComputeOzonFloor_FallbackWithoutNetPrice(t *testing.T) {
	// No net_price → relative floor: current × (1 − 30%/100) by default.
	floor, reason := computeOzonFloor(ozonFloorInputs{
		CurrentPriceRub: 1000,
	})
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if floor != 700 {
		t.Fatalf("floor = %v, want 700", floor)
	}

	// Custom max discount.
	floor, _ = computeOzonFloor(ozonFloorInputs{CurrentPriceRub: 1000, MaxDiscountPercent: 10})
	if floor != 900 {
		t.Fatalf("floor = %v, want 900", floor)
	}
}

func TestComputeOzonFloor_NothingToComputeFrom(t *testing.T) {
	_, reason := computeOzonFloor(ozonFloorInputs{})
	if reason != "missing_net_price" {
		t.Fatalf("reason = %q, want missing_net_price", reason)
	}
}

func TestDecideOzonMarginFloor(t *testing.T) {
	// Below floor → raise to floor.
	decision := decideOzonMarginFloor(600, 750)
	if !decision.ShouldChange || decision.NewPriceRub != 750 || decision.Direction != "up" {
		t.Fatalf("unexpected decision: %+v", decision)
	}

	// At/above floor → no change.
	if d := decideOzonMarginFloor(750, 750); d.ShouldChange {
		t.Fatalf("expected no change at floor, got %+v", d)
	}

	// Floor ≥ 3× current → corrupted economics, skip.
	if d := decideOzonMarginFloor(200, 600); d.SkipReason != "floor_suspicious" {
		t.Fatalf("expected floor_suspicious, got %+v", d)
	}
}

func TestDecideOzonCompetitorFollow_ClampsToFloor(t *testing.T) {
	// Competitor 700 × (1 − 2%) = 686, below floor 720 → clamp to 720.
	// Step cap: current 730 × (1 − 10%) = 657 does not bind.
	decision := decideOzonCompetitorFollow(730, 720, 700, 2, 10)
	if !decision.ShouldChange {
		t.Fatalf("expected a change, got %+v", decision)
	}
	if decision.NewPriceRub != 720 {
		t.Fatalf("new price = %v, want floor 720", decision.NewPriceRub)
	}
	if decision.Direction != "down" {
		t.Fatalf("direction = %q, want down", decision.Direction)
	}
}

func TestDecideOzonCompetitorFollow_StepCap(t *testing.T) {
	// Target 686 is far below current 1000; step 3% caps the move at 970.
	decision := decideOzonCompetitorFollow(1000, 500, 700, 2, 3)
	if !decision.ShouldChange || decision.NewPriceRub != 970 {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestDecideOzonCompetitorFollow_Skips(t *testing.T) {
	if d := decideOzonCompetitorFollow(1000, 700, 0, 2, 3); d.SkipReason != "no_competitor_price" {
		t.Fatalf("expected no_competitor_price, got %+v", d)
	}
	if d := decideOzonCompetitorFollow(0, 700, 900, 2, 3); d.SkipReason != "invalid_current_price" {
		t.Fatalf("expected invalid_current_price, got %+v", d)
	}
	// Dead-band: competitor implies current price already.
	if d := decideOzonCompetitorFollow(1000, 700, 1015, 1, 3); d.ShouldChange {
		t.Fatalf("expected dead-band no-op, got %+v", d)
	}
}

func TestOzonHourlyGuardReason(t *testing.T) {
	// Strategy cap (2/hour, safety margin under Ozon's hard 10).
	if reason := ozonHourlyGuardReason(1, ozonStrategyHourlyWriteCap); reason != "" {
		t.Fatalf("expected pass below cap, got %q", reason)
	}
	if reason := ozonHourlyGuardReason(2, ozonStrategyHourlyWriteCap); reason == "" {
		t.Fatal("expected block at strategy cap")
	}
	// Hard Ozon cap (10/hour) for manual writes and rollbacks.
	if reason := ozonHourlyGuardReason(9, ozonHardHourlyWriteCap); reason != "" {
		t.Fatalf("expected pass at 9/10, got %q", reason)
	}
	reason := ozonHourlyGuardReason(10, ozonHardHourlyWriteCap)
	if reason == "" || !strings.Contains(reason, "10/10") {
		t.Fatalf("expected block at 10/10, got %q", reason)
	}
}

func TestOzonEffectiveNetPrice_FallbackToSellicoEconomics(t *testing.T) {
	// No net_price from Ozon → cost comes from the Sellico unit-economics
	// mirror: cost_price + other_costs. logistics_cost_rub must NOT be added
	// (Ozon logistics is already inside the commission percentages).
	econ := &sqlcgen.OzonProductEconomic{
		CostPriceRub:     floatToPgNumeric(400),
		OtherCostsRub:    floatToPgNumeric(50),
		LogisticsCostRub: floatToPgNumeric(120),
	}
	if cost := ozonEffectiveNetPrice(0, econ); cost != 450 {
		t.Fatalf("cost = %v, want 450 (400 cost + 50 other, no logistics)", cost)
	}
	// The fallback cost must feed the same floor formula as a native net_price:
	// (450 + 25) / (1 − 0.20) = 593.75
	floor, reason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:      ozonEffectiveNetPrice(0, econ),
		CommissionFBOPct: 20,
		AcquiringRub:     25,
	})
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if floor != 593.75 {
		t.Fatalf("floor = %v, want 593.75", floor)
	}
}

func TestOzonEffectiveNetPrice_PrefersNativeNetPrice(t *testing.T) {
	econ := &sqlcgen.OzonProductEconomic{CostPriceRub: floatToPgNumeric(400)}
	if cost := ozonEffectiveNetPrice(500, econ); cost != 500 {
		t.Fatalf("cost = %v, want 500 (Ozon net_price wins over economics)", cost)
	}
}

func TestOzonEffectiveNetPrice_NoData(t *testing.T) {
	if cost := ozonEffectiveNetPrice(0, nil); cost != 0 {
		t.Fatalf("cost = %v, want 0 without any source", cost)
	}
	// A row without a positive cost_price is unusable even when present.
	if cost := ozonEffectiveNetPrice(0, &sqlcgen.OzonProductEconomic{OtherCostsRub: floatToPgNumeric(50)}); cost != 0 {
		t.Fatalf("cost = %v, want 0 when cost_price is absent", cost)
	}
}
