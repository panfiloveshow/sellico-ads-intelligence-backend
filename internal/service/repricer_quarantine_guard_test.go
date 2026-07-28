package service

import (
	"testing"
)

func TestCheckQuarantineRisk(t *testing.T) {
	// the real case: rolling 16991 back to 2132 is an 8x drop — WB quarantines it
	err := checkQuarantineRisk(priceChangeIntent{NmID: 184010772, OldPriceRub: 16991, NewPriceRub: 2132})
	if err == nil {
		t.Fatal("8x drop should be rejected before reaching WB")
	}

	// exactly 3x down — WB's threshold is inclusive, still rejected
	if checkQuarantineRisk(priceChangeIntent{OldPriceRub: 3000, NewPriceRub: 1000}) == nil {
		t.Fatal("3x drop should be rejected")
	}

	// just under 3x — allowed
	if err := checkQuarantineRisk(priceChangeIntent{OldPriceRub: 2999, NewPriceRub: 1000}); err != nil {
		t.Fatalf("sub-3x drop should pass: %v", err)
	}

	// raises are never quarantined
	if err := checkQuarantineRisk(priceChangeIntent{OldPriceRub: 2132, NewPriceRub: 16991}); err != nil {
		t.Fatalf("raise should pass: %v", err)
	}

	// discounts are what WB compares: same base, deep new discount = 5x drop for the buyer
	if checkQuarantineRisk(priceChangeIntent{OldPriceRub: 1000, OldDiscount: 0, NewPriceRub: 1000, NewDiscount: 80}) == nil {
		t.Fatal("drop via discount should be rejected")
	}
}

func TestCheckRaiseAnomaly(t *testing.T) {
	// the real July case: a floor-lifted +5% shipped 2132 → 16991 (8x) to WB
	err := checkRaiseAnomaly(priceChangeIntent{NmID: 184010772, Source: "manual", OldPriceRub: 2132, NewPriceRub: 16991})
	if err == nil {
		t.Fatal("8x raise should be rejected before reaching WB")
	}

	// exactly 3x up — rejected, same inclusive threshold as the drop guard
	if checkRaiseAnomaly(priceChangeIntent{Source: "schedule", OldPriceRub: 1000, NewPriceRub: 3000}) == nil {
		t.Fatal("3x raise should be rejected")
	}

	// just under 3x — allowed
	if err := checkRaiseAnomaly(priceChangeIntent{Source: "schedule", OldPriceRub: 1000, NewPriceRub: 2999}); err != nil {
		t.Fatalf("sub-3x raise should pass: %v", err)
	}

	// strategy margin-floor recovery makes large corrective raises on purpose
	if err := checkRaiseAnomaly(priceChangeIntent{Source: "strategy", OldPriceRub: 2132, NewPriceRub: 16991}); err != nil {
		t.Fatalf("strategy raise should pass: %v", err)
	}

	// no baseline — nothing to compare against
	if err := checkRaiseAnomaly(priceChangeIntent{Source: "manual", OldPriceRub: 0, NewPriceRub: 5000}); err != nil {
		t.Fatalf("raise without baseline should pass: %v", err)
	}

	// raise hidden in a discount cut: same base, dropping an 80% discount = 5x for the buyer
	if checkRaiseAnomaly(priceChangeIntent{Source: "manual", OldPriceRub: 1000, OldDiscount: 80, NewPriceRub: 1000, NewDiscount: 0}) == nil {
		t.Fatal("raise via discount removal should be rejected")
	}
}
