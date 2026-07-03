package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestExtractRecommendationApplyMetrics(t *testing.T) {
	metrics := []byte(`{"wb_product_id":183310308,"wb_norm_query":"петля мебельная","competitive_bid":220}`)

	if got := extractInt64FromMetrics(metrics, "wb_product_id"); got != 183310308 {
		t.Fatalf("expected wb product id 183310308, got %d", got)
	}
	if got := extractStringFromMetrics(metrics, "wb_norm_query"); got != "петля мебельная" {
		t.Fatalf("expected wb norm query, got %q", got)
	}
	if got := extractSuggestedBid(metrics, "competitive_bid", 0.9); got != 198 {
		t.Fatalf("expected suggested bid 198, got %d", got)
	}
}

func TestNormalizeCreateCampaignActionInputUsesExplicitRealProductIDs(t *testing.T) {
	cabinetID := uuid.New()

	input, err := normalizeCreateCampaignActionInput(CreateCampaignActionInput{
		SellerCabinetID: cabinetID,
		Name:            " Growth Search ",
		NMIDs:           []int64{146168367, 146168367, 200425104},
		BidType:         "",
		PaymentType:     "",
		PlacementTypes:  []string{"search", "recommendations", "search"},
	})

	if err != nil {
		t.Fatalf("expected valid create campaign input, got %v", err)
	}
	if input.Name != "Growth Search" || input.SellerCabinetID != cabinetID {
		t.Fatalf("unexpected normalized identity: %+v", input)
	}
	if len(input.NMIDs) != 2 || input.NMIDs[0] != 146168367 || input.NMIDs[1] != 200425104 {
		t.Fatalf("expected deduplicated explicit nm_ids, got %+v", input.NMIDs)
	}
	if input.BidType != "manual" || input.PaymentType != "cpm" {
		t.Fatalf("expected safe WB defaults, got %+v", input)
	}
	if strings.Join(input.PlacementTypes, ",") != "search,recommendations" {
		t.Fatalf("expected deduplicated placements, got %+v", input.PlacementTypes)
	}
}

func TestNormalizeCreateCampaignActionInputRejectsMissingRealProductIDs(t *testing.T) {
	_, err := normalizeCreateCampaignActionInput(CreateCampaignActionInput{
		SellerCabinetID: uuid.New(),
		Name:            "Growth Search",
		NMIDs:           []int64{0},
	})
	if err == nil {
		t.Fatal("expected validation error for missing real WB product IDs")
	}
}

func TestNormalizeCreateCampaignActionInputOmitsPlacementsForUnifiedBid(t *testing.T) {
	input, err := normalizeCreateCampaignActionInput(CreateCampaignActionInput{
		SellerCabinetID: uuid.New(),
		Name:            "Unified Growth",
		NMIDs:           []int64{146168367},
		BidType:         "unified",
		PlacementTypes:  []string{"search"},
	})
	if err != nil {
		t.Fatalf("expected valid unified campaign input, got %v", err)
	}
	if len(input.PlacementTypes) != 0 {
		t.Fatalf("unified bid campaign must not send placement_types, got %+v", input.PlacementTypes)
	}
}

func TestExtractRecommendationApplyMetricsReturnsZeroForInvalidJSON(t *testing.T) {
	metrics := []byte(`not-json`)

	if got := extractInt64FromMetrics(metrics, "wb_product_id"); got != 0 {
		t.Fatalf("expected zero for invalid numeric metric, got %d", got)
	}
	if got := extractStringFromMetrics(metrics, "wb_norm_query"); got != "" {
		t.Fatalf("expected empty string for invalid string metric, got %q", got)
	}
}

func TestClusterMinusCPOGuardrailBlocksWithoutTargetCPO(t *testing.T) {
	reason := clusterMinusCPOGuardrailReason([]byte(`{"spend":3000}`))

	if reason == "" {
		t.Fatal("expected cluster minus guardrail to require target CPO evidence")
	}
}

func TestClusterMinusCPOGuardrailBlocksBelowOnePointFiveTargetCPO(t *testing.T) {
	reason := clusterMinusCPOGuardrailReason([]byte(`{"spend":2100,"target_cpo":1500}`))

	if reason == "" {
		t.Fatal("expected cluster minus guardrail to block spend below 1.5x target CPO")
	}
}

func TestClusterMinusCPOGuardrailAllowsEnoughSpend(t *testing.T) {
	reason := clusterMinusCPOGuardrailReason([]byte(`{"spend":2300,"target_cpo":1500}`))

	if reason != "" {
		t.Fatalf("expected cluster minus guardrail to allow enough spend, got %q", reason)
	}
}

func TestBudgetDepositGuardrailRequiresFreshRealBalance(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	reason := budgetDepositGuardrailReason(sqlcgen.SellerAdBalance{
		Balance:    500000,
		CapturedAt: pgtype.Timestamptz{Time: now.Add(-25 * time.Hour), Valid: true},
	}, 1000, now)
	if reason == "" {
		t.Fatal("expected stale balance guardrail")
	}

	reason = budgetDepositGuardrailReason(sqlcgen.SellerAdBalance{
		Balance:    500000,
		CapturedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
	}, 6000, now)
	if reason == "" {
		t.Fatal("expected insufficient WB ad balance guardrail")
	}
}

func TestBudgetDepositGuardrailAllowsAmountWithinRealBalance(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	reason := budgetDepositGuardrailReason(sqlcgen.SellerAdBalance{
		Balance:    500000,
		CapturedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
	}, 5000, now)
	if reason != "" {
		t.Fatalf("expected deposit within real WB ad balance, got %q", reason)
	}
}

func TestBidChangeOutcomeFromStatsComparesChangeDayToNextDay(t *testing.T) {
	changeTime := time.Date(2026, 5, 27, 11, 40, 0, 0, time.UTC)

	outcome := bidChangeOutcomeFromStats(changeTime, []sqlcgen.CampaignStat{
		{
			Date:        pgtype.Date{Time: changeTime, Valid: true},
			Impressions: 1000,
			Clicks:      40,
			Spend:       1200,
			Orders:      pgtype.Int8{Int64: 4, Valid: true},
			Revenue:     pgtype.Int8{Int64: 12000, Valid: true},
		},
		{
			Date:        pgtype.Date{Time: changeTime.AddDate(0, 0, 1), Valid: true},
			Impressions: 1400,
			Clicks:      56,
			Spend:       1500,
			Orders:      pgtype.Int8{Int64: 6, Valid: true},
			Revenue:     pgtype.Int8{Int64: 18000, Valid: true},
		},
	})

	if outcome == nil {
		t.Fatal("expected bid change outcome from real campaign stats")
	}
	if outcome.Window != "change_day_to_next_day" || outcome.BaselineDate != "2026-05-27" || outcome.OutcomeDate != "2026-05-28" {
		t.Fatalf("unexpected outcome window: %+v", outcome)
	}
	if outcome.Baseline.Orders != 4 || outcome.Outcome.Orders != 6 || outcome.Delta.Orders != 2 {
		t.Fatalf("expected real order delta, got %+v", outcome)
	}
	if outcome.Trend != "improving" {
		t.Fatalf("expected improving trend, got %q", outcome.Trend)
	}
}

func TestBidChangeOutcomeFromStatsRequiresBothDays(t *testing.T) {
	changeTime := time.Date(2026, 5, 27, 11, 40, 0, 0, time.UTC)

	outcome := bidChangeOutcomeFromStats(changeTime, []sqlcgen.CampaignStat{
		{
			Date:   pgtype.Date{Time: changeTime, Valid: true},
			Clicks: 40,
			Spend:  1200,
		},
	})

	if outcome != nil {
		t.Fatalf("did not expect outcome without next-day real stats, got %+v", outcome)
	}
}

func TestPhraseClusterActionTargetUsesPhraseRealIdentifiers(t *testing.T) {
	phrase := sqlcgen.Phrase{
		WbProductID: pgtype.Int8{Int64: 183310308, Valid: true},
		WbNormQuery: "петля мебельная",
		Keyword:     "fallback keyword",
	}

	nmID, normQuery, err := phraseClusterActionTarget(phrase, nil)
	if err != nil {
		t.Fatalf("expected target, got error: %v", err)
	}
	if nmID != 183310308 {
		t.Fatalf("expected nm id 183310308, got %d", nmID)
	}
	if normQuery != "петля мебельная" {
		t.Fatalf("expected norm query from phrase, got %q", normQuery)
	}
}

func TestPhraseClusterActionTargetFallsBackToRecommendationMetrics(t *testing.T) {
	phrase := sqlcgen.Phrase{}
	metrics := []byte(`{"wb_product_id":183310308,"wb_norm_query":"петля мебельная"}`)

	nmID, normQuery, err := phraseClusterActionTarget(phrase, metrics)
	if err != nil {
		t.Fatalf("expected target, got error: %v", err)
	}
	if nmID != 183310308 {
		t.Fatalf("expected nm id from metrics, got %d", nmID)
	}
	if normQuery != "петля мебельная" {
		t.Fatalf("expected norm query from metrics, got %q", normQuery)
	}
}

func TestPhraseClusterActionTargetRejectsMissingRealIdentifiers(t *testing.T) {
	_, _, err := phraseClusterActionTarget(sqlcgen.Phrase{}, []byte(`{}`))
	if err == nil {
		t.Fatal("expected missing identifier error")
	}
}
