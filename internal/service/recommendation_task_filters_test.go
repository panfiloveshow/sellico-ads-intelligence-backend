package service

import (
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestFilterRecommendationsForTaskViewUsesDerivedTaskFields(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	overdue := true

	items := []domain.Recommendation{
		{
			Title:     "Снизить ставку",
			Type:      domain.RecommendationTypeLowerBid,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: now.Add(-72 * time.Hour),
		},
		{
			Title:     "SEO задача",
			Type:      domain.RecommendationTypeOptimizeSEO,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: now.Add(-72 * time.Hour),
		},
		{
			Title:     "Свежая потеря",
			Type:      domain.RecommendationTypeHighSpendLowOrders,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: now.Add(-2 * time.Hour),
		},
	}

	result := filterRecommendationsForTaskView(items, RecommendationListFilter{
		TaskCategory:  domain.RecommendationTaskCategoryLosses,
		TaskOwnerRole: domain.RecommendationTaskOwnerMarketer,
		Overdue:       &overdue,
	}, now)

	if len(result) != 1 {
		t.Fatalf("expected one overdue marketer loss task, got %+v", result)
	}
	if result[0].Title != "Снизить ставку" {
		t.Fatalf("unexpected filtered recommendation: %+v", result[0])
	}
}

func TestFilterRecommendationsForTaskViewCanSelectFreshTasks(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	overdue := false

	result := filterRecommendationsForTaskView([]domain.Recommendation{
		{
			Title:     "Просроченная задача",
			Type:      domain.RecommendationTypeStockAlert,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: now.Add(-72 * time.Hour),
		},
		{
			Title:     "Свежая задача",
			Type:      domain.RecommendationTypeStockAlert,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: now.Add(-2 * time.Hour),
		},
	}, RecommendationListFilter{
		TaskOwnerRole: domain.RecommendationTaskOwnerMarketplaceManager,
		Overdue:       &overdue,
	}, now)

	if len(result) != 1 || result[0].Title != "Свежая задача" {
		t.Fatalf("expected only fresh marketplace-manager task, got %+v", result)
	}
}
