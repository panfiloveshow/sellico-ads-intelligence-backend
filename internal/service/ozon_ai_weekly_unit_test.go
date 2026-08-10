package service

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestOzonSKUMarginPct(t *testing.T) {
	t.Run("positive margin", func(t *testing.T) {
		// price 1000, cost 400, commission 15% (=150), acquiring 20 → margin
		// (1000-400-150-20)/1000*100 = 43%.
		m := ozonSKUMarginPct(1000, 400, 15, 20)
		require.NotNil(t, m)
		assert.InDelta(t, 43.0, *m, 0.01)
	})

	t.Run("negative margin is returned", func(t *testing.T) {
		// cost above what commission leaves → negative, must not be nil.
		m := ozonSKUMarginPct(1000, 950, 20, 30)
		require.NotNil(t, m)
		assert.Less(t, *m, 0.0)
	})

	t.Run("unknown cost → nil", func(t *testing.T) {
		assert.Nil(t, ozonSKUMarginPct(1000, 0, 15, 20))
	})

	t.Run("unknown price → nil", func(t *testing.T) {
		assert.Nil(t, ozonSKUMarginPct(0, 400, 15, 20))
	})
}

func TestIsoWeekStart(t *testing.T) {
	// A Wednesday resolves to that week's Monday.
	wed := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC) // 2026-08-12 is a Wednesday
	assert.Equal(t, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), isoWeekStart(wed))
	// Sunday resolves back to the same week's Monday (weekday 0 handled as 7).
	sun := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC) // Sunday
	assert.Equal(t, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), isoWeekStart(sun))
}

func readinessStats(total, shadowTotal, shadowPassed, evaluatedPairs int64, firstShadowAt *time.Time, avgDelta float64) sqlcgen.GetAIReadinessStatsRow {
	row := sqlcgen.GetAIReadinessStatsRow{
		DecisionsTotal: total,
		ShadowTotal:    shadowTotal,
		ShadowPassed:   shadowPassed,
		EvaluatedPairs: evaluatedPairs,
		AvgDrrDelta:    floatToPgNumeric(avgDelta),
	}
	if firstShadowAt != nil {
		row.FirstShadowAt = pgtype.Timestamptz{Time: *firstShadowAt, Valid: true}
	}
	return row
}

func TestComputeAIReadiness(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	t.Run("recommends when all thresholds met", func(t *testing.T) {
		created := now.AddDate(0, 0, -8) // 8 shadow days
		stats := readinessStats(12, 12, 11, 5, nil, -3.5)
		r := computeAIReadiness(1, created, stats, now)
		assert.Equal(t, 1, r.CurrentLevel)
		assert.Equal(t, 8, r.ShadowDays)
		assert.EqualValues(t, 12, r.DecisionsTotal)
		assert.InDelta(t, 91.67, r.WithinGuardrailsPct, 0.1)
		require.NotNil(t, r.ProjectedDRRDelta)
		assert.InDelta(t, -3.5, *r.ProjectedDRRDelta, 0.01)
		assert.True(t, r.RecommendNextLevel)
	})

	t.Run("too few days blocks", func(t *testing.T) {
		created := now.AddDate(0, 0, -2) // only 2 days
		stats := readinessStats(20, 20, 20, 5, nil, -1)
		r := computeAIReadiness(1, created, stats, now)
		assert.False(t, r.RecommendNextLevel)
		assert.Contains(t, r.Reason, "понаблюдать")
	})

	t.Run("too few decisions blocks", func(t *testing.T) {
		created := now.AddDate(0, 0, -10)
		stats := readinessStats(4, 4, 4, 0, nil, 0)
		r := computeAIReadiness(1, created, stats, now)
		assert.False(t, r.RecommendNextLevel)
		assert.Nil(t, r.ProjectedDRRDelta) // no evaluated pairs
	})

	t.Run("low guardrail share blocks", func(t *testing.T) {
		created := now.AddDate(0, 0, -10)
		stats := readinessStats(20, 20, 10, 3, nil, -2) // 50% within guardrails
		r := computeAIReadiness(1, created, stats, now)
		assert.False(t, r.RecommendNextLevel)
		assert.InDelta(t, 50.0, r.WithinGuardrailsPct, 0.01)
	})

	t.Run("max level never recommends", func(t *testing.T) {
		created := now.AddDate(0, 0, -30)
		stats := readinessStats(50, 50, 50, 10, nil, -5)
		r := computeAIReadiness(3, created, stats, now)
		assert.False(t, r.RecommendNextLevel)
		assert.Contains(t, r.Reason, "максимальном")
	})

	t.Run("shadow_days uses earlier of strategy and first shadow decision", func(t *testing.T) {
		created := now.AddDate(0, 0, -3)
		firstShadow := now.AddDate(0, 0, -9)
		stats := readinessStats(12, 12, 12, 0, &firstShadow, 0)
		r := computeAIReadiness(1, created, stats, now)
		assert.Equal(t, 9, r.ShadowDays)
	})
}
