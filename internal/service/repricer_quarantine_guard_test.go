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
