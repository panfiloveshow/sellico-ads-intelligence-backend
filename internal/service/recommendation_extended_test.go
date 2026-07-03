package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestStockAlertRecommendationInputUsesRealEvidence(t *testing.T) {
	workspaceID := uuid.New()
	productID := uuid.New()
	capturedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	input := stockAlertRecommendationInput(workspaceID, productID, "Петля мебельная 35 мм", 183310308, 3, "product_snapshot", capturedAt)

	require.Equal(t, workspaceID, input.WorkspaceID)
	require.Equal(t, productID, *input.ProductID)
	require.Equal(t, domain.RecommendationTypeStockAlert, input.Type)
	require.Equal(t, domain.SeverityHigh, input.Severity)
	require.Equal(t, int32(3), input.SourceMetrics["stock_total"])
	require.Equal(t, "product_snapshot", input.SourceMetrics["source"])
	require.Contains(t, input.Description, "подтвержденный остаток: 3")
}

func TestStockAlertRecommendationInputMarksZeroStockCritical(t *testing.T) {
	input := stockAlertRecommendationInput(uuid.New(), uuid.New(), "Петля мебельная 35 мм", 183310308, 0, "delivery_data", time.Now())

	require.Equal(t, domain.SeverityCritical, input.Severity)
	require.Contains(t, input.Description, "подтвержденный остаток: 0")
}

func TestCardConversionIssueRecommendationInputUsesOnlyRealPhraseStats(t *testing.T) {
	workspaceID := uuid.New()
	phrase := sqlcgen.Phrase{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(uuid.New()),
		ProductID:   uuidToPgtype(uuid.New()),
		WbProductID: pgtype.Int8{Int64: 183310308, Valid: true},
		WbNormQuery: "петля мебельная",
		Keyword:     "петля мебельная",
	}
	stat := sqlcgen.PhraseStat{
		Date:   pgtype.Date{Time: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC), Valid: true},
		Spend:  700,
		Clicks: 8,
		Atbs:   pgtype.Int8{Int64: 0, Valid: true},
		Orders: pgtype.Int8{Int64: 0, Valid: true},
	}

	require.False(t, isTrashPhraseCandidate(stat, domain.DefaultThresholds()))
	require.True(t, isCardConversionIssueCandidate(stat))

	input := cardConversionIssueRecommendationInput(workspaceID, phrase, stat)

	require.Equal(t, workspaceID, input.WorkspaceID)
	require.Equal(t, domain.RecommendationTypeCardConversionIssue, input.Type)
	require.Equal(t, domain.SeverityMedium, input.Severity)
	require.Equal(t, int64(700), input.SourceMetrics["spend"])
	require.Equal(t, int64(8), input.SourceMetrics["clicks"])
	require.Equal(t, "real phrase_stats clicks/atbs/orders; no card metrics inferred", input.SourceMetrics["decision_basis"])
	require.Contains(t, input.Description, "0 корзин")
	require.Contains(t, *input.NextAction, "минусуйте только после достаточного порога данных")
}

func TestTrashPhraseRecommendationInputRequiresEnoughRealPhraseStats(t *testing.T) {
	workspaceID := uuid.New()
	phrase := sqlcgen.Phrase{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(uuid.New()),
		ProductID:   uuidToPgtype(uuid.New()),
		WbProductID: pgtype.Int8{Int64: 183310308, Valid: true},
		WbNormQuery: "петля мебельная",
		Keyword:     "петля мебельная",
	}
	stat := sqlcgen.PhraseStat{
		Date:   pgtype.Date{Time: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC), Valid: true},
		Spend:  5000,
		Clicks: 24,
		Atbs:   pgtype.Int8{Int64: 0, Valid: true},
		Orders: pgtype.Int8{Int64: 0, Valid: true},
	}
	thresholds := domain.RecommendationThresholds{CampaignPoorCPO: 1000}

	require.True(t, isTrashPhraseCandidate(stat, thresholds))

	input := trashPhraseRecommendationInput(workspaceID, phrase, stat, thresholds)

	require.Equal(t, domain.RecommendationTypeAddMinusPhrase, input.Type)
	require.Equal(t, domain.SeverityHigh, input.Severity)
	require.Equal(t, int64(5000), input.SourceMetrics["spend"])
	require.Equal(t, int64(24), input.SourceMetrics["clicks"])
	require.Equal(t, float64(1000), input.SourceMetrics["campaign_poor_cpo"])
	require.Equal(t, int64(1500), input.SourceMetrics["min_spend"])
	require.Equal(t, int64(20), input.SourceMetrics["min_clicks"])
	require.Equal(t, "real phrase_stats spend/clicks/atbs/orders", input.SourceMetrics["decision_basis"])
	require.Contains(t, input.Description, "кандидат на минус-фразу")
}

func TestTrashPhraseCandidateRequiresOnePointFiveTargetCPO(t *testing.T) {
	stat := sqlcgen.PhraseStat{
		Date:   pgtype.Date{Time: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC), Valid: true},
		Spend:  2100,
		Clicks: 24,
		Atbs:   pgtype.Int8{Int64: 0, Valid: true},
		Orders: pgtype.Int8{Int64: 0, Valid: true},
	}

	require.False(t, isTrashPhraseCandidate(stat, domain.DefaultThresholds()))
}

func TestOfferConversionIssueRecommendationInputUsesOnlyRealPhraseStats(t *testing.T) {
	workspaceID := uuid.New()
	phrase := sqlcgen.Phrase{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(uuid.New()),
		ProductID:   uuidToPgtype(uuid.New()),
		WbProductID: pgtype.Int8{Int64: 183310308, Valid: true},
		WbNormQuery: "петля мебельная",
		Keyword:     "петля мебельная",
	}
	stat := sqlcgen.PhraseStat{
		Date:   pgtype.Date{Time: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC), Valid: true},
		Spend:  900,
		Clicks: 12,
		Atbs:   pgtype.Int8{Int64: 3, Valid: true},
		Orders: pgtype.Int8{Int64: 0, Valid: true},
	}

	require.True(t, isOfferConversionIssueCandidate(stat))
	require.False(t, isCardConversionIssueCandidate(stat))

	input := offerConversionIssueRecommendationInput(workspaceID, phrase, stat)

	require.Equal(t, workspaceID, input.WorkspaceID)
	require.Equal(t, domain.RecommendationTypeOfferConversionIssue, input.Type)
	require.Equal(t, domain.SeverityMedium, input.Severity)
	require.Equal(t, int64(3), input.SourceMetrics["atbs"])
	require.Equal(t, int64(0), input.SourceMetrics["orders"])
	require.Equal(t, "real phrase_stats clicks/atbs/orders; no delivery or price inferred", input.SourceMetrics["decision_basis"])
	require.Contains(t, input.Description, "3 корзин")
	require.Contains(t, input.Description, "0 заказов")
}

func TestSEOIdeaPhraseRecommendationInputDoesNotInferMargin(t *testing.T) {
	workspaceID := uuid.New()
	thresholds := domain.RecommendationThresholds{CampaignPoorCPO: 1000}
	phrase := sqlcgen.Phrase{
		ID:          uuidToPgtype(uuid.New()),
		CampaignID:  uuidToPgtype(uuid.New()),
		WbProductID: pgtype.Int8{Int64: 183310308, Valid: true},
		WbNormQuery: "петля с доводчиком",
		Keyword:     "петля с доводчиком",
	}
	stat := sqlcgen.PhraseStat{
		Date:   pgtype.Date{Time: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC), Valid: true},
		Spend:  900,
		Clicks: 20,
		Atbs:   pgtype.Int8{Int64: 6, Valid: true},
		Orders: pgtype.Int8{Int64: 2, Valid: true},
	}

	require.True(t, isSEOIdeaPhraseCandidate(stat, thresholds))

	input := seoIdeaPhraseRecommendationInput(workspaceID, phrase, stat, thresholds)

	require.Equal(t, domain.RecommendationTypeOptimizeSEO, input.Type)
	require.Equal(t, domain.SeverityMedium, input.Severity)
	require.Equal(t, int64(2), input.SourceMetrics["orders"])
	require.Equal(t, float64(450), input.SourceMetrics["cpo"])
	require.Equal(t, float64(1000), input.SourceMetrics["target_cpo"])
	require.Equal(t, "real phrase_stats spend/orders CPO; no margin or revenue inferred", input.SourceMetrics["decision_basis"])
	require.Contains(t, input.Description, "CPO 450")
	require.Contains(t, input.Description, "маржинальность нужно проверить отдельно")
}

func TestSEOIdeaPhraseCandidateRequiresCPOEvidenceWithinThreshold(t *testing.T) {
	thresholds := domain.RecommendationThresholds{CampaignPoorCPO: 1000}

	require.False(t, isSEOIdeaPhraseCandidate(sqlcgen.PhraseStat{
		Spend:  2500,
		Orders: pgtype.Int8{Int64: 2, Valid: true},
	}, thresholds))

	require.False(t, isSEOIdeaPhraseCandidate(sqlcgen.PhraseStat{
		Spend:  0,
		Orders: pgtype.Int8{Int64: 2, Valid: true},
	}, thresholds))

	require.True(t, isSEOIdeaPhraseCandidate(sqlcgen.PhraseStat{
		Spend:  900,
		Orders: pgtype.Int8{Int64: 2, Valid: true},
	}, thresholds))
}
