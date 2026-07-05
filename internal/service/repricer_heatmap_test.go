package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// WB statistics sends order timestamps as "2006-01-02T15:04:05" in MSK without
// a zone. Date-only rows carry no time → ok=false (skip in heatmap).
func TestParseReportDateTime(t *testing.T) {
	ts, ok := parseReportDateTime("2026-07-04T15:30:00")
	require.True(t, ok)
	assert.Equal(t, 15, ts.Hour())

	ts, ok = parseReportDateTime("2026-07-04T12:00:00Z") // RFC3339 UTC → MSK 15:00
	require.True(t, ok)
	assert.Equal(t, 15, ts.Hour())

	_, ok = parseReportDateTime("2026-07-04")
	assert.False(t, ok)

	_, ok = parseReportDateTime("")
	assert.False(t, ok)
}

func TestBuildOrdersHeatmap(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	cells := []sqlcgen.OrdersHeatmapCell{
		{DayOfWeek: 6, Hour: 18, Orders: 30, Units: 33, RevenueK: 123450},
		{DayOfWeek: 1, Hour: 9, Orders: 10, Units: 11, RevenueK: 50000},
		{DayOfWeek: 9, Hour: 30, Orders: 99, Units: 99}, // out of range → ignored
	}

	hm := buildOrdersHeatmap(cells, from, to, "units")
	require.Len(t, hm.Days, 7)
	require.Len(t, hm.Days[0].Hours, 24)

	sat := hm.Days[5].Hours[18]
	assert.Equal(t, int64(33), sat.Value)
	assert.InDelta(t, 1.0, sat.Intensity, 1e-9)

	mon := hm.Days[0].Hours[9]
	assert.Equal(t, int64(11), mon.Value)
	assert.InDelta(t, 11.0/33.0, mon.Intensity, 1e-9)

	// Empty cell stays zero.
	assert.Equal(t, int64(0), hm.Days[2].Hours[3].Value)
	assert.Equal(t, 0.0, hm.Days[2].Hours[3].Intensity)

	require.NotNil(t, hm.Peak)
	assert.Equal(t, "Сб", hm.Peak.DayLabel)
	assert.Equal(t, 18, hm.Peak.Hour)
	assert.Equal(t, int64(44), hm.Totals.Units)
	assert.Equal(t, int64(1735), hm.Totals.RevenueRub) // (123450+50000+50)/100 rounded per cell: 1235+500

	// Unknown metric falls back to units.
	hm2 := buildOrdersHeatmap(cells, from, to, "bogus")
	assert.Equal(t, "units", hm2.Metric)
}
