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
	result := make([]domain.ProductAdsSummary, 0, len(data.products))
	previousFrom, previousTo := previousPeriodRange(dateFrom, dateTo)

	for _, product := range data.products {
		if filter.SellerCabinetID != nil && product.SellerCabinetID != *filter.SellerCabinetID {
			continue
		}
		if filter.Title != "" && !strings.Contains(strings.ToLower(product.Title), strings.ToLower(filter.Title)) {
			continue
		}

		cabinet := data.cabinets[product.SellerCabinetID]
		campaigns := data.lookupProductCampaigns(product.ID)

		// Скрываем товары без связанных кампаний с данными за период
		hasAnyStats := false
		for _, c := range campaigns {
			if len(data.campaignStatsByID[c.ID]) > 0 || c.Status == "active" || c.Status == "paused" {
				hasAnyStats = true
				break
			}
		}
		if len(campaigns) > 0 && !hasAnyStats {
			continue
		}

		queries := s.queriesForCampaigns(data, campaigns)
		querySummaries := s.querySummariesForPhrases(data, queries, dateFrom, dateTo)
		metrics, note := s.aggregateProductMetrics(data, product.ID, campaigns, dateFrom, dateTo)
		previousMetrics, _ := s.aggregateProductMetrics(data, product.ID, campaigns, previousFrom, previousTo)
		healthStatus, healthReason, primaryAction := classifyProductHealth(metrics, len(campaigns), len(querySummaries))
		periodCompare := buildPeriodCompare(metrics, previousMetrics)
		if !matchesProductView(filter.View, healthStatus, periodCompare) {
			continue
		}

		result = append(result, domain.ProductAdsSummary{
			ID:               product.ID,
			WorkspaceID:      product.WorkspaceID,
			SellerCabinetID:  product.SellerCabinetID,
			IntegrationID:    cabinet.ExternalIntegrationID,
			IntegrationName:  cabinet.Name,
			CabinetName:      cabinet.Name,
			WBProductID:      product.WBProductID,
			Title:            product.Title,
			Brand:            product.Brand,
			Category:         product.Category,
			ImageURL:         product.ImageURL,
			Price:            product.Price,
			CampaignsCount:   len(campaigns),
			QueriesCount:     len(queries),
			HealthStatus:     healthStatus,
			HealthReason:     healthReason,
			PrimaryAction:    primaryAction,
			FreshnessState:   freshnessStateFromSync(cabinet.LastAutoSync),
			Performance:      metrics,
			PeriodCompare:    periodCompare,
			RelatedCampaigns: buildCampaignRefs(campaigns),
			TopQueries:       buildQuerySummaryRefs(trimQuerySummaries(querySummaries, 5)),
			WasteQueries:     buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "waste", 3)),
			WinningQueries:   buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "promising", 3)),
			Evidence:         data.extensionEvidence.productEvidenceIndexed(product.ID),
			DataCoverageNote: note,
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
		metrics.DataMode = "exact"
		previousMetrics := aggregateCampaignStats(data.campaignStatsByID[campaign.ID], previousFrom, previousTo)
		previousMetrics.DataMode = "exact"
		querySummaries := s.querySummariesForPhrases(data, data.campaignPhrases[campaign.ID], dateFrom, dateTo)
		healthStatus, healthReason, primaryAction := classifyCampaignHealth(metrics, len(relatedProducts), len(querySummaries), campaign.Status)
		periodCompare := buildPeriodCompare(metrics, previousMetrics)
		if !matchesCampaignView(filter.View, campaign.Status, healthStatus, periodCompare) {
			continue
		}

		result = append(result, domain.CampaignPerformanceSummary{
			ID:              campaign.ID,
			WorkspaceID:     campaign.WorkspaceID,
			SellerCabinetID: campaign.SellerCabinetID,
			IntegrationID:   cabinet.ExternalIntegrationID,
			IntegrationName: cabinet.Name,
			CabinetName:     cabinet.Name,
			WBCampaignID:    campaign.WBCampaignID,
			Name:            campaign.Name,
			Status:          campaign.Status,
			CampaignType:    campaign.CampaignType,
			BidType:         campaign.BidType,
			PaymentType:     campaign.PaymentType,
			DailyBudget:     campaign.DailyBudget,
			LastSync:        cabinet.LastSyncedAt,
			HealthStatus:    healthStatus,
			HealthReason:    healthReason,
			PrimaryAction:   primaryAction,
			FreshnessState:  freshnessStateFromSync(cabinet.LastAutoSync),
			Performance:     metrics,
			PeriodCompare:   periodCompare,
			RelatedProducts: buildProductRefs(relatedProducts),
			TopQueries:      buildQuerySummaryRefs(trimQuerySummaries(querySummaries, 5)),
			WasteQueries:    buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "waste", 3)),
			WinningQueries:  buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "promising", 3)),
			Evidence:        data.extensionEvidence.campaignEvidenceIndexed(campaign.ID),
			CreatedAt:       campaign.CreatedAt,
			UpdatedAt:       campaign.UpdatedAt,
		})
	}

	return result
}

func (s *AdsReadService) buildQuerySummaries(data *adsWorkspaceData, dateFrom, dateTo time.Time, filter QuerySummaryFilter) []domain.QueryPerformanceSummary {
	result := make([]domain.QueryPerformanceSummary, 0, len(data.phrases))
	campaignByID := make(map[uuid.UUID]domain.Campaign, len(data.campaigns))
	for _, campaign := range data.campaigns {
		campaignByID[campaign.ID] = campaign
	}

	for _, phrase := range data.phrases {
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

		relatedProducts := data.lookupCampaignProducts(campaign.ID)
		if filter.ProductID != nil && !containsProductID(relatedProducts, *filter.ProductID) {
			continue
		}

		summary := s.buildQuerySummary(data, phrase, campaign, relatedProducts, dateFrom, dateTo)
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

	result := make([]domain.QueryPerformanceSummary, 0, len(phrases))
	for _, phrase := range dedupePhrases(phrases) {
		campaign, ok := campaignByID[phrase.CampaignID]
		if !ok {
			continue
		}
		result = append(result, s.buildQuerySummary(data, phrase, campaign, data.lookupCampaignProducts(campaign.ID), dateFrom, dateTo))
	}

	sortQuerySummaries(result)
	return result
}

func (s *AdsReadService) buildQuerySummary(data *adsWorkspaceData, phrase domain.Phrase, campaign domain.Campaign, relatedProducts []domain.Product, dateFrom, dateTo time.Time) domain.QueryPerformanceSummary {
	cabinet := data.cabinets[campaign.SellerCabinetID]
	metrics := aggregatePhraseStats(data.phraseStatsByID[phrase.ID], dateFrom, dateTo)
	previousFrom, previousTo := previousPeriodRange(dateFrom, dateTo)
	previousMetrics := aggregatePhraseStats(data.phraseStatsByID[phrase.ID], previousFrom, previousTo)
	periodCompare := buildPeriodCompare(metrics, previousMetrics)
	signalCategory, healthStatus, healthReason, primaryAction := classifyQuerySignal(phrase, metrics)
	priorityScore := scoreQueryPriority(signalCategory, metrics, periodCompare)

	return domain.QueryPerformanceSummary{
		ID:              phrase.ID,
		WorkspaceID:     phrase.WorkspaceID,
		CampaignID:      phrase.CampaignID,
		SellerCabinetID: campaign.SellerCabinetID,
		IntegrationID:   cabinet.ExternalIntegrationID,
		IntegrationName: cabinet.Name,
		CabinetName:     cabinet.Name,
		CampaignName:    campaign.Name,
		WBCampaignID:    campaign.WBCampaignID,
		WBClusterID:     phrase.WBClusterID,
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

func containsUUID(items []uuid.UUID, target uuid.UUID) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// aggregateCampaignStats sums all campaign stats.
