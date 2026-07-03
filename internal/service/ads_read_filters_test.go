package service

import (
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestMatchesQueryViewSupportsOperationalClusterFilters(t *testing.T) {
	tests := []struct {
		name    string
		view    string
		summary domain.QueryPerformanceSummary
		want    bool
	}{
		{
			name: "with orders",
			view: "with_orders",
			summary: domain.QueryPerformanceSummary{Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Orders:   2,
			}},
			want: true,
		},
		{
			name: "no orders requires real stats",
			view: "no_orders",
			summary: domain.QueryPerformanceSummary{Performance: domain.AdsMetricsSummary{
				DataMode: "unavailable",
			}},
			want: false,
		},
		{
			name: "high ctr",
			view: "high_ctr",
			summary: domain.QueryPerformanceSummary{Performance: domain.AdsMetricsSummary{
				Clicks: 12,
				CTR:    0.04,
			}},
			want: true,
		},
		{
			name: "high drr",
			view: "high_drr",
			summary: domain.QueryPerformanceSummary{Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    2200,
				Orders:   2,
				Revenue:  4000,
				DRR:      55,
			}},
			want: true,
		},
		{
			name: "carts without orders",
			view: "carts_without_orders",
			summary: domain.QueryPerformanceSummary{Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Clicks:   12,
				Atbs:     3,
				Orders:   0,
			}},
			want: true,
		},
		{
			name: "minus candidate from trash signal",
			view: "minus_candidates",
			summary: domain.QueryPerformanceSummary{
				SignalCategory: "trash",
				Performance:    domain.AdsMetricsSummary{DataMode: "exact"},
			},
			want: true,
		},
		{
			name: "seo candidate",
			view: "seo_candidates",
			summary: domain.QueryPerformanceSummary{
				SignalCategory: "seo_idea",
				Performance:    domain.AdsMetricsSummary{DataMode: "exact"},
			},
			want: true,
		},
		{
			name: "new clusters maps to insufficient real data state",
			view: "new_clusters",
			summary: domain.QueryPerformanceSummary{
				HealthStatus: "insufficient_data",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesQueryView(tt.view, tt.summary); got != tt.want {
				t.Fatalf("matchesQueryView(%q)=%v, want %v", tt.view, got, tt.want)
			}
		})
	}
}
