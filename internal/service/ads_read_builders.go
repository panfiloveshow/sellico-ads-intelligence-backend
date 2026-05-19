package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func (s *AdsReadService) buildCabinetSummaries(data *adsWorkspaceData) []domain.CabinetSummary {
	productCountByCabinet := make(map[uuid.UUID]int)
	campaignCountByCabinet := make(map[uuid.UUID]int)
	activeCampaignCountByCabinet := make(map[uuid.UUID]int)
	queryCountByCabinet := make(map[uuid.UUID]int)

	for _, product := range data.products {
		productCountByCabinet[product.SellerCabinetID]++
	}
	for _, campaign := range data.campaigns {
		campaignCountByCabinet[campaign.SellerCabinetID]++
		if campaign.Status == "active" {
			activeCampaignCountByCabinet[campaign.SellerCabinetID]++
		}
	}
	campaignByID := make(map[uuid.UUID]domain.Campaign, len(data.campaigns))
	for _, campaign := range data.campaigns {
		campaignByID[campaign.ID] = campaign
	}
	for _, phrase := range data.phrases {
		campaign, ok := campaignByID[phrase.CampaignID]
		if !ok {
			continue
		}
		queryCountByCabinet[campaign.SellerCabinetID]++
	}

	result := make([]domain.CabinetSummary, 0, len(data.cabinets))
	for _, cabinet := range data.cabinets {
		publicID := cabinet.ID.String()
		if cabinet.ExternalIntegrationID != nil && *cabinet.ExternalIntegrationID != "" {
			publicID = *cabinet.ExternalIntegrationID
		}
		result = append(result, domain.CabinetSummary{
			ID:                   publicID,
			CabinetID:            cabinet.ID,
			IntegrationID:        cabinet.ExternalIntegrationID,
			IntegrationName:      cabinet.Name,
			CabinetName:          cabinet.Name,
			Status:               cabinet.Status,
			FreshnessState:       freshnessStateFromSync(cabinet.LastAutoSync),
			LastSync:             cabinet.LastSyncedAt,
			LastAutoSync:         cabinet.LastAutoSync,
			CampaignsCount:       campaignCountByCabinet[cabinet.ID],
			ProductsCount:        productCountByCabinet[cabinet.ID],
			QueriesCount:         queryCountByCabinet[cabinet.ID],
			ActiveCampaignsCount: activeCampaignCountByCabinet[cabinet.ID],
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CabinetName < result[j].CabinetName
	})
	return result
}

func (s *AdsReadService) buildProductSummaries(data *adsWorkspaceData, dateFrom, dateTo time.Time, filter ProductSummaryFilter) []domain.ProductAdsSummary {
	result := make([]domain.ProductAdsSummary, 0, len(data.productStatsByLink))
	previousFrom, previousTo := previousPeriodRange(dateFrom, dateTo)
	campaignByID := data.campaignByIDMap()
	productByID := data.productByIDMap()

	for key := range data.productStatsByLink {
		product, ok := productByID[key.productID]
		if !ok {
			continue
		}
		campaign, ok := campaignByID[key.campaignID]
		if !ok {
			continue
		}
		if filter.SellerCabinetID != nil && product.SellerCabinetID != *filter.SellerCabinetID {
			continue
		}
		if filter.SellerCabinetID != nil && campaign.SellerCabinetID != *filter.SellerCabinetID {
			continue
		}
		if filter.Title != "" && !strings.Contains(strings.ToLower(product.Title), strings.ToLower(filter.Title)) {
			continue
		}

		campaignsForRow := []domain.Campaign{campaign}
		queries := s.queriesForCampaignProduct(data, campaign.ID, product.ID)
		querySummaries := s.querySummariesForPhrases(data, queries, dateFrom, dateTo)
		metrics := s.aggregateProductCampaignMetrics(data, product.ID, campaign.ID, dateFrom, dateTo)
		if metrics.DataMode == "unavailable" {
			continue
		}
		previousMetrics := s.aggregateProductCampaignMetrics(data, product.ID, campaign.ID, previousFrom, previousTo)
		business := aggregateProductBusiness(data.productBusinessByID[product.ID], metrics.Spend, dateFrom, dateTo)
		healthStatus, healthReason, primaryAction := classifyProductHealth(metrics, 1, len(querySummaries))
		periodCompare := buildPeriodCompare(metrics, previousMetrics)
		if !matchesProductView(filter.View, healthStatus, periodCompare) {
			continue
		}
		campaignID := campaign.ID
		campaignName := campaign.Name
		wbCampaignID := campaign.WBCampaignID

		cabinet := data.cabinets[product.SellerCabinetID]
		result = append(result, domain.ProductAdsSummary{
			ID:               product.ID,
			WorkspaceID:      product.WorkspaceID,
			SellerCabinetID:  product.SellerCabinetID,
			IntegrationID:    cabinet.ExternalIntegrationID,
			IntegrationName:  cabinet.Name,
			CabinetName:      cabinet.Name,
			CampaignID:       &campaignID,
			CampaignName:     &campaignName,
			WBCampaignID:     &wbCampaignID,
			RowKey:           fmt.Sprintf("%s:%s", campaign.ID, product.ID),
			WBProductID:      product.WBProductID,
			Title:            product.Title,
			Brand:            product.Brand,
			Category:         product.Category,
			ImageURL:         product.ImageURL,
			Price:            product.Price,
			CampaignsCount:   1,
			QueriesCount:     len(queries),
			HealthStatus:     healthStatus,
			HealthReason:     healthReason,
			PrimaryAction:    primaryAction,
			FreshnessState:   freshnessStateFromSync(cabinet.LastAutoSync),
			Performance:      metrics,
			Business:         business,
			PeriodCompare:    periodCompare,
			RelatedCampaigns: buildCampaignRefs(campaignsForRow),
			TopQueries:       buildQuerySummaryRefs(trimQuerySummaries(querySummaries, 5)),
			WasteQueries:     buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "waste", 3)),
			WinningQueries:   buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "promising", 3)),
			Evidence:         data.extensionEvidence.productEvidenceIndexed(product.ID),
			CreatedAt:        product.CreatedAt,
			UpdatedAt:        product.UpdatedAt,
		})
	}

	return result
}

func (s *AdsReadService) buildCampaignSummaries(data *adsWorkspaceData, dateFrom, dateTo time.Time, filter CampaignSummaryFilter) []domain.CampaignPerformanceSummary {
	result := make([]domain.CampaignPerformanceSummary, 0, len(data.campaigns))
	previousFrom, previousTo := previousPeriodRange(dateFrom, dateTo)

	for _, campaign := range data.campaigns {
		if filter.SellerCabinetID != nil && campaign.SellerCabinetID != *filter.SellerCabinetID {
			continue
		}
		if filter.Status != "" && campaign.Status != filter.Status {
			continue
		}
		if filter.Name != "" && !strings.Contains(strings.ToLower(campaign.Name), strings.ToLower(filter.Name)) {
			continue
		}

		// Скрываем кампании без данных за период (кроме active/paused)
		hasStats := len(data.campaignStatsByID[campaign.ID]) > 0
		if !hasStats && campaign.Status != "active" && campaign.Status != "paused" {
			continue
		}

		relatedProducts := data.lookupCampaignProducts(campaign.ID)
		if filter.ProductID != nil && !containsProductID(relatedProducts, *filter.ProductID) {
			continue
		}

		cabinet := data.cabinets[campaign.SellerCabinetID]
		metrics := aggregateCampaignStats(data.campaignStatsByID[campaign.ID], dateFrom, dateTo)
		previousMetrics := aggregateCampaignStats(data.campaignStatsByID[campaign.ID], previousFrom, previousTo)
		querySummaries := s.querySummariesForPhrases(data, data.campaignPhrases[campaign.ID], dateFrom, dateTo)
		healthStatus, healthReason, primaryAction := classifyCampaignHealth(metrics, len(relatedProducts), len(querySummaries), campaign.Status)
		periodCompare := buildPeriodCompare(metrics, previousMetrics)
		if !matchesCampaignView(filter.View, campaign.Status, healthStatus, periodCompare) {
			continue
		}

		result = append(result, domain.CampaignPerformanceSummary{
			ID:                       campaign.ID,
			WorkspaceID:              campaign.WorkspaceID,
			SellerCabinetID:          campaign.SellerCabinetID,
			IntegrationID:            cabinet.ExternalIntegrationID,
			IntegrationName:          cabinet.Name,
			CabinetName:              cabinet.Name,
			WBCampaignID:             campaign.WBCampaignID,
			Name:                     campaign.Name,
			Status:                   campaign.Status,
			CampaignType:             campaign.CampaignType,
			BidType:                  campaign.BidType,
			PaymentType:              campaign.PaymentType,
			DailyBudget:              campaign.DailyBudget,
			PlacementSearch:          campaign.PlacementSearch,
			PlacementRecommendations: campaign.PlacementRecommendations,
			WBCreatedAt:              campaign.WBCreatedAt,
			WBStartedAt:              campaign.WBStartedAt,
			WBUpdatedAt:              campaign.WBUpdatedAt,
			WBDeletedAt:              campaign.WBDeletedAt,
			LatestBudget:             campaignBudgetPtr(data.campaignBudgets[campaign.ID]),
			LastSync:                 cabinet.LastSyncedAt,
			HealthStatus:             healthStatus,
			HealthReason:             healthReason,
			PrimaryAction:            primaryAction,
			FreshnessState:           freshnessStateFromSync(cabinet.LastAutoSync),
			Performance:              metrics,
			PeriodCompare:            periodCompare,
			RelatedProducts:          buildProductRefs(relatedProducts),
			Products:                 s.buildCampaignProductPerformance(data, campaign, relatedProducts, dateFrom, dateTo),
			TopQueries:               buildQuerySummaryRefs(trimQuerySummaries(querySummaries, 25)),
			WasteQueries:             buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "waste", 3)),
			WinningQueries:           buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "promising", 3)),
			Evidence:                 data.extensionEvidence.campaignEvidenceIndexed(campaign.ID),
			CreatedAt:                campaign.CreatedAt,
			UpdatedAt:                campaign.UpdatedAt,
		})
	}

	return result
}

func (s *AdsReadService) buildQuerySummaries(data *adsWorkspaceData, dateFrom, dateTo time.Time, filter QuerySummaryFilter) []domain.QueryPerformanceSummary {
	result := make([]domain.QueryPerformanceSummary, 0, len(data.phraseStatsByID))
	campaignByID := make(map[uuid.UUID]domain.Campaign, len(data.campaigns))
	for _, campaign := range data.campaigns {
		campaignByID[campaign.ID] = campaign
	}
	productByID := data.productByIDMap()
	phraseByID := make(map[uuid.UUID]domain.Phrase, len(data.phrases))
	for _, phrase := range data.phrases {
		phraseByID[phrase.ID] = phrase
	}

	for phraseID, stats := range data.phraseStatsByID {
		if len(stats) == 0 {
			continue
		}
		phrase, ok := phraseByID[phraseID]
		if !ok {
			continue
		}
		campaign, ok := campaignByID[phrase.CampaignID]
		if !ok {
			continue
		}
		if filter.CampaignID != nil && campaign.ID != *filter.CampaignID {
			continue
		}
		if filter.SellerCabinetID != nil && campaign.SellerCabinetID != *filter.SellerCabinetID {
			continue
		}
		if filter.Search != "" && !strings.Contains(strings.ToLower(phrase.Keyword), strings.ToLower(filter.Search)) {
			continue
		}

		relatedProducts := productsForPhrase(data, productByID, campaign, phrase)
		if filter.ProductID != nil {
			if phrase.ProductID == nil || *phrase.ProductID != *filter.ProductID {
				continue
			}
		}

		summary := s.buildQuerySummary(data, phrase, campaign, relatedProducts, dateFrom, dateTo)
		if !isStatsBackedNormQuerySummary(summary) {
			continue
		}
		if !matchesQueryView(filter.View, summary) {
			continue
		}
		result = append(result, summary)
	}

	return result
}

func (s *AdsReadService) buildAttentionItems(data *adsWorkspaceData, products []domain.ProductAdsSummary, campaigns []domain.CampaignPerformanceSummary, queries []domain.QueryPerformanceSummary) []domain.AttentionItem {
	items := make([]domain.AttentionItem, 0, 8)

	if data.lastAutoSync == nil {
		items = append(items, domain.AttentionItem{
			Type:        "missing_auto_sync",
			Title:       "Auto-sync ещё не зафиксирован",
			Description: "Данные по рекламе появятся только после первого автоматического сбора.",
			Severity:    "medium",
			ActionLabel: "Открыть Jobs",
			ActionPath:  "/ads-intelligence/jobs",
			SourceType:  "job_run",
		})
	} else if data.lastAutoSync.ResultState == "failed" || data.lastAutoSync.ResultState == "partial" || data.lastAutoSync.ResultState == "empty" {
		items = append(items, domain.AttentionItem{
			Type:        "sync_degraded",
			Title:       "Последний auto-sync требует внимания",
			Description: fmt.Sprintf("Статус данных: %s. Нужно проверить проблемы последней фоновой задачи.", data.lastAutoSync.ResultState),
			Severity:    "high",
			ActionLabel: "Открыть Jobs",
			ActionPath:  fmt.Sprintf("/ads-intelligence/jobs?job_id=%s", data.lastAutoSync.JobRunID.String()),
			SourceType:  "job_run",
			SourceID:    stringPtr(data.lastAutoSync.JobRunID.String()),
		})
	}

	for _, campaign := range campaigns {
		if campaign.Performance.Spend >= 1000 && campaign.Performance.Orders == 0 {
			id := campaign.ID.String()
			items = append(items, domain.AttentionItem{
				Type:        "campaign_spend_without_orders",
				Title:       fmt.Sprintf("Кампания \"%s\" тратит без заказов", campaign.Name),
				Description: fmt.Sprintf("Расход %d ₽ за период без подтверждённых заказов.", campaign.Performance.Spend),
				Severity:    "high",
				ActionLabel: "Открыть кампанию",
				ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
				SourceType:  "campaign",
				SourceID:    &id,
			})
		}
	}

	for _, product := range products {
		if product.HealthStatus != "waste" && product.HealthStatus != "low_ctr" {
			continue
		}
		id := product.ID.String()
		description := "Товар требует проверки рекламного спроса и связанных кампаний."
		if product.HealthReason != nil {
			description = *product.HealthReason
		}
		items = append(items, domain.AttentionItem{
			Type:        "product_attention",
			Title:       fmt.Sprintf("Товар \"%s\" требует внимания", product.Title),
			Description: description,
			Severity:    "medium",
			ActionLabel: "Открыть товар",
			ActionPath:  fmt.Sprintf("/ads-intelligence/products?product_id=%s", id),
			SourceType:  "product",
			SourceID:    &id,
		})
	}

	for _, query := range queries {
		if query.Performance.Impressions >= 200 && query.Performance.Clicks == 0 {
			id := query.ID.String()
			items = append(items, domain.AttentionItem{
				Type:        "query_without_clicks",
				Title:       fmt.Sprintf("Запрос \"%s\" не даёт кликов", query.Keyword),
				Description: fmt.Sprintf("%d показов за период без кликов.", query.Performance.Impressions),
				Severity:    "medium",
				ActionLabel: "Открыть запрос",
				ActionPath:  fmt.Sprintf("/ads-intelligence/phrases?phrase_id=%s", id),
				SourceType:  "query",
				SourceID:    &id,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return attentionSeverityRank(items[i].Severity) > attentionSeverityRank(items[j].Severity)
	})

	return items
}

func (s *AdsReadService) queriesForCampaigns(data *adsWorkspaceData, campaigns []domain.Campaign) []domain.Phrase {
	queries := make([]domain.Phrase, 0)
	for _, campaign := range campaigns {
		queries = append(queries, data.campaignPhrases[campaign.ID]...)
	}
	return dedupePhrases(queries)
}

func (s *AdsReadService) querySummariesForPhrases(data *adsWorkspaceData, phrases []domain.Phrase, dateFrom, dateTo time.Time) []domain.QueryPerformanceSummary {
	if len(phrases) == 0 {
		return nil
	}

	campaignByID := make(map[uuid.UUID]domain.Campaign, len(data.campaigns))
	for _, campaign := range data.campaigns {
		campaignByID[campaign.ID] = campaign
	}
	productByID := data.productByIDMap()

	result := make([]domain.QueryPerformanceSummary, 0, len(phrases))
	for _, phrase := range dedupePhrases(phrases) {
		campaign, ok := campaignByID[phrase.CampaignID]
		if !ok {
			continue
		}
		summary := s.buildQuerySummary(data, phrase, campaign, productsForPhrase(data, productByID, campaign, phrase), dateFrom, dateTo)
		if !isStatsBackedNormQuerySummary(summary) {
			continue
		}
		result = append(result, summary)
	}

	sortQuerySummaries(result)
	return result
}

func isStatsBackedNormQuerySummary(summary domain.QueryPerformanceSummary) bool {
	return summary.Performance.DataMode != "unavailable" &&
		summary.WBProductID != nil &&
		strings.TrimSpace(summary.WBNormQuery) != ""
}

func (s *AdsReadService) logNormQueryReadRows(rows []domain.QueryPerformanceSummary) {
	withNMID := 0
	withCampaign := 0
	withProduct := 0
	withStats := 0
	for _, row := range rows {
		if row.WBProductID != nil {
			withNMID++
		}
		if row.WBCampaignID != 0 && row.CampaignName != "" {
			withCampaign++
		}
		if row.ProductID != nil || row.ProductName != nil {
			withProduct++
		}
		if row.Performance.DataMode != "unavailable" {
			withStats++
		}
	}
	s.logger.Info().
		Int("total", len(rows)).
		Int("withNmId", withNMID).
		Int("withCampaign", withCampaign).
		Int("withProduct", withProduct).
		Int("withStats", withStats).
		Interface("sample", sampleQueryRows(rows, 5)).
		Msg("[NQ READ] rows")
}

func sampleQueryRows(rows []domain.QueryPerformanceSummary, limit int) []map[string]interface{} {
	if len(rows) < limit {
		limit = len(rows)
	}
	sample := make([]map[string]interface{}, 0, limit)
	for i := 0; i < limit; i++ {
		row := rows[i]
		var wbProductID interface{} = nil
		if row.WBProductID != nil {
			wbProductID = *row.WBProductID
		}
		var productName interface{} = nil
		if row.ProductName != nil {
			productName = *row.ProductName
		}
		sample = append(sample, map[string]interface{}{
			"campaignId":   row.WBCampaignID,
			"campaignName": row.CampaignName,
			"nmId":         wbProductID,
			"productName":  productName,
			"normQuery":    row.WBNormQuery,
			"views":        row.Performance.Impressions,
			"clicks":       row.Performance.Clicks,
			"spend":        row.Performance.Spend,
			"atbs":         row.Performance.Atbs,
			"orders":       row.Performance.Orders,
			"dataMode":     row.Performance.DataMode,
		})
	}
	return sample
}

func (s *AdsReadService) buildQuerySummary(data *adsWorkspaceData, phrase domain.Phrase, campaign domain.Campaign, relatedProducts []domain.Product, dateFrom, dateTo time.Time) domain.QueryPerformanceSummary {
	cabinet := data.cabinets[campaign.SellerCabinetID]
	metrics := aggregatePhraseStats(data.phraseStatsByID[phrase.ID], dateFrom, dateTo)
	previousFrom, previousTo := previousPeriodRange(dateFrom, dateTo)
	previousMetrics := aggregatePhraseStats(data.phraseStatsByID[phrase.ID], previousFrom, previousTo)
	periodCompare := buildPeriodCompare(metrics, previousMetrics)
	signalCategory, healthStatus, healthReason, primaryAction := classifyQuerySignal(phrase, metrics)
	priorityScore := scoreQueryPriority(signalCategory, metrics, periodCompare)
	productID := phrase.ProductID
	productName := (*string)(nil)
	wbProductID := phrase.WBProductID
	if len(relatedProducts) == 1 {
		product := relatedProducts[0]
		productName = &product.Title
		if productID == nil {
			v := product.ID
			productID = &v
		}
		if wbProductID == nil {
			v := product.WBProductID
			wbProductID = &v
		}
	}

	return domain.QueryPerformanceSummary{
		ID:              phrase.ID,
		WorkspaceID:     phrase.WorkspaceID,
		CampaignID:      phrase.CampaignID,
		ProductID:       productID,
		SellerCabinetID: campaign.SellerCabinetID,
		IntegrationID:   cabinet.ExternalIntegrationID,
		IntegrationName: cabinet.Name,
		CabinetName:     cabinet.Name,
		CampaignName:    campaign.Name,
		WBCampaignID:    campaign.WBCampaignID,
		ProductName:     productName,
		WBProductID:     wbProductID,
		WBClusterID:     phrase.WBClusterID,
		WBNormQuery:     phrase.WBNormQuery,
		Keyword:         phrase.Keyword,
		CurrentBid:      phrase.CurrentBid,
		ClusterSize:     phrase.Count,
		Source:          "normquery",
		SignalCategory:  signalCategory,
		HealthStatus:    healthStatus,
		HealthReason:    healthReason,
		PrimaryAction:   primaryAction,
		FreshnessState:  freshnessStateFromSync(cabinet.LastAutoSync),
		Performance:     metrics,
		PeriodCompare:   periodCompare,
		PriorityScore:   priorityScore,
		RelatedProducts: buildProductRefs(relatedProducts),
		Evidence:        data.extensionEvidence.phraseEvidenceIndexed(phrase.ID),
		CreatedAt:       phrase.CreatedAt,
		UpdatedAt:       phrase.UpdatedAt,
	}
}

func (s *AdsReadService) queriesForCampaignProduct(data *adsWorkspaceData, campaignID, productID uuid.UUID) []domain.Phrase {
	queries := make([]domain.Phrase, 0)
	for _, phrase := range data.campaignPhrases[campaignID] {
		if phrase.ProductID == nil || *phrase.ProductID != productID {
			continue
		}
		queries = append(queries, phrase)
	}
	return dedupePhrases(queries)
}

func (s *AdsReadService) buildCampaignProductPerformance(data *adsWorkspaceData, campaign domain.Campaign, products []domain.Product, dateFrom, dateTo time.Time) []domain.CampaignProductPerformanceSummary {
	if len(products) == 0 {
		return nil
	}
	result := make([]domain.CampaignProductPerformanceSummary, 0, len(products))
	for _, product := range products {
		metrics := s.aggregateProductCampaignMetrics(data, product.ID, campaign.ID, dateFrom, dateTo)
		linkMeta := data.campaignProductMeta[productCampaignKey{productID: product.ID, campaignID: campaign.ID}]
		subjectName := linkMeta.SubjectName
		if subjectName == nil {
			subjectName = product.Category
		}
		result = append(result, domain.CampaignProductPerformanceSummary{
			ID:                 product.ID,
			ProductID:          product.ID,
			WBProductID:        product.WBProductID,
			ProductName:        product.Title,
			SubjectName:        subjectName,
			BidSearch:          linkMeta.BidSearch,
			BidRecommendations: linkMeta.BidRecommendations,
			Performance:        metrics,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := result[i].Performance
		right := result[j].Performance
		if left.Spend != right.Spend {
			return left.Spend > right.Spend
		}
		if left.Orders != right.Orders {
			return left.Orders > right.Orders
		}
		return result[i].WBProductID < result[j].WBProductID
	})
	return result
}

func campaignBudgetPtr(value domain.CampaignBudgetSummary) *domain.CampaignBudgetSummary {
	if value.CapturedAt.IsZero() && value.Cash == 0 && value.Netting == 0 && value.Total == 0 {
		return nil
	}
	return &value
}

func productsForPhrase(data *adsWorkspaceData, productByID map[uuid.UUID]domain.Product, campaign domain.Campaign, phrase domain.Phrase) []domain.Product {
	if phrase.ProductID != nil {
		if product, ok := productByID[*phrase.ProductID]; ok {
			return []domain.Product{product}
		}
	}
	if phrase.WBProductID == nil || *phrase.WBProductID <= 0 {
		return nil
	}
	productByWB := data.productByCabinetAndWBIDMap()
	product, ok := productByWB[productCabinetWBKey(campaign.SellerCabinetID, *phrase.WBProductID)]
	if !ok {
		return nil
	}
	return []domain.Product{product}
}

func (s *AdsReadService) aggregateProductMetrics(data *adsWorkspaceData, productID uuid.UUID, campaigns []domain.Campaign, dateFrom, dateTo time.Time) (domain.AdsMetricsSummary, *string) {
	if stats := data.productStatsByID[productID]; len(stats) > 0 {
		return aggregateProductStats(stats, dateFrom, dateTo), nil
	}

	if len(campaigns) == 0 {
		return domain.AdsMetricsSummary{DataMode: "unavailable"}, nil
	}

	metrics := domain.AdsMetricsSummary{}
	mode := "exact"
	exactCampaigns := 0
	sharedCampaigns := 0
	for _, campaign := range campaigns {
		productIDs := data.campaignProductIDs[campaign.ID]
		if len(productIDs) == 0 || !containsUUID(productIDs, productID) {
			continue
		}
		if len(productIDs) > 1 {
			sharedCampaigns++
			mode = "shared"
			continue
		}
		campaignMetrics := aggregateCampaignStats(data.campaignStatsByID[campaign.ID], dateFrom, dateTo)
		metrics.Impressions += campaignMetrics.Impressions
		metrics.Clicks += campaignMetrics.Clicks
		metrics.Spend += campaignMetrics.Spend
		metrics.Orders += campaignMetrics.Orders
		metrics.Revenue += campaignMetrics.Revenue
		metrics.Atbs += campaignMetrics.Atbs
		metrics.Canceled += campaignMetrics.Canceled
		metrics.Shks += campaignMetrics.Shks
		exactCampaigns++
	}
	metrics = finalizeMetrics(metrics, mode)
	if sharedCampaigns == 0 {
		return metrics, nil
	}
	if exactCampaigns == 0 {
		metrics = finalizeMetrics(domain.AdsMetricsSummary{}, "shared")
	}
	note := fmt.Sprintf(
		"Товар связан с %d multi-product камп. Их расход и выручка нельзя честно отнести к одному артикулу, поэтому они не включены в товарные KPI.",
		sharedCampaigns,
	)
	return metrics, &note
}

func (s *AdsReadService) aggregateProductCampaignMetrics(data *adsWorkspaceData, productID, campaignID uuid.UUID, dateFrom, dateTo time.Time) domain.AdsMetricsSummary {
	stats := data.productStatsByLink[productCampaignKey{productID: productID, campaignID: campaignID}]
	if len(stats) == 0 {
		return domain.AdsMetricsSummary{DataMode: "unavailable"}
	}
	return aggregateProductStats(stats, dateFrom, dateTo)
}

func containsUUID(items []uuid.UUID, target uuid.UUID) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// aggregateCampaignStats sums all campaign stats.
