package handler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProductEconomicsCSV(t *testing.T) {
	csvBody := `wb_product_id,cost_price,logistics_cost,max_allowed_drr,source,effective_at
101,600,90,25.5,csv,2026-05-28
`

	rows, err := parseProductEconomicsCSV(strings.NewReader(csvBody))

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(101), rows[0].WBProductID)
	require.NotNil(t, rows[0].CostPrice)
	require.Equal(t, int64(600), *rows[0].CostPrice)
	require.NotNil(t, rows[0].LogisticsCost)
	require.Equal(t, int64(90), *rows[0].LogisticsCost)
	require.NotNil(t, rows[0].MaxAllowedDRR)
	require.Equal(t, 25.5, *rows[0].MaxAllowedDRR)
	require.Equal(t, "csv", rows[0].Source)
	require.NotNil(t, rows[0].EffectiveAt)
	require.Equal(t, "2026-05-28", rows[0].EffectiveAt.Format("2006-01-02"))
}

func TestParseProductEconomicsCSVRequiresWBProductID(t *testing.T) {
	_, err := parseProductEconomicsCSV(strings.NewReader("cost_price\n100\n"))

	require.EqualError(t, err, "csv must include wb_product_id")
}
