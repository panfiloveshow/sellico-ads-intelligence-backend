package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestBidEngine_BlocksIncreaseWhenGuardrailDenies(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     20,
		Spend:      1000,
		Revenue:    10000,
		Placement:  "search",
		IncreaseGuardrail: &BidIncreaseGuardrail{
			Allowed: false,
			Reason:  "stock_total is unavailable",
		},
	})

	require.Nil(t, decision)
}

func TestBidEngine_AllowsReductionWhenGuardrailDeniesIncrease(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     20,
		Spend:      1000,
		Revenue:    2000,
		Placement:  "search",
		IncreaseGuardrail: &BidIncreaseGuardrail{
			Allowed: false,
			Reason:  "stock_total is unavailable",
		},
	})

	require.NotNil(t, decision)
	require.Less(t, decision.NewBid, decision.OldBid)
}

func TestBidEngineDaypartingUsesPersistedBaselineWithoutCompounding(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	strategy := domain.Strategy{Type: domain.StrategyTypeDayparting, Params: domain.StrategyParams{
		BaseMultiplier: 1, HourlyMultipliers: map[string]float64{"12": 1.2}, Timezone: "Europe/Moscow",
	}}
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC) // 12:00 Europe/Moscow

	decision := engine.CalculateBid(strategy, BidContext{
		CurrentBid: 120, DaypartingBaselineBid: 100, DecisionTime: now, Placement: "search",
	})
	require.Nil(t, decision, "the persisted target must not be multiplied again")

	decision = engine.CalculateBid(strategy, BidContext{
		CurrentBid: 100, DaypartingBaselineBid: 100, DecisionTime: now, Placement: "search",
	})
	require.NotNil(t, decision)
	require.Equal(t, 115, decision.NewBid, "common max-change guard still caps the first slot move")

	decision = engine.CalculateBid(strategy, BidContext{
		CurrentBid: 100, DaypartingBaselineBid: 100, DaypartingSlotApplied: true, DecisionTime: now, Placement: "search",
	})
	require.Nil(t, decision)
}

func TestBidEngineDaypartingUsesMoscowTimezone(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())
	strategy := domain.Strategy{Type: domain.StrategyTypeDayparting, Params: domain.StrategyParams{
		BaseMultiplier: 1, HourlyMultipliers: map[string]float64{"0": 1.1}, Timezone: "Europe/Moscow",
	}}
	decision := engine.CalculateBid(strategy, BidContext{
		CurrentBid: 100, DaypartingBaselineBid: 100,
		DecisionTime: time.Date(2026, 7, 13, 21, 30, 0, 0, time.UTC), Placement: "search",
	})
	require.NotNil(t, decision)
	require.Equal(t, 110, decision.NewBid)
}

func TestProductReputationBidIncreaseBlockReasonUsesOnlyRealWeakSignals(t *testing.T) {
	require.Contains(t, productReputationBidIncreaseBlockReason(sqlcgen.Product{
		Rating:       pgtype.Float8{Float64: 3.9, Valid: true},
		ReviewsCount: pgtype.Int4{Int32: 50, Valid: true},
	}), "rating")

	require.Contains(t, productReputationBidIncreaseBlockReason(sqlcgen.Product{
		Rating:       pgtype.Float8{Float64: 4.8, Valid: true},
		ReviewsCount: pgtype.Int4{Int32: 3, Valid: true},
	}), "reviews count")

	require.Empty(t, productReputationBidIncreaseBlockReason(sqlcgen.Product{
		Rating:       pgtype.Float8{Float64: 4.8, Valid: true},
		ReviewsCount: pgtype.Int4{Int32: 20, Valid: true},
	}))

	require.Empty(t, productReputationBidIncreaseBlockReason(sqlcgen.Product{}))
}

func TestBidEngine_BlocksIncreaseWhenCurrentACoSExceedsMaxACoS(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       2,
			MaxACoS:          30,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      1000,
		Revenue:    3000,
		Placement:  "search",
	})

	require.Nil(t, decision)
}

func TestBidEngine_AllowsReductionWhenCurrentACoSExceedsMaxACoS(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MaxACoS:          30,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      1000,
		Revenue:    2000,
		Placement:  "search",
	})

	require.NotNil(t, decision)
	require.Less(t, decision.NewBid, decision.OldBid)
}

func TestBidEngine_AllowsIncreaseBelowMaxACoS(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       2,
			MaxACoS:          30,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      1000,
		Revenue:    5000,
		Placement:  "search",
	})

	require.NotNil(t, decision)
	require.Greater(t, decision.NewBid, decision.OldBid)
}

func TestBidEngine_BlocksIncreaseWhenCurrentCPCExceedsMaxCPC(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       2,
			MaxCPC:           50,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      2400,
		Revenue:    10000,
		Orders:     10,
		Placement:  "search",
	})

	require.Nil(t, decision)
}

func TestBidEngine_BlocksIncreaseWithoutCPOEvidenceWhenMaxCPOConfigured(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       2,
			MaxCPO:           1000,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      500,
		Revenue:    10000,
		Orders:     0,
		Placement:  "search",
	})

	require.Nil(t, decision)
}

func TestBidEngine_AllowsIncreaseBelowMaxCostLimits(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       2,
			MaxCPC:           50,
			MaxCPO:           1000,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      800,
		Revenue:    10000,
		Orders:     4,
		Placement:  "search",
	})

	require.NotNil(t, decision)
	require.Greater(t, decision.NewBid, decision.OldBid)
}

func TestBidEngine_AllowsReductionWhenCurrentCPOExceedsMaxCPO(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MaxCPO:           1000,
			MinClicks:        10,
			MaxChangePercent: 20,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      3000,
		Revenue:    2000,
		Orders:     2,
		Placement:  "search",
	})

	require.NotNil(t, decision)
	require.Less(t, decision.NewBid, decision.OldBid)
}

func TestBidEngine_ExplainsMaxBidLimitInDecisionReason(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MinClicks:        10,
			MaxBid:           115,
			MaxChangePercent: 100,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      1000,
		Revenue:    10000,
		Placement:  "search",
		IncreaseGuardrail: &BidIncreaseGuardrail{
			Allowed: true,
		},
	})

	require.NotNil(t, decision)
	require.Equal(t, 115, decision.NewBid)
	require.Contains(t, decision.Reason, "max_bid 115 applied")
}

func TestBidEngine_ExplainsMaxChangeLimitInDecisionReason(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MinClicks:        10,
			MaxBid:           1000,
			MaxChangePercent: 10,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      1000,
		Revenue:    10000,
		Placement:  "search",
		IncreaseGuardrail: &BidIncreaseGuardrail{
			Allowed: true,
		},
	})

	require.NotNil(t, decision)
	require.Equal(t, 110, decision.NewBid)
	require.Contains(t, decision.Reason, "max_change_percent 10.0 applied")
}

func TestBidEngine_ExplainsMinBidLimitInDecisionReason(t *testing.T) {
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MinClicks:        10,
			MinBid:           90,
			MaxChangePercent: 100,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      1000,
		Revenue:    2000,
		Placement:  "search",
	})

	require.NotNil(t, decision)
	require.Equal(t, 90, decision.NewBid)
	require.Contains(t, decision.Reason, "min_bid 90 applied")
}

func TestBidEngine_MinBidFloorWinsOverMaxChangePercent(t *testing.T) {
	// CurrentBid sits far below MinBid. The change cap alone would leave the bid at
	// 115 (100 + 15%), below the configured floor; MinBid must win and lift it to 200.
	engine := NewBidEngine(zerolog.Nop())

	decision := engine.CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeROAS,
		Params: domain.StrategyParams{
			TargetROAS:       4,
			MinClicks:        10,
			MinBid:           200,
			MaxChangePercent: 15,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     40,
		Spend:      1000,
		Revenue:    10000,
		Placement:  "search",
		IncreaseGuardrail: &BidIncreaseGuardrail{
			Allowed: true,
		},
	})

	require.NotNil(t, decision)
	require.Equal(t, 200, decision.NewBid)
	require.Contains(t, decision.Reason, "min_bid 200 applied")
}

func TestDefaultStrategyParams_AutopilotGuardrails(t *testing.T) {
	params := domain.DefaultStrategyParams()

	require.Equal(t, 3, params.AutomationLevel)
	require.Equal(t, float64(15), params.MaxChangePercent)
	require.Equal(t, 120, params.CooldownMinutes)
	require.Equal(t, 3, params.MaxChangesPerDay)
	require.Equal(t, 36, params.MaxDataAgeHours)
}

func TestStrategyAutomationSkipReasonHonorsTrustLevels(t *testing.T) {
	require.Empty(t, strategyAutomationSkipReason(domain.Strategy{
		Params: domain.StrategyParams{AutomationLevel: 1},
	}))

	require.Empty(t, strategyAutomationSkipReason(domain.Strategy{
		Params: domain.StrategyParams{AutomationLevel: 2},
	}))

	require.Empty(t, strategyAutomationSkipReason(domain.Strategy{
		Params: domain.StrategyParams{AutomationLevel: 3},
	}))

	require.Contains(t, strategyAutomationSkipReason(domain.Strategy{
		Params: domain.StrategyParams{AutomationLevel: 5},
	}), "invalid")

	require.Empty(t, strategyAutomationSkipReason(domain.Strategy{}))
}

func TestCampaignStatsAreStale_UsesRealSyncTimestamp(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	stale, age := campaignStatsAreStale([]sqlcgen.CampaignStat{{
		CreatedAt: pgtype.Timestamptz{Time: now.Add(-37 * time.Hour), Valid: true},
	}}, 36, now)
	require.True(t, stale)
	require.Equal(t, 37*time.Hour, age)

	stale, _ = campaignStatsAreStale([]sqlcgen.CampaignStat{{
		CreatedAt: pgtype.Timestamptz{Time: now.Add(-35 * time.Hour), Valid: true},
	}}, 36, now)
	require.False(t, stale)

	stale, _ = campaignStatsAreStale([]sqlcgen.CampaignStat{{}}, 36, now)
	require.True(t, stale)
}

func TestWBAPIAutomationGuardrailReasonBlocksUnsafeLatestSync(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	nextAllowedAt := now.Add(30 * time.Minute)

	require.Contains(t,
		wbAPIAutomationGuardrailReason(&domain.SellerCabinetAutoSyncSummary{
			RateLimited:   true,
			NextAllowedAt: &nextAllowedAt,
		}, now),
		"rate limit",
	)
	require.Contains(t,
		wbAPIAutomationGuardrailReason(&domain.SellerCabinetAutoSyncSummary{WBErrors: 3}, now),
		"3 WB API errors",
	)
	require.Contains(t,
		wbAPIAutomationGuardrailReason(&domain.SellerCabinetAutoSyncSummary{ResultState: "partial"}, now),
		"partial",
	)
	require.Empty(t,
		wbAPIAutomationGuardrailReason(&domain.SellerCabinetAutoSyncSummary{ResultState: "ok"}, now),
	)
}

func TestWBEndpointRateLimitBlockReasonIncludesEndpointAndRetryWindow(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	reason := wbEndpointRateLimitBlockReason(wbEndpointCampaignActions, sqlcgen.WBAPIRateLimit{
		NextAllowedAt:     pgtype.Timestamptz{Time: now.Add(45 * time.Second), Valid: true},
		RetryAfterSeconds: 45,
		LastStatus:        429,
	}, now)

	require.Contains(t, reason, wbEndpointCampaignActions)
	require.Contains(t, reason, "retry_after_seconds=45")
	require.Contains(t, reason, "last_status=429")

	require.Empty(t, wbEndpointRateLimitBlockReason(wbEndpointCampaignActions, sqlcgen.WBAPIRateLimit{
		NextAllowedAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
	}, now))
}

func TestIsRateLimitIssueFromErrorDetectsWB429(t *testing.T) {
	require.True(t, isRateLimitIssueFromError(fmt.Errorf("WB API returned 429 rate limited")))
	require.False(t, isRateLimitIssueFromError(fmt.Errorf("WB API returned 500")))
	require.False(t, isRateLimitIssueFromError(nil))
}

func TestDailyBudgetIncreaseGuardrailBlocksIncreaseAtDailyLimit(t *testing.T) {
	today := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	reason := dailyBudgetIncreaseGuardrailReason(sqlcgen.Campaign{
		DailyBudget: pgtype.Int8{Int64: 1000, Valid: true},
	}, []sqlcgen.CampaignStat{
		{
			Date:  pgtype.Date{Time: today, Valid: true},
			Spend: 1000,
		},
	}, &BidDecision{OldBid: 100, NewBid: 110}, today)

	require.Contains(t, reason, "today spend 1000")
}

func TestDailyBudgetIncreaseGuardrailBlocksIncreaseWhenProjectedSpendReachesDailyLimit(t *testing.T) {
	today := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	reason := dailyBudgetIncreaseGuardrailReason(sqlcgen.Campaign{
		DailyBudget: pgtype.Int8{Int64: 1000, Valid: true},
	}, []sqlcgen.CampaignStat{
		{
			Date:  pgtype.Date{Time: today, Valid: true},
			Spend: 600,
		},
	}, &BidDecision{OldBid: 100, NewBid: 110}, today)

	require.Contains(t, reason, "projected today spend 1200")
}

func TestRecentClicksIncreaseGuardrailBlocksIncreaseWithoutTodayEvidence(t *testing.T) {
	today := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	reason := recentClicksIncreaseGuardrailReason([]sqlcgen.CampaignStat{
		{
			Date:   pgtype.Date{Time: today.AddDate(0, 0, -1), Valid: true},
			Clicks: 30,
		},
	}, &BidDecision{OldBid: 100, NewBid: 110}, today)

	require.Contains(t, reason, "today campaign click evidence is unavailable")
}

func TestRecentClicksIncreaseGuardrailBlocksIncreaseBelowClickMinimum(t *testing.T) {
	today := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	reason := recentClicksIncreaseGuardrailReason([]sqlcgen.CampaignStat{
		{
			Date:   pgtype.Date{Time: today, Valid: true},
			Clicks: 19,
		},
	}, &BidDecision{OldBid: 100, NewBid: 110}, today)

	require.Contains(t, reason, "today clicks 19")
}

func TestRecentClicksIncreaseGuardrailAllowsReductionBelowClickMinimum(t *testing.T) {
	today := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	reason := recentClicksIncreaseGuardrailReason([]sqlcgen.CampaignStat{
		{
			Date:   pgtype.Date{Time: today, Valid: true},
			Clicks: 3,
		},
	}, &BidDecision{OldBid: 100, NewBid: 80}, today)

	require.Empty(t, reason)
}

func TestDailyBudgetIncreaseGuardrailRequiresTodaySpendEvidence(t *testing.T) {
	today := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	reason := dailyBudgetIncreaseGuardrailReason(sqlcgen.Campaign{
		DailyBudget: pgtype.Int8{Int64: 1000, Valid: true},
	}, []sqlcgen.CampaignStat{
		{
			Date:  pgtype.Date{Time: today.AddDate(0, 0, -1), Valid: true},
			Spend: 300,
		},
	}, &BidDecision{OldBid: 100, NewBid: 110}, today)

	require.Contains(t, reason, "today campaign spend is unavailable")
}

func TestDailyBudgetIncreaseGuardrailAllowsBidReductionAtDailyLimit(t *testing.T) {
	today := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	reason := dailyBudgetIncreaseGuardrailReason(sqlcgen.Campaign{
		DailyBudget: pgtype.Int8{Int64: 1000, Valid: true},
	}, []sqlcgen.CampaignStat{
		{
			Date:  pgtype.Date{Time: today, Valid: true},
			Spend: 1200,
		},
	}, &BidDecision{OldBid: 100, NewBid: 80}, today)

	require.Empty(t, reason)
}

func TestCountRecentBidChanges_RespectsProductScopeAndCampaignLevelChanges(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	campaignID := uuid.New()
	productID := uuid.New()
	otherProductID := uuid.New()
	products := map[uuid.UUID]struct{}{productID: {}}

	changes := []sqlcgen.BidChange{
		{
			CampaignID: uuidToPgtype(campaignID),
			ProductID:  pgtype.UUID{},
			Placement:  "search",
			WbStatus:   "applied",
			CreatedAt:  pgtype.Timestamptz{Time: now.Add(-30 * time.Minute), Valid: true},
		},
		{
			CampaignID: uuidToPgtype(uuid.New()),
			ProductID:  uuidToPgtype(productID),
			Placement:  "search",
			WbStatus:   "applied",
			CreatedAt:  pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		},
		{
			ProductID: uuidToPgtype(otherProductID),
			Placement: "search",
			WbStatus:  "applied",
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		},
		{
			ProductID: uuidToPgtype(productID),
			Placement: "recommendations",
			WbStatus:  "applied",
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		},
		{
			ProductID: uuidToPgtype(productID),
			Placement: "search",
			WbStatus:  "failed",
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		},
	}

	count := countRecentBidChanges(changes, products, campaignID, "search", now.Add(-24*time.Hour))
	require.Equal(t, 2, count)
}

func TestCountRecentBidChanges_IgnoresUnrelatedCampaignLevelChanges(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	campaignID := uuid.New()
	productID := uuid.New()
	products := map[uuid.UUID]struct{}{productID: {}}

	changes := []sqlcgen.BidChange{
		{
			CampaignID: uuidToPgtype(uuid.New()),
			ProductID:  pgtype.UUID{},
			Placement:  "search",
			WbStatus:   "applied",
			CreatedAt:  pgtype.Timestamptz{Time: now.Add(-30 * time.Minute), Valid: true},
		},
		{
			CampaignID: uuidToPgtype(uuid.New()),
			ProductID:  uuidToPgtype(productID),
			Placement:  "search",
			WbStatus:   "applied",
			CreatedAt:  pgtype.Timestamptz{Time: now.Add(-30 * time.Minute), Valid: true},
		},
	}

	count := countRecentBidChanges(changes, products, campaignID, "search", now.Add(-24*time.Hour))
	require.Equal(t, 1, count)
}
