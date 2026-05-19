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

func TestBuildProductSummariesUsesProductStatsLinksOnly(t *testing.T) {
	svc := &AdsReadService{}
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	campaignID := uuid.New()
	productID := uuid.New()
	catalogOnlyProductID := uuid.New()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	orders := int64(7)
	revenue := int64(20000)
	atbs := int64(11)

	data := &adsWorkspaceData{
		cabinets: map[uuid.UUID]domain.SellerCabinet{
			cabinetID: {
				ID:          cabinetID,
				WorkspaceID: workspaceID,
				Name:        "WB cabinet",
				Status:      "connected",
			},
		},
		campaigns: []domain.Campaign{
			{
				ID:              campaignID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBCampaignID:    171584733,
				Name:            "Рюкзак Клик",
				Status:          "active",
			},
		},
		products: []domain.Product{
			{
				ID:              productID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBProductID:     183310308,
				Title:           "Дорожная сумка из экокожи",
				CreatedAt:       now,
				UpdatedAt:       now,
			},
			{
				ID:              catalogOnlyProductID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBProductID:     999999999,
				Title:           "Каталожный товар без рекламной статистики",
				CreatedAt:       now,
				UpdatedAt:       now,
			},
		},
		productStatsByLink: map[productCampaignKey][]domain.ProductStat{
			{productID: productID, campaignID: campaignID}: {
				{
					ProductID:   productID,
					CampaignID:  campaignID,
					Date:        now,
					Impressions: 1000,
					Clicks:      50,
					Spend:       2500,
					Orders:      &orders,
					Revenue:     &revenue,
					Atbs:        &atbs,
				},
			},
		},
		campaignProductIDs: map[uuid.UUID][]uuid.UUID{
			campaignID: {productID, catalogOnlyProductID},
		},
		productCampaignIDs: map[uuid.UUID][]uuid.UUID{
			productID:            {campaignID},
			catalogOnlyProductID: {campaignID},
		},
		productBusinessByID: map[uuid.UUID][]domain.ProductBusinessSummary{},
		phraseStatsByID:     map[uuid.UUID][]domain.PhraseStat{},
		campaignPhrases:     map[uuid.UUID][]domain.Phrase{},
		extensionEvidence:   &workspaceExtensionEvidence{},
	}

	rows := svc.buildProductSummaries(data, now.AddDate(0, 0, -30), now, ProductSummaryFilter{})

	if len(rows) != 1 {
		t.Fatalf("expected only product rows backed by fullstats nms stats, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ID != productID {
		t.Fatalf("expected stats-backed product %s, got %s", productID, row.ID)
	}
	if row.CampaignID == nil || *row.CampaignID != campaignID {
		t.Fatalf("expected campaign-product row for campaign %s, got %v", campaignID, row.CampaignID)
	}
	if row.RowKey == "" {
		t.Fatalf("expected stable campaign-product row key")
	}
	if row.Performance.DataMode != "exact" {
		t.Fatalf("expected exact stats from product_stats, got %q", row.Performance.DataMode)
	}
	if row.Performance.Spend != 2500 || row.Performance.Revenue != revenue || row.Performance.Orders != orders || row.Performance.Atbs != atbs {
		t.Fatalf("unexpected product performance: %+v", row.Performance)
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

func TestBuildQuerySummariesOnlyReturnsStatsBackedNormQueryRows(t *testing.T) {
	svc := &AdsReadService{}
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	campaignID := uuid.New()
	productID := uuid.New()
	oldPhraseID := uuid.New()
	normQueryPhraseID := uuid.New()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	wbProductID := int64(183310308)
	atbs := int64(2)
	orders := int64(1)

	data := &adsWorkspaceData{
		cabinets: map[uuid.UUID]domain.SellerCabinet{
			cabinetID: {ID: cabinetID, WorkspaceID: workspaceID, Name: "WB cabinet"},
		},
		campaigns: []domain.Campaign{
			{
				ID:              campaignID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBCampaignID:    171584733,
				Name:            "Рюкзак Клик",
				Status:          "active",
			},
		},
		products: []domain.Product{
			{
				ID:              productID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBProductID:     wbProductID,
				Title:           "Рюкзак",
			},
		},
		phrases: []domain.Phrase{
			{
				ID:          oldPhraseID,
				WorkspaceID: workspaceID,
				CampaignID:  campaignID,
				Keyword:     "171584733 183310308 Рюкзак",
			},
			{
				ID:          normQueryPhraseID,
				WorkspaceID: workspaceID,
				CampaignID:  campaignID,
				ProductID:   &productID,
				WBProductID: &wbProductID,
				WBNormQuery: "рюкзак женский",
				Keyword:     "рюкзак женский",
			},
		},
		phraseStatsByID: map[uuid.UUID][]domain.PhraseStat{
			normQueryPhraseID: {
				{
					PhraseID:    normQueryPhraseID,
					Date:        now,
					Impressions: 0,
					Clicks:      4,
					Spend:       125,
					Atbs:        &atbs,
					Orders:      &orders,
				},
			},
		},
	}

	rows := svc.buildQuerySummaries(data, now.AddDate(0, 0, -30), now, QuerySummaryFilter{})

	if len(rows) != 1 {
		t.Fatalf("expected only normquery stats-backed phrase row, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ID != normQueryPhraseID {
		t.Fatalf("expected stats-backed normquery phrase %s, got %s", normQueryPhraseID, row.ID)
	}
	if row.ProductID == nil || *row.ProductID != productID || row.WBProductID == nil || *row.WBProductID != wbProductID {
		t.Fatalf("expected campaign-product identity on phrase row: %+v", row)
	}
	if row.Performance.DataMode != "exact" || row.Performance.Clicks != 4 || row.Performance.Atbs != atbs || row.Performance.Orders != orders {
		t.Fatalf("unexpected phrase performance: %+v", row.Performance)
	}
}

func TestAggregateCampaignStatsMarksOutOfRangeStatsUnavailable(t *testing.T) {
	campaignID := uuid.New()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	metrics := aggregateCampaignStats(
		[]domain.CampaignStat{
			{CampaignID: campaignID, Date: now.AddDate(0, 0, -45), Impressions: 1000, Clicks: 50, Spend: 2500},
		},
		now.AddDate(0, 0, -30),
		now,
	)

	if metrics.DataMode != "unavailable" {
		t.Fatalf("expected unavailable data mode for out-of-range campaign stats, got %q", metrics.DataMode)
	}
	if metrics.Spend != 0 || metrics.Impressions != 0 || metrics.Clicks != 0 {
		t.Fatalf("out-of-range stats must not be exposed as current metrics: %+v", metrics)
	}
}

func TestAggregateCampaignStatsKeepsExactZeroRows(t *testing.T) {
	campaignID := uuid.New()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	metrics := aggregateCampaignStats(
		[]domain.CampaignStat{
			{CampaignID: campaignID, Date: now, Impressions: 0, Clicks: 0, Spend: 0},
		},
		now.AddDate(0, 0, -30),
		now,
	)

	if metrics.DataMode != "exact" {
		t.Fatalf("expected exact data mode for a real zero stat row, got %q", metrics.DataMode)
	}
}

func TestBuildAdsDataStatusReportsEmptyPeriod(t *testing.T) {
	cabinetID := uuid.New()
	campaignID := uuid.New()
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	data := &adsWorkspaceData{
		cabinets: map[uuid.UUID]domain.SellerCabinet{
			cabinetID: {ID: cabinetID, WorkspaceID: workspaceID, Name: "WB cabinet"},
		},
		campaigns: []domain.Campaign{
			{
				ID:              campaignID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				Status:          "active",
			},
		},
		phrases: []domain.Phrase{
			{ID: phraseID, WorkspaceID: workspaceID, CampaignID: campaignID, WBNormQuery: "рюкзак", Keyword: "рюкзак"},
		},
		products:            []domain.Product{},
		campaignStatsByID:   map[uuid.UUID][]domain.CampaignStat{campaignID: {{CampaignID: campaignID, Date: now.AddDate(0, 0, -45), Impressions: 100}}},
		phraseStatsByID:     map[uuid.UUID][]domain.PhraseStat{phraseID: {{PhraseID: phraseID, Date: now.AddDate(0, 0, -45), Impressions: 50}}},
		productBusinessByID: map[uuid.UUID][]domain.ProductBusinessSummary{},
	}

	status := buildAdsDataStatus(data, now.AddDate(0, 0, -30), now, &cabinetID)

	if status.State != "empty_period" {
		t.Fatalf("expected empty_period state, got %+v", status)
	}
	if status.HasCurrentStats {
		t.Fatalf("expected no current stats in selected period")
	}
	if status.CampaignsTotal != 1 || status.CampaignsWithStats != 0 {
		t.Fatalf("unexpected campaign coverage: %+v", status)
	}
	if status.QueriesTotal != 1 || status.QueriesWithStats != 0 {
		t.Fatalf("unexpected query coverage: %+v", status)
	}
}

func TestBuildAdsDataStatusReportsReadyWithRealCoverage(t *testing.T) {
	cabinetID := uuid.New()
	campaignID := uuid.New()
	workspaceID := uuid.New()
	phraseID := uuid.New()
	productID := uuid.New()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	data := &adsWorkspaceData{
		cabinets: map[uuid.UUID]domain.SellerCabinet{
			cabinetID: {ID: cabinetID, WorkspaceID: workspaceID, Name: "WB cabinet"},
		},
		campaigns: []domain.Campaign{
			{
				ID:              campaignID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				Status:          "active",
			},
		},
		phrases: []domain.Phrase{
			{ID: phraseID, WorkspaceID: workspaceID, CampaignID: campaignID, WBNormQuery: "рюкзак", Keyword: "рюкзак"},
		},
		products: []domain.Product{
			{ID: productID, WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductID: 123, Title: "Рюкзак"},
		},
		campaignStatsByID: map[uuid.UUID][]domain.CampaignStat{
			campaignID: {{CampaignID: campaignID, Date: now, Impressions: 100, Clicks: 10, Spend: 500}},
		},
		phraseStatsByID: map[uuid.UUID][]domain.PhraseStat{
			phraseID: {{PhraseID: phraseID, Date: now, Impressions: 80, Clicks: 8, Spend: 400}},
		},
		productBusinessByID: map[uuid.UUID][]domain.ProductBusinessSummary{
			productID: {{Date: now, Orders: 2, Sales: 1, OrderedRevenue: 3000, SoldRevenue: 1500}},
		},
	}

	status := buildAdsDataStatus(data, now.AddDate(0, 0, -30), now, &cabinetID)

	if status.State != "ready" {
		t.Fatalf("expected ready state, got %+v", status)
	}
	if !status.HasConnectedCabinet || !status.HasCurrentStats {
		t.Fatalf("expected connected cabinet and current stats: %+v", status)
	}
	if status.CampaignsWithStats != 1 || status.QueriesWithStats != 1 || status.ProductsWithBusinessData != 1 {
		t.Fatalf("unexpected coverage counters: %+v", status)
	}
}

func TestEnrichAdsDataStatusReportsQueuedPhaseRetries(t *testing.T) {
	jobRunID := uuid.New()
	runAt := time.Date(2026, 5, 19, 14, 30, 0, 0, time.UTC)
	status := domain.AdsDataStatus{
		State:          "ready",
		Reason:         "ok",
		FreshnessState: "fresh",
	}

	svc := &AdsReadService{backendVersion: "test-version"}
	svc.enrichAdsDataStatus(&status, runAt.AddDate(0, 0, -30), runAt, &domain.SellerCabinetAutoSyncSummary{
		JobRunID: jobRunID,
		Status:   "completed",
		PhaseRetries: []domain.AdsSyncPhaseRetry{
			{Phase: SyncPhasePhrases, Status: "queued", RunAt: &runAt},
		},
	})

	if status.BackendVersion != "test-version" {
		t.Fatalf("expected backend version to be exposed, got %q", status.BackendVersion)
	}
	if len(status.PhaseRetries) != 1 || status.PhaseRetries[0].Phase != SyncPhasePhrases {
		t.Fatalf("expected queued phrase retry, got %+v", status.PhaseRetries)
	}
	if len(status.Issues) == 0 || status.Issues[0].Stage != "phase_retry_queued" {
		t.Fatalf("expected phase retry issue, got %+v", status.Issues)
	}
}
