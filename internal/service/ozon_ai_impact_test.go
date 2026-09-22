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

	// An incomplete after-week is still pending.
	outcome := aiImpactOutcome(before, aiImpactWindowTotals{Days: 3}, now.AddDate(0, 0, -5), now)
	assert.Equal(t, domain.AIOutcomePendingEval, outcome.Status)

	// Two complete, elapsed windows can be evaluated.
	outcome = aiImpactOutcome(before, aiImpactWindowTotals{Days: aiImpactWindowDays}, now.AddDate(0, 0, -8), now)
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

	// A missing baseline must not be compared with a complete after-week.
	outcome = aiImpactOutcome(aiImpactWindowTotals{}, aiImpactWindowTotals{Days: 7}, now.AddDate(0, 0, -8), now)
	assert.Equal(t, domain.AIOutcomePendingEval, outcome.Status)
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

func TestAIImpactAggregate_ObservedDeltasDoNotProveSavings(t *testing.T) {
	rows := []aiImpactRow{
		// Observed spend and revenue dropped; causal savings are unknown.
		{Evaluated: true, HasNumbers: true, SpendB: 300, SpendA: 200, RevenueB: 1000, RevenueA: 950,
			DRRBefore: f(30), DRRAfter: f(21.05)},
		// The second campaign also spent less and sold less.
		{Evaluated: true, HasNumbers: true, SpendB: 300, SpendA: 100, RevenueB: 1000, RevenueA: 500,
			DRRBefore: f(30), DRRAfter: f(20)},
		// The third campaign spent more and sold more.
		{Evaluated: true, HasNumbers: true, SpendB: 100, SpendA: 180, RevenueB: 800, RevenueA: 1200,
			DRRBefore: f(12.5), DRRAfter: f(15)},
		// Applied but not yet evaluated — counted only in decisions_applied.
		{Evaluated: false},
	}

	for i := range rows {
		rows[i].CampaignID = int64(i + 1)
		rows[i].AppliedAt = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	}
	summary := aiImpactAggregate(rows)

	assert.Equal(t, int64(4), summary.DecisionsApplied)
	assert.Equal(t, int64(3), summary.DecisionsEvaluated)

	// spend_delta = (200-300) + (100-300) + (180-100) = -220
	assert.InDelta(t, -220, summary.SpendDeltaRub, 0.001)
	// revenue_delta = (950-1000) + (500-1000) + (1200-800) = -150
	assert.InDelta(t, -150, summary.RevenueDeltaRub, 0.001)
	assert.Nil(t, summary.SavedRub)
	assert.Nil(t, summary.ExtraRevenueRub)
	assert.Equal(t, "observed_only", summary.AttributionStatus)

	require.NotNil(t, summary.AvgDRRBefore)
	require.NotNil(t, summary.AvgDRRAfter)
	assert.InDelta(t, (30+30+12.5)/3, *summary.AvgDRRBefore, 0.01)
	assert.InDelta(t, (21.05+20+15)/3, *summary.AvgDRRAfter, 0.01)
}

func TestAIImpactAggregate_Empty(t *testing.T) {
	summary := aiImpactAggregate(nil)
	assert.Equal(t, int64(0), summary.DecisionsApplied)
	assert.Equal(t, int64(0), summary.DecisionsEvaluated)
	assert.Nil(t, summary.AvgDRRBefore)
	assert.Nil(t, summary.AvgDRRAfter)
	assert.Equal(t, aiImpactSummaryDays, summary.WindowDays)
}
