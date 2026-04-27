package service

import (
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func aggregateCampaignStats(stats []domain.CampaignStat, _, _ time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	for _, stat := range stats {
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
	}
	return finalizeMetrics(result, "exact")
}

// aggregatePhraseStats sums all phrase stats.
// Date filtering is already done at SQL level.
func aggregatePhraseStats(stats []domain.PhraseStat, _, _ time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	for _, stat := range stats {
		result.Impressions += stat.Impressions
		result.Clicks += stat.Clicks
		result.Spend += stat.Spend
	}
	return finalizeMetrics(result, "exact")
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

func aggregateWorkspaceMetrics(statsByCampaign map[uuid.UUID][]domain.CampaignStat, dateFrom, dateTo time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	for _, stats := range statsByCampaign {
		summary := aggregateCampaignStats(stats, dateFrom, dateTo)
		result.Impressions += summary.Impressions
		result.Clicks += summary.Clicks
		result.Spend += summary.Spend
		result.Orders += summary.Orders
		result.Revenue += summary.Revenue
	}
	return finalizeMetrics(result, "exact")
}

func finalizeMetrics(metrics domain.AdsMetricsSummary, mode string) domain.AdsMetricsSummary {
	metrics.DataMode = mode
	if metrics.Impressions > 0 {
		metrics.CTR = float64(metrics.Clicks) / float64(metrics.Impressions)
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
