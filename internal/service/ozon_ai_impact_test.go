package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestAIImpactWindows(t *testing.T) {
	applied := time.Date(2026, 8, 1, 14, 30, 0, 0, time.UTC)
	w := aiImpactWindows(applied)

	// Before: [Jul 25, Jul 31]; the apply day (Aug 1) belongs to neither side.
	assert.Equal(t, time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC), w.BeforeFrom)
	assert.Equal(t, time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC), w.BeforeTo)
	// After: [Aug 2, Aug 8].
	assert.Equal(t, time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), w.AfterFrom)
	assert.Equal(t, time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC), w.AfterTo)

	// Both windows span exactly 7 calendar days.
	assert.Equal(t, 6*24*time.Hour, w.BeforeTo.Sub(w.BeforeFrom))
	assert.Equal(t, 6*24*time.Hour, w.AfterTo.Sub(w.AfterFrom))
}

func TestAIImpactOutcome_States(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	before := aiImpactWindowTotals{SpendRub: 100, RevenueRub: 1000, Days: 7}

	// Enough after-days → evaluated.
	outcome := aiImpactOutcome(before, aiImpactWindowTotals{Days: 3}, now.AddDate(0, 0, -5), now)
	assert.Equal(t, domain.AIOutcomeEvaluated, outcome.Status)

	// Exactly the minimum counts.
	outcome = aiImpactOutcome(before, aiImpactWindowTotals{Days: aiImpactMinAfterDays}, now.AddDate(0, 0, -4), now)
	assert.Equal(t, domain.AIOutcomeEvaluated, outcome.Status)

	// Too few after-days, decision still young → pending.
	outcome = aiImpactOutcome(before, aiImpactWindowTotals{Days: 2}, now.AddDate(0, 0, -5), now)
	assert.Equal(t, domain.AIOutcomePendingEval, outcome.Status)

	// Too few after-days and older than 14 days → not evaluable.
	outcome = aiImpactOutcome(before, aiImpactWindowTotals{Days: 2}, now.AddDate(0, 0, -15), now)
	assert.Equal(t, domain.AIOutcomeNotEvaluable, outcome.Status)

	// Exactly 14 days is still within the waiting budget.
	outcome = aiImpactOutcome(before, aiImpactWindowTotals{Days: 0}, now.AddDate(0, 0, -14), now)
	assert.Equal(t, domain.AIOutcomePendingEval, outcome.Status)

	// An empty before-window does not block evaluation.
	outcome = aiImpactOutcome(aiImpactWindowTotals{}, aiImpactWindowTotals{Days: 7}, now.AddDate(0, 0, -8), now)
	assert.Equal(t, domain.AIOutcomeEvaluated, outcome.Status)
}

func TestDRRPct(t *testing.T) {
	drr := drrPct(150, 1000)
	require.NotNil(t, drr)
	assert.InDelta(t, 15.0, *drr, 0.001)

	// Zero / negative revenue → undefined, not zero.
	assert.Nil(t, drrPct(150, 0))
	assert.Nil(t, drrPct(0, 0))
	assert.Nil(t, drrPct(10, -5))
}

func f(v float64) *float64 { return &v }

func TestAIImpactAggregate_SavedAndExtraFormulas(t *testing.T) {
	rows := []aiImpactRow{
		// Spend dropped 300→200, revenue held (1000→950 ≥ 900): saved 100.
		{Evaluated: true, HasNumbers: true, SpendB: 300, SpendA: 200, RevenueB: 1000, RevenueA: 950,
			DRRBefore: f(30), DRRAfter: f(21.05)},
		// Spend dropped but revenue collapsed below 90% → no saving counted.
		{Evaluated: true, HasNumbers: true, SpendB: 300, SpendA: 100, RevenueB: 1000, RevenueA: 500,
			DRRBefore: f(30), DRRAfter: f(20)},
		// Spend grew, revenue grew: extra revenue 400, no saving.
		{Evaluated: true, HasNumbers: true, SpendB: 100, SpendA: 180, RevenueB: 800, RevenueA: 1200,
			DRRBefore: f(12.5), DRRAfter: f(15)},
		// Applied but not yet evaluated — counted only in decisions_applied.
		{Evaluated: false},
	}

	summary := aiImpactAggregate(rows)

	assert.Equal(t, int64(4), summary.DecisionsApplied)
	assert.Equal(t, int64(3), summary.DecisionsEvaluated)

	// spend_delta = (200-300) + (100-300) + (180-100) = -220
	assert.InDelta(t, -220, summary.SpendDeltaRub, 0.001)
	// revenue_delta = (950-1000) + (500-1000) + (1200-800) = -150
	assert.InDelta(t, -150, summary.RevenueDeltaRub, 0.001)
	// saved: only row 1 qualifies (revenue held): 300-200 = 100
	assert.InDelta(t, 100, summary.SavedRub, 0.001)
	// extra revenue: only row 3 grew: 400
	assert.InDelta(t, 400, summary.ExtraRevenueRub, 0.001)

	require.NotNil(t, summary.AvgDRRBefore)
	require.NotNil(t, summary.AvgDRRAfter)
	assert.InDelta(t, (30+30+12.5)/3, *summary.AvgDRRBefore, 0.01)
	assert.InDelta(t, (21.05+20+15)/3, *summary.AvgDRRAfter, 0.01)
}

func TestAIImpactAggregate_RevenueHoldBoundary(t *testing.T) {
	// revenue_after exactly at 90% of revenue_before still counts as held.
	rows := []aiImpactRow{
		{Evaluated: true, HasNumbers: true, SpendB: 200, SpendA: 150, RevenueB: 1000, RevenueA: 900},
	}
	summary := aiImpactAggregate(rows)
	assert.InDelta(t, 50, summary.SavedRub, 0.001)

	// A hair below 90% no longer counts.
	rows[0].RevenueA = 899.99
	summary = aiImpactAggregate(rows)
	assert.InDelta(t, 0, summary.SavedRub, 0.001)
}

func TestAIImpactAggregate_Empty(t *testing.T) {
	summary := aiImpactAggregate(nil)
	assert.Equal(t, int64(0), summary.DecisionsApplied)
	assert.Equal(t, int64(0), summary.DecisionsEvaluated)
	assert.Nil(t, summary.AvgDRRBefore)
	assert.Nil(t, summary.AvgDRRAfter)
	assert.Equal(t, aiImpactSummaryDays, summary.WindowDays)
}

func TestAIImpactAggregate_SpendIncreaseNeverSaves(t *testing.T) {
	rows := []aiImpactRow{
		{Evaluated: true, HasNumbers: true, SpendB: 100, SpendA: 100, RevenueB: 500, RevenueA: 500},
		{Evaluated: true, HasNumbers: true, SpendB: 100, SpendA: 150, RevenueB: 500, RevenueA: 600},
	}
	summary := aiImpactAggregate(rows)
	assert.InDelta(t, 0, summary.SavedRub, 0.001)
	assert.InDelta(t, 100, summary.ExtraRevenueRub, 0.001)
}
