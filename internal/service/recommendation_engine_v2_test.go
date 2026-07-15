package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestBuildWindowedCampaignRecommendationUsesPeriodAndExplainsConfidence(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	dateFrom := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	dateTo := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	summary := domain.CampaignPerformanceSummary{
		ID:             campaignID,
		Name:           "Поиск",
		Status:         "active",
		WBCampaignID:   123,
		FreshnessState: "fresh",
		Performance: domain.AdsMetricsSummary{
			Impressions: 2400,
			Clicks:      48,
			Spend:       3200,
			Orders:      0,
			DataMode:    "exact",
			CTR:         2,
			CPC:         66.67,
		},
		PeriodCompare: &domain.AdsPeriodCompare{
			Previous: domain.AdsMetricsSummary{Impressions: 2100, Clicks: 43, Spend: 2800, Orders: 0, DataMode: "exact"},
			Trend:    "declining",
		},
	}

	inputs := buildWindowedCampaignRecommendationInputs(workspaceID, summary, domain.DefaultThresholds(), dateFrom, dateTo)
	require.NotEmpty(t, inputs)

	var noOrders *RecommendationUpsertInput
	for index := range inputs {
		if inputs[index].Type == domain.RecommendationTypeHighSpendLowOrders {
			noOrders = &inputs[index]
			break
		}
	}
	require.NotNil(t, noOrders)
	require.Greater(t, noOrders.Confidence, 0.80)
	require.Equal(t, recommendationWindow{DateFrom: "2026-07-02", DateTo: "2026-07-15"}, noOrders.SourceMetrics["analysis_window"])
	require.Equal(t, "declining", noOrders.SourceMetrics["period_trend"])
	require.Contains(t, noOrders.SourceMetrics, "confidence_factors")
	action, ok := noOrders.SourceMetrics["action"].(recommendationActionMetadata)
	require.True(t, ok)
	require.True(t, action.CanApply)
	require.True(t, action.RequiresConfirmation)
}

func TestBuildWindowedCampaignRecommendationBlocksActionWhenSyncIsNotFresh(t *testing.T) {
	summary := domain.CampaignPerformanceSummary{
		ID:             uuid.New(),
		Name:           "Каталог",
		Status:         "active",
		FreshnessState: "stale",
		Performance: domain.AdsMetricsSummary{
			Impressions: 3000,
			Clicks:      60,
			Spend:       5000,
			Orders:      0,
			DataMode:    "exact",
		},
	}
	inputs := buildWindowedCampaignRecommendationInputs(uuid.New(), summary, domain.DefaultThresholds(), time.Now().AddDate(0, 0, -13), time.Now())
	require.NotEmpty(t, inputs)

	for _, input := range inputs {
		if input.Type != domain.RecommendationTypeHighSpendLowOrders {
			continue
		}
		action := input.SourceMetrics["action"].(recommendationActionMetadata)
		require.False(t, action.CanApply)
		require.NotNil(t, action.BlockReason)
		require.Contains(t, *action.BlockReason, "свежесть")
		return
	}
	t.Fatal("high spend without orders recommendation not generated")
}

func TestBuildWindowedCampaignScalingNeverInventsExecutableAggregateBid(t *testing.T) {
	summary := domain.CampaignPerformanceSummary{
		ID:             uuid.New(),
		Name:           "Бренд",
		Status:         "active",
		FreshnessState: "fresh",
		Performance: domain.AdsMetricsSummary{
			Impressions: 8000,
			Clicks:      140,
			Spend:       5000,
			Orders:      18,
			Revenue:     35000,
			ROAS:        7,
			CPC:         35.71,
			CPO:         277.78,
			DataMode:    "exact",
		},
	}
	inputs := buildWindowedCampaignRecommendationInputs(uuid.New(), summary, domain.DefaultThresholds(), time.Now().AddDate(0, 0, -13), time.Now())
	require.Len(t, inputs, 1)
	require.Equal(t, domain.RecommendationTypeBidAdjustment, inputs[0].Type)
	action := inputs[0].SourceMetrics["action"].(recommendationActionMetadata)
	require.False(t, action.CanApply)
	require.Contains(t, *action.BlockReason, "авторитетной ставки")
	require.Equal(t, int64(35000), inputs[0].SourceMetrics["revenue"])
}

func TestBuildWindowedCampaignRecommendationSkipsUnavailableData(t *testing.T) {
	inputs := buildWindowedCampaignRecommendationInputs(uuid.New(), domain.CampaignPerformanceSummary{
		ID:             uuid.New(),
		Status:         "active",
		FreshnessState: "unknown",
		Performance:    domain.AdsMetricsSummary{DataMode: "unavailable"},
	}, domain.DefaultThresholds(), time.Now().AddDate(0, 0, -13), time.Now())
	require.Empty(t, inputs)
}

func TestEnrichRecommendationDecisionMetadataReadsStoredSourceMetrics(t *testing.T) {
	sourceMetrics, err := json.Marshal(map[string]any{
		"analysis_window":    recommendationWindow{DateFrom: "2026-07-02", DateTo: "2026-07-15"},
		"confidence_factors": []recommendationConfidenceFactor{{Code: "fresh_sync", Impact: 0.1, Reason: "Свежие данные"}},
		"action":             recommendationActionMetadata{Kind: "pause_campaign", CanApply: false, RequiresConfirmation: true, BlockReason: stringPointer("Нужна синхронизация")},
		"decision_basis":     "Только реальные campaign_stats",
	})
	require.NoError(t, err)

	recommendation := domain.Recommendation{SourceMetrics: sourceMetrics}
	enrichRecommendationDecisionMetadata(&recommendation)

	require.NotNil(t, recommendation.AnalysisWindow)
	require.Equal(t, "2026-07-02", recommendation.AnalysisWindow.DateFrom)
	require.Len(t, recommendation.ConfidenceFactors, 1)
	require.NotNil(t, recommendation.Action)
	require.False(t, recommendation.Action.CanApply)
	require.Equal(t, "Только реальные campaign_stats", recommendation.DecisionBasis)
}
