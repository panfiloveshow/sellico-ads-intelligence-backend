package domain

import "testing"

func TestRecommendationTaskOwnerRole(t *testing.T) {
	tests := []struct {
		name    string
		recType string
		want    string
	}{
		{
			name:    "marketer handles spend control",
			recType: RecommendationTypeLowerBid,
			want:    RecommendationTaskOwnerMarketer,
		},
		{
			name:    "seo handles SEO ideas",
			recType: RecommendationTypeOptimizeSEO,
			want:    RecommendationTaskOwnerSEO,
		},
		{
			name:    "content handles card conversion",
			recType: RecommendationTypeCardConversionIssue,
			want:    RecommendationTaskOwnerContent,
		},
		{
			name:    "marketplace manager handles stock",
			recType: RecommendationTypeStockAlert,
			want:    RecommendationTaskOwnerMarketplaceManager,
		},
		{
			name:    "technical specialist handles api risks",
			recType: "wb_api_errors",
			want:    RecommendationTaskOwnerTechnicalSpecialist,
		},
		{
			name:    "unknown",
			recType: "unknown_recommendation",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecommendationTaskOwnerRole(tt.recType); got != tt.want {
				t.Fatalf("RecommendationTaskOwnerRole(%q) = %q, want %q", tt.recType, got, tt.want)
			}
		})
	}
}
