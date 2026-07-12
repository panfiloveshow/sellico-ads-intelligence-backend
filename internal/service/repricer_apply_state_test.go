package service

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestPartialPriceGoodFailure(t *testing.T) {
	t.Parallel()

	reason, failed := partialPriceGoodFailure(wb.PriceTaskGood{Status: 3})
	require.False(t, failed)
	require.Empty(t, reason)

	reason, failed = partialPriceGoodFailure(wb.PriceTaskGood{Status: 4, ErrorText: "bad discount"})
	require.True(t, failed)
	require.Equal(t, "bad discount", reason)

	reason, failed = partialPriceGoodFailure(wb.PriceTaskGood{Status: 6})
	require.True(t, failed)
	require.Equal(t, "WB item status 6", reason)
}

func TestRollbackBaselineMatches(t *testing.T) {
	t.Parallel()

	change := sqlcgen.PriceChange{NewPriceRub: 1250, NewDiscountPercent: 17}
	require.True(t, rollbackBaselineMatches(sqlcgen.ProductPrice{
		PriceRub:        1250,
		DiscountPercent: 17,
	}, change))
	require.False(t, rollbackBaselineMatches(sqlcgen.ProductPrice{
		PriceRub:        1249,
		DiscountPercent: 17,
	}, change))
	require.False(t, rollbackBaselineMatches(sqlcgen.ProductPrice{
		PriceRub:        1250,
		DiscountPercent: 18,
	}, change))
}
