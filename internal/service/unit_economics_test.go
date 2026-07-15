package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUnitEconomicsReadiness_AllowsBidIncreaseOnlyWithFreshCompleteSource(t *testing.T) {
	readiness := &UnitEconomicsReadiness{
		Source:               "sellico-unit-economics",
		CheckedAt:            time.Now(),
		Complete:             true,
		CheckedProductIDs:    []int64{101},
		MaxAllowedDRRPercent: 20,
	}

	require.True(t, readiness.AllowsBidIncrease())
	require.Empty(t, readiness.BlockReason())
}

func TestUnitEconomicsReadiness_BlocksMissingTimestampAndCoverage(t *testing.T) {
	readiness := &UnitEconomicsReadiness{Source: "sellico-unit-economics", Complete: true, CheckedProductIDs: []int64{101}, MaxAllowedDRRPercent: 20}
	require.False(t, readiness.AllowsBidIncrease())
	require.Equal(t, "unit economics check timestamp is unavailable", readiness.BlockReason())

	readiness.CheckedAt = time.Now()
	readiness.Complete = false
	require.False(t, readiness.AllowsBidIncrease())
	require.Equal(t, "unit economics coverage is incomplete", readiness.BlockReason())
}

func TestUnitEconomicsReadiness_BlocksMissingSource(t *testing.T) {
	readiness := &UnitEconomicsReadiness{}

	require.False(t, readiness.AllowsBidIncrease())
	require.Equal(t, "unit economics source is unavailable", readiness.BlockReason())
}

func TestUnitEconomicsReadiness_BlocksMissingUnprofitableAndStaleProducts(t *testing.T) {
	require.Equal(t, "unit economics is missing for 2 product(s): wb_product_ids=101,102", (&UnitEconomicsReadiness{
		Source:                     "sellico-unit-economics",
		MissingEconomicsProductIDs: []int64{101, 102},
	}).BlockReason())

	require.Equal(t, "unit economics marks 1 product(s) as unprofitable: wb_product_ids=202", (&UnitEconomicsReadiness{
		Source:                 "sellico-unit-economics",
		UnprofitableProductIDs: []int64{202},
	}).BlockReason())

	require.Equal(t, "unit economics is stale for 1 product(s): wb_product_ids=303", (&UnitEconomicsReadiness{
		Source:          "sellico-unit-economics",
		StaleProductIDs: []int64{303},
	}).BlockReason())
}

func TestUnitEconomicsReadiness_BlockReasonLimitsProductIDSample(t *testing.T) {
	reason := (&UnitEconomicsReadiness{
		Source:                     "sellico-unit-economics",
		MissingEconomicsProductIDs: []int64{101, 102, 103, 104, 105, 106, 107},
	}).BlockReason()

	require.Contains(t, reason, "wb_product_ids=101,102,103,104,105,+2 more")
}
