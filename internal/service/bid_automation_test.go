package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestExtensionBidMismatchGuardrailReasonBlocksOnLatestLiveMismatch(t *testing.T) {
	oldBid := int64(300)
	liveBid := int64(340)
	now := time.Now().UTC()

	reason := extensionBidMismatchGuardrailReason(300, []domain.ExtensionBidSnapshot{
		{
			VisibleBid: &liveBid,
			CapturedAt: now,
		},
		{
			VisibleBid: &oldBid,
			CapturedAt: now.Add(-time.Hour),
		},
	})

	require.Contains(t, reason, "live cabinet bid 340")
	require.Contains(t, reason, "synced WB API bid 300")
}

func TestWorkspaceAutomationGuardrailIsFailClosedAndHonorsManualHold(t *testing.T) {
	reason, err := workspaceAutomationGuardrailReason(nil)
	require.NoError(t, err)
	require.Contains(t, reason, "not explicitly enabled")

	raw, err := json.Marshal(domain.WorkspaceSettings{Automation: &domain.AutomationSettings{Enabled: true}})
	require.NoError(t, err)
	reason, err = workspaceAutomationGuardrailReason(raw)
	require.NoError(t, err)
	require.Empty(t, reason)

	raw, err = json.Marshal(domain.WorkspaceSettings{Automation: &domain.AutomationSettings{
		Enabled: true, ManualHold: true, HoldReason: "operator review",
	}})
	require.NoError(t, err)
	reason, err = workspaceAutomationGuardrailReason(raw)
	require.NoError(t, err)
	require.Equal(t, "operator review", reason)
}

func TestEvaluateLiveBidWriteGuardrailRechecksOperatorControlsAndStrategyActivity(t *testing.T) {
	enabled, err := json.Marshal(domain.WorkspaceSettings{Automation: &domain.AutomationSettings{Enabled: true}})
	require.NoError(t, err)

	reason, err := evaluateLiveBidWriteGuardrail(enabled, true, true)
	require.NoError(t, err)
	require.Empty(t, reason)

	reason, err = evaluateLiveBidWriteGuardrail(enabled, true, false)
	require.NoError(t, err)
	require.Contains(t, reason, "no longer active")

	reason, err = evaluateLiveBidWriteGuardrail(enabled, false, false)
	require.NoError(t, err)
	require.Contains(t, reason, "no longer available")

	held, err := json.Marshal(domain.WorkspaceSettings{Automation: &domain.AutomationSettings{
		Enabled: true, ManualHold: true, HoldReason: "emergency stop",
	}})
	require.NoError(t, err)
	reason, err = evaluateLiveBidWriteGuardrail(held, true, true)
	require.NoError(t, err)
	require.Equal(t, "emergency stop", reason)

	reason, err = evaluateLiveBidWriteGuardrail(nil, true, true)
	require.NoError(t, err)
	require.Contains(t, reason, "not explicitly enabled")
}

func TestSellerCabinetAutomationGuardrailRequiresFreshReadyCoverage(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	require.Contains(t, sellerCabinetAutomationGuardrailReason(sqlcgen.SellerCabinetSyncState{}, false, 36, now), "no completed")

	ready := sqlcgen.SellerCabinetSyncState{
		Status:          "ready",
		CompletedAt:     pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		DataThroughDate: pgtype.Date{Time: now.AddDate(0, 0, -1), Valid: true},
	}
	require.Empty(t, sellerCabinetAutomationGuardrailReason(ready, true, 36, now))

	ready.Status = "partial"
	require.Contains(t, sellerCabinetAutomationGuardrailReason(ready, true, 36, now), "partial")
	ready.Status = "ready"
	ready.DataThroughDate = pgtype.Date{Time: now.AddDate(0, 0, -3), Valid: true}
	require.Contains(t, sellerCabinetAutomationGuardrailReason(ready, true, 36, now), "does not cover")
}

func TestAutomationBidActionKeySerializesConflictingStrategiesPerCampaign(t *testing.T) {
	cabinetID := uuid.New()
	campaign := sqlcgen.Campaign{SellerCabinetID: uuidToPgtype(cabinetID), WbCampaignID: 42}
	observedAt := time.Date(2026, 7, 14, 10, 2, 0, 0, time.UTC)

	first, firstObservation := automationBidActionKeys(campaign, 1001, &BidDecision{OldBid: 100, NewBid: 110, Placement: "search"}, observedAt)
	conflicting, conflictingObservation := automationBidActionKeys(campaign, 1001, &BidDecision{OldBid: 100, NewBid: 120, Placement: "search"}, observedAt)
	otherSKU, otherObservation := automationBidActionKeys(campaign, 1002, &BidDecision{OldBid: 100, NewBid: 110, Placement: "search"}, observedAt)
	fresh, freshObservation := automationBidActionKeys(campaign, 1001, &BidDecision{OldBid: 100, NewBid: 110, Placement: "search"}, observedAt.Add(time.Minute))

	require.NotEqual(t, first, conflicting)
	require.Equal(t, firstObservation, conflictingObservation)
	require.NotEqual(t, firstObservation, otherObservation)
	require.NotEqual(t, first, otherSKU)
	require.NotEqual(t, firstObservation, freshObservation)
	require.NotEqual(t, first, fresh)
}

func TestAutomationBidReconciliationStatusUsesActualSyncedBid(t *testing.T) {
	require.Equal(t, "applied", automationBidReconciliationStatus(100, 120, 120))
	require.Equal(t, "not_applied", automationBidReconciliationStatus(100, 120, 100))
	require.Equal(t, "superseded", automationBidReconciliationStatus(100, 120, 110))
}

func TestCampaignDailyLimitIncreaseGuardrailFailsClosed(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	decision := &BidDecision{OldBid: 100, NewBid: 110}

	reason := campaignDailyLimitIncreaseGuardrailReason(sqlcgen.CampaignDailyLimit{Enabled: true}, nil, decision, now)
	require.Contains(t, reason, "no positive real cap")

	reason = campaignDailyLimitIncreaseGuardrailReason(sqlcgen.CampaignDailyLimit{Enabled: true, DailyLimit: 1000}, []sqlcgen.CampaignStat{{
		Date: pgtype.Date{Time: now, Valid: true}, Spend: 1000,
	}}, decision, now)
	require.Contains(t, reason, "reached configured")
}

func TestCampaignSpendCapIncreaseGuardrailUsesConfiguredOrSyncedLimit(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	decision := &BidDecision{OldBid: 100, NewBid: 110}
	stats := []sqlcgen.CampaignStat{{Date: pgtype.Date{Time: now, Valid: true}, Spend: 500}}

	require.Contains(t, campaignSpendCapIncreaseGuardrailReason(sqlcgen.Campaign{}, sqlcgen.CampaignDailyLimit{}, false, stats, decision, now), "unavailable")
	require.Empty(t, campaignSpendCapIncreaseGuardrailReason(sqlcgen.Campaign{
		DailyBudget: pgtype.Int8{Int64: 1000, Valid: true},
	}, sqlcgen.CampaignDailyLimit{}, false, stats, decision, now))
	require.Contains(t, campaignSpendCapIncreaseGuardrailReason(sqlcgen.Campaign{}, sqlcgen.CampaignDailyLimit{
		Enabled: true, DailyLimit: 500,
	}, true, stats, decision, now), "reached")
}

func TestClosedCampaignStatsExcludeIncompleteTodayFromEfficiencyMetrics(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	yesterday := now.AddDate(0, 0, -1)
	stats := []sqlcgen.CampaignStat{
		{
			Date:        pgtype.Date{Time: yesterday, Valid: true},
			Impressions: 1000,
			Clicks:      20,
			Spend:       100,
			Orders:      pgtype.Int8{Int64: 10, Valid: true},
			Revenue:     pgtype.Int8{Int64: 1000, Valid: true},
		},
		{
			// Today's spend can arrive before today's orders and revenue. It must
			// remain available to pacing guardrails, but not efficiency decisions.
			Date:        pgtype.Date{Time: now, Valid: true},
			Impressions: 5000,
			Clicks:      100,
			Spend:       9000,
			Orders:      pgtype.Int8{Int64: 0, Valid: true},
			Revenue:     pgtype.Int8{Int64: 0, Valid: true},
		},
	}

	closed := closedCampaignStats(stats, now)
	require.Len(t, closed, 1)
	impressions, clicks, orders, spend, revenue := aggregateBidPerformance(closed)
	require.Equal(t, int64(1000), impressions)
	require.Equal(t, int64(20), clicks)
	require.Equal(t, int64(10), orders)
	require.Equal(t, float64(100), spend)
	require.Equal(t, float64(1000), revenue)

	// Intraday pacing still sees the real current-day spend.
	todaySpend, ok := campaignSpendForDate(stats, now)
	require.True(t, ok)
	require.Equal(t, int64(9000), todaySpend)
}

func TestClosedCampaignStatsUseMoscowCalendarBoundary(t *testing.T) {
	// 22:30 UTC is already the next calendar day in Moscow.
	now := time.Date(2026, 7, 14, 22, 30, 0, 0, time.UTC)
	stats := []sqlcgen.CampaignStat{
		{Date: pgtype.Date{Time: time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC), Valid: true}, Spend: 100},
		{Date: pgtype.Date{Time: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Valid: true}, Spend: 200},
	}

	closed := closedCampaignStats(stats, now)
	require.Len(t, closed, 1)
	require.Equal(t, int64(100), closed[0].Spend)
	require.Equal(t, "2026-07-14", lastClosedCampaignStatDate(now).Format("2006-01-02"))
}

func TestClosedCampaignStatsFreshnessCannotBeMaskedByToday(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	stats := []sqlcgen.CampaignStat{
		{
			Date:      pgtype.Date{Time: now.AddDate(0, 0, -1), Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-48 * time.Hour), Valid: true},
		},
		{
			Date:      pgtype.Date{Time: now, Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		},
	}

	stale, age := campaignStatsAreStale(closedCampaignStats(stats, now), 36, now)
	require.True(t, stale)
	require.Equal(t, 48*time.Hour, age)
}

func TestClosedDayPerformanceDrivesACoSAndCPOGuardrails(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	stats := []sqlcgen.CampaignStat{
		{
			Date:    pgtype.Date{Time: now.AddDate(0, 0, -1), Valid: true},
			Clicks:  20,
			Spend:   100,
			Orders:  pgtype.Int8{Int64: 10, Valid: true},
			Revenue: pgtype.Int8{Int64: 1000, Valid: true},
		},
		{
			Date:    pgtype.Date{Time: now, Valid: true},
			Clicks:  100,
			Spend:   9000,
			Orders:  pgtype.Int8{Int64: 0, Valid: true},
			Revenue: pgtype.Int8{Int64: 0, Valid: true},
		},
	}

	_, clicks, orders, spend, revenue := aggregateBidPerformance(closedCampaignStats(stats, now))
	decision := NewBidEngine(zerolog.Nop()).CalculateBid(domain.Strategy{
		Type: domain.StrategyTypeACoS,
		Params: domain.StrategyParams{
			TargetACoS:       20,
			MaxCPO:           50,
			MinClicks:        10,
			MaxChangePercent: 15,
		},
	}, BidContext{
		CurrentBid: 100,
		Clicks:     clicks,
		Orders:     orders,
		Spend:      spend,
		Revenue:    revenue,
	})

	require.NotNil(t, decision)
	require.Greater(t, decision.NewBid, decision.OldBid)
	require.NotNil(t, decision.ACoS)
	require.Equal(t, float64(10), *decision.ACoS)
}

func TestClosedDayPerformancePreventsTodayFromTriggeringAntiWaste(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	stats := []sqlcgen.CampaignStat{
		{Date: pgtype.Date{Time: now.AddDate(0, 0, -1), Valid: true}, Clicks: 20, Spend: 100, Revenue: pgtype.Int8{Int64: 1000, Valid: true}},
		{Date: pgtype.Date{Time: now, Valid: true}, Clicks: 60, Spend: 5000, Revenue: pgtype.Int8{Int64: 0, Valid: true}},
	}

	_, clicks, orders, spend, revenue := aggregateBidPerformance(closedCampaignStats(stats, now))
	decision := NewBidEngine(zerolog.Nop()).CalculateBid(domain.Strategy{
		Type:   domain.StrategyTypeAntiSliv,
		Params: domain.StrategyParams{MaxACoS: 30},
	}, BidContext{CurrentBid: 100, Clicks: clicks, Orders: orders, Spend: spend, Revenue: revenue})

	require.Nil(t, decision)
}

func TestProductBidObservationUsesExactCampaignProduct(t *testing.T) {
	productID := uuid.New()
	otherID := uuid.New()
	observedAt := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	links := []sqlcgen.CampaignProduct{
		{ProductID: uuidToPgtype(otherID), BidSearch: pgtype.Int8{Int64: 900, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: observedAt.Add(time.Hour), Valid: true}},
		{ProductID: uuidToPgtype(productID), BidSearch: pgtype.Int8{Int64: 710, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: observedAt, Valid: true}},
	}

	bid, capturedAt, ok, err := productBidObservationFromLinks(links, productID, "combined")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 710, bid)
	require.Equal(t, observedAt, capturedAt)

	links[1].UpdatedAt = pgtype.Timestamptz{}
	_, _, ok, err = productBidObservationFromLinks(links, productID, "combined")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestCampaignStatsFromProductStatsPreservesExactAttribution(t *testing.T) {
	productID := uuid.New()
	campaignID := uuid.New()
	createdAt := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	date := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)

	stats := campaignStatsFromProductStats([]sqlcgen.ProductStat{{
		ProductID:   uuidToPgtype(productID),
		CampaignID:  uuidToPgtype(campaignID),
		Date:        pgtype.Date{Time: date, Valid: true},
		Impressions: 101,
		Clicks:      12,
		Spend:       345,
		Orders:      pgtype.Int8{Int64: 4, Valid: true},
		Revenue:     pgtype.Int8{Int64: 6789, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
	}})

	require.Len(t, stats, 1)
	require.Equal(t, uuidToPgtype(campaignID), stats[0].CampaignID)
	require.Equal(t, int64(101), stats[0].Impressions)
	require.Equal(t, int64(12), stats[0].Clicks)
	require.Equal(t, int64(345), stats[0].Spend)
	require.Equal(t, int64(4), stats[0].Orders.Int64)
	require.Equal(t, int64(6789), stats[0].Revenue.Int64)
	require.Equal(t, createdAt, stats[0].CreatedAt.Time)

	// The bid engine consumes the converted slice, so metrics from other
	// products in the campaign cannot leak into a product-level decision.
	var totalSpend int64
	for _, stat := range stats {
		totalSpend += stat.Spend
	}
	require.Equal(t, int64(345), totalSpend)
}

func TestSellerBalanceIncreaseGuardrailRequiresFreshPositiveBalance(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	decision := &BidDecision{OldBid: 100, NewBid: 110}

	require.Contains(t, sellerBalanceIncreaseGuardrailReason(sqlcgen.SellerAdBalance{
		CapturedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, Balance: 0,
	}, decision, now), "zero")
	require.Contains(t, sellerBalanceIncreaseGuardrailReason(sqlcgen.SellerAdBalance{
		CapturedAt: pgtype.Timestamptz{Time: now.Add(-25 * time.Hour), Valid: true}, Balance: 0,
	}, decision, now), "stale")
}

func TestAutomationBidPlacementFollowsSyncedBidMode(t *testing.T) {
	placement, err := automationBidPlacement(sqlcgen.Campaign{BidType: domain.BidTypeUnified})
	require.NoError(t, err)
	require.Equal(t, "combined", placement)

	placement, err = automationBidPlacement(sqlcgen.Campaign{
		BidType: domain.BidTypeManual, PlacementSearch: pgtype.Bool{Bool: true, Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, "search", placement)

	_, err = automationBidPlacement(sqlcgen.Campaign{BidType: domain.BidTypeManual})
	require.Error(t, err)
}

func TestDynamicMinimumBidGuardrailFailsClosed(t *testing.T) {
	require.Contains(t, dynamicMinimumBidGuardrailReason(nil, []int64{1001}, "search", 100), "unavailable")
	require.Contains(t, dynamicMinimumBidGuardrailReason([]wb.WBMinimumBidDTO{{
		NmID: 1001, Placement: "search", MinBid: 120,
	}}, []int64{1001}, "search", 100), "below WB minimum")
	require.Empty(t, dynamicMinimumBidGuardrailReason([]wb.WBMinimumBidDTO{{
		NmID: 1001, Placement: "search", MinBid: 80,
	}}, []int64{1001}, "search", 100))
	require.Equal(t, "recommendation", minimumBidPlacement("recommendations"))
}

func TestExtensionBidMismatchGuardrailReasonAllowsMatchingOrMissingEvidence(t *testing.T) {
	currentBid := int64(300)
	now := time.Now().UTC()

	require.Empty(t, extensionBidMismatchGuardrailReason(300, []domain.ExtensionBidSnapshot{
		{
			VisibleBid: &currentBid,
			CapturedAt: now,
		},
	}))
	require.Empty(t, extensionBidMismatchGuardrailReason(300, nil))
	require.Empty(t, extensionBidMismatchGuardrailReason(300, []domain.ExtensionBidSnapshot{
		{
			CapturedAt: now,
		},
	}))
}
