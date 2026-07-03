package service

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func aggregateCampaignStats(stats []domain.CampaignStat, dateFrom, dateTo time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	matched := false
	for _, stat := range stats {
		if !dateInRange(stat.Date, dateFrom, dateTo) {
			continue
		}
		matched = true
		result.Impressions += stat.Impressions
		result.Clicks += stat.Clicks
		result.Spend += stat.Spend
		if stat.Orders != nil {
			result.Orders += *stat.Orders
		}
		if stat.Revenue != nil {
			result.Revenue += *stat.Revenue
		}
		if stat.Atbs != nil {
			result.Atbs += *stat.Atbs
		}
		if stat.Canceled != nil {
			result.Canceled += *stat.Canceled
		}
		if stat.Shks != nil {
			result.Shks += *stat.Shks
		}
	}
	if !matched {
		return domain.AdsMetricsSummary{DataMode: "unavailable"}
	}
	if !matched {
		return domain.AdsMetricsSummary{DataMode: "unavailable"}
	}
	return finalizeMetrics(result, "exact")
}

func aggregatePhraseStats(stats []domain.PhraseStat, dateFrom, dateTo time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	matched := false
	var avgPositionWeightedSum float64
	var avgPositionWeight int64
	for _, stat := range stats {
		if !dateInRange(stat.Date, dateFrom, dateTo) {
			continue
		}
		matched = true
		result.Impressions += stat.Impressions
		result.Clicks += stat.Clicks
		result.Spend += stat.Spend
		if stat.Atbs != nil {
			result.Atbs += *stat.Atbs
		}
		if stat.Orders != nil {
			result.Orders += *stat.Orders
		}
		if stat.AvgPos != nil {
			weight := stat.Impressions
			if weight == 0 {
				weight = stat.Clicks
			}
			if weight == 0 {
				weight = 1
			}
			avgPositionWeightedSum += *stat.AvgPos * float64(weight)
			avgPositionWeight += weight
		}
	}
	if !matched {
		return domain.AdsMetricsSummary{DataMode: "unavailable"}
	}
	if avgPositionWeight > 0 {
		result.AvgPosition = avgPositionWeightedSum / float64(avgPositionWeight)
	}
	return finalizeMetrics(result, "exact")
}

func aggregateProductStats(stats []domain.ProductStat, dateFrom, dateTo time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	matched := false
	for _, stat := range stats {
		if !dateInRange(stat.Date, dateFrom, dateTo) {
			continue
		}
		matched = true
		result.Impressions += stat.Impressions
		result.Clicks += stat.Clicks
		result.Spend += stat.Spend
		if stat.Orders != nil {
			result.Orders += *stat.Orders
		}
		if stat.Revenue != nil {
			result.Revenue += *stat.Revenue
		}
		if stat.Atbs != nil {
			result.Atbs += *stat.Atbs
		}
		if stat.Canceled != nil {
			result.Canceled += *stat.Canceled
		}
		if stat.Shks != nil {
			result.Shks += *stat.Shks
		}
	}
	if !matched {
		return domain.AdsMetricsSummary{DataMode: "unavailable"}
	}
	return finalizeMetrics(result, "exact")
}

func dateInRange(date, dateFrom, dateTo time.Time) bool {
	day := normalizeStatDate(date)
	return !day.Before(normalizeStatDate(dateFrom)) && !day.After(normalizeStatDate(dateTo))
}

func normalizeStatDate(date time.Time) time.Time {
	year, month, day := date.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func aggregateProductBusiness(rows []domain.ProductBusinessSummary, adSpend int64, dateFrom, dateTo time.Time) domain.ProductBusinessSummary {
	if len(rows) == 0 {
		return domain.ProductBusinessSummary{AdSpend: adSpend, DataMode: "unavailable"}
	}

	result := domain.ProductBusinessSummary{AdSpend: adSpend, DataMode: "reports"}
	for _, row := range rows {
		if !dateInRange(row.Date, dateFrom, dateTo) {
			continue
		}
		result.Orders += row.Orders
		result.CanceledOrders += row.CanceledOrders
		result.Sales += row.Sales
		result.Returns += row.Returns
		result.OrderedRevenue += row.OrderedRevenue
		result.SoldRevenue += row.SoldRevenue
		result.ReturnedRevenue += row.ReturnedRevenue
	}
	if result.Orders == 0 && result.Sales == 0 && result.OrderedRevenue == 0 && result.SoldRevenue == 0 {
		result.DataMode = "unavailable"
		return result
	}
	if result.Orders > 0 {
		result.BuyoutRate = float64(result.Sales) / float64(result.Orders)
	}
	if result.Sales+result.Returns > 0 {
		result.ReturnRate = float64(result.Returns) / float64(result.Sales+result.Returns)
	}
	if result.SoldRevenue > 0 {
		result.AdToSoldRevenue = float64(adSpend) / float64(result.SoldRevenue) * 100
	}
	return result
}

// filterCampaignStatsByCabinet returns only campaign stats for campaigns belonging to the specified cabinet.
func filterCampaignStatsByCabinet(data *adsWorkspaceData, cabinetID uuid.UUID) map[uuid.UUID][]domain.CampaignStat {
	cabinetCampaigns := make(map[uuid.UUID]struct{})
	for _, c := range data.campaigns {
		if c.SellerCabinetID == cabinetID {
			cabinetCampaigns[c.ID] = struct{}{}
		}
	}
	filtered := make(map[uuid.UUID][]domain.CampaignStat, len(cabinetCampaigns))
	for campaignID, stats := range data.campaignStatsByID {
		if _, ok := cabinetCampaigns[campaignID]; ok {
			filtered[campaignID] = stats
		}
	}
	return filtered
}

func buildAdsDataStatus(data *adsWorkspaceData, dateFrom, dateTo time.Time, cabinetFilter *uuid.UUID) domain.AdsDataStatus {
	status := domain.AdsDataStatus{
		State:                    "ready",
		Reason:                   "Данные рекламной аналитики загружены из реальных синхронизаций.",
		FreshnessState:           "unknown",
		HasConnectedCabinet:      len(data.cabinets) > 0,
		ProductsTotal:            len(data.products),
		QueriesTotal:             len(data.phrases),
		ProductsWithBusinessData: countProductsWithBusinessData(data, dateFrom, dateTo, cabinetFilter),
	}
	if data.lastAutoSync != nil {
		status.FreshnessState = data.lastAutoSync.FreshnessState
		status.LastSyncedAt = data.lastAutoSync.FinishedAt
	}

	campaignIDs := make(map[uuid.UUID]struct{})
	for _, campaign := range data.campaigns {
		if cabinetFilter != nil && campaign.SellerCabinetID != *cabinetFilter {
			continue
		}
		status.CampaignsTotal++
		campaignIDs[campaign.ID] = struct{}{}
		if campaign.Status == "active" || campaign.Status == "paused" {
			status.HasCampaigns = true
		}
		if campaignHasStatsInRange(data.campaignStatsByID[campaign.ID], dateFrom, dateTo) {
			status.CampaignsWithStats++
			status.HasCurrentStats = true
		}
	}

	status.QueriesTotal = 0
	for _, phrase := range data.phrases {
		if _, ok := campaignIDs[phrase.CampaignID]; !ok {
			continue
		}
		status.QueriesTotal++
		if phraseHasStatsInRange(data.phraseStatsByID[phrase.ID], dateFrom, dateTo) {
			status.QueriesWithStats++
			status.HasCurrentStats = true
		}
	}
	if mismatches := data.extensionEvidence.bidMismatchCount(data.phrases, campaignIDs); mismatches > 0 {
		status.Issues = append(status.Issues, domain.AdsDataStatusIssue{
			Stage:      "api_extension_mismatch",
			Message:    fmt.Sprintf("Найдено %d live-расхождений между ставками из официальной синхронизации WB и видимыми ставками в кабинете. Перед автодействиями обновите sync или повторите capture.", mismatches),
			ActionPath: "/ads-intelligence/evidence-debug",
		})
	}

	status.ProductsTotal = 0
	for _, product := range data.products {
		if cabinetFilter != nil && product.SellerCabinetID != *cabinetFilter {
			continue
		}
		status.ProductsTotal++
	}

	switch {
	case !status.HasConnectedCabinet:
		status.State = "no_connected_cabinet"
		status.Reason = "Нет подключенного кабинета маркетплейса."
	case status.CampaignsTotal == 0:
		status.State = "sync_required"
		status.Reason = "Кампании еще не синхронизированы."
	case !status.HasCurrentStats:
		status.State = "empty_period"
		status.Reason = "За выбранный период нет синхронизированной рекламной статистики."
	case status.FreshnessState == "stale":
		status.State = "stale"
		status.Reason = "Последняя синхронизация устарела; метрики могут быть неполными."
	case status.CampaignsWithStats < status.CampaignsTotal || status.QueriesWithStats < status.QueriesTotal:
		status.State = "partial"
		status.Reason = "Данные частичные: не у всех кампаний или фраз есть статистика за период."
	}

	return status
}

func (s *AdsReadService) enrichAdsDataStatus(status *domain.AdsDataStatus, dateFrom, dateTo time.Time, lastSync *domain.SellerCabinetAutoSyncSummary) {
	status.BackendVersion = s.backendVersion
	status.DateFrom = dateFrom.Format(exportDateLayout)
	status.DateTo = dateTo.Format(exportDateLayout)
	if s.unitEconomicsConfigured {
		status.UnitEconomicsState = "configured"
		status.UnitEconomicsReason = "Unit economics readiness checks are configured for bid scale-up decisions."
	} else {
		status.UnitEconomicsState = "not_configured"
		status.UnitEconomicsReason = "Unit economics is not connected; profitability and margin-aware scale-up are unavailable."
	}
	if lastSync == nil {
		return
	}
	status.ActiveJobRunID = &lastSync.JobRunID
	status.ActiveSyncPhase = lastSync.SyncPhase
	status.PhaseRetries = lastSync.PhaseRetries
	if lastSync.DateFrom != "" {
		status.DateFrom = lastSync.DateFrom
	}
	if lastSync.DateTo != "" {
		status.DateTo = lastSync.DateTo
	}
	if lastSync.RateLimited {
		status.State = "rate_limited"
		status.Reason = "WB ограничил запросы рекламной статистики; часть данных временно недоступна."
		status.RateLimitEndpoint = lastSync.RateLimitEndpoint
		status.RetryAfterSeconds = lastSync.RetryAfterSeconds
		status.NextAllowedAt = lastSync.NextAllowedAt
		status.Issues = append(status.Issues, domain.AdsDataStatusIssue{
			Stage:      "wb_rate_limit",
			Message:    "WB временно ограничил запросы. Дождитесь окончания лимита или откройте журнал синхронизации.",
			ActionPath: fmt.Sprintf("/ads-intelligence/jobs?job_id=%s", lastSync.JobRunID.String()),
		})
	}
	if len(lastSync.PhaseRetries) > 0 {
		status.Issues = append(status.Issues, domain.AdsDataStatusIssue{
			Stage:      "phase_retry_queued",
			Message:    "Часть фаз синхронизации поставлена в автоматический повтор после лимита WB.",
			ActionPath: fmt.Sprintf("/ads-intelligence/jobs?job_id=%s", lastSync.JobRunID.String()),
		})
	}
	if status.State == "partial" || status.State == "stale" || status.State == "empty_period" {
		status.Issues = append(status.Issues, domain.AdsDataStatusIssue{
			Stage:      status.State,
			Message:    status.Reason,
			ActionPath: fmt.Sprintf("/ads-intelligence/jobs?job_id=%s", lastSync.JobRunID.String()),
		})
	}
}

func campaignHasStatsInRange(stats []domain.CampaignStat, dateFrom, dateTo time.Time) bool {
	for _, stat := range stats {
		if dateInRange(stat.Date, dateFrom, dateTo) {
			return true
		}
	}
	return false
}

func phraseHasStatsInRange(stats []domain.PhraseStat, dateFrom, dateTo time.Time) bool {
	for _, stat := range stats {
		if dateInRange(stat.Date, dateFrom, dateTo) {
			return true
		}
	}
	return false
}

func countProductsWithBusinessData(data *adsWorkspaceData, dateFrom, dateTo time.Time, cabinetFilter *uuid.UUID) int {
	total := 0
	for _, product := range data.products {
		if cabinetFilter != nil && product.SellerCabinetID != *cabinetFilter {
			continue
		}
		for _, row := range data.productBusinessByID[product.ID] {
			if dateInRange(row.Date, dateFrom, dateTo) && (row.Orders > 0 || row.Sales > 0 || row.OrderedRevenue > 0 || row.SoldRevenue > 0) {
				total++
				break
			}
		}
	}
	return total
}

func aggregateWorkspaceMetrics(statsByCampaign map[uuid.UUID][]domain.CampaignStat, dateFrom, dateTo time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	for _, stats := range statsByCampaign {
		summary := aggregateCampaignStats(stats, dateFrom, dateTo)
		result.Impressions += summary.Impressions
		result.Clicks += summary.Clicks
		result.Spend += summary.Spend
		result.Orders += summary.Orders
		result.Revenue += summary.Revenue
		result.Atbs += summary.Atbs
		result.Canceled += summary.Canceled
		result.Shks += summary.Shks
	}
	return finalizeMetrics(result, "exact")
}

func finalizeMetrics(metrics domain.AdsMetricsSummary, mode string) domain.AdsMetricsSummary {
	metrics.DataMode = mode
	if metrics.Impressions > 0 {
		metrics.CTR = float64(metrics.Clicks) / float64(metrics.Impressions)
		metrics.CPM = float64(metrics.Spend) / float64(metrics.Impressions) * 1000
	}
	if metrics.Clicks > 0 {
		metrics.CPC = float64(metrics.Spend) / float64(metrics.Clicks)
		metrics.ConversionRate = float64(metrics.Orders) / float64(metrics.Clicks)
	}
	if metrics.Orders > 0 {
		metrics.CPO = float64(metrics.Spend) / float64(metrics.Orders)
	}
	if metrics.Spend > 0 {
		metrics.ROAS = float64(metrics.Revenue) / float64(metrics.Spend)
	}
	if metrics.Revenue > 0 {
		metrics.DRR = float64(metrics.Spend) / float64(metrics.Revenue) * 100
	}
	if metrics.Clicks > 0 && metrics.Atbs > 0 {
		metrics.CartRate = float64(metrics.Atbs) / float64(metrics.Clicks)
	}
	return metrics
}

func previousPeriodRange(dateFrom, dateTo time.Time) (time.Time, time.Time) {
	duration := dateTo.Sub(dateFrom) + 24*time.Hour
	previousTo := dateFrom.Add(-24 * time.Hour)
	previousFrom := previousTo.Add(-duration + 24*time.Hour)
	return previousFrom, previousTo
}

func buildPeriodCompare(current, previous domain.AdsMetricsSummary) *domain.AdsPeriodCompare {
	return &domain.AdsPeriodCompare{
		Current:  current,
		Previous: previous,
		Delta: domain.AdsMetricsDelta{
			Impressions:    current.Impressions - previous.Impressions,
			Clicks:         current.Clicks - previous.Clicks,
			Spend:          current.Spend - previous.Spend,
			Orders:         current.Orders - previous.Orders,
			Revenue:        current.Revenue - previous.Revenue,
			CTR:            current.CTR - previous.CTR,
			CPC:            current.CPC - previous.CPC,
			CPO:            current.CPO - previous.CPO,
			ROAS:           current.ROAS - previous.ROAS,
			ConversionRate: current.ConversionRate - previous.ConversionRate,
		},
		Trend: deriveMetricsTrend(current, previous),
	}
}

func deriveMetricsTrend(current, previous domain.AdsMetricsSummary) string {
	if current.Impressions == 0 && current.Clicks == 0 && current.Spend == 0 && current.Orders == 0 && current.Revenue == 0 {
		if previous.Impressions == 0 && previous.Clicks == 0 && previous.Spend == 0 && previous.Orders == 0 && previous.Revenue == 0 {
			return "flat"
		}
		return "declining"
	}
	if previous.Impressions == 0 && previous.Clicks == 0 && previous.Spend == 0 && previous.Orders == 0 && previous.Revenue == 0 {
		return "new"
	}

	revenueDelta := current.Revenue - previous.Revenue
	orderDelta := current.Orders - previous.Orders
	spendDelta := current.Spend - previous.Spend
	ctrDelta := current.CTR - previous.CTR

	switch {
	case revenueDelta > 0 && orderDelta >= 0 && spendDelta <= revenueDelta:
		return "improving"
	case revenueDelta < 0 || orderDelta < 0 || (spendDelta > 0 && revenueDelta <= 0):
		return "declining"
	case ctrDelta > 0.01 || ctrDelta < -0.01:
		return "volatile"
	default:
		return "flat"
	}
}

func buildCampaignRefs(campaigns []domain.Campaign) []domain.AdsEntityRef {
	result := make([]domain.AdsEntityRef, 0, len(campaigns))
	for _, campaign := range campaigns {
		wbID := campaign.WBCampaignID
		result = append(result, domain.AdsEntityRef{
			ID:    campaign.ID,
			Label: campaign.Name,
			WBID:  &wbID,
		})
	}
	return result
}

func buildProductRefs(products []domain.Product) []domain.AdsEntityRef {
	result := make([]domain.AdsEntityRef, 0, len(products))
	for _, product := range products {
		wbID := product.WBProductID
		result = append(result, domain.AdsEntityRef{
			ID:    product.ID,
			Label: product.Title,
			WBID:  &wbID,
		})
	}
	return result
}
