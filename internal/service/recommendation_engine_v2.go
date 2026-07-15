package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

const recommendationAnalysisWindowDays = 14

type recommendationInsightsReader interface {
	ListCampaignSummaries(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter CampaignSummaryFilter) ([]domain.CampaignPerformanceSummary, error)
}

type recommendationConfidenceFactor struct {
	Code   string  `json:"code"`
	Impact float64 `json:"impact"`
	Reason string  `json:"reason"`
}

type recommendationWindow struct {
	DateFrom string `json:"date_from"`
	DateTo   string `json:"date_to"`
}

type recommendationActionMetadata struct {
	Kind                 string  `json:"kind"`
	CanApply             bool    `json:"can_apply"`
	RequiresConfirmation bool    `json:"requires_confirmation"`
	BlockReason          *string `json:"block_reason,omitempty"`
}

func (e *RecommendationEngine) generateWindowedCampaignRecommendations(
	ctx context.Context,
	workspaceID uuid.UUID,
	thresholds domain.RecommendationThresholds,
	now time.Time,
) ([]domain.Recommendation, error) {
	dateTo := normalizeStatDate(now)
	dateFrom := dateTo.AddDate(0, 0, -(recommendationAnalysisWindowDays - 1))
	summaries, err := e.insights.ListCampaignSummaries(ctx, workspaceID, dateFrom, dateTo, CampaignSummaryFilter{})
	if err != nil {
		return nil, fmt.Errorf("load period-aware campaign insights: %w", err)
	}

	generated := make([]domain.Recommendation, 0, len(summaries))
	managedTypes := []string{
		domain.RecommendationTypeLowCTR,
		domain.RecommendationTypeCampaignTestSpend,
		domain.RecommendationTypeHighSpendLowOrders,
		domain.RecommendationTypeBidAdjustment,
	}

	for _, summary := range summaries {
		campaignID := summary.ID
		inputs := buildWindowedCampaignRecommendationInputs(workspaceID, summary, thresholds, dateFrom, dateTo)
		fired := make(map[string]struct{}, len(inputs))
		for _, input := range inputs {
			recommendation, upsertErr := e.recommendations.UpsertActive(ctx, input)
			if upsertErr != nil {
				return nil, upsertErr
			}
			generated = append(generated, *recommendation)
			fired[input.Type] = struct{}{}
		}
		for _, recommendationType := range managedTypes {
			if _, ok := fired[recommendationType]; ok {
				continue
			}
			if closeErr := e.recommendations.CloseActive(ctx, workspaceID, recommendationType, &campaignID, nil, nil); closeErr != nil {
				return nil, closeErr
			}
		}
	}

	return generated, nil
}

func buildWindowedCampaignRecommendationInputs(
	workspaceID uuid.UUID,
	summary domain.CampaignPerformanceSummary,
	thresholds domain.RecommendationThresholds,
	dateFrom time.Time,
	dateTo time.Time,
) []RecommendationUpsertInput {
	if summary.Status != "active" && summary.Status != "paused" {
		return nil
	}
	current := summary.Performance
	if current.DataMode == "unavailable" {
		return nil
	}
	previous := domain.AdsMetricsSummary{DataMode: "unavailable"}
	trend := "unknown"
	if summary.PeriodCompare != nil {
		previous = summary.PeriodCompare.Previous
		trend = summary.PeriodCompare.Trend
	}

	inputs := make([]RecommendationUpsertInput, 0, 4)
	campaignID := summary.ID

	if current.Impressions >= int64(thresholds.CampaignHighImpressions) && current.Clicks == 0 {
		confirmed := previous.Impressions >= int64(thresholds.CampaignHighImpressions) && previous.Clicks == 0
		confidence, factors := calculateRecommendationConfidence(summary, current, previous, confirmed)
		action := recommendationActionMetadata{
			Kind:                 "review_campaign_relevance",
			CanApply:             false,
			RequiresConfirmation: true,
			BlockReason:          stringPointer("Изменение карточки и релевантности требует решения менеджера."),
		}
		inputs = append(inputs, RecommendationUpsertInput{
			WorkspaceID: workspaceID,
			CampaignID:  &campaignID,
			Title:       "Много показов без кликов",
			Description: fmt.Sprintf("Кампания %q получила %d показов и 0 кликов за %d дней. Вывод построен по всему периоду, а не по одному последнему дню.", summary.Name, current.Impressions, recommendationAnalysisWindowDays),
			Type:        domain.RecommendationTypeLowCTR,
			Severity:    domain.SeverityHigh,
			Confidence:  confidence,
			SourceMetrics: windowedCampaignSourceMetrics(summary, current, previous, trend, dateFrom, dateTo, factors, action,
				"Реальные campaign_stats: показы выше порога, кликов за период нет."),
			NextAction: strPtr("Проверьте релевантность запросов и карточки. Не увеличивайте расход, пока CTR не появится."),
		})
	}

	if current.Orders == 0 && current.Spend >= thresholds.CampaignMaxTestSpend && current.Clicks < int64(thresholds.CampaignZeroOrdersClick) {
		confirmed := previous.Spend > 0 && previous.Orders == 0
		confidence, factors := calculateRecommendationConfidence(summary, current, previous, confirmed)
		action := pauseCampaignAction(summary)
		inputs = append(inputs, RecommendationUpsertInput{
			WorkspaceID: workspaceID,
			CampaignID:  &campaignID,
			Title:       "Тестовый расход кампании достиг лимита",
			Description: fmt.Sprintf("Кампания %q потратила %d ₽ без заказов за %d дней, но получила только %d кликов из %d, необходимых для более устойчивого решения.", summary.Name, current.Spend, recommendationAnalysisWindowDays, current.Clicks, thresholds.CampaignZeroOrdersClick),
			Type:        domain.RecommendationTypeCampaignTestSpend,
			Severity:    domain.SeverityMedium,
			Confidence:  confidence,
			SourceMetrics: windowedCampaignSourceMetrics(summary, current, previous, trend, dateFrom, dateTo, factors, action,
				"Реальные campaign_stats: тестовый лимит расхода достигнут, заказов нет, выборка кликов ещё ограничена."),
			NextAction: strPtr("Не расширяйте тест. Проверьте трафик и карточку; паузу применяйте только после подтверждения менеджером."),
		})
	}

	if current.Orders == 0 && current.Clicks >= int64(thresholds.CampaignZeroOrdersClick) && current.Spend >= thresholds.CampaignMaxSpendNoOrder {
		confirmed := previous.Spend > 0 && previous.Orders == 0
		confidence, factors := calculateRecommendationConfidence(summary, current, previous, confirmed)
		action := pauseCampaignAction(summary)
		inputs = append(inputs, RecommendationUpsertInput{
			WorkspaceID: workspaceID,
			CampaignID:  &campaignID,
			Title:       "Клики не конвертируются в заказы",
			Description: fmt.Sprintf("Кампания %q получила %d кликов и потратила %d ₽, но не получила заказов за %d дней.", summary.Name, current.Clicks, current.Spend, recommendationAnalysisWindowDays),
			Type:        domain.RecommendationTypeHighSpendLowOrders,
			Severity:    domain.SeverityHigh,
			Confidence:  confidence,
			SourceMetrics: windowedCampaignSourceMetrics(summary, current, previous, trend, dateFrom, dateTo, factors, action,
				"Реальные campaign_stats: достаточная выборка кликов и расход выше лимита при нуле заказов."),
			NextAction: strPtr("Проверьте поисковые запросы и конверсию карточки; рассмотрите паузу после подтверждения."),
		})
	}

	if current.Clicks > 0 {
		poorCost := current.CPC >= thresholds.CampaignHighCPC && (current.Orders == 0 || current.CPO >= thresholds.CampaignPoorCPO)
		strongReturn := current.Spend > 0 && current.Clicks >= int64(thresholds.CampaignRaiseBidClicks) && current.Orders >= int64(thresholds.CampaignRaiseBidOrders) && current.Revenue > 0 && current.ROAS >= thresholds.CampaignStrongROAS
		if poorCost || strongReturn {
			previousConfirmed := false
			title := "Ставки кампании требуют пересмотра"
			description := fmt.Sprintf("Кампания %q: CPC %.2f ₽, CPO %.2f ₽ за %d дней.", summary.Name, current.CPC, current.CPO, recommendationAnalysisWindowDays)
			nextAction := "Откройте товарные размещения и изменяйте только ставки с подтверждённым NMID и типом размещения."
			decisionBasis := "Реальные campaign_stats показывают высокий CPC/CPO; агрегат кампании не содержит одной авторитетной ставки."
			severity := domain.SeverityMedium
			if strongReturn && !poorCost {
				title = "Кампания эффективна — проверьте потенциал масштабирования"
				description = fmt.Sprintf("Кампания %q получила %d заказов, выручку %d ₽ и ROAS %.2f за %d дней.", summary.Name, current.Orders, current.Revenue, current.ROAS, recommendationAnalysisWindowDays)
				nextAction = "Проверьте юнит-экономику и остатки товаров, затем масштабируйте только конкретные прибыльные размещения."
				decisionBasis = "Реальные campaign_stats подтверждают заказы, выручку и ROAS; это сигнал для проверки масштаба, не команда на автоматическое повышение агрегатной ставки."
				previousConfirmed = previous.Spend > 0 && previous.Orders > 0 && previous.ROAS >= thresholds.CampaignStrongROAS
				if current.ROAS >= thresholds.CampaignStrongROAS*1.5 {
					severity = domain.SeverityHigh
				}
			} else {
				previousConfirmed = previous.Clicks > 0 && previous.CPC >= thresholds.CampaignHighCPC && (previous.Orders == 0 || previous.CPO >= thresholds.CampaignPoorCPO)
			}
			confidence, factors := calculateRecommendationConfidence(summary, current, previous, previousConfirmed)
			action := recommendationActionMetadata{
				Kind:                 "review_product_bids",
				CanApply:             false,
				RequiresConfirmation: true,
				BlockReason:          stringPointer("У кампании нет одной авторитетной ставки: действие должно быть рассчитано по товару и размещению."),
			}
			inputs = append(inputs, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				CampaignID:  &campaignID,
				Title:       title,
				Description: description,
				Type:        domain.RecommendationTypeBidAdjustment,
				Severity:    severity,
				Confidence:  confidence,
				SourceMetrics: windowedCampaignSourceMetrics(summary, current, previous, trend, dateFrom, dateTo, factors, action,
					decisionBasis),
				NextAction: strPtr(nextAction),
			})
		}
	}

	return inputs
}

func calculateRecommendationConfidence(
	summary domain.CampaignPerformanceSummary,
	current domain.AdsMetricsSummary,
	previous domain.AdsMetricsSummary,
	confirmedInPreviousPeriod bool,
) (float64, []recommendationConfidenceFactor) {
	confidence := 0.35
	factors := []recommendationConfidenceFactor{{Code: "base", Impact: 0.35, Reason: "Базовая уверенность для правила на реальных данных."}}
	add := func(code string, impact float64, reason string) {
		confidence += impact
		factors = append(factors, recommendationConfidenceFactor{Code: code, Impact: impact, Reason: reason})
	}

	switch current.DataMode {
	case "exact":
		add("exact_metrics", 0.15, "Метрики получены из точной дневной статистики кампании.")
	case "partial":
		add("partial_metrics", 0.05, "Доступна только часть метрик кампании.")
	default:
		add("unknown_metrics", -0.10, "Режим данных не подтверждён.")
	}
	switch summary.FreshnessState {
	case "fresh":
		add("fresh_sync", 0.10, "Синхронизация кабинета свежая.")
	case "stale":
		add("stale_sync", -0.15, "Синхронизация устарела.")
	default:
		add("unknown_freshness", -0.05, "Свежесть синхронизации не подтверждена.")
	}
	if current.Impressions >= 2000 {
		add("impression_sample", 0.10, "Накоплена крупная выборка показов.")
	} else if current.Impressions >= 500 {
		add("impression_sample", 0.05, "Накоплена рабочая выборка показов.")
	}
	if current.Clicks >= 40 {
		add("click_sample", 0.08, "Накоплена достаточная выборка кликов.")
	} else if current.Clicks >= 10 {
		add("click_sample", 0.04, "Есть клики, но выборка ещё ограничена.")
	}
	if current.Orders >= 5 {
		add("order_sample", 0.07, "Есть достаточная выборка заказов.")
	} else if current.Orders > 0 {
		add("order_sample", 0.03, "Есть реальные заказы, но выборка небольшая.")
	}
	if previous.DataMode != "unavailable" {
		add("previous_period_available", 0.04, "Есть данные предыдущего равного периода.")
	}
	if confirmedInPreviousPeriod {
		add("signal_repeated", 0.08, "Сигнал подтверждается в предыдущем периоде.")
	}
	if summary.Evidence != nil && summary.Evidence.ConfirmedInCabinet {
		add("cabinet_confirmation", 0.03, "Сигнал дополнительно подтверждён данными кабинета.")
	}

	return math.Round(math.Max(0.20, math.Min(0.95, confidence))*100) / 100, factors
}

func pauseCampaignAction(summary domain.CampaignPerformanceSummary) recommendationActionMetadata {
	metadata := recommendationActionMetadata{
		Kind:                 "pause_campaign",
		RequiresConfirmation: true,
	}
	switch {
	case summary.Status != "active":
		metadata.BlockReason = stringPointer("Кампания уже не активна.")
	case summary.Performance.DataMode != "exact":
		metadata.BlockReason = stringPointer("Действие заблокировано: статистика кампании неполная.")
	case summary.FreshnessState != "fresh":
		metadata.BlockReason = stringPointer("Действие заблокировано: свежесть синхронизации не подтверждена.")
	default:
		metadata.CanApply = true
	}
	return metadata
}

func windowedCampaignSourceMetrics(
	summary domain.CampaignPerformanceSummary,
	current domain.AdsMetricsSummary,
	previous domain.AdsMetricsSummary,
	trend string,
	dateFrom time.Time,
	dateTo time.Time,
	factors []recommendationConfidenceFactor,
	action recommendationActionMetadata,
	decisionBasis string,
) map[string]any {
	previousFrom, previousTo := previousPeriodRange(dateFrom, dateTo)
	return map[string]any{
		"wb_campaign_id":  summary.WBCampaignID,
		"impressions":     current.Impressions,
		"clicks":          current.Clicks,
		"spend":           current.Spend,
		"orders":          current.Orders,
		"revenue":         current.Revenue,
		"ctr":             current.CTR,
		"cpc":             current.CPC,
		"cpo":             current.CPO,
		"roas":            current.ROAS,
		"drr":             current.DRR,
		"data_mode":       current.DataMode,
		"freshness_state": summary.FreshnessState,
		"analysis_window": recommendationWindow{
			DateFrom: dateFrom.Format("2006-01-02"),
			DateTo:   dateTo.Format("2006-01-02"),
		},
		"previous_window": recommendationWindow{
			DateFrom: previousFrom.Format("2006-01-02"),
			DateTo:   previousTo.Format("2006-01-02"),
		},
		"previous_metrics":   previous,
		"period_trend":       trend,
		"confidence_factors": factors,
		"action":             action,
		"decision_basis":     decisionBasis,
	}
}

func dedupeRecommendationsByID(items []domain.Recommendation) []domain.Recommendation {
	result := make([]domain.Recommendation, 0, len(items))
	indices := make(map[uuid.UUID]int, len(items))
	for _, item := range items {
		if index, ok := indices[item.ID]; ok {
			result[index] = item
			continue
		}
		indices[item.ID] = len(result)
		result = append(result, item)
	}
	return result
}

func stringPointer(value string) *string {
	return &value
}
