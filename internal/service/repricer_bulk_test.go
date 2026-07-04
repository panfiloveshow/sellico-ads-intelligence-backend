package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
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
