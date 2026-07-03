package sellico

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseUnitEconomicsReadinessPayload(t *testing.T) {
	parsed := parseUnitEconomicsReadinessPayload(map[string]any{
		"source":                        "sellico-unit-economics",
		"checked_at":                    "2026-05-27T12:00:00Z",
		"missing_economics_product_ids": []any{float64(101), "102"},
		"unprofitable_product_ids":      []any{float64(201)},
		"stale_product_ids":             []any{"301"},
	})

	require.Equal(t, "sellico-unit-economics", parsed.Source)
	require.Equal(t, int64(101), parsed.MissingEconomicsProductIDs[0])
	require.Equal(t, int64(102), parsed.MissingEconomicsProductIDs[1])
	require.Equal(t, []int64{201}, parsed.UnprofitableProductIDs)
	require.Equal(t, []int64{301}, parsed.StaleProductIDs)
	require.False(t, parsed.CheckedAt.IsZero())
}

func TestParseUnitEconomicsReadinessPayload_UnwrapsNestedReadiness(t *testing.T) {
	parsed := parseUnitEconomicsReadinessPayload(map[string]any{
		"readiness": map[string]any{
			"data_source":                "unit-economics",
			"missingEconomicsProductIds": []any{"777"},
		},
	})

	require.Equal(t, "unit-economics", parsed.Source)
	require.Equal(t, []int64{777}, parsed.MissingEconomicsProductIDs)
}
