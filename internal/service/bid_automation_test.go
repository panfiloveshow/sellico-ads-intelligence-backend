package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
