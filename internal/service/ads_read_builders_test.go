package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
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

func TestProductFromSqlcMapsReputationEvidence(t *testing.T) {
	product := productFromSqlc(sqlcgen.Product{
		ID:              uuidToPgtype(uuid.New()),
		WorkspaceID:     uuidToPgtype(uuid.New()),
		SellerCabinetID: uuidToPgtype(uuid.New()),
		WbProductID:     123456,
		Title:           "Петля мебельная",
		Rating:          pgtype.Float8{Float64: 4.4, Valid: true},
		ReviewsCount:    pgtype.Int4{Int32: 17, Valid: true},
	})

	if product.Rating == nil || *product.Rating != 4.4 {
		t.Fatalf("expected product rating 4.4, got %+v", product.Rating)
	}
	if product.ReviewsCount == nil || *product.ReviewsCount != 17 {
		t.Fatalf("expected product reviews count 17, got %+v", product.ReviewsCount)
	}
}

func TestBuildProductStockRunoutForecastUsesRealStockAndSales(t *testing.T) {
	capturedAt := time.Date(2026, 5, 28, 9, 0, 0, 0, time.UTC)

	forecast := buildProductStockRunoutForecast(
		&domain.ProductStockEvidence{StockTotal: 12, Source: "product_snapshot", CapturedAt: capturedAt},
		domain.ProductBusinessSummary{Sales: 21, DataMode: "reports"},
		time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
	)

	if forecast == nil {
		t.Fatal("expected stock runout forecast")
	}
	if forecast.State != "runout_soon" {
		t.Fatalf("expected runout_soon forecast, got %+v", forecast)
	}
	if forecast.DaysToEmpty == nil || *forecast.DaysToEmpty != 4 {
		t.Fatalf("expected 4 days to empty from 12 stock / 3 daily sales, got %+v", forecast.DaysToEmpty)
	}
	if forecast.AverageDailySales != 3 || forecast.PeriodDays != 7 {
		t.Fatalf("expected real average sales over 7 days, got %+v", forecast)
	}
	if !strings.Contains(forecast.Reason, "21 продажам") || !strings.Contains(forecast.Source, "product_snapshot") {
		t.Fatalf("expected real evidence reason/source, got %+v", forecast)
	}
}

func TestBuildProductStockRunoutForecastDoesNotInventSalesWhenReportsUnavailable(t *testing.T) {
	forecast := buildProductStockRunoutForecast(
		&domain.ProductStockEvidence{StockTotal: 12, Source: "product_snapshot", CapturedAt: time.Now()},
		domain.ProductBusinessSummary{DataMode: "unavailable"},
		time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
	)

	if forecast == nil {
		t.Fatal("expected truthful unavailable stock runout forecast")
	}
	if forecast.State != "sales_unavailable" {
		t.Fatalf("expected sales_unavailable state, got %+v", forecast)
	}
	if forecast.DaysToEmpty != nil || forecast.AverageDailySales != 0 {
		t.Fatalf("forecast must not invent sales or days to empty: %+v", forecast)
	}
}

func TestProductStockRunoutAttentionUsesForecastEvidence(t *testing.T) {
	daysToEmpty := 4.0

	item, ok := productStockRunoutAttentionItem(domain.ProductAdsSummary{
		ID:    uuid.New(),
		Title: "Петля мебельная",
		StockRunout: &domain.ProductStockRunoutForecast{
			State:             "runout_soon",
			StockTotal:        12,
			AverageDailySales: 3,
			DaysToEmpty:       &daysToEmpty,
			PeriodDays:        7,
		},
	})

	if !ok {
		t.Fatal("expected stock runout attention item")
	}
	if item.Type != "product_stock_runout" || !strings.Contains(item.Description, "12 шт.") || !strings.Contains(item.Description, "3.0 шт./день") {
		t.Fatalf("expected stock forecast evidence in attention item, got %+v", item)
	}
}

func TestBuildCampaignFinanceSummaryUsesRealWBDocuments(t *testing.T) {
	older := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	summary := buildCampaignFinanceSummary([]sqlcgen.WBAdFinanceDocument{
		{
			DocumentType: "upd",
			Amount:       120000,
			DocumentDate: pgtype.Timestamptz{Time: older, Valid: true},
		},
		{
			DocumentType: "payment",
			Amount:       45000,
			DocumentDate: pgtype.Timestamptz{Time: newer, Valid: true},
		},
		{
			DocumentType: "upd",
			Amount:       -5000,
			DocumentDate: pgtype.Timestamptz{Time: newer.Add(-time.Hour), Valid: true},
		},
	})

	if summary == nil {
		t.Fatal("expected campaign finance summary")
	}
	if summary.DocumentsCount != 3 || summary.Amount != 160000 {
		t.Fatalf("expected count and stored WB finance amount sum, got %+v", summary)
	}
	if summary.DocumentTypes["upd"] != 2 || summary.DocumentTypes["payment"] != 1 {
		t.Fatalf("expected document type counts from WB finance rows, got %+v", summary.DocumentTypes)
	}
	if summary.LatestDocumentAt == nil || !summary.LatestDocumentAt.Equal(newer) {
		t.Fatalf("expected latest WB document date, got %+v", summary.LatestDocumentAt)
	}
	if summary.DataMode != "wb_finance" {
		t.Fatalf("expected wb_finance data mode, got %q", summary.DataMode)
	}
}

func TestBuildCampaignFinanceSummaryReturnsNilWithoutDocuments(t *testing.T) {
	if summary := buildCampaignFinanceSummary(nil); summary != nil {
		t.Fatalf("expected no finance summary without WB documents, got %+v", summary)
	}
}

func TestRecentCampaignBidChangeSummariesUseStoredAuditRowsOnly(t *testing.T) {
	campaignID := uuid.New()
	otherCampaignID := uuid.New()
	productID := uuid.New()
	phraseID := uuid.New()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	rows := []sqlcgen.BidChange{
		{
			ID:         uuidToPgtype(uuid.New()),
			CampaignID: uuidToPgtype(campaignID),
			ProductID:  uuidToPgtype(productID),
			PhraseID:   uuidToPgtype(phraseID),
			Placement:  "search",
			OldBid:     300,
			NewBid:     360,
			Reason:     "ДРР ниже цели",
			Source:     domain.BidSourceStrategy,
			WbStatus:   "applied",
			CreatedAt:  pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		},
		{
			ID:         uuidToPgtype(uuid.New()),
			CampaignID: uuidToPgtype(otherCampaignID),
			Placement:  "search",
			OldBid:     100,
			NewBid:     150,
			WbStatus:   "applied",
			CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		},
		{
			ID:         uuidToPgtype(uuid.New()),
			CampaignID: uuidToPgtype(campaignID),
			Placement:  "recommendations",
			OldBid:     420,
			NewBid:     390,
			Reason:     "CPO выше лимита",
			Source:     domain.BidSourceManual,
			WbStatus:   "applied",
			CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		},
	}

	result := recentCampaignBidChangeSummaries(rows, campaignID, 5)

	if len(result) != 2 {
		t.Fatalf("expected 2 campaign changes, got %d: %+v", len(result), result)
	}
	if result[0].Placement != "recommendations" || result[0].OldBid != 420 || result[0].NewBid != 390 {
		t.Fatalf("expected newest campaign change first, got %+v", result[0])
	}
	if !result[0].CanRollback || result[0].RollbackBid == nil || *result[0].RollbackBid != 420 {
		t.Fatalf("expected rollback metadata from stored applied bid change, got %+v", result[0])
	}
	if result[1].ProductID == nil || *result[1].ProductID != productID || result[1].PhraseID == nil || *result[1].PhraseID != phraseID {
		t.Fatalf("expected product/phrase context from stored audit row, got %+v", result[1])
	}
}

func TestCampaignRecommendationSummariesIncludeCampaignPhraseAndProductScopes(t *testing.T) {
	campaignID := uuid.New()
	otherCampaignID := uuid.New()
	productID := uuid.New()
	otherProductID := uuid.New()
	phraseID := uuid.New()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	nextAction := pgtype.Text{String: "Проверить кластер", Valid: true}
	confidence, err := numericFromFloat64(0.82)
	if err != nil {
		t.Fatalf("numeric confidence: %v", err)
	}

	rows := []sqlcgen.Recommendation{
		{
			ID:         uuidToPgtype(uuid.New()),
			CampaignID: uuidToPgtype(campaignID),
			Title:      "Снизить слабые кластеры",
			Type:       domain.RecommendationTypeLowerBid,
			Severity:   domain.SeverityMedium,
			Confidence: confidence,
			NextAction: nextAction,
			Status:     domain.RecommendationStatusActive,
			CreatedAt:  pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		},
		{
			ID:        uuidToPgtype(uuid.New()),
			PhraseID:  uuidToPgtype(phraseID),
			Title:     "Минусовать мусорный запрос",
			Type:      domain.RecommendationTypeAddMinusPhrase,
			Severity:  domain.SeverityHigh,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		},
		{
			ID:        uuidToPgtype(uuid.New()),
			ProductID: uuidToPgtype(productID),
			Title:     "Пополнить остатки",
			Type:      domain.RecommendationTypeStockAlert,
			Severity:  domain.SeverityCritical,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-3 * time.Hour), Valid: true},
		},
		{
			ID:         uuidToPgtype(uuid.New()),
			CampaignID: uuidToPgtype(otherCampaignID),
			ProductID:  uuidToPgtype(otherProductID),
			Title:      "Чужая рекомендация",
			Severity:   domain.SeverityCritical,
			Status:     domain.RecommendationStatusActive,
			CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
		},
	}

	result := campaignRecommendationSummaries(rows, domain.Campaign{ID: campaignID}, []domain.Product{{ID: productID}}, []domain.Phrase{{ID: phraseID}}, 5)

	if len(result) != 3 {
		t.Fatalf("expected 3 scoped recommendations, got %d: %+v", len(result), result)
	}
	if result[0].Scope != "product" || result[0].Severity != domain.SeverityCritical {
		t.Fatalf("expected highest severity product recommendation first, got %+v", result[0])
	}
	if result[1].Scope != "query" || result[1].PhraseID == nil || *result[1].PhraseID != phraseID {
		t.Fatalf("expected phrase-scoped recommendation, got %+v", result[1])
	}
	if result[2].Scope != "campaign" || result[2].NextAction == nil || *result[2].NextAction != nextAction.String || result[2].Confidence != 0.82 {
		t.Fatalf("expected direct campaign recommendation metadata, got %+v", result[2])
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

func TestBuildProductDecisionScoresUseRealEvidenceAndExposeMissingEvidence(t *testing.T) {
	rating := 4.7
	reviewsCount := 28
	scores := buildProductDecisionScores(domain.Product{
		Rating:       &rating,
		ReviewsCount: &reviewsCount,
	}, domain.AdsMetricsSummary{
		DataMode:       "exact",
		Impressions:    1200,
		Clicks:         120,
		Orders:         8,
		Spend:          900,
		Revenue:        9000,
		CTR:            0.1,
		ROAS:           10,
		DRR:            10,
		ConversionRate: 0.066,
		CartRate:       0.18,
		AvgPosition:    5.4,
	}, domain.ProductBusinessSummary{
		Orders:     10,
		Sales:      8,
		BuyoutRate: 0.8,
		DataMode:   "reports",
	}, &domain.ProductStockEvidence{
		StockTotal: 42,
		Source:     "product_snapshot",
		CapturedAt: time.Now(),
	})

	if scores.Advertising.Value == nil || *scores.Advertising.Value < 90 {
		t.Fatalf("expected high advertising score from real ads evidence, got %+v", scores.Advertising)
	}
	if scores.Readiness.Value == nil || *scores.Readiness.Value < 90 {
		t.Fatalf("expected high readiness score from stock/reputation/business evidence, got %+v", scores.Readiness)
	}
	if !stringSliceContains(scores.Readiness.Evidence, "stock") || !stringSliceContains(scores.Readiness.Evidence, "rating") || !stringSliceContains(scores.Readiness.Evidence, "buyout_rate") {
		t.Fatalf("expected readiness score evidence to name real sources, got %+v", scores.Readiness.Evidence)
	}
	if !stringSliceContains(scores.Readiness.MissingEvidence, "unit_economics_margin") || !stringSliceContains(scores.Readiness.MissingEvidence, "market_price") || !stringSliceContains(scores.Readiness.MissingEvidence, "photo_content") {
		t.Fatalf("expected readiness score to keep missing evidence explicit, got %+v", scores.Readiness.MissingEvidence)
	}
	if scores.Growth.Value == nil || !stringSliceContains(scores.Growth.MissingEvidence, "budget_headroom") || !stringSliceContains(scores.Growth.MissingEvidence, "winning_queries") {
		t.Fatalf("expected partial growth score with missing growth evidence, got %+v", scores.Growth)
	}
}

func TestBuildProductDecisionScoresUseRealCardContentEvidence(t *testing.T) {
	rating := 4.6
	reviewsCount := 30
	brand := "Acme"
	category := "Мебельная фурнитура"
	imageURL := "https://cdn.wb.ru/product.jpg"

	scores := buildProductDecisionScores(domain.Product{
		Title:        "Петля мебельная",
		Brand:        &brand,
		Category:     &category,
		ImageURL:     &imageURL,
		Rating:       &rating,
		ReviewsCount: &reviewsCount,
	}, domain.AdsMetricsSummary{
		DataMode: "exact",
		Clicks:   20,
		Orders:   2,
		Revenue:  4000,
		Spend:    500,
		CartRate: 0.08,
	}, domain.ProductBusinessSummary{
		Orders:     2,
		BuyoutRate: 0.7,
		DataMode:   "reports",
	}, &domain.ProductStockEvidence{
		StockTotal: 30,
		Source:     "product_snapshot",
		CapturedAt: time.Now(),
	})

	if !stringSliceContains(scores.Readiness.Evidence, "card_content") {
		t.Fatalf("expected real card content evidence, got %+v", scores.Readiness)
	}
	if stringSliceContains(scores.Readiness.MissingEvidence, "photo_content") {
		t.Fatalf("did not expect missing photo_content when title and image evidence exist, got %+v", scores.Readiness.MissingEvidence)
	}
}

func TestApplyProductEconomicsUsesOnlyCompleteRealInputsForMargin(t *testing.T) {
	price := int64(1000)
	cost := int64(450)
	logistics := int64(100)
	other := int64(50)
	tax := 6.0
	commission := 15.0
	maxAllowedDRR := 30.0

	business := applyProductEconomics(domain.ProductBusinessSummary{
		Sales:   2,
		AdSpend: 95,
	}, domain.Product{
		Price: &price,
	}, domain.ProductEconomics{
		CostPrice:         &cost,
		LogisticsCost:     &logistics,
		OtherCosts:        &other,
		TaxRatePercent:    &tax,
		CommissionPercent: &commission,
		MaxAllowedDRR:     &maxAllowedDRR,
		Source:            "manual_csv",
	})

	if business.MarginBeforeAds == nil || *business.MarginBeforeAds != 190 {
		t.Fatalf("expected margin from complete real economics inputs, got %+v", business.MarginBeforeAds)
	}
	if business.MarginBeforeAdsPercent == nil || *business.MarginBeforeAdsPercent != 19 {
		t.Fatalf("expected margin percent 19, got %+v", business.MarginBeforeAdsPercent)
	}
	if business.MarginBeforeAdsTotal == nil || *business.MarginBeforeAdsTotal != 380 {
		t.Fatalf("expected period margin from real sales count, got %+v", business.MarginBeforeAdsTotal)
	}
	if business.ProfitAfterAds == nil || *business.ProfitAfterAds != 285 {
		t.Fatalf("expected profit after ads from real sales count and ad spend, got %+v", business.ProfitAfterAds)
	}
	if business.MarginalDRR == nil || *business.MarginalDRR != 25 {
		t.Fatalf("expected marginal drr from period margin, got %+v", business.MarginalDRR)
	}
	if business.MaxAllowedDRR == nil || *business.MaxAllowedDRR != 30 || business.EconomicsSource != "manual_csv" || business.EconomicsDataMode != "manual" {
		t.Fatalf("expected copied economics evidence, got %+v", business)
	}

	derived := applyProductEconomics(domain.ProductBusinessSummary{}, domain.Product{
		Price: &price,
	}, domain.ProductEconomics{
		CostPrice:         &cost,
		LogisticsCost:     &logistics,
		OtherCosts:        &other,
		TaxRatePercent:    &tax,
		CommissionPercent: &commission,
		Source:            "manual_csv",
	})
	if derived.MaxAllowedDRR == nil || *derived.MaxAllowedDRR != 19 {
		t.Fatalf("expected max allowed DRR derived from complete real margin inputs, got %+v", derived.MaxAllowedDRR)
	}

	incomplete := applyProductEconomics(domain.ProductBusinessSummary{}, domain.Product{
		Price: &price,
	}, domain.ProductEconomics{
		CostPrice: &cost,
	})
	if incomplete.MarginBeforeAds != nil || incomplete.MarginBeforeAdsPercent != nil || incomplete.MarginBeforeAdsTotal != nil || incomplete.ProfitAfterAds != nil || incomplete.MarginalDRR != nil {
		t.Fatalf("did not expect margin fields when economics inputs are incomplete: %+v", incomplete)
	}
}

func TestApplyProductEconomicsDoesNotInventPeriodProfitWithoutRealSales(t *testing.T) {
	price := int64(1000)
	cost := int64(450)
	logistics := int64(100)
	other := int64(50)
	tax := 6.0
	commission := 15.0

	business := applyProductEconomics(domain.ProductBusinessSummary{
		AdSpend: 95,
	}, domain.Product{
		Price: &price,
	}, domain.ProductEconomics{
		CostPrice:         &cost,
		LogisticsCost:     &logistics,
		OtherCosts:        &other,
		TaxRatePercent:    &tax,
		CommissionPercent: &commission,
		Source:            "manual_csv",
	})

	if business.MarginBeforeAds == nil || *business.MarginBeforeAds != 190 {
		t.Fatalf("expected unit margin from complete economics, got %+v", business.MarginBeforeAds)
	}
	if business.MarginBeforeAdsTotal != nil || business.ProfitAfterAds != nil || business.MarginalDRR != nil {
		t.Fatalf("did not expect period profit or marginal DRR without real sales count: %+v", business)
	}
}

func TestBuildProductDecisionScoresUseImportedUnitEconomicsEvidence(t *testing.T) {
	rating := 4.8
	reviewsCount := 34
	margin := int64(1900)
	maxAllowedDRR := 28.0

	scores := buildProductDecisionScores(domain.Product{
		Rating:       &rating,
		ReviewsCount: &reviewsCount,
	}, domain.AdsMetricsSummary{
		DataMode:       "exact",
		Impressions:    2000,
		Clicks:         180,
		Orders:         14,
		Spend:          1500,
		Revenue:        12000,
		CTR:            0.09,
		ROAS:           8,
		DRR:            12.5,
		ConversionRate: 0.077,
		CartRate:       0.16,
		AvgPosition:    4,
	}, domain.ProductBusinessSummary{
		Orders:          16,
		Sales:           14,
		BuyoutRate:      0.875,
		MarginBeforeAds: &margin,
		MaxAllowedDRR:   &maxAllowedDRR,
		DataMode:        "reports",
	}, &domain.ProductStockEvidence{
		StockTotal: 90,
		Source:     "product_snapshot",
		CapturedAt: time.Now(),
	})

	if !stringSliceContains(scores.Readiness.Evidence, "unit_economics_margin") {
		t.Fatalf("expected readiness to use imported unit economics evidence, got %+v", scores.Readiness)
	}
	if stringSliceContains(scores.Readiness.MissingEvidence, "unit_economics_margin") {
		t.Fatalf("did not expect missing unit economics with margin evidence, got %+v", scores.Readiness.MissingEvidence)
	}
	if !stringSliceContains(scores.Growth.Evidence, "max_allowed_drr") {
		t.Fatalf("expected growth to use safe DRR from imported economics, got %+v", scores.Growth)
	}
	if stringSliceContains(scores.Growth.MissingEvidence, "unit_economics_margin") {
		t.Fatalf("did not expect growth missing unit economics when imported economics exists, got %+v", scores.Growth.MissingEvidence)
	}
}

func TestApplyProductSalesFunnelEvidenceUsesOnlyOverlappingRealPeriods(t *testing.T) {
	productID := uuid.New()
	capturedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	business := applyProductSalesFunnelEvidence(domain.ProductBusinessSummary{}, []sqlcgen.ProductSalesFunnelPeriod{
		{
			ProductID:  uuidToPgtype(productID),
			DateFrom:   pgDate(time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)),
			DateTo:     pgDate(time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)),
			OpenCount:  85,
			CartCount:  17,
			OrderCount: 5,
			Source:     "wb_sales_funnel_products_v3",
			CapturedAt: pgtype.Timestamptz{Time: capturedAt, Valid: true},
		},
		{
			ProductID:  uuidToPgtype(productID),
			DateFrom:   pgDate(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)),
			DateTo:     pgDate(time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)),
			OpenCount:  100,
			CartCount:  99,
			OrderCount: 30,
			Source:     "wb_sales_funnel_products_v3",
			CapturedAt: pgtype.Timestamptz{Time: capturedAt.Add(-24 * time.Hour), Valid: true},
		},
	}, time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC), time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC))

	if business.SalesFunnelOpenCount != 85 || business.SalesFunnelCartCount != 17 || business.SalesFunnelOrderCount != 5 || business.SalesFunnelDataMode != "reports" || business.SalesFunnelSource != "wb_sales_funnel_products_v3" {
		t.Fatalf("expected only overlapping WB sales funnel period evidence, got %+v", business)
	}
	if business.SalesFunnelOpenToCartConversion == nil || *business.SalesFunnelOpenToCartConversion != 20 {
		t.Fatalf("expected real open-to-cart conversion from WB funnel counts, got %+v", business.SalesFunnelOpenToCartConversion)
	}
	if business.SalesFunnelCartToOrderConversion == nil || *business.SalesFunnelCartToOrderConversion <= 29.4 || *business.SalesFunnelCartToOrderConversion >= 29.5 {
		t.Fatalf("expected real cart-to-order conversion from WB funnel counts, got %+v", business.SalesFunnelCartToOrderConversion)
	}
	if business.SalesFunnelCapturedAt == nil || !business.SalesFunnelCapturedAt.Equal(capturedAt) {
		t.Fatalf("expected latest captured_at from real sales funnel row, got %+v", business.SalesFunnelCapturedAt)
	}
}

func TestBuildProductDecisionScoresUseSalesFunnelCartEvidence(t *testing.T) {
	openToCart := 18.0
	cartToOrder := 27.0
	scores := buildProductDecisionScores(domain.Product{}, domain.AdsMetricsSummary{
		DataMode:    "exact",
		Impressions: 1000,
		Clicks:      50,
		Spend:       500,
		CartRate:    0.04,
	}, domain.ProductBusinessSummary{
		SalesFunnelCartCount:             11,
		SalesFunnelOpenToCartConversion:  &openToCart,
		SalesFunnelCartToOrderConversion: &cartToOrder,
		SalesFunnelDataMode:              "reports",
	}, nil)

	if !stringSliceContains(scores.Readiness.Evidence, "sales_funnel_carts") {
		t.Fatalf("expected readiness score to expose sales funnel carts evidence, got %+v", scores.Readiness)
	}
	if !stringSliceContains(scores.Readiness.Evidence, "sales_funnel_open_to_cart") || !stringSliceContains(scores.Readiness.Evidence, "sales_funnel_cart_to_order") {
		t.Fatalf("expected readiness score to expose WB funnel conversion evidence, got %+v", scores.Readiness)
	}
	if stringSliceContains(scores.Readiness.MissingEvidence, "sales_funnel_carts") {
		t.Fatalf("did not expect missing sales funnel carts when WB funnel evidence exists, got %+v", scores.Readiness)
	}
	if stringSliceContains(scores.Readiness.MissingEvidence, "sales_funnel_open_to_cart") || stringSliceContains(scores.Readiness.MissingEvidence, "sales_funnel_cart_to_order") {
		t.Fatalf("did not expect missing WB funnel conversions when real conversion evidence exists, got %+v", scores.Readiness.MissingEvidence)
	}
}

func TestBuildProductDecisionScoresReportsMissingSalesFunnelConversionsWithoutDenominators(t *testing.T) {
	scores := buildProductDecisionScores(domain.Product{}, domain.AdsMetricsSummary{
		DataMode: "exact",
		Clicks:   10,
		Spend:    500,
	}, domain.ProductBusinessSummary{
		SalesFunnelDataMode: "reports",
	}, nil)

	if !stringSliceContains(scores.Readiness.MissingEvidence, "sales_funnel_open_to_cart") || !stringSliceContains(scores.Readiness.MissingEvidence, "sales_funnel_cart_to_order") {
		t.Fatalf("expected missing WB funnel conversions without real denominators, got %+v", scores.Readiness.MissingEvidence)
	}
}

func TestApplyProductCommissionTariffEvidenceExposesRealWBTariffs(t *testing.T) {
	business := applyProductCommissionTariffEvidence(domain.ProductBusinessSummary{}, sqlcgen.WBCommissionTariff{
		SubjectName:     "Петли мебельные",
		KGVPMarketplace: pgtype.Float8{Float64: 15.5, Valid: true},
		KGVPSupplier:    pgtype.Float8{Float64: 12.5, Valid: true},
		KGVPPickup:      pgtype.Float8{Float64: 14.5, Valid: true},
	})

	if business.WBCommissionSubjectName != "Петли мебельные" || business.WBCommissionDataMode != "wb_tariffs" {
		t.Fatalf("expected WB commission tariff evidence, got %+v", business)
	}
	if business.WBCommissionMarketplacePercent == nil || *business.WBCommissionMarketplacePercent != 15.5 {
		t.Fatalf("expected marketplace commission from WB tariff, got %+v", business.WBCommissionMarketplacePercent)
	}
	if business.CommissionPercent != nil || business.MarginBeforeAds != nil {
		t.Fatalf("WB commission tariff candidates must not invent selected commission or margin without seller sales model: %+v", business)
	}
}

func TestBuildProductDecisionScoresUseWBCommissionTariffEvidence(t *testing.T) {
	commission := 15.5
	scores := buildProductDecisionScores(domain.Product{}, domain.AdsMetricsSummary{
		DataMode:    "exact",
		Impressions: 1000,
		Clicks:      50,
		Spend:       500,
		CartRate:    0.04,
	}, domain.ProductBusinessSummary{
		WBCommissionMarketplacePercent: &commission,
		WBCommissionDataMode:           "wb_tariffs",
	}, nil)

	if !stringSliceContains(scores.Readiness.Evidence, "wb_commission_tariffs") {
		t.Fatalf("expected readiness score to expose WB commission tariff evidence, got %+v", scores.Readiness)
	}
	if stringSliceContains(scores.Readiness.MissingEvidence, "wb_commission_tariffs") {
		t.Fatalf("did not expect missing WB commission tariff when real tariff exists, got %+v", scores.Readiness)
	}
}

func TestBuildProductDecisionScoresDoNotInventValuesWithoutEvidence(t *testing.T) {
	scores := buildProductDecisionScores(domain.Product{}, domain.AdsMetricsSummary{
		DataMode: "unavailable",
	}, domain.ProductBusinessSummary{
		DataMode: "unavailable",
	}, nil)

	if scores.Advertising.Value != nil || scores.Advertising.DataMode != "unavailable" || !stringSliceContains(scores.Advertising.MissingEvidence, "ad_stats") {
		t.Fatalf("expected unavailable advertising score without stats evidence, got %+v", scores.Advertising)
	}
	if scores.Readiness.Value != nil || scores.Readiness.DataMode != "unavailable" {
		t.Fatalf("expected unavailable readiness score without product evidence, got %+v", scores.Readiness)
	}
	if scores.Growth.Value != nil || scores.Growth.DataMode != "unavailable" {
		t.Fatalf("expected unavailable growth score without growth evidence, got %+v", scores.Growth)
	}
}

func TestBuildProductDecisionSummaryScaleCandidateRemainsPartialWithMissingEvidence(t *testing.T) {
	scores := domain.ProductDecisionScores{
		Advertising: decisionScorePtr(95, "exact", nil),
		Readiness:   decisionScorePtr(92, "partial", []string{"unit_economics_margin"}),
		Growth:      decisionScorePtr(88, "partial", []string{"budget_headroom", "winning_queries"}),
	}

	decision := buildProductDecisionSummary(scores)
	if decision.Decision != "scale_candidate_partial" || decision.DataMode != "partial" {
		t.Fatalf("expected partial scale candidate, got %+v", decision)
	}
	if !stringSliceContains(decision.MissingEvidence, "unit_economics_margin") || !stringSliceContains(decision.MissingEvidence, "budget_headroom") {
		t.Fatalf("expected missing evidence to stay visible, got %+v", decision.MissingEvidence)
	}
}

func TestBuildProductDecisionSummaryClassifiesReadinessRisk(t *testing.T) {
	decision := buildProductDecisionSummary(domain.ProductDecisionScores{
		Advertising: decisionScorePtr(82, "exact", nil),
		Readiness:   decisionScorePtr(35, "partial", []string{"stock", "rating"}),
		Growth:      decisionScorePtr(30, "partial", []string{"stock"}),
	})

	if decision.Decision != "ads_working_readiness_risk" {
		t.Fatalf("expected readiness risk decision, got %+v", decision)
	}
	if !strings.Contains(decision.Reason, "готовность товара") {
		t.Fatalf("expected readiness reason, got %q", decision.Reason)
	}
}

func TestBuildProductDecisionSummaryDoesNotInventDecisionWithoutScores(t *testing.T) {
	decision := buildProductDecisionSummary(domain.ProductDecisionScores{
		Advertising: domain.DecisionScoreSummary{DataMode: "unavailable", MissingEvidence: []string{"ad_stats"}},
		Readiness:   domain.DecisionScoreSummary{DataMode: "unavailable", MissingEvidence: []string{"stock"}},
		Growth:      domain.DecisionScoreSummary{DataMode: "unavailable", MissingEvidence: []string{"orders_drr"}},
	})

	if decision.Decision != "insufficient_evidence" || decision.DataMode != "unavailable" {
		t.Fatalf("expected insufficient evidence decision, got %+v", decision)
	}
	if !stringSliceContains(decision.MissingEvidence, "ad_stats") || !stringSliceContains(decision.MissingEvidence, "stock") {
		t.Fatalf("expected missing evidence list, got %+v", decision.MissingEvidence)
	}
}

func TestBuildProductSummariesAppliesNoStockStatusFromEvidence(t *testing.T) {
	svc := &AdsReadService{}
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	campaignID := uuid.New()
	productID := uuid.New()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	orders := int64(1)
	revenue := int64(1500)
	atbs := int64(2)

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
				Name:            "Поиск WB",
				Status:          "active",
			},
		},
		products: []domain.Product{
			{
				ID:              productID,
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBProductID:     183310308,
				Title:           "Петля мебельная",
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
					Impressions: 500,
					Clicks:      20,
					Spend:       700,
					Orders:      &orders,
					Revenue:     &revenue,
					Atbs:        &atbs,
				},
			},
		},
		productStockEvidence: map[uuid.UUID]productStockEvidence{
			productID: {
				StockTotal: 0,
				Source:     "product_snapshot",
				CapturedAt: now,
			},
		},
		campaignProductIDs:  map[uuid.UUID][]uuid.UUID{campaignID: {productID}},
		productCampaignIDs:  map[uuid.UUID][]uuid.UUID{productID: {campaignID}},
		productBusinessByID: map[uuid.UUID][]domain.ProductBusinessSummary{},
		phraseStatsByID:     map[uuid.UUID][]domain.PhraseStat{},
		campaignPhrases:     map[uuid.UUID][]domain.Phrase{},
		extensionEvidence:   &workspaceExtensionEvidence{},
	}

	rows := svc.buildProductSummaries(data, now.AddDate(0, 0, -30), now, ProductSummaryFilter{})
	if len(rows) != 1 {
		t.Fatalf("expected product summary, got %d", len(rows))
	}
	if rows[0].HealthStatus != "no_stock" {
		t.Fatalf("expected no_stock status from stock evidence, got %q", rows[0].HealthStatus)
	}
	if rows[0].StockEvidence == nil || rows[0].StockEvidence.StockTotal != 0 || rows[0].StockEvidence.Source != "product_snapshot" {
		t.Fatalf("expected structured stock evidence, got %+v", rows[0].StockEvidence)
	}
	if rows[0].HealthReason == nil || !strings.Contains(*rows[0].HealthReason, "подтверждённый остаток: 0") {
		t.Fatalf("expected stock evidence reason, got %v", rows[0].HealthReason)
	}
}

func TestBuildAttentionItemsReportsProductNoStock(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	productID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           productID,
			Title:        "Петля мебельная",
			HealthStatus: "no_stock",
			HealthReason: stringPtr("Товар рекламируется, но подтверждённый остаток: 0 шт. Источник: product_snapshot."),
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 0,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, nil, nil)

	item, ok := findAttentionItem(items, "product_no_stock")
	if !ok {
		t.Fatalf("expected product no-stock attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != productID.String() {
		t.Fatalf("expected product source id %s, got %+v", productID, item.SourceID)
	}
}

func TestBuildAttentionItemsReportsProductCardIssueType(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	productID := uuid.New()
	reason := "Товар получает показы, но не собирает клики."

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           productID,
			Title:        "Петля мебельная",
			HealthStatus: "card_issue",
			HealthReason: &reason,
		},
	}, nil, nil)

	item, ok := findAttentionItem(items, "product_card_issue")
	if !ok {
		t.Fatalf("expected product card issue attention item, got %+v", items)
	}
	if item.Description != reason || item.Severity != domain.SeverityMedium {
		t.Fatalf("expected card issue evidence and medium severity, got %+v", item)
	}
	if item.SourceID == nil || *item.SourceID != productID.String() {
		t.Fatalf("expected product source id %s, got %+v", productID, item.SourceID)
	}
}

func TestBuildAttentionItemsReportsProductOfferIssueType(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	reason := "Товар получил 8 корзин, но 0 заказов."

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           uuid.New(),
			Title:        "Петля мебельная",
			HealthStatus: "offer_issue",
			HealthReason: &reason,
		},
	}, nil, nil)

	item, ok := findAttentionItem(items, "product_offer_issue")
	if !ok {
		t.Fatalf("expected product offer issue attention item, got %+v", items)
	}
	if item.Description != reason || item.Severity != domain.SeverityMedium {
		t.Fatalf("expected offer issue evidence and medium severity, got %+v", item)
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

func TestBuildAttentionItemsReportsMissingUnitEconomicsForHighSpendProduct(t *testing.T) {
	svc := &AdsReadService{}
	productID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:    productID,
			Title: "Петля мебельная 35 мм",
			Performance: domain.AdsMetricsSummary{
				Spend: 4500,
			},
		},
	}, nil, nil)

	item, ok := findAttentionItem(items, "missing_unit_economics")
	if !ok {
		t.Fatalf("expected missing unit economics attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != productID.String() {
		t.Fatalf("expected product source id %s, got %+v", productID, item.SourceID)
	}
	if !strings.Contains(item.Description, "unit economics") || !strings.Contains(item.Description, "ROAS") {
		t.Fatalf("expected truthful missing-economics description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsDoesNotReportMissingUnitEconomicsWhenConfigured(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:    uuid.New(),
			Title: "Петля мебельная 35 мм",
			Performance: domain.AdsMetricsSummary{
				Spend: 4500,
			},
		},
	}, nil, nil)

	if _, ok := findAttentionItem(items, "missing_unit_economics"); ok {
		t.Fatalf("did not expect missing unit economics attention item when integration is configured: %+v", items)
	}
}

func TestBuildAttentionItemsDoesNotReportMissingUnitEconomicsWhenLocalEconomicsImported(t *testing.T) {
	svc := &AdsReadService{}
	maxAllowedDRR := 32.0

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:    uuid.New(),
			Title: "Петля мебельная 35 мм",
			Performance: domain.AdsMetricsSummary{
				Spend: 4500,
			},
			Business: domain.ProductBusinessSummary{
				MaxAllowedDRR:     &maxAllowedDRR,
				EconomicsDataMode: "manual",
			},
		},
	}, nil, nil)

	if _, ok := findAttentionItem(items, "missing_unit_economics"); ok {
		t.Fatalf("did not expect missing unit economics attention item when local economics exists: %+v", items)
	}
}

func TestBuildAttentionItemsReportsProductScaleCandidateWithStockEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	productID := uuid.New()
	margin := int64(3200)
	marginPercent := 32.0
	maxAllowedDRR := 28.0

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           productID,
			Title:        "Петля с доводчиком",
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
			Business: domain.ProductBusinessSummary{
				MarginBeforeAds:        &margin,
				MarginBeforeAdsPercent: &marginPercent,
				MaxAllowedDRR:          &maxAllowedDRR,
				EconomicsDataMode:      "manual",
			},
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 42,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, nil, nil)

	item, ok := findAttentionItem(items, "product_scale_candidate")
	if !ok {
		t.Fatalf("expected product scale candidate attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != productID.String() {
		t.Fatalf("expected product source id %s, got %+v", productID, item.SourceID)
	}
	if !strings.Contains(item.Description, "4 заказов") || !strings.Contains(item.Description, "остаток: 42 шт") || !strings.Contains(item.Description, "маржа до рекламы 3200 ₽") || !strings.Contains(item.Description, "максимальный допустимый ДРР 28.0%") {
		t.Fatalf("expected real scale evidence with imported economics, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsProductScaleCandidateWithoutProductEconomicsEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           uuid.New(),
			Title:        "Петля с доводчиком",
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 42,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, nil, nil)

	if _, ok := findAttentionItem(items, "product_scale_candidate"); ok {
		t.Fatalf("did not expect scale candidate without product-level economics evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsProductScaleCandidateWhenDRRExceedsProductEconomicsLimit(t *testing.T) {
	svc := &AdsReadService{}
	maxAllowedDRR := 8.0

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           uuid.New(),
			Title:        "Петля с доводчиком",
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
			Business: domain.ProductBusinessSummary{
				MaxAllowedDRR:     &maxAllowedDRR,
				EconomicsDataMode: "manual",
			},
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 42,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, nil, nil)

	if _, ok := findAttentionItem(items, "product_scale_candidate"); ok {
		t.Fatalf("did not expect scale candidate when DRR exceeds product economics limit: %+v", items)
	}
}

func TestBuildAttentionItemsReportsProductReputationGuardrailWithLowRatingEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	productID := uuid.New()
	rating := 3.9
	reviewsCount := 42

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           productID,
			Title:        "Петля с доводчиком",
			Rating:       &rating,
			ReviewsCount: &reviewsCount,
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 42,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, nil, nil)

	item, ok := findAttentionItem(items, "product_reputation_guardrail")
	if !ok {
		t.Fatalf("expected product reputation guardrail attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityHigh || !strings.Contains(item.Description, "рейтинг 3.9") || !strings.Contains(item.Title, "мягко") {
		t.Fatalf("expected real low-rating guardrail evidence, got %+v", item)
	}
	if _, ok := findAttentionItem(items, "product_scale_candidate"); ok {
		t.Fatalf("did not expect product scale candidate with weak reputation guardrail: %+v", items)
	}
}

func TestBuildAttentionItemsReportsProductReputationGuardrailWithLowReviewsEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	reviewsCount := 3

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           uuid.New(),
			Title:        "Петля с доводчиком",
			ReviewsCount: &reviewsCount,
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
		},
	}, nil, nil)

	item, ok := findAttentionItem(items, "product_reputation_guardrail")
	if !ok {
		t.Fatalf("expected product reputation guardrail attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityMedium || !strings.Contains(item.Description, "3 отзывов") {
		t.Fatalf("expected real low-reviews guardrail evidence, got %+v", item)
	}
}

func TestBuildAttentionItemsSkipsProductScaleCandidateWithoutStockEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           uuid.New(),
			Title:        "Петля с доводчиком",
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
		},
	}, nil, nil)

	if _, ok := findAttentionItem(items, "product_scale_candidate"); ok {
		t.Fatalf("did not expect product scale candidate without stock evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsProductReputationGuardrailWithoutEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	maxAllowedDRR := 28.0

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           uuid.New(),
			Title:        "Петля с доводчиком",
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
			Business: domain.ProductBusinessSummary{
				MaxAllowedDRR:     &maxAllowedDRR,
				EconomicsDataMode: "manual",
			},
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 42,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, nil, nil)

	if _, ok := findAttentionItem(items, "product_reputation_guardrail"); ok {
		t.Fatalf("did not expect reputation guardrail without rating or reviews evidence: %+v", items)
	}
	if _, ok := findAttentionItem(items, "product_scale_candidate"); !ok {
		t.Fatalf("expected scale candidate to remain available without reputation evidence, got %+v", items)
	}
}

func TestBuildAttentionItemsSkipsProductScaleCandidateWhenEconomicsNotConfigured(t *testing.T) {
	svc := &AdsReadService{}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:           uuid.New(),
			Title:        "Петля с доводчиком",
			HealthStatus: "growth_candidate",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   4,
				Revenue:  12000,
				DRR:      10,
			},
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 42,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, nil, nil)

	if _, ok := findAttentionItem(items, "product_scale_candidate"); ok {
		t.Fatalf("did not expect product scale candidate when unit economics checks are not configured: %+v", items)
	}
}

func TestBuildAttentionItemsReportsLowCampaignBudgetFromRealBudgetSnapshot(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	capturedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			LatestBudget: &domain.CampaignBudgetSummary{
				Total:      75000,
				CapturedAt: capturedAt,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_low_budget")
	if !ok {
		t.Fatalf("expected low campaign budget attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "750 ₽") {
		t.Fatalf("expected ruble budget in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsEmptyCampaignBudgetFromRealBudgetSnapshot(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	capturedAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			LatestBudget: &domain.CampaignBudgetSummary{
				Total:      0,
				CapturedAt: capturedAt,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_budget_empty")
	if !ok {
		t.Fatalf("expected empty campaign budget attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "0 ₽") || !strings.Contains(item.Description, "2026-05-27 12:00") {
		t.Fatalf("expected real zero budget snapshot in description, got %q", item.Description)
	}
	if _, ok := findAttentionItem(items, "campaign_low_budget"); ok {
		t.Fatalf("did not expect duplicate low-budget attention for empty budget: %+v", items)
	}
}

func TestBuildAttentionItemsReportsRejectedCampaignStatus(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "rejected",
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_status_attention")
	if !ok {
		t.Fatalf("expected campaign status attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity for rejected campaign, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "rejected") || !strings.Contains(item.Description, "модерацию") {
		t.Fatalf("expected real rejected status in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsStoppedCampaignStatus(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "stopped",
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_status_attention")
	if !ok {
		t.Fatalf("expected campaign status attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityHigh {
		t.Fatalf("expected high severity for stopped campaign, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "stopped") {
		t.Fatalf("expected real stopped status in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsPausedCampaignStatus(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     uuid.New(),
			Name:   "Поиск WB",
			Status: "paused",
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_status_attention"); ok {
		t.Fatalf("did not expect campaign status attention for paused campaign: %+v", items)
	}
}

func TestBuildAttentionItemsReportsCampaignCostSpike(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			PeriodCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					Clicks:      20,
					Impressions: 3000,
					CPC:         120,
					CPM:         800,
				},
				Previous: domain.AdsMetricsSummary{
					Clicks:      20,
					Impressions: 3000,
					CPC:         60,
					CPM:         400,
				},
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_cost_spike")
	if !ok {
		t.Fatalf("expected campaign cost spike attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity for doubled CPC/CPM, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "CPC вырос с 60.00 ₽ до 120.00 ₽") || !strings.Contains(item.Description, "CPM вырос с 400.00 ₽ до 800.00 ₽") {
		t.Fatalf("expected real CPC/CPM evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsCampaignCostSpikeWithoutPreviousEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     uuid.New(),
			Name:   "Поиск WB",
			Status: "active",
			PeriodCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					Clicks:      20,
					Impressions: 3000,
					CPC:         120,
					CPM:         800,
				},
			},
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_cost_spike"); ok {
		t.Fatalf("did not expect cost spike without previous CPC/CPM evidence: %+v", items)
	}
}

func TestBuildAttentionItemsReportsCampaignDRROverSafeThreshold(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			Performance: domain.AdsMetricsSummary{
				Spend:   4200,
				Orders:  3,
				Revenue: 8000,
				DRR:     52.5,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_drr_over_safe_threshold")
	if !ok {
		t.Fatalf("expected campaign DRR attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity for DRR >= 50, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "ДРР 52.5%") || !strings.Contains(item.Description, "расход 4200 ₽") || !strings.Contains(item.Description, "выручка 8000 ₽") {
		t.Fatalf("expected real campaign DRR evidence in description, got %q", item.Description)
	}
	if strings.Contains(item.Description, "лимит") {
		t.Fatalf("description must not claim a configured user limit without real target evidence: %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsCampaignDRROverConfiguredLimit(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	strategyID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{
		campaignDRRLimits: map[uuid.UUID]campaignDRRLimit{
			campaignID: {
				StrategyID:   strategyID,
				StrategyName: "Антислив WB",
				Limit:        25,
				Source:       "max_acos",
			},
		},
	}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    4200,
				Orders:   3,
				Revenue:  12000,
				DRR:      35,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_drr_over_configured_limit")
	if !ok {
		t.Fatalf("expected configured DRR limit attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityHigh {
		t.Fatalf("expected high severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "ДРР 35.0%") || !strings.Contains(item.Description, "лимита 25.0%") || !strings.Contains(item.Description, "Антислив WB") {
		t.Fatalf("expected real configured limit evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsConfiguredDRRLimitWithoutStrategyEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    4200,
				Orders:   3,
				Revenue:  12000,
				DRR:      35,
			},
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_drr_over_configured_limit"); ok {
		t.Fatalf("did not expect configured DRR limit attention without strategy evidence: %+v", items)
	}
}

func TestCampaignDRRLimitsFromStrategiesUsesStrictestCampaignLimit(t *testing.T) {
	campaignID := uuid.New()
	antiSlivID := uuid.New()
	acosID := uuid.New()

	limits := campaignDRRLimitsFromStrategies([]sqlcgen.Strategy{
		{
			ID:       uuidToPgtype(antiSlivID),
			Name:     "Антислив WB",
			Type:     domain.StrategyTypeAntiSliv,
			Params:   []byte(`{"max_acos":30}`),
			IsActive: true,
		},
		{
			ID:       uuidToPgtype(acosID),
			Name:     "ACoS контроль",
			Type:     domain.StrategyTypeACoS,
			Params:   []byte(`{"target_acos":22}`),
			IsActive: true,
		},
	}, []sqlcgen.StrategyBinding{
		{StrategyID: uuidToPgtype(antiSlivID), CampaignID: uuidToPgtype(campaignID)},
		{StrategyID: uuidToPgtype(acosID), CampaignID: uuidToPgtype(campaignID)},
	})

	limit, ok := limits[campaignID]
	if !ok {
		t.Fatalf("expected campaign DRR limit")
	}
	if limit.Limit != 22 || limit.Source != "target_acos" || limit.StrategyName != "ACoS контроль" {
		t.Fatalf("expected strictest configured limit from ACoS strategy, got %+v", limit)
	}
}

func TestBuildAttentionItemsSkipsCampaignDRRWarningWithoutRevenueEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     uuid.New(),
			Name:   "Поиск WB",
			Status: "active",
			Performance: domain.AdsMetricsSummary{
				Spend:  4200,
				Orders: 3,
				DRR:    52.5,
			},
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_drr_over_safe_threshold"); ok {
		t.Fatalf("did not expect campaign DRR attention without revenue evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsCampaignDRRWarningBelowSafeThreshold(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     uuid.New(),
			Name:   "Поиск WB",
			Status: "active",
			Performance: domain.AdsMetricsSummary{
				Spend:   3000,
				Orders:  4,
				Revenue: 10000,
				DRR:     30,
			},
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_drr_over_safe_threshold"); ok {
		t.Fatalf("did not expect campaign DRR attention below safe threshold: %+v", items)
	}
}

func TestBuildAttentionItemsReportsCampaignNoStats(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			Performance: domain.AdsMetricsSummary{
				DataMode: "unavailable",
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_no_stats")
	if !ok {
		t.Fatalf("expected campaign no-stats attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "нет подтверждённых строк рекламной статистики") || strings.Contains(item.Description, "0 ₽") {
		t.Fatalf("expected missing-stats description without fabricated zero metrics, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsCampaignNoStatsWhenRealZeroStatsExist(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     uuid.New(),
			Name:   "Поиск WB",
			Status: "active",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
			},
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_no_stats"); ok {
		t.Fatalf("did not expect no-stats attention when a real zero stat row exists: %+v", items)
	}
}

func TestBuildAttentionItemsReportsCampaignLowCTR(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:           campaignID,
			Name:         "Поиск WB",
			Status:       "active",
			HealthStatus: "low_ctr",
			Performance: domain.AdsMetricsSummary{
				DataMode:    "exact",
				Impressions: 6000,
				Clicks:      0,
				CTR:         0,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_low_ctr")
	if !ok {
		t.Fatalf("expected campaign low CTR attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityHigh || !strings.Contains(item.Description, "6000 показов") || !strings.Contains(item.Description, "0 кликов") {
		t.Fatalf("expected real low CTR evidence, got %+v", item)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
}

func TestBuildAttentionItemsSkipsCampaignLowCTRWithoutStatsEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:           uuid.New(),
			Name:         "Поиск WB",
			Status:       "active",
			HealthStatus: "low_ctr",
			Performance: domain.AdsMetricsSummary{
				DataMode: "unavailable",
			},
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_low_ctr"); ok {
		t.Fatalf("did not expect low CTR attention without real stats evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsLowBudgetWithoutRealBudgetSnapshot(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     uuid.New(),
			Name:   "Поиск WB",
			Status: "active",
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_low_budget"); ok {
		t.Fatalf("did not expect low budget attention without latest budget evidence: %+v", items)
	}
}

func TestBuildCampaignBudgetPaceMarksOverPaceFromRealSpend(t *testing.T) {
	dailyBudget := int64(3000)

	pace := buildCampaignBudgetPace(&dailyBudget, 7200, 0, mustDate("2026-05-01"), mustDate("2026-05-02"), mustDate("2026-05-28"))

	if pace == nil {
		t.Fatal("expected budget pace")
	}
	if pace.State != "over_pace" {
		t.Fatalf("expected over_pace, got %q", pace.State)
	}
	if pace.PeriodDays != 2 || pace.PlannedSpend != 6000 || pace.ActualSpend != 7200 {
		t.Fatalf("unexpected pace values: %+v", pace)
	}
	if pace.WeeklyBudget != 21000 || pace.MonthlyBudget != 90000 {
		t.Fatalf("expected derived weekly/monthly budget from real daily budget, got %+v", pace)
	}
	if pace.UtilizationPercent != 120 {
		t.Fatalf("expected 120%% utilization, got %v", pace.UtilizationPercent)
	}
}

func TestBuildCampaignBudgetPaceProjectsTodaySpendFromRealTodayStats(t *testing.T) {
	dailyBudget := int64(3000)
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	pace := buildCampaignBudgetPace(&dailyBudget, 2800, 2800, mustDate("2026-05-28"), mustDate("2026-05-28"), now)

	if pace == nil {
		t.Fatal("expected budget pace")
	}
	if pace.State != "over_pace" {
		t.Fatalf("expected projected over_pace, got %+v", pace)
	}
	if pace.ProjectedTodaySpend == nil || *pace.ProjectedTodaySpend != 5600 {
		t.Fatalf("expected projected spend 5600 from 2800 by 12:00, got %+v", pace.ProjectedTodaySpend)
	}
	if pace.ProjectedTodayUtilizationPercent == nil || *pace.ProjectedTodayUtilizationPercent < 186 || *pace.ProjectedTodayUtilizationPercent > 187 {
		t.Fatalf("expected projected utilization around 186.7%%, got %+v", pace.ProjectedTodayUtilizationPercent)
	}
	if !strings.Contains(pace.Reason, "прогноз к концу дня") {
		t.Fatalf("expected forecast reason, got %q", pace.Reason)
	}
}

func TestBuildCampaignBudgetPaceSkipsProjectionWithoutTodayEvidence(t *testing.T) {
	dailyBudget := int64(3000)
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	pace := buildCampaignBudgetPace(&dailyBudget, 900, 0, mustDate("2026-05-28"), mustDate("2026-05-28"), now)

	if pace == nil {
		t.Fatal("expected budget pace")
	}
	if pace.ProjectedTodaySpend != nil || pace.ProjectedTodayUtilizationPercent != nil {
		t.Fatalf("did not expect projection without real today spend evidence, got %+v", pace)
	}
}

func TestBuildCampaignBudgetPaceRequiresRealDailyBudget(t *testing.T) {
	if pace := buildCampaignBudgetPace(nil, 7200, 7200, mustDate("2026-05-01"), mustDate("2026-05-02"), mustDate("2026-05-28")); pace != nil {
		t.Fatalf("did not expect pace without daily budget, got %+v", pace)
	}
}

func TestBuildAttentionItemsReportsCampaignBudgetOverPace(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	projectedSpend := int64(9600)
	projectedUtilization := float64(320)

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			BudgetPace: &domain.CampaignBudgetPaceSummary{
				State:                            "over_pace",
				PlannedSpend:                     6000,
				ActualSpend:                      7200,
				UtilizationPercent:               120,
				ProjectedTodaySpend:              &projectedSpend,
				ProjectedTodayUtilizationPercent: &projectedUtilization,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_budget_over_pace")
	if !ok {
		t.Fatalf("expected budget over-pace attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "7200 ₽") || !strings.Contains(item.Description, "6000 ₽") {
		t.Fatalf("expected real spend and planned spend in description, got %q", item.Description)
	}
	if !strings.Contains(item.Description, "Прогноз на сегодня: 9600 ₽") || !strings.Contains(item.Description, "320%") {
		t.Fatalf("expected projected daily pace in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsCampaignBudgetUnderPaceGrowth(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	productID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:         productID,
			CampaignID: &campaignID,
			Title:      "Петля с доводчиком",
			Decision: domain.ProductDecisionSummary{
				Decision: "scale_candidate_partial",
			},
			StockEvidence: &domain.ProductStockEvidence{
				StockTotal: 42,
				Source:     "product_snapshot",
				CapturedAt: time.Now(),
			},
		},
	}, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			BudgetPace: &domain.CampaignBudgetPaceSummary{
				State:              "under_pace",
				PlannedSpend:       3000,
				ActualSpend:        900,
				UtilizationPercent: 30,
			},
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    900,
				Orders:   5,
				Revenue:  12000,
				DRR:      7.5,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_budget_under_pace_growth")
	if !ok {
		t.Fatalf("expected under-pace growth attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "900 ₽") || !strings.Contains(item.Description, "3000 ₽") || !strings.Contains(item.Description, "ДРР 7.5%") || !strings.Contains(item.Description, "подтверждённым остатком: 1") {
		t.Fatalf("expected real under-pace growth evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsCampaignBudgetUnderPaceGrowthWithoutProductReadiness(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, []domain.ProductAdsSummary{
		{
			ID:         uuid.New(),
			CampaignID: &campaignID,
			Decision: domain.ProductDecisionSummary{
				Decision: "scale_candidate_partial",
			},
		},
	}, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			BudgetPace: &domain.CampaignBudgetPaceSummary{
				State:              "under_pace",
				PlannedSpend:       3000,
				ActualSpend:        900,
				UtilizationPercent: 30,
			},
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    900,
				Orders:   5,
				Revenue:  12000,
				DRR:      7.5,
			},
		},
	}, nil)

	if _, ok := findAttentionItem(items, "campaign_budget_under_pace_growth"); ok {
		t.Fatalf("did not expect under-pace growth attention without real product stock evidence: %+v", items)
	}
}

func TestBuildCampaignBudgetRunoutMarksEndingSoonFromFreshBudgetSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	budget := &domain.CampaignBudgetSummary{
		Total:      25000,
		CapturedAt: now.Add(-15 * time.Minute),
	}

	runout := buildCampaignBudgetRunout(budget, 1200, now)

	if runout == nil {
		t.Fatal("expected budget runout forecast")
	}
	if runout.State != "will_end_soon" {
		t.Fatalf("expected will_end_soon state, got %q", runout.State)
	}
	if runout.RemainingBudget != 250 || runout.SpendToday != 1200 {
		t.Fatalf("unexpected runout values: %+v", runout)
	}
	if runout.HoursToEmpty != 2.5 {
		t.Fatalf("expected 2.5 hours to empty, got %v", runout.HoursToEmpty)
	}
}

func TestBuildCampaignBudgetRunoutRequiresFreshBudgetSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	budget := &domain.CampaignBudgetSummary{
		Total:      25000,
		CapturedAt: now.AddDate(0, 0, -1),
	}

	if runout := buildCampaignBudgetRunout(budget, 1200, now); runout != nil {
		t.Fatalf("did not expect budget runout forecast from stale budget snapshot: %+v", runout)
	}
}

func TestBuildAttentionItemsReportsCampaignBudgetRunoutSoon(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	capturedAt := time.Date(2026, 5, 27, 11, 45, 0, 0, time.UTC)

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			BudgetRunout: &domain.CampaignBudgetRunoutSummary{
				State:           "will_end_soon",
				RemainingBudget: 250,
				SpendToday:      1200,
				HoursToEmpty:    2.5,
				CapturedAt:      capturedAt,
			},
		},
	}, nil)

	item, ok := findAttentionItem(items, "campaign_budget_runout_soon")
	if !ok {
		t.Fatalf("expected budget runout attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "250 ₽") || !strings.Contains(item.Description, "1200 ₽") || !strings.Contains(item.Description, "2.5 ч") {
		t.Fatalf("expected real budget forecast evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsWBAPIRateLimitFromLastSync(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	jobRunID := uuid.New()
	nextAllowedAt := time.Date(2026, 5, 27, 13, 0, 0, 0, time.UTC)

	items := svc.buildAttentionItems(&adsWorkspaceData{
		lastAutoSync: &domain.SellerCabinetAutoSyncSummary{
			JobRunID:          jobRunID,
			ResultState:       "partial",
			RateLimited:       true,
			RateLimitEndpoint: "adv/v2/fullstats",
			NextAllowedAt:     &nextAllowedAt,
		},
	}, nil, nil, nil)

	item, ok := findAttentionItem(items, "wb_api_rate_limited")
	if !ok {
		t.Fatalf("expected WB API rate limit attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != jobRunID.String() {
		t.Fatalf("expected job run source id %s, got %+v", jobRunID, item.SourceID)
	}
	if !strings.Contains(item.Description, "adv/v2/fullstats") || !strings.Contains(item.Description, "2026-05-27 13:00") {
		t.Fatalf("expected real endpoint and retry window in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsWBAPIErrorsFromLastSync(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	jobRunID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{
		lastAutoSync: &domain.SellerCabinetAutoSyncSummary{
			JobRunID:    jobRunID,
			ResultState: "partial",
			WBErrors:    3,
		},
	}, nil, nil, nil)

	item, ok := findAttentionItem(items, "wb_api_errors")
	if !ok {
		t.Fatalf("expected WB API errors attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != jobRunID.String() {
		t.Fatalf("expected job run source id %s, got %+v", jobRunID, item.SourceID)
	}
	if !strings.Contains(item.Description, "3 WB API ошибок") {
		t.Fatalf("expected real WB error count in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsWBAPIAlertWithoutLastSyncEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{
		lastAutoSync: &domain.SellerCabinetAutoSyncSummary{
			JobRunID:    uuid.New(),
			ResultState: "success",
		},
	}, nil, nil, nil)

	if _, ok := findAttentionItem(items, "wb_api_rate_limited"); ok {
		t.Fatalf("did not expect WB API rate limit attention without rate limit evidence: %+v", items)
	}
	if _, ok := findAttentionItem(items, "wb_api_errors"); ok {
		t.Fatalf("did not expect WB API errors attention without WB error evidence: %+v", items)
	}
}

func TestBuildAttentionItemsReportsCampaignImprovedAfterBidChange(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	changeID := uuid.New()
	dateFrom := mustDate("2026-05-20")
	dateTo := mustDate("2026-05-27")
	changedAt := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)

	items := svc.buildAttentionItems(&adsWorkspaceData{
		bidChanges: []sqlcgen.BidChange{
			{
				ID:         uuidToPgtype(changeID),
				CampaignID: uuidToPgtype(campaignID),
				Placement:  "search",
				OldBid:     300,
				NewBid:     345,
				WbStatus:   "applied",
				CreatedAt:  pgtype.Timestamptz{Time: changedAt, Valid: true},
			},
		},
	}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			PeriodCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					DataMode: "exact",
					Orders:   7,
					Revenue:  21000,
					Spend:    2100,
					ROAS:     10,
					DRR:      10,
				},
				Previous: domain.AdsMetricsSummary{
					DataMode: "exact",
					Orders:   3,
					Revenue:  6000,
					Spend:    1800,
					ROAS:     3.3,
					DRR:      30,
				},
				Trend: "improving",
			},
		},
	}, nil, attentionPeriod{dateFrom: dateFrom, dateTo: dateTo})

	item, ok := findAttentionItem(items, "campaign_bid_change_improved")
	if !ok {
		t.Fatalf("expected campaign bid improvement attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityMedium {
		t.Fatalf("expected medium severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "300 ₽ → 345 ₽") || !strings.Contains(item.Description, "заказы 3 → 7") || !strings.Contains(item.Description, "ROAS 3.3 → 10.0") {
		t.Fatalf("expected real bid change and performance evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsCampaignRegressedAfterBidChange(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	dateFrom := mustDate("2026-05-20")
	dateTo := mustDate("2026-05-27")
	changedAt := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)

	items := svc.buildAttentionItems(&adsWorkspaceData{
		bidChanges: []sqlcgen.BidChange{
			{
				CampaignID: uuidToPgtype(campaignID),
				Placement:  "search",
				OldBid:     300,
				NewBid:     345,
				WbStatus:   "applied",
				CreatedAt:  pgtype.Timestamptz{Time: changedAt, Valid: true},
			},
		},
	}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			PeriodCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					DataMode: "exact",
					Orders:   2,
					Revenue:  4000,
					Spend:    1800,
					ROAS:     2.2,
					DRR:      45,
				},
				Previous: domain.AdsMetricsSummary{
					DataMode: "exact",
					Orders:   6,
					Revenue:  18000,
					Spend:    2700,
					ROAS:     6.7,
					DRR:      15,
				},
				Trend: "declining",
			},
		},
	}, nil, attentionPeriod{dateFrom: dateFrom, dateTo: dateTo})

	item, ok := findAttentionItem(items, "campaign_bid_change_regressed")
	if !ok {
		t.Fatalf("expected campaign bid regression attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity from DRR regression, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != campaignID.String() {
		t.Fatalf("expected campaign source id %s, got %+v", campaignID, item.SourceID)
	}
	if !strings.Contains(item.Description, "300 ₽ → 345 ₽") || !strings.Contains(item.Description, "заказы 6 → 2") || !strings.Contains(item.Description, "ROAS 6.7 → 2.2") || !strings.Contains(item.Description, "откат к 300 ₽") {
		t.Fatalf("expected real bid change regression evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsBidChangeRegressionWithoutPreviousEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	dateFrom := mustDate("2026-05-20")
	dateTo := mustDate("2026-05-27")

	items := svc.buildAttentionItems(&adsWorkspaceData{
		bidChanges: []sqlcgen.BidChange{
			{
				CampaignID: uuidToPgtype(campaignID),
				Placement:  "search",
				OldBid:     300,
				NewBid:     345,
				WbStatus:   "applied",
				CreatedAt:  pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC), Valid: true},
			},
		},
	}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			PeriodCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					DataMode: "exact",
					Orders:   2,
					Revenue:  4000,
					ROAS:     2.2,
				},
				Previous: domain.AdsMetricsSummary{
					DataMode: "unavailable",
				},
				Trend: "declining",
			},
		},
	}, nil, attentionPeriod{dateFrom: dateFrom, dateTo: dateTo})

	if _, ok := findAttentionItem(items, "campaign_bid_change_regressed"); ok {
		t.Fatalf("did not expect bid regression attention without previous-period evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsBidChangeImprovementWithoutPreviousEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	dateFrom := mustDate("2026-05-20")
	dateTo := mustDate("2026-05-27")

	items := svc.buildAttentionItems(&adsWorkspaceData{
		bidChanges: []sqlcgen.BidChange{
			{
				CampaignID: uuidToPgtype(campaignID),
				Placement:  "search",
				OldBid:     300,
				NewBid:     345,
				WbStatus:   "applied",
				CreatedAt:  pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC), Valid: true},
			},
		},
	}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			PeriodCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					DataMode: "exact",
					Orders:   7,
					Revenue:  21000,
					ROAS:     10,
				},
				Previous: domain.AdsMetricsSummary{
					DataMode: "unavailable",
				},
				Trend: "improving",
			},
		},
	}, nil, attentionPeriod{dateFrom: dateFrom, dateTo: dateTo})

	if _, ok := findAttentionItem(items, "campaign_bid_change_improved"); ok {
		t.Fatalf("did not expect bid improvement attention without previous-period evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsBidChangeImprovementWithoutAppliedChange(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, []domain.CampaignPerformanceSummary{
		{
			ID:     campaignID,
			Name:   "Поиск WB",
			Status: "active",
			PeriodCompare: &domain.AdsPeriodCompare{
				Current:  domain.AdsMetricsSummary{DataMode: "exact", Orders: 7, Revenue: 21000, ROAS: 10},
				Previous: domain.AdsMetricsSummary{DataMode: "exact", Orders: 3, Revenue: 6000, ROAS: 3.3},
				Trend:    "improving",
			},
		},
	}, nil, attentionPeriod{dateFrom: mustDate("2026-05-20"), dateTo: mustDate("2026-05-27")})

	if _, ok := findAttentionItem(items, "campaign_bid_change_improved"); ok {
		t.Fatalf("did not expect bid improvement attention without applied bid change evidence: %+v", items)
	}
}

func TestBuildAttentionItemsReportsOverdueActiveRecommendations(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	recommendationID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{
		activeRecommendations: []sqlcgen.Recommendation{
			{
				ID:        uuidToPgtype(recommendationID),
				Title:     "Кластер тратит бюджет без корзин и заказов",
				Status:    domain.RecommendationStatusActive,
				CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-72 * time.Hour), Valid: true},
			},
			{
				ID:        uuidToPgtype(uuid.New()),
				Title:     "Свежая рекомендация",
				Status:    domain.RecommendationStatusActive,
				CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Hour), Valid: true},
			},
		},
	}, nil, nil, nil)

	item, ok := findAttentionItem(items, "overdue_recommendations")
	if !ok {
		t.Fatalf("expected overdue recommendations attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != recommendationID.String() {
		t.Fatalf("expected oldest recommendation source id %s, got %+v", recommendationID, item.SourceID)
	}
	if !strings.Contains(item.Description, "старше 48 часов") {
		t.Fatalf("expected overdue age in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsFreshRecommendations(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{
		activeRecommendations: []sqlcgen.Recommendation{
			{
				ID:        uuidToPgtype(uuid.New()),
				Title:     "Свежая рекомендация",
				Status:    domain.RecommendationStatusActive,
				CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Hour), Valid: true},
			},
		},
	}, nil, nil, nil)

	if _, ok := findAttentionItem(items, "overdue_recommendations"); ok {
		t.Fatalf("did not expect overdue recommendations attention for fresh active recommendation: %+v", items)
	}
}

func TestCountDecisionQueueBucketsCombinesAttentionAndActiveRecommendations(t *testing.T) {
	buckets := countDecisionQueueBuckets(
		[]domain.AttentionItem{
			{Type: "campaign_spend_without_orders"},
			{Type: "product_card_issue"},
			{Type: "query_seo_idea"},
			{Type: "wb_api_errors"},
		},
		[]sqlcgen.Recommendation{
			{Type: domain.RecommendationTypeHighSpendLowOrders, Status: domain.RecommendationStatusActive},
			{Type: domain.RecommendationTypeRaiseBid, Status: domain.RecommendationStatusActive},
			{Type: domain.RecommendationTypeStockAlert, Status: domain.RecommendationStatusCompleted},
		},
	)

	if buckets["losses"] != 2 {
		t.Fatalf("expected 2 loss-control items from attention+active recommendation, got %+v", buckets)
	}
	if buckets["growth"] != 2 {
		t.Fatalf("expected 2 growth items from attention+active recommendation, got %+v", buckets)
	}
	if buckets["card_tasks"] != 1 || buckets["api_risks"] != 1 {
		t.Fatalf("expected product/API buckets from attention only, got %+v", buckets)
	}
}

func TestRecommendationTaskCountsUsesOnlyActiveRealCreatedAt(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	active, overdue := recommendationTaskCounts([]sqlcgen.Recommendation{
		{
			Status:    domain.RecommendationStatusActive,
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-72 * time.Hour), Valid: true},
		},
		{
			Status:    domain.RecommendationStatusActive,
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-2 * time.Hour), Valid: true},
		},
		{
			Status:    domain.RecommendationStatusCompleted,
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-96 * time.Hour), Valid: true},
		},
		{
			Status: domain.RecommendationStatusActive,
		},
	}, now)

	if active != 3 {
		t.Fatalf("expected 3 active recommendations, got %d", active)
	}
	if overdue != 1 {
		t.Fatalf("expected 1 overdue recommendation with real created_at evidence, got %d", overdue)
	}
}

func TestCountRecommendationTaskOwnerBucketsUsesActiveRecommendations(t *testing.T) {
	buckets := countRecommendationTaskOwnerBuckets([]sqlcgen.Recommendation{
		{Type: domain.RecommendationTypeLowerBid, Status: domain.RecommendationStatusActive},
		{Type: domain.RecommendationTypeOptimizeSEO, Status: domain.RecommendationStatusActive},
		{Type: domain.RecommendationTypeStockAlert, Status: domain.RecommendationStatusActive},
		{Type: domain.RecommendationTypePhotoImprovement, Status: domain.RecommendationStatusCompleted},
		{Type: "unknown_recommendation", Status: domain.RecommendationStatusActive},
	})

	if buckets[domain.RecommendationTaskOwnerMarketer] != 1 {
		t.Fatalf("expected marketer bucket from active bid task, got %+v", buckets)
	}
	if buckets[domain.RecommendationTaskOwnerSEO] != 1 {
		t.Fatalf("expected SEO bucket from active SEO task, got %+v", buckets)
	}
	if buckets[domain.RecommendationTaskOwnerMarketplaceManager] != 1 {
		t.Fatalf("expected marketplace manager bucket from active stock task, got %+v", buckets)
	}
	if _, ok := buckets[domain.RecommendationTaskOwnerContent]; ok {
		t.Fatalf("did not expect completed content task in buckets, got %+v", buckets)
	}
}

func TestCountRecommendationTaskOwnerBucketsReturnsNilWithoutActiveKnownOwners(t *testing.T) {
	if buckets := countRecommendationTaskOwnerBuckets([]sqlcgen.Recommendation{
		{Type: domain.RecommendationTypePhotoImprovement, Status: domain.RecommendationStatusCompleted},
		{Type: "unknown_recommendation", Status: domain.RecommendationStatusActive},
	}); buckets != nil {
		t.Fatalf("expected nil buckets without active known task owners, got %+v", buckets)
	}
}

func TestBuildAttentionItemsReportsQueryBidBelowMinimumFromBidSnapshot(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()
	currentBid := int64(120)
	capturedAt := time.Date(2026, 5, 27, 11, 15, 0, 0, time.UTC)

	items := svc.buildAttentionItems(&adsWorkspaceData{
		bidSnapshotsByPhrase: map[uuid.UUID]sqlcgen.BidSnapshot{
			phraseID: {
				PhraseID:       uuidToPgtype(phraseID),
				CompetitiveBid: 180,
				CpmMin:         150,
				CapturedAt:     pgtype.Timestamptz{Time: capturedAt, Valid: true},
			},
		},
	}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:         phraseID,
			Keyword:    "петля мебельная",
			CurrentBid: &currentBid,
		},
	})

	item, ok := findAttentionItem(items, "query_bid_below_min")
	if !ok {
		t.Fatalf("expected bid below min attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if item.Severity != domain.SeverityCritical {
		t.Fatalf("expected critical severity for bid below WB minimum, got %q", item.Severity)
	}
	if !strings.Contains(item.Description, "минимальной ставки WB 150") || !strings.Contains(item.Description, "2026-05-27 11:15") {
		t.Fatalf("expected real minimum bid in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsQueryBidBelowCompetitiveFromBidSnapshot(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()
	currentBid := int64(160)

	items := svc.buildAttentionItems(&adsWorkspaceData{
		bidSnapshotsByPhrase: map[uuid.UUID]sqlcgen.BidSnapshot{
			phraseID: {
				PhraseID:       uuidToPgtype(phraseID),
				CompetitiveBid: 220,
				CpmMin:         150,
				CapturedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
	}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:         phraseID,
			Keyword:    "петля мебельная",
			CurrentBid: &currentBid,
		},
	})

	item, ok := findAttentionItem(items, "query_bid_below_competitive")
	if !ok {
		t.Fatalf("expected bid below competitive attention item, got %+v", items)
	}
	if !strings.Contains(item.Description, "конкурентной ставки WB 220") {
		t.Fatalf("expected real competitive bid in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsQueryClicksWithoutCarts(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:      phraseID,
			Keyword: "петля мебельная",
			Performance: domain.AdsMetricsSummary{
				Clicks: 12,
				Atbs:   0,
				Orders: 0,
				Spend:  700,
			},
		},
	})

	item, ok := findAttentionItem(items, "query_clicks_without_carts")
	if !ok {
		t.Fatalf("expected clicks without carts attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if !strings.Contains(item.Description, "12 кликов") || !strings.Contains(item.Description, "0 корзин") {
		t.Fatalf("expected click/cart evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsQueryCartsWithoutOrders(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:      phraseID,
			Keyword: "петля мебельная",
			Performance: domain.AdsMetricsSummary{
				Clicks: 12,
				Atbs:   3,
				Orders: 0,
				Spend:  700,
			},
		},
	})

	item, ok := findAttentionItem(items, "query_carts_without_orders")
	if !ok {
		t.Fatalf("expected carts without orders attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if !strings.Contains(item.Description, "3 корзин") || !strings.Contains(item.Description, "0 заказов") {
		t.Fatalf("expected cart/order evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsQuerySEOIdea(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:             phraseID,
			Keyword:        "петля мебельная с доводчиком",
			SignalCategory: "seo_idea",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    1200,
				Orders:   3,
				Revenue:  12000,
				DRR:      10,
			},
		},
	})

	item, ok := findAttentionItem(items, "query_seo_idea")
	if !ok {
		t.Fatalf("expected seo idea attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if !strings.Contains(item.Description, "3 заказов") || !strings.Contains(item.Description, "ДРР 10.0%") || !strings.Contains(item.Description, "маржинальность") {
		t.Fatalf("expected real SEO signal with margin caveat, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsQueryWinner(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:             phraseID,
			Keyword:        "петля 35 мм",
			SignalCategory: "winner",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    900,
				Orders:   1,
				Revenue:  4500,
				DRR:      20,
			},
		},
	})

	item, ok := findAttentionItem(items, "query_winner")
	if !ok {
		t.Fatalf("expected winner query attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if !strings.Contains(item.Description, "расход 900 ₽") || !strings.Contains(item.Description, "выручка 4500 ₽") || !strings.Contains(item.Description, "unit economics") {
		t.Fatalf("expected real winner evidence with economics caveat, got %q", item.Description)
	}
}

func TestBuildAttentionItemsReportsQueryTopPositionReached(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:             phraseID,
			Keyword:        "петля 35 мм",
			SignalCategory: "winner",
			Performance: domain.AdsMetricsSummary{
				DataMode:    "exact",
				Spend:       900,
				Orders:      2,
				Revenue:     6000,
				DRR:         15,
				AvgPosition: 2.4,
			},
		},
	})

	item, ok := findAttentionItem(items, "query_top_position_reached")
	if !ok {
		t.Fatalf("expected top position attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityLow {
		t.Fatalf("expected low severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if !strings.Contains(item.Description, "Средняя позиция 2.4") || !strings.Contains(item.Description, "2 заказов") || !strings.Contains(item.Description, "ДРР 15.0%") {
		t.Fatalf("expected real position and performance evidence, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsQueryTopPositionWithoutAvgPositionEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:             uuid.New(),
			Keyword:        "петля 35 мм",
			SignalCategory: "winner",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    900,
				Orders:   2,
				Revenue:  6000,
				DRR:      15,
			},
		},
	})

	if _, ok := findAttentionItem(items, "query_top_position_reached"); ok {
		t.Fatalf("did not expect top position attention without avg position evidence: %+v", items)
	}
}

func TestBuildAttentionItemsReportsQueryDRROverConfiguredLimit(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	campaignID := uuid.New()
	phraseID := uuid.New()
	strategyID := uuid.New()
	currentBid := int64(240)

	items := svc.buildAttentionItems(&adsWorkspaceData{
		campaignDRRLimits: map[uuid.UUID]campaignDRRLimit{
			campaignID: {
				StrategyID:   strategyID,
				StrategyName: "Антислив WB",
				Limit:        20,
				Source:       "max_acos",
			},
		},
	}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:             phraseID,
			CampaignID:     campaignID,
			Keyword:        "петля 35 мм",
			CurrentBid:     &currentBid,
			SignalCategory: "winner",
			Performance: domain.AdsMetricsSummary{
				DataMode:    "exact",
				Spend:       1800,
				Orders:      3,
				Revenue:     6000,
				DRR:         30,
				AvgPosition: 2.1,
			},
		},
	})

	item, ok := findAttentionItem(items, "query_drr_over_configured_limit")
	if !ok {
		t.Fatalf("expected query configured DRR limit attention item, got %+v", items)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if !strings.Contains(item.Description, "ДРР 30.0%") || !strings.Contains(item.Description, "лимита 20.0%") || !strings.Contains(item.Description, "текущая ставка 240 ₽") {
		t.Fatalf("expected real DRR, limit and bid evidence, got %q", item.Description)
	}
	if _, ok := findAttentionItem(items, "query_winner"); ok {
		t.Fatalf("did not expect winner attention when configured DRR limit is exceeded: %+v", items)
	}
	if _, ok := findAttentionItem(items, "query_top_position_reached"); ok {
		t.Fatalf("did not expect top position attention when configured DRR limit is exceeded: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsQueryDRRLimitWithoutBidEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:      uuid.New(),
			Keyword: "петля 35 мм",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    2200,
				Orders:   2,
				Revenue:  4000,
				DRR:      55,
			},
		},
	})

	if _, ok := findAttentionItem(items, "query_drr_over_safe_threshold"); ok {
		t.Fatalf("did not expect query DRR alert without current bid evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsQueryGrowthWithoutRevenueEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:             uuid.New(),
			Keyword:        "петля 35 мм",
			SignalCategory: "winner",
			Performance: domain.AdsMetricsSummary{
				DataMode: "exact",
				Spend:    900,
				Orders:   1,
				DRR:      20,
			},
		},
	})

	if _, ok := findAttentionItem(items, "query_winner"); ok {
		t.Fatalf("did not expect winner attention without revenue evidence: %+v", items)
	}
	if _, ok := findAttentionItem(items, "query_seo_idea"); ok {
		t.Fatalf("did not expect seo attention without revenue evidence: %+v", items)
	}
}

func TestBuildAttentionItemsReportsQueryLowDataGuardrail(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}
	phraseID := uuid.New()
	currentBid := int64(180)

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:           phraseID,
			Keyword:      "петля мебельная",
			CurrentBid:   &currentBid,
			HealthStatus: "insufficient_data",
			Performance: domain.AdsMetricsSummary{
				DataMode:    "exact",
				Impressions: 120,
				Clicks:      3,
				Spend:       180,
			},
		},
	})

	item, ok := findAttentionItem(items, "query_low_data_guardrail")
	if !ok {
		t.Fatalf("expected low-data guardrail attention item, got %+v", items)
	}
	if item.Severity != domain.SeverityLow {
		t.Fatalf("expected low severity, got %q", item.Severity)
	}
	if item.SourceID == nil || *item.SourceID != phraseID.String() {
		t.Fatalf("expected query source id %s, got %+v", phraseID, item.SourceID)
	}
	if !strings.Contains(item.Description, "120 показов") || !strings.Contains(item.Description, "3 кликов") || !strings.Contains(item.Description, "ставка 180 ₽") {
		t.Fatalf("expected real low-data evidence in description, got %q", item.Description)
	}
}

func TestBuildAttentionItemsSkipsQueryLowDataGuardrailWithoutBidEvidence(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:           uuid.New(),
			Keyword:      "петля мебельная",
			HealthStatus: "insufficient_data",
			Performance: domain.AdsMetricsSummary{
				DataMode:    "exact",
				Impressions: 120,
				Clicks:      3,
				Spend:       180,
			},
		},
	})

	if _, ok := findAttentionItem(items, "query_low_data_guardrail"); ok {
		t.Fatalf("did not expect low-data guardrail without current bid evidence: %+v", items)
	}
}

func TestBuildAttentionItemsSkipsQueryConversionWarningWhenOrdersExist(t *testing.T) {
	svc := &AdsReadService{unitEconomicsConfigured: true}

	items := svc.buildAttentionItems(&adsWorkspaceData{}, nil, nil, []domain.QueryPerformanceSummary{
		{
			ID:      uuid.New(),
			Keyword: "петля мебельная",
			Performance: domain.AdsMetricsSummary{
				Clicks: 12,
				Atbs:   3,
				Orders: 1,
				Spend:  700,
			},
		},
	})

	if _, ok := findAttentionItem(items, "query_clicks_without_carts"); ok {
		t.Fatalf("did not expect clicks without carts warning when orders exist: %+v", items)
	}
	if _, ok := findAttentionItem(items, "query_carts_without_orders"); ok {
		t.Fatalf("did not expect carts without orders warning when orders exist: %+v", items)
	}
}

func findAttentionItem(items []domain.AttentionItem, itemType string) (domain.AttentionItem, bool) {
	for _, item := range items {
		if item.Type == itemType {
			return item, true
		}
	}
	return domain.AttentionItem{}, false
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

func TestEnrichAdsDataStatusReportsUnitEconomicsState(t *testing.T) {
	status := domain.AdsDataStatus{}
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

	(&AdsReadService{}).enrichAdsDataStatus(&status, now.AddDate(0, 0, -7), now, nil)
	if status.UnitEconomicsState != "not_configured" {
		t.Fatalf("expected not_configured unit economics state, got %q", status.UnitEconomicsState)
	}

	status = domain.AdsDataStatus{}
	(&AdsReadService{unitEconomicsConfigured: true}).enrichAdsDataStatus(&status, now.AddDate(0, 0, -7), now, nil)
	if status.UnitEconomicsState != "configured" {
		t.Fatalf("expected configured unit economics state, got %q", status.UnitEconomicsState)
	}
}

func mustDate(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func stringSliceContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func decisionScorePtr(value int, dataMode string, missing []string) domain.DecisionScoreSummary {
	return domain.DecisionScoreSummary{
		Value:           &value,
		DataMode:        dataMode,
		MissingEvidence: missing,
	}
}
