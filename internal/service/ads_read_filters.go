package service

import (
	"sort"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func buildQuerySummaryRefs(items []domain.QueryPerformanceSummary) []domain.AdsEntityRef {
	result := make([]domain.AdsEntityRef, 0, len(items))
	for _, item := range items {
		wbID := item.WBClusterID
		result = append(result, domain.AdsEntityRef{
			ID:     item.ID,
			Label:  item.Keyword,
			WBID:   &wbID,
			Count:  item.ClusterSize,
			Source: item.SignalCategory,
		})
	}
	return result
}

func dedupeCampaigns(items []domain.Campaign) []domain.Campaign {
	seen := make(map[uuid.UUID]struct{}, len(items))
	result := make([]domain.Campaign, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}

func dedupeProducts(items []domain.Product) []domain.Product {
	seen := make(map[uuid.UUID]struct{}, len(items))
	result := make([]domain.Product, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}

func dedupePhrases(items []domain.Phrase) []domain.Phrase {
	seen := make(map[uuid.UUID]struct{}, len(items))
	result := make([]domain.Phrase, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}

func containsProductID(products []domain.Product, productID uuid.UUID) bool {
	for _, product := range products {
		if product.ID == productID {
			return true
		}
	}
	return false
}

func sortProductSummaries(items []domain.ProductAdsSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Performance.Spend == items[j].Performance.Spend {
			return items[i].Title < items[j].Title
		}
		return items[i].Performance.Spend > items[j].Performance.Spend
	})
}

func sortCampaignSummaries(items []domain.CampaignPerformanceSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Performance.Spend == items[j].Performance.Spend {
			return items[i].Name < items[j].Name
		}
		return items[i].Performance.Spend > items[j].Performance.Spend
	})
}

func sortQuerySummaries(items []domain.QueryPerformanceSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].PriorityScore != items[j].PriorityScore {
			return items[i].PriorityScore > items[j].PriorityScore
		}
		leftRank := querySignalRank(items[i].SignalCategory)
		rightRank := querySignalRank(items[j].SignalCategory)
		if leftRank == rightRank {
			if items[i].Performance.Spend == items[j].Performance.Spend {
				if items[i].Performance.Impressions == items[j].Performance.Impressions {
					return items[i].Keyword < items[j].Keyword
				}
				return items[i].Performance.Impressions > items[j].Performance.Impressions
			}
			return items[i].Performance.Spend > items[j].Performance.Spend
		}
		return leftRank > rightRank
	})
}

func scoreQueryPriority(signalCategory string, metrics domain.AdsMetricsSummary, compare *domain.AdsPeriodCompare) int {
	score := 0

	switch signalCategory {
	case "waste":
		score += 80
	case "promising":
		score += 65
	case "high_volume":
		score += 50
	case "monitor":
		score += 20
	default:
		score += 10
	}

	if metrics.Spend >= 1000 {
		score += 20
	} else if metrics.Spend >= 500 {
		score += 10
	}
	if metrics.Impressions >= 1000 {
		score += 10
	} else if metrics.Impressions >= 300 {
		score += 5
	}
	if metrics.Clicks == 0 && metrics.Impressions >= 200 {
		score += 15
	}
	if compare != nil {
		switch compare.Trend {
		case "declining":
			score += 12
		case "improving":
			score += 8
		case "new":
			score += 6
		case "volatile":
			score += 4
		}
	}

	if score > 100 {
		return 100
	}
	return score
}

func matchesProductView(view, healthStatus string, compare *domain.AdsPeriodCompare) bool {
	switch view {
	case "", "all":
		return true
	case "scale":
		return healthStatus == "growing" || (compare != nil && (compare.Trend == "improving" || compare.Trend == "new"))
	case "save":
		return healthStatus == "waste" || healthStatus == "low_ctr" || (compare != nil && compare.Trend == "declining")
	case "watch":
		return healthStatus == "monitor" || healthStatus == "partial" || healthStatus == "insufficient_data"
	default:
		return true
	}
}

func matchesCampaignView(view, status, healthStatus string, compare *domain.AdsPeriodCompare) bool {
	switch view {
	case "", "all":
		return true
	case "profitable":
		return healthStatus == "growing" || (compare != nil && compare.Trend == "improving")
	case "waste":
		return healthStatus == "waste" || healthStatus == "low_ctr"
	case "stale":
		return healthStatus == "stale" || status == "paused" || (compare != nil && compare.Trend == "declining")
	case "watch":
		return healthStatus == "monitor" || healthStatus == "partial"
	default:
		return true
	}
}

func matchesQueryView(view string, summary domain.QueryPerformanceSummary) bool {
	switch view {
	case "", "all":
		return true
	case "priority":
		return summary.PriorityScore >= 40
	case "waste":
		return summary.SignalCategory == "waste"
	case "promising":
		return summary.SignalCategory == "promising"
	case "high_volume":
		return summary.SignalCategory == "high_volume"
	case "watch":
		return summary.SignalCategory == "monitor"
	default:
		return true
	}
}

func trimAttention(items []domain.AttentionItem, limit int) []domain.AttentionItem {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func trimProductSummaries(items []domain.ProductAdsSummary, limit int) []domain.ProductAdsSummary {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func trimCampaignSummaries(items []domain.CampaignPerformanceSummary, limit int) []domain.CampaignPerformanceSummary {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func trimQuerySummaries(items []domain.QueryPerformanceSummary, limit int) []domain.QueryPerformanceSummary {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func selectQuerySummariesBySignal(items []domain.QueryPerformanceSummary, signal string, limit int) []domain.QueryPerformanceSummary {
	filtered := make([]domain.QueryPerformanceSummary, 0, len(items))
	for _, item := range items {
		if item.SignalCategory == signal {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) <= limit {
		return filtered
	}
	return filtered[:limit]
}

func querySignalRank(value string) int {
	switch value {
	case "waste":
		return 4
	case "promising":
		return 3
	case "high_volume":
		return 2
	case "monitor":
		return 1
	default:
		return 0
	}
}
