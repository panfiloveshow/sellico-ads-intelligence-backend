package service

import (
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestValidateProductEconomicsInputRequiresRealEconomicsField(t *testing.T) {
	_, err := validateProductEconomicsInput(domain.ProductEconomicsInput{WBProductID: 123})

	require.EqualError(t, err, "at least one economics field is required")
}

func TestValidateProductEconomicsInputRejectsInvalidPercent(t *testing.T) {
	cost := int64(100)
	percent := 101.0
	_, err := validateProductEconomicsInput(domain.ProductEconomicsInput{
		WBProductID:   123,
		CostPrice:     &cost,
		MaxAllowedDRR: &percent,
	})

	require.EqualError(t, err, "max_allowed_drr must be between 0 and 100")
}

func TestValidateProductEconomicsInputDefaultsSource(t *testing.T) {
	cost := int64(100)
	input, err := validateProductEconomicsInput(domain.ProductEconomicsInput{
		WBProductID: 123,
		CostPrice:   &cost,
	})

	require.NoError(t, err)
	require.Equal(t, "manual", input.Source)
}
