package service

import (
	"testing"
	"time"
)

func TestComputeTotalDRR(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }

	tests := []struct {
		name        string
		spend       float64
		revenue     float64
		lastData    time.Time
		maxAgeHours int
		wantStatus  string
		wantValue   float64
	}{
		{
			name:       "fresh data yields a usable percentage",
			spend:      12_000,
			revenue:    300_000,
			lastData:   day(10), // yesterday: 12h old at noon today
			wantStatus: totalDRRStatusOK,
			wantValue:  4,
		},
		{
			name:       "no turnover is undefined, never zero",
			spend:      12_000,
			revenue:    0,
			lastData:   day(10),
			wantStatus: totalDRRStatusNoData,
			wantValue:  0,
		},
		{
			name:       "no rows at all",
			spend:      0,
			revenue:    0,
			lastData:   time.Time{},
			wantStatus: totalDRRStatusNoData,
			wantValue:  0,
		},
		{
			name:       "turnover exists but the sync is behind",
			spend:      12_000,
			revenue:    300_000,
			lastData:   day(8), // window ended 2026-08-09 00:00 → 60h old
			wantStatus: totalDRRStatusStale,
			wantValue:  0,
		},
		{
			name:        "a wider freshness limit accepts the same data",
			spend:       12_000,
			revenue:     300_000,
			lastData:    day(8),
			maxAgeHours: 72,
			wantStatus:  totalDRRStatusOK,
			wantValue:   4,
		},
		{
			name:       "zero spend is a real zero, not missing data",
			spend:      0,
			revenue:    300_000,
			lastData:   day(11),
			wantStatus: totalDRRStatusOK,
			wantValue:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeTotalDRR(tc.spend, tc.revenue, tc.lastData, now, tc.maxAgeHours)
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Value != tc.wantValue {
				t.Fatalf("value = %v, want %v", got.Value, tc.wantValue)
			}
			if got.Scope != totalDRRScopeCabinet {
				t.Fatalf("scope = %q, want %q", got.Scope, totalDRRScopeCabinet)
			}
		})
	}
}

func TestTotalDRRIncreaseBlockReason(t *testing.T) {
	ceiling := func(v float64) *float64 { return &v }
	ok := func(v float64) totalDRR {
		return totalDRR{Value: v, Status: totalDRRStatusOK, Scope: totalDRRScopeCabinet}
	}

	tests := []struct {
		name      string
		max       *float64
		total     totalDRR
		wantBlock bool
	}{
		{"no ceiling configured", nil, ok(90), false},
		{"zero ceiling is not a ceiling", ceiling(0), ok(90), false},
		{"below the ceiling", ceiling(10), ok(7.5), false},
		{"exactly at the ceiling blocks", ceiling(10), ok(10), true},
		{"above the ceiling blocks", ceiling(10), ok(14.2), true},
		{
			// A stalled ozon:sync_analytics must not freeze every increase
			// across every cabinet.
			name:      "stale data never blocks",
			max:       ceiling(10),
			total:     totalDRR{Value: 0, Status: totalDRRStatusStale},
			wantBlock: false,
		},
		{
			name:      "missing data never blocks",
			max:       ceiling(10),
			total:     totalDRR{Value: 0, Status: totalDRRStatusNoData},
			wantBlock: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason := totalDRRIncreaseBlockReason(tc.max, tc.total)
			if got := reason != ""; got != tc.wantBlock {
				t.Fatalf("blocked = %v (reason %q), want %v", got, reason, tc.wantBlock)
			}
		})
	}
}

func TestComputeIncrementalDRR(t *testing.T) {
	tests := []struct {
		name                    string
		prevSpend, prevTurnover float64
		prevOrders              int64
		curSpend, curTurnover   float64
		curOrders               int64
		wantVerdict             string
		wantValue               float64
	}{
		{
			name:      "more spend brought more turnover",
			prevSpend: 1000, prevTurnover: 20000, prevOrders: 40,
			curSpend: 2000, curTurnover: 30000, curOrders: 55,
			wantVerdict: incrementalDRRAccretive,
			wantValue:   10, // 1000 extra spend / 10000 extra turnover
		},
		{
			// The case the whole feature exists for: the level ДРР can still
			// look fine here while the extra rouble bought nothing.
			name:      "more spend, flat turnover",
			prevSpend: 1000, prevTurnover: 20000, prevOrders: 40,
			curSpend: 2000, curTurnover: 19500, curOrders: 39,
			wantVerdict: incrementalDRRCannibalizing,
		},
		{
			name:      "cut spend, kept turnover",
			prevSpend: 2000, prevTurnover: 20000, prevOrders: 40,
			curSpend: 1000, curTurnover: 20500, curOrders: 41,
			wantVerdict: incrementalDRRFreed,
		},
		{
			name:      "cut spend, lost turnover with it",
			prevSpend: 2000, prevTurnover: 20000, prevOrders: 40,
			curSpend: 1000, curTurnover: 15000, curOrders: 30,
			wantVerdict: incrementalDRRCostly,
			wantValue:   20, // -1000 / -5000
		},
		{
			name:      "too few orders to compare",
			prevSpend: 1000, prevTurnover: 20000, prevOrders: 2,
			curSpend: 2000, curTurnover: 30000, curOrders: 55,
			wantVerdict: incrementalDRRNotEnoughData,
		},
		{
			name:      "spend barely moved — noise, not a signal",
			prevSpend: 1000, prevTurnover: 20000, prevOrders: 40,
			curSpend: 1050, curTurnover: 30000, curOrders: 55,
			wantVerdict: incrementalDRRNotEnoughData,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeIncrementalDRR(tc.prevSpend, tc.prevTurnover, tc.prevOrders, tc.curSpend, tc.curTurnover, tc.curOrders)
			if got.Verdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q", got.Verdict, tc.wantVerdict)
			}
			if got.Value != tc.wantValue {
				t.Fatalf("value = %v, want %v", got.Value, tc.wantValue)
			}
		})
	}
}

func TestDerivedTotalDRRCeiling(t *testing.T) {
	full := func(margin float64) cabinetMargin {
		return cabinetMargin{WeightedMarginPct: margin, Coverage: 1}
	}

	tests := []struct {
		name         string
		margin       cabinetMargin
		targetProfit float64
		buyout       float64
		want         *float64
	}{
		{
			// 30% margin, keep 10% profit, 90% of orders actually delivered.
			name:   "headroom after the target profit, discounted by buyout",
			margin: full(30), targetProfit: 10, buyout: 90,
			want: ptrFloat(18), // (30-10) × 0.9
		},
		{
			name:   "default buyout when none is configured",
			margin: full(30), targetProfit: 10, buyout: 0,
			want: ptrFloat(18),
		},
		{
			name:   "no headroom left — no ceiling rather than a wrong one",
			margin: full(12), targetProfit: 15, buyout: 90,
			want: nil,
		},
		{
			name:   "unknown margin yields nothing",
			margin: cabinetMargin{}, targetProfit: 10, buyout: 90,
			want: nil,
		},
		{
			// Most of the turnover has no known cost: the average is not
			// representative, so no guardrail is derived from it.
			name:   "thin cost coverage is not trusted",
			margin: cabinetMargin{WeightedMarginPct: 40, Coverage: 0.2}, targetProfit: 10, buyout: 90,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := derivedTotalDRRCeiling(tc.margin, tc.targetProfit, tc.buyout)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("got %v, want no ceiling", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("got no ceiling, want %v", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Fatalf("got %v, want %v", *got, *tc.want)
			}
		})
	}
}

// An explicit ceiling always wins over the derived one.
func TestResolveTotalDRRCeilingPrefersExplicit(t *testing.T) {
	margin := cabinetMargin{WeightedMarginPct: 30, Coverage: 1}

	got, source := resolveTotalDRRCeiling(ptrFloat(7), margin, 10, 90)
	if source != "explicit" || got == nil || *got != 7 {
		t.Fatalf("got %v from %q, want 7 from explicit", got, source)
	}

	got, source = resolveTotalDRRCeiling(nil, margin, 10, 90)
	if source != "unit_economics" || got == nil || *got != 18 {
		t.Fatalf("got %v from %q, want 18 from unit_economics", got, source)
	}

	got, source = resolveTotalDRRCeiling(nil, cabinetMargin{}, 10, 90)
	if source != "none" || got != nil {
		t.Fatalf("got %v from %q, want no ceiling", got, source)
	}
}

func ptrFloat(v float64) *float64 { return &v }

func TestOzonAPIBudgetBlockReason(t *testing.T) {
	// 6000/day with automation allowed 80% → 4800.
	const limit = ozonBidWriteDailyLimit

	tests := []struct {
		name        string
		used        int64
		planned     int64
		limit       int64
		wantBlocked bool
	}{
		{"plenty of room", 100, 1, limit, false},
		{"just under automation's share", 4799, 1, limit, false},
		{"one call past automation's share", 4800, 1, limit, true},
		{"well past", 5900, 1, limit, true},
		{
			// A category with no published limit must not block anything.
			name: "unknown limit never blocks", used: 999999, planned: 100, limit: 0,
			wantBlocked: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason := ozonAPIBudgetBlockReason(tc.used, tc.planned, tc.limit, ozonAPICategoryBidWrite)
			if got := reason != ""; got != tc.wantBlocked {
				t.Fatalf("blocked = %v (%q), want %v", got, reason, tc.wantBlocked)
			}
		})
	}
}

// Data covering today must never be reported as stale, whatever the limit.
func TestComputeTotalDRRTodayIsNeverStale(t *testing.T) {
	now := time.Date(2026, 8, 11, 23, 59, 0, 0, time.UTC)
	today := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)

	got := computeTotalDRR(100, 1000, today, now, 1)
	if got.Status != totalDRRStatusOK {
		t.Fatalf("status = %q, want %q", got.Status, totalDRRStatusOK)
	}
	if got.AgeHours != 0 {
		t.Fatalf("age = %v, want 0", got.AgeHours)
	}
}

func TestOzonPostingCancelledAndMeasuredBuyout(t *testing.T) {
	// Статусы Ozon: доставки и промежуточные состояния выкупом не считаются
	// отменёнными, отмена и непринятие — считаются.
	for _, s := range []string{"cancelled", "CANCELLED", " not_accepted ", "canceled"} {
		if !ozonPostingCancelled(s) {
			t.Fatalf("статус %q обязан считаться отменой", s)
		}
	}
	for _, s := range []string{"delivered", "delivering", "awaiting_deliver", "acceptance_in_progress", ""} {
		if ozonPostingCancelled(s) {
			t.Fatalf("статус %q отменой не является", s)
		}
	}

	// 1000 отправлений, 80 отменено → выкуп 92%.
	if got, ok := ozonMeasuredBuyoutPercent(1000, 80); !ok || got != 92 {
		t.Fatalf("получено %v (ok=%v), ожидалось 92", got, ok)
	}
	// Тонкая выборка не должна вытеснять настройку.
	if _, ok := ozonMeasuredBuyoutPercent(minPostingsForBuyout-1, 0); ok {
		t.Fatal("выборка меньше порога не должна приниматься")
	}
	// Порченые счётчики отбрасываются, а не дают выкуп больше 100%.
	if _, ok := ozonMeasuredBuyoutPercent(100, 120); ok {
		t.Fatal("отмен больше отправлений — измерение недействительно")
	}
}

// Измеренный выкуп обязан менять потолок: при 92% он выше, чем при 90%.
func TestMeasuredBuyoutMovesCeiling(t *testing.T) {
	margin := cabinetMargin{WeightedMarginPct: 30, Coverage: 1}

	assumed := derivedTotalDRRCeiling(margin, 10, defaultExpectedBuyoutPercent)
	measured := derivedTotalDRRCeiling(margin, 10, 92)
	if assumed == nil || measured == nil {
		t.Fatal("оба потолка должны считаться")
	}
	if *measured <= *assumed {
		t.Fatalf("измеренный выкуп 92%% должен давать потолок выше допущения: %v против %v", *measured, *assumed)
	}
}
