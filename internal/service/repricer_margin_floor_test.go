package service

import (
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestClampToMarginFloor(t *testing.T) {
	// already below the floor (85% discount): a manual +5% must stay +5%, not jump to the floor base
	cur := domain.ProductPrice{PriceRub: 2132, DiscountPercent: 85}
	if got, ok := clampToMarginFloor(cur, 2239, 85, 2549); !ok || got != 2239 {
		t.Fatalf("upward move below floor: got %d, ok=%v; want 2239, true", got, ok)
	}

	// already below the floor and moving down — refuse
	if _, ok := clampToMarginFloor(cur, 2000, 85, 2549); ok {
		t.Fatal("downward move below floor should be skipped")
	}

	// above the floor and crossing it — clamp exactly to the floor
	above := domain.ProductPrice{PriceRub: 3000, DiscountPercent: 0}
	if got, ok := clampToMarginFloor(above, 2000, 0, 2549); !ok || got != 2549 {
		t.Fatalf("crossing the floor: got %d, ok=%v; want 2549, true", got, ok)
	}

	// above the floor and staying above — untouched
	if got, ok := clampToMarginFloor(above, 3150, 0, 2549); !ok || got != 3150 {
		t.Fatalf("above floor: got %d, ok=%v; want 3150, true", got, ok)
	}
}
