package domain

import "testing"

func TestRecommendationTaskCategory(t *testing.T) {
	tests := []struct {
		name    string
		recType string
		want    string
	}{
		{
			name:    "budget losses",
			recType: RecommendationTypeHighSpendLowOrders,
			want:    RecommendationTaskCategoryLosses,
		},
		{
			name:    "growth",
			recType: RecommendationTypeRaiseBid,
			want:    RecommendationTaskCategoryGrowth,
		},
		{
			name:    "card task",
			recType: RecommendationTypeStockAlert,
			want:    RecommendationTaskCategoryCardTasks,
		},
		{
			name:    "api risk",
			recType: "wb_api_rate_limited",
			want:    RecommendationTaskCategoryAPIRisks,
		},
		{
			name:    "unknown",
			recType: "unknown_recommendation",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecommendationTaskCategory(tt.recType); got != tt.want {
				t.Fatalf("RecommendationTaskCategory(%q) = %q, want %q", tt.recType, got, tt.want)
			}
		})
	}
}
