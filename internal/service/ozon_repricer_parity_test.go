package service

import (
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// --- inventory demand (ozon_price_inventory_demand) ---

func TestDecideOzonInventoryDemand_OverstockStepsDown(t *testing.T) {
	// 300 units at 1 unit/day = 300 days of stock (> 60 default) and slow
	// velocity threshold 2/day → step down 3% (default): 1000 → 970.
	params := domain.StrategyParams{SlowVelocityPerDay: 2}
	d := decideOzonInventoryDemand(1000, 500, 300, true, 1, params)
	if !d.ShouldChange || d.Direction != "down" {
		t.Fatalf("decision = %+v, want a down change", d)
	}
	if d.NewPriceRub != 970 {
		t.Fatalf("new price = %v, want 970", d.NewPriceRub)
	}
}

func TestDecideOzonInventoryDemand_DownClampedToFloor(t *testing.T) {
	// The 3% step (970) undershoots the 990₽ floor → clamp to the floor.
	params := domain.StrategyParams{SlowVelocityPerDay: 2}
	d := decideOzonInventoryDemand(1000, 990, 300, true, 1, params)
	if !d.ShouldChange || d.Direction != "down" {
		t.Fatalf("decision = %+v, want a down change", d)
	}
	if d.NewPriceRub != 990 {
		t.Fatalf("new price = %v, want floor 990", d.NewPriceRub)
	}
}

func TestDecideOzonInventoryDemand_LowStockRaisesToCap(t *testing.T) {
	// 5 units at 1 unit/day = 5 days (< 14 default) → step up 3%, capped by
	// max_price_rub 1020: 1000 → 1020.
	maxPrice := int64(1020)
	params := domain.StrategyParams{MaxPriceRub: &maxPrice}
	d := decideOzonInventoryDemand(1000, 500, 5, true, 1, params)
	if !d.ShouldChange || d.Direction != "up" {
		t.Fatalf("decision = %+v, want an up change", d)
	}
	if d.NewPriceRub != 1020 {
		t.Fatalf("new price = %v, want 1020", d.NewPriceRub)
	}
}

func TestDecideOzonInventoryDemand_LowStockWithoutMaxPriceSkips(t *testing.T) {
	d := decideOzonInventoryDemand(1000, 500, 5, true, 1, domain.StrategyParams{})
	if d.ShouldChange || d.SkipReason != "max_price_required_for_increase" {
		t.Fatalf("decision = %+v, want skip max_price_required_for_increase", d)
	}
}

func TestDecideOzonInventoryDemand_UnknownStockSkips(t *testing.T) {
	d := decideOzonInventoryDemand(1000, 500, 0, false, 1, domain.StrategyParams{})
	if d.ShouldChange || d.SkipReason != "stock_unknown" {
		t.Fatalf("decision = %+v, want skip stock_unknown", d)
	}
}

func TestDecideOzonInventoryDemand_BalancedNoChange(t *testing.T) {
	// 30 days of stock with active sales — between low (14) and overstock (60).
	d := decideOzonInventoryDemand(1000, 500, 90, true, 3, domain.StrategyParams{})
	if d.ShouldChange {
		t.Fatalf("decision = %+v, want no change", d)
	}
	if d.Reason != "inventory_balanced" {
		t.Fatalf("reason = %q, want inventory_balanced", d.Reason)
	}
}

// --- ad-linked (ozon_price_ad_linked) ---

func floatPtr(v float64) *float64 { return &v }

func TestDecideOzonAdLinked_DRRMath(t *testing.T) {
	// spend 300₽ / revenue 2000₽ → ДРР 15%; ceiling 10% → step down 3%.
	drr := 300.0 / 2000.0 * 100
	if drr != 15 {
		t.Fatalf("drr = %v, want 15", drr)
	}
	params := domain.StrategyParams{MaxAllowedDRRPercent: floatPtr(10)}
	d := decideOzonAdLinked(1000, 500, &drr, 300, nil, params)
	if !d.ShouldChange || d.Direction != "down" {
		t.Fatalf("decision = %+v, want a down change", d)
	}
	if d.NewPriceRub != 970 {
		t.Fatalf("new price = %v, want 970", d.NewPriceRub)
	}
}

func TestDecideOzonAdLinked_DownClampedToFloor(t *testing.T) {
	params := domain.StrategyParams{MaxAllowedDRRPercent: floatPtr(10)}
	d := decideOzonAdLinked(1000, 995, floatPtr(25), 300, nil, params)
	if !d.ShouldChange || d.NewPriceRub != 995 {
		t.Fatalf("decision = %+v, want change clamped to floor 995", d)
	}
}

func TestDecideOzonAdLinked_WithinLimitNoChange(t *testing.T) {
	params := domain.StrategyParams{MaxAllowedDRRPercent: floatPtr(20)}
	d := decideOzonAdLinked(1000, 500, floatPtr(15), 300, nil, params)
	if d.ShouldChange {
		t.Fatalf("decision = %+v, want no change", d)
	}
	if d.Reason != "drr_within_limit" {
		t.Fatalf("reason = %q, want drr_within_limit", d.Reason)
	}
}

func TestDecideOzonAdLinked_NoSpendSkips(t *testing.T) {
	params := domain.StrategyParams{MaxAllowedDRRPercent: floatPtr(10)}
	d := decideOzonAdLinked(1000, 500, floatPtr(25), 0, nil, params)
	if d.ShouldChange || d.SkipReason != "no_ad_spend" {
		t.Fatalf("decision = %+v, want skip no_ad_spend", d)
	}
}

func TestDecideOzonAdLinked_MissingDRRSkips(t *testing.T) {
	// Spend without revenue → ДРР is undefined (WB parity: skip, not infinity).
	params := domain.StrategyParams{MaxAllowedDRRPercent: floatPtr(10)}
	d := decideOzonAdLinked(1000, 500, nil, 300, nil, params)
	if d.ShouldChange || d.SkipReason != "missing_ad_data" {
		t.Fatalf("decision = %+v, want skip missing_ad_data", d)
	}
}

func TestDecideOzonAdLinked_EconomicsFallbackCeiling(t *testing.T) {
	// No strategy ceiling — the Sellico economics max_allowed_drr (10%)
	// applies and 15% ДРР triggers the step down.
	d := decideOzonAdLinked(1000, 500, floatPtr(15), 300, floatPtr(10), domain.StrategyParams{})
	if !d.ShouldChange || d.Direction != "down" {
		t.Fatalf("decision = %+v, want a down change via economics ceiling", d)
	}
}

// --- schedule due/revert selection (pure helpers) ---

func TestOzonScheduleIsDue(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		status   string
		startsAt time.Time
		want     bool
	}{
		{"pending in the past fires", domain.OzonScheduleStatusPending, now.Add(-time.Minute), true},
		{"pending exactly now fires", domain.OzonScheduleStatusPending, now, true},
		{"pending in the future waits", domain.OzonScheduleStatusPending, now.Add(time.Minute), false},
		{"cancelled never fires", domain.OzonScheduleStatusCancelled, now.Add(-time.Hour), false},
		{"applied never re-fires", domain.OzonScheduleStatusApplied, now.Add(-time.Hour), false},
	}
	for _, tc := range cases {
		if got := ozonScheduleIsDue(tc.status, tc.startsAt, now); got != tc.want {
			t.Errorf("%s: ozonScheduleIsDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOzonScheduleNeedsRevert(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	cases := []struct {
		name   string
		status string
		endsAt *time.Time
		revert *float64
		want   bool
	}{
		{"applied with expired ends_at reverts", domain.OzonScheduleStatusApplied, &past, floatPtr(990), true},
		{"applied before ends_at waits", domain.OzonScheduleStatusApplied, &future, floatPtr(990), false},
		{"applied without ends_at never reverts", domain.OzonScheduleStatusApplied, nil, floatPtr(990), false},
		{"applied without revert price never reverts", domain.OzonScheduleStatusApplied, &past, nil, false},
		{"pending never reverts", domain.OzonScheduleStatusPending, &past, floatPtr(990), false},
		{"reverted never re-reverts", domain.OzonScheduleStatusReverted, &past, floatPtr(990), false},
	}
	for _, tc := range cases {
		if got := ozonScheduleNeedsRevert(tc.status, tc.endsAt, tc.revert, now); got != tc.want {
			t.Errorf("%s: ozonScheduleNeedsRevert = %v, want %v", tc.name, got, tc.want)
		}
	}
}
