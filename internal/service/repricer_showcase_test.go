package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestCatalogSppPercentUsesCurrentSellerPrice(t *testing.T) {
	base, discount, current := int64(762), 22, int64(594)
	item := domain.ProductCatalogItem{
		PriceRub:           &base,
		DiscountPercent:    &discount,
		DiscountedPriceRub: &current,
	}

	assert.Equal(t, 43.1, catalogSppPercent(item, 338))
}

func TestCatalogSppPercentFallsBackToCalculatedEffectivePrice(t *testing.T) {
	base, discount := int64(1000), 20
	item := domain.ProductCatalogItem{PriceRub: &base, DiscountPercent: &discount}

	assert.Equal(t, 10.0, catalogSppPercent(item, 720))
	assert.Zero(t, catalogSppPercent(item, 800))
}
