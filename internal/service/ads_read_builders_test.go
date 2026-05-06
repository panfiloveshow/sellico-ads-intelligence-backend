package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestAggregateProductMetricsSkipsSharedCampaignStats(t *testing.T) {
	svc := &AdsReadService{}
	productID := uuid.New()
	otherProductID := uuid.New()
	campaignID := uuid.New()
	now := time.Now()
	orders := int64(12)
	revenue := int64(24000)

	data := &adsWorkspaceData{
		campaignStatsByID: map[uuid.UUID][]domain.CampaignStat{
			campaignID: {
				{
					CampaignID:  campaignID,
					Date:        now,
					Impressions: 1000,
					Clicks:      100,
					Spend:       6000,
					Orders:      &orders,
					Revenue:     &revenue,
				},
			},
		},
		campaignProductIDs: map[uuid.UUID][]uuid.UUID{
			campaignID: {productID, otherProductID},
		},
	}

	metrics, note := svc.aggregateProductMetrics(
		data,
		productID,
		[]domain.Campaign{{ID: campaignID}},
		now.AddDate(0, 0, -30),
		now,
	)

	if metrics.DataMode != "shared" {
		t.Fatalf("expected shared data mode, got %q", metrics.DataMode)
	}
	if metrics.Spend != 0 || metrics.Revenue != 0 || metrics.Orders != 0 {
		t.Fatalf("shared campaign stats must not be copied to product metrics: %+v", metrics)
	}
	if note == nil || !strings.Contains(*note, "нельзя честно отнести") {
		t.Fatalf("expected data coverage note, got %v", note)
	}
}

func TestBuildQuerySummaryMarksMissingPhraseStatsUnavailable(t *testing.T) {
	svc := &AdsReadService{}
	cabinetID := uuid.New()
	campaignID := uuid.New()
	phraseID := uuid.New()
	workspaceID := uuid.New()
	now := time.Now()

	data := &adsWorkspaceData{
		cabinets: map[uuid.UUID]domain.SellerCabinet{
			cabinetID: {
				ID:   cabinetID,
				Name: "WB cabinet",
			},
		},
		phraseStatsByID: map[uuid.UUID][]domain.PhraseStat{},
	}

	summary := svc.buildQuerySummary(
		data,
		domain.Phrase{
			ID:          phraseID,
			WorkspaceID: workspaceID,
			CampaignID:  campaignID,
			Keyword:     "петли мебельные",
		},
		domain.Campaign{
			ID:              campaignID,
			WorkspaceID:     workspaceID,
			SellerCabinetID: cabinetID,
			Name:            "Search campaign",
		},
		nil,
		now.AddDate(0, 0, -30),
		now,
	)

	if summary.Performance.DataMode != "unavailable" {
		t.Fatalf("expected unavailable data mode for missing phrase stats, got %q", summary.Performance.DataMode)
	}
	if summary.HealthStatus != "insufficient_data" || summary.SignalCategory != "insufficient_data" {
		t.Fatalf("expected insufficient data signal, got health=%q signal=%q", summary.HealthStatus, summary.SignalCategory)
	}
}
