package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	lowCampaignBudgetThreshold = int64(100000)
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
		business = applyProductSalesFunnelEvidence(business, data.productFunnelByID[product.ID], dateFrom, dateTo)
		if tariff, ok := productCommissionTariff(data, product, key); ok {
			business = applyProductCommissionTariffEvidence(business, tariff)
		}
		if economics, ok := data.productEconomicsByWBID[product.WBProductID]; ok {
			business = applyProductEconomics(business, product, economics)
		}
		healthStatus, healthReason, primaryAction := classifyProductHealth(metrics, 1, len(querySummaries))
		healthStatus, healthReason, primaryAction = applyProductSalesFunnelHealth(healthStatus, healthReason, primaryAction, business)
		healthStatus, healthReason, primaryAction = applyProductEconomicsHealth(healthStatus, healthReason, primaryAction, metrics, business)
		stockEvidence, hasStockEvidence := data.productStockEvidence[product.ID]
		healthStatus, healthReason, primaryAction = applyProductStockHealth(healthStatus, healthReason, primaryAction, stockEvidence, hasStockEvidence)
		periodCompare := buildPeriodCompare(metrics, previousMetrics)
		if !matchesProductView(filter.View, healthStatus, periodCompare) {
			continue
		}
		campaignID := campaign.ID
		campaignName := campaign.Name
		wbCampaignID := campaign.WBCampaignID
		var stockSummary *domain.ProductStockEvidence
		if hasStockEvidence {
			stockSummary = &domain.ProductStockEvidence{
				StockTotal: stockEvidence.StockTotal,
				Source:     stockEvidence.Source,
				CapturedAt: stockEvidence.CapturedAt,
			}
		}

		scores := buildProductDecisionScores(product, metrics, business, stockSummary)
		stockRunout := buildProductStockRunoutForecast(stockSummary, business, dateFrom, dateTo)
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
			Rating:           product.Rating,
			ReviewsCount:     product.ReviewsCount,
			CampaignsCount:   1,
			QueriesCount:     len(queries),
			HealthStatus:     healthStatus,
			HealthReason:     healthReason,
			PrimaryAction:    primaryAction,
			FreshnessState:   freshnessStateFromSync(cabinet.LastAutoSync),
			Performance:      metrics,
			Business:         business,
			Scores:           scores,
			Decision:         buildProductDecisionSummary(scores),
			PeriodCompare:    periodCompare,
			RelatedCampaigns: buildCampaignRefs(campaignsForRow),
			TopQueries:       buildQuerySummaryRefs(trimQuerySummaries(querySummaries, 5)),
			WasteQueries:     buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "waste", 3)),
			WinningQueries:   buildQuerySummaryRefs(selectQuerySummariesBySignal(querySummaries, "promising", 3)),
			StockEvidence:    stockSummary,
			StockRunout:      stockRunout,
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
	now := time.Now().UTC()

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
		todayMetrics := aggregateCampaignStats(data.campaignStatsByID[campaign.ID], now, now)
		querySummaries := s.querySummariesForPhrases(data, data.campaignPhrases[campaign.ID], dateFrom, dateTo)
		healthStatus, healthReason, primaryAction := classifyCampaignHealth(metrics, len(relatedProducts), len(querySummaries), campaign.Status)
		periodCompare := buildPeriodCompare(metrics, previousMetrics)
		latestBudget := campaignBudgetPtr(data.campaignBudgets[campaign.ID])
		budgetPace := buildCampaignBudgetPace(campaign.DailyBudget, metrics.Spend, todayMetrics.Spend, dateFrom, dateTo, now)
		budgetRunout := buildCampaignBudgetRunout(latestBudget, todayMetrics.Spend, now)
		financeSummary := buildCampaignFinanceSummary(data.financeDocsByWBCampaignID[campaign.WBCampaignID])
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
			LatestBudget:             latestBudget,
			BudgetPace:               budgetPace,
			BudgetRunout:             budgetRunout,
			AdFinance:                financeSummary,
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
			RecentBidChanges:         recentCampaignBidChangeSummaries(data.bidChanges, campaign.ID, 5),
			ActiveRecommendations:    campaignRecommendationSummaries(data.activeRecommendations, campaign, relatedProducts, data.campaignPhrases[campaign.ID], 5),
			Evidence:                 data.extensionEvidence.campaignEvidenceIndexed(campaign.ID),
			CreatedAt:                campaign.CreatedAt,
			UpdatedAt:                campaign.UpdatedAt,
		})
	}

	return result
}

func buildCampaignFinanceSummary(rows []sqlcgen.WBAdFinanceDocument) *domain.CampaignFinanceSummary {
	if len(rows) == 0 {
		return nil
	}

	documentTypes := make(map[string]int)
	var amount int64
	var latest *time.Time
	for _, row := range rows {
		amount += row.Amount
		if row.DocumentType != "" {
			documentTypes[row.DocumentType]++
		}
		if row.DocumentDate.Valid {
			documentDate := row.DocumentDate.Time
			if latest == nil || documentDate.After(*latest) {
				latest = &documentDate
			}
		}
	}

	if len(documentTypes) == 0 {
		documentTypes = nil
	}
	return &domain.CampaignFinanceSummary{
		DocumentsCount:   len(rows),
		Amount:           amount,
		DocumentTypes:    documentTypes,
		LatestDocumentAt: latest,
		DataMode:         "wb_finance",
	}
}

func recentCampaignBidChangeSummaries(rows []sqlcgen.BidChange, campaignID uuid.UUID, limit int) []domain.CampaignBidChangeSummary {
	if limit <= 0 {
		return nil
	}
	changes := make([]domain.BidChange, 0, limit)
	for _, row := range rows {
		if !row.CampaignID.Valid || uuidFromPgtype(row.CampaignID) != campaignID {
			continue
		}
		changes = append(changes, bidChangeFromSqlc(row))
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].CreatedAt.After(changes[j].CreatedAt)
	})
	if len(changes) > limit {
		changes = changes[:limit]
	}
	result := make([]domain.CampaignBidChangeSummary, 0, len(changes))
	for _, change := range changes {
		result = append(result, domain.CampaignBidChangeSummary{
			ID:               change.ID,
			ProductID:        change.ProductID,
			PhraseID:         change.PhraseID,
			RecommendationID: change.RecommendationID,
			Placement:        change.Placement,
			OldBid:           change.OldBid,
			NewBid:           change.NewBid,
			Reason:           change.Reason,
			Source:           change.Source,
			WBStatus:         change.WBStatus,
			CanRollback:      change.CanRollback,
			RollbackBid:      change.RollbackBid,
			CreatedAt:        change.CreatedAt,
		})
	}
	return result
}

func campaignRecommendationSummaries(rows []sqlcgen.Recommendation, campaign domain.Campaign, relatedProducts []domain.Product, phrases []domain.Phrase, limit int) []domain.CampaignRecommendationSummary {
	if limit <= 0 {
		return nil
	}
	productIDs := make(map[uuid.UUID]struct{}, len(relatedProducts))
	for _, product := range relatedProducts {
		productIDs[product.ID] = struct{}{}
	}
	phraseIDs := make(map[uuid.UUID]struct{}, len(phrases))
	for _, phrase := range phrases {
		phraseIDs[phrase.ID] = struct{}{}
	}

	result := make([]domain.CampaignRecommendationSummary, 0, limit)
	seen := make(map[uuid.UUID]struct{})
	for _, row := range rows {
		id := uuidFromPgtype(row.ID)
		if _, ok := seen[id]; ok {
			continue
		}
		scope, ok := campaignRecommendationScope(row, campaign.ID, productIDs, phraseIDs)
		if !ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, domain.CampaignRecommendationSummary{
			ID:         id,
			PhraseID:   uuidToPtr(row.PhraseID),
			ProductID:  uuidToPtr(row.ProductID),
			Scope:      scope,
			Title:      row.Title,
			Type:       row.Type,
			Severity:   row.Severity,
			Confidence: numericToFloat64(row.Confidence),
			NextAction: textToPtr(row.NextAction),
			Status:     row.Status,
			CreatedAt:  row.CreatedAt.Time,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		leftRank := attentionSeverityRank(result[i].Severity)
		rightRank := attentionSeverityRank(result[j].Severity)
		if leftRank == rightRank {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return leftRank > rightRank
	})
	if len(result) > limit {
		return result[:limit]
	}
	return result
}

func campaignRecommendationScope(row sqlcgen.Recommendation, campaignID uuid.UUID, productIDs, phraseIDs map[uuid.UUID]struct{}) (string, bool) {
	if row.CampaignID.Valid && uuidFromPgtype(row.CampaignID) == campaignID {
		return "campaign", true
	}
	if row.PhraseID.Valid {
		if _, ok := phraseIDs[uuidFromPgtype(row.PhraseID)]; ok {
			return "query", true
		}
	}
	if row.ProductID.Valid {
		if _, ok := productIDs[uuidFromPgtype(row.ProductID)]; ok {
			return "product", true
		}
	}
	return "", false
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

type attentionPeriod struct {
	dateFrom time.Time
	dateTo   time.Time
}

func (s *AdsReadService) buildAttentionItems(data *adsWorkspaceData, products []domain.ProductAdsSummary, campaigns []domain.CampaignPerformanceSummary, queries []domain.QueryPerformanceSummary, periods ...attentionPeriod) []domain.AttentionItem {
	items := make([]domain.AttentionItem, 0, 8)
	var period attentionPeriod
	hasPeriod := false
	if len(periods) > 0 {
		period = periods[0]
		hasPeriod = !period.dateFrom.IsZero() && !period.dateTo.IsZero()
	}

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
	if item, ok := wbAPISyncAttentionItem(data.lastAutoSync); ok {
		items = append(items, item)
	}

	if !s.unitEconomicsConfigured {
		if product, ok := highestSpendProductMissingEconomics(products, 3000); ok {
			id := product.ID.String()
			items = append(items, domain.AttentionItem{
				Type:        "missing_unit_economics",
				Title:       fmt.Sprintf("Для товара \"%s\" не подключена экономика", product.Title),
				Description: fmt.Sprintf("Расход %d ₽ за период нельзя оценить по марже: unit economics не подключена. Не масштабируйте товар по ROAS без себестоимости и целевого ДРР.", product.Performance.Spend),
				Severity:    "high",
				ActionLabel: "Открыть настройки экономики",
				ActionPath:  "/ads-intelligence/settings/economics",
				SourceType:  "product",
				SourceID:    &id,
			})
		}
	}

	if overdueCount, oldest, ok := overdueActiveRecommendation(data.activeRecommendations, time.Now()); ok {
		id := uuidFromPgtype(oldest.ID).String()
		items = append(items, domain.AttentionItem{
			Type:        "overdue_recommendations",
			Title:       "Активные рекомендации не обработаны",
			Description: fmt.Sprintf("%d активн. рекомендаций старше 48 часов. Самая старая: %q от %s.", overdueCount, oldest.Title, oldest.CreatedAt.Time.Format("2006-01-02")),
			Severity:    "high",
			ActionLabel: "Открыть рекомендации",
			ActionPath:  "/ads-intelligence/recommendations?status=active",
			SourceType:  "recommendation",
			SourceID:    &id,
		})
	}

	for _, campaign := range campaigns {
		if item, ok := campaignStatusAttentionItem(campaign); ok {
			items = append(items, item)
		}
		if item, ok := campaignBidChangeImprovementAttentionItem(data, campaign, period, hasPeriod); ok {
			items = append(items, item)
		}
		if item, ok := campaignBidChangeRegressionAttentionItem(data, campaign, period, hasPeriod); ok {
			items = append(items, item)
		}
		if item, ok := campaignCostSpikeAttentionItem(campaign); ok {
			items = append(items, item)
		}
		if item, ok := campaignDRRLimitAttentionItem(data, campaign); ok {
			items = append(items, item)
		} else if item, ok := campaignDRRAttentionItem(campaign); ok {
			items = append(items, item)
		}
		if item, ok := campaignNoStatsAttentionItem(campaign); ok {
			items = append(items, item)
		}
		if item, ok := campaignLowCTRAttentionItem(campaign); ok {
			items = append(items, item)
		}

		if campaign.Status == "active" && campaign.BudgetPace != nil && campaign.BudgetPace.State == "over_pace" {
			id := campaign.ID.String()
			description := fmt.Sprintf("Расход за период: %d ₽ при плане %d ₽ (%0.0f%%). Снизьте слабые кластеры и оставьте winners.", campaign.BudgetPace.ActualSpend, campaign.BudgetPace.PlannedSpend, campaign.BudgetPace.UtilizationPercent)
			if campaign.BudgetPace.ProjectedTodaySpend != nil && campaign.BudgetPace.ProjectedTodayUtilizationPercent != nil {
				description = fmt.Sprintf("%s Прогноз на сегодня: %d ₽ (%0.0f%% дневного бюджета).", description, *campaign.BudgetPace.ProjectedTodaySpend, *campaign.BudgetPace.ProjectedTodayUtilizationPercent)
			}
			items = append(items, domain.AttentionItem{
				Type:        "campaign_budget_over_pace",
				Title:       fmt.Sprintf("Кампания \"%s\" расходует бюджет быстрее плана", campaign.Name),
				Description: description,
				Severity:    "high",
				ActionLabel: "Открыть кампанию",
				ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
				SourceType:  "campaign",
				SourceID:    &id,
			})
		}
		if item, ok := campaignBudgetUnderPaceGrowthAttentionItem(campaign, products); ok {
			items = append(items, item)
		}

		if campaign.Status == "active" && campaign.BudgetRunout != nil && campaign.BudgetRunout.State == "will_end_soon" {
			id := campaign.ID.String()
			items = append(items, domain.AttentionItem{
				Type:        "campaign_budget_runout_soon",
				Title:       fmt.Sprintf("Бюджет кампании \"%s\" скоро закончится", campaign.Name),
				Description: fmt.Sprintf("Подтвержденный остаток бюджета: %d ₽, расход сегодня: %d ₽. При текущем темпе бюджет закончится примерно через %.1f ч; snapshot WB от %s.", campaign.BudgetRunout.RemainingBudget, campaign.BudgetRunout.SpendToday, campaign.BudgetRunout.HoursToEmpty, campaign.BudgetRunout.CapturedAt.Format("2006-01-02 15:04")),
				Severity:    domain.SeverityCritical,
				ActionLabel: "Открыть кампанию",
				ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
				SourceType:  "campaign",
				SourceID:    &id,
			})
		}

		if campaign.Status == "active" && campaign.LatestBudget != nil && campaign.LatestBudget.Total == 0 {
			id := campaign.ID.String()
			items = append(items, domain.AttentionItem{
				Type:        "campaign_budget_empty",
				Title:       fmt.Sprintf("В кампании \"%s\" нет бюджета", campaign.Name),
				Description: fmt.Sprintf("Последний WB snapshot бюджета показывает 0 ₽ от %s. Пополните бюджет или ограничьте расход слабых запросов перед изменением ставок.", campaign.LatestBudget.CapturedAt.Format("2006-01-02 15:04")),
				Severity:    domain.SeverityCritical,
				ActionLabel: "Открыть кампанию",
				ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
				SourceType:  "campaign",
				SourceID:    &id,
			})
		} else if campaign.Status == "active" && campaign.LatestBudget != nil && campaign.LatestBudget.Total <= lowCampaignBudgetThreshold {
			id := campaign.ID.String()
			items = append(items, domain.AttentionItem{
				Type:        "campaign_low_budget",
				Title:       fmt.Sprintf("В кампании \"%s\" низкий бюджет", campaign.Name),
				Description: fmt.Sprintf("Подтвержденный остаток бюджета: %d ₽. Пополните бюджет или ограничьте расход слабых запросов.", campaign.LatestBudget.Total/100),
				Severity:    "high",
				ActionLabel: "Открыть кампанию",
				ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
				SourceType:  "campaign",
				SourceID:    &id,
			})
		}

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
		if item, reputationGuardrail := productReputationGuardrailAttentionItem(product); reputationGuardrail {
			items = append(items, item)
		} else {
			if item, ok := s.productGrowthAttentionItem(product); ok {
				items = append(items, item)
			}
		}
		if item, ok := productStockRunoutAttentionItem(product); ok {
			items = append(items, item)
		}
		if !productNeedsAttention(product.HealthStatus) {
			continue
		}
		id := product.ID.String()
		description := "Товар требует проверки рекламного спроса и связанных кампаний."
		if product.HealthReason != nil {
			description = *product.HealthReason
		}
		items = append(items, domain.AttentionItem{
			Type:        productAttentionType(product.HealthStatus),
			Title:       fmt.Sprintf("Товар \"%s\" требует внимания", product.Title),
			Description: description,
			Severity:    productAttentionSeverity(product),
			ActionLabel: "Открыть товар",
			ActionPath:  fmt.Sprintf("/ads-intelligence/products?product_id=%s", id),
			SourceType:  "product",
			SourceID:    &id,
		})
	}

	for _, query := range queries {
		if item, ok := queryBidAttentionItem(data, query); ok {
			items = append(items, item)
		}
		if item, ok := queryConversionAttentionItem(query); ok {
			items = append(items, item)
		}
		if item, ok := queryDRRLimitAttentionItem(data, query); ok {
			items = append(items, item)
		} else {
			if item, ok := queryGrowthAttentionItem(query); ok {
				items = append(items, item)
			}
			if item, ok := queryTopPositionReachedAttentionItem(query); ok {
				items = append(items, item)
			}
		}
		if item, ok := queryLowDataGuardrailAttentionItem(query); ok {
			items = append(items, item)
		}

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

func campaignStatusAttentionItem(campaign domain.CampaignPerformanceSummary) (domain.AttentionItem, bool) {
	status := strings.ToLower(strings.TrimSpace(campaign.Status))
	if status == "" || status == "active" || status == "paused" || status == "ready" {
		return domain.AttentionItem{}, false
	}

	severity := domain.SeverityHigh
	title := fmt.Sprintf("Кампания \"%s\" не активна", campaign.Name)
	description := fmt.Sprintf("WB-статус кампании: %s. Проверьте причину остановки в кабинете и историю действий перед повторным запуском.", campaign.Status)
	switch status {
	case "rejected", "declined", "error":
		severity = domain.SeverityCritical
		title = fmt.Sprintf("Кампания \"%s\" отклонена или в ошибке", campaign.Name)
		description = fmt.Sprintf("WB-статус кампании: %s. Проверьте модерацию, права API и карточки товаров перед любыми ставками или запуском.", campaign.Status)
	case "stopped", "deleted", "archived", "canceled", "cancelled":
		title = fmt.Sprintf("Кампания \"%s\" остановлена", campaign.Name)
	}

	id := campaign.ID.String()
	return domain.AttentionItem{
		Type:        "campaign_status_attention",
		Title:       title,
		Description: description,
		Severity:    severity,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func wbAPISyncAttentionItem(sync *domain.SellerCabinetAutoSyncSummary) (domain.AttentionItem, bool) {
	if sync == nil || (!sync.RateLimited && sync.WBErrors <= 0) {
		return domain.AttentionItem{}, false
	}

	id := sync.JobRunID.String()
	item := domain.AttentionItem{
		Severity:    domain.SeverityCritical,
		ActionLabel: "Открыть Jobs",
		ActionPath:  fmt.Sprintf("/ads-intelligence/jobs?job_id=%s", id),
		SourceType:  "job_run",
		SourceID:    &id,
	}

	if sync.RateLimited {
		item.Type = "wb_api_rate_limited"
		item.Title = "WB API временно ограничил синхронизацию"
		description := "Последний auto-sync получил rate limit от WB API; часть рекламных данных может быть неполной."
		if sync.RateLimitEndpoint != "" {
			description = fmt.Sprintf("%s Endpoint: %s.", description, sync.RateLimitEndpoint)
		}
		if sync.NextAllowedAt != nil {
			description = fmt.Sprintf("%s Следующий разрешённый запуск после %s.", description, sync.NextAllowedAt.UTC().Format("2006-01-02 15:04"))
		} else if sync.RetryAfterSeconds > 0 {
			description = fmt.Sprintf("%s Retry-after: %d сек.", description, sync.RetryAfterSeconds)
		}
		item.Description = description
		return item, true
	}

	item.Type = "wb_api_errors"
	item.Title = "WB API вернул ошибки при синхронизации"
	item.Description = fmt.Sprintf("Последний auto-sync зафиксировал %d WB API ошибок. Проверьте права токена, доступность WB API и журнал синхронизации перед изменением ставок.", sync.WBErrors)
	return item, true
}

func campaignBidChangeImprovementAttentionItem(data *adsWorkspaceData, campaign domain.CampaignPerformanceSummary, period attentionPeriod, hasPeriod bool) (domain.AttentionItem, bool) {
	if data == nil || !hasPeriod || campaign.Status != "active" || campaign.PeriodCompare == nil {
		return domain.AttentionItem{}, false
	}
	change, ok := latestAppliedBidChangeForCampaign(data.bidChanges, campaign.ID, period.dateFrom, period.dateTo)
	if !ok {
		return domain.AttentionItem{}, false
	}

	compare := campaign.PeriodCompare
	current := compare.Current
	previous := compare.Previous
	if current.DataMode == "unavailable" || previous.DataMode == "unavailable" || current.Orders == 0 || current.Revenue <= 0 {
		return domain.AttentionItem{}, false
	}
	if compare.Trend != "improving" {
		return domain.AttentionItem{}, false
	}
	if current.Orders <= previous.Orders && current.Revenue <= previous.Revenue {
		return domain.AttentionItem{}, false
	}
	if previous.ROAS > 0 && current.ROAS <= previous.ROAS {
		return domain.AttentionItem{}, false
	}
	if previous.DRR > 0 && current.DRR >= previous.DRR {
		return domain.AttentionItem{}, false
	}

	id := campaign.ID.String()
	return domain.AttentionItem{
		Type:        "campaign_bid_change_improved",
		Title:       fmt.Sprintf("Кампания \"%s\" улучшилась после изменения ставки", campaign.Name),
		Description: fmt.Sprintf("Applied изменение ставки %s: %d ₽ → %d ₽ от %s. Текущий период лучше предыдущего: заказы %d → %d, выручка %d ₽ → %d ₽, ROAS %.1f → %.1f. Сохраняйте контроль ДРР перед дальнейшим масштабированием.", change.Placement, change.OldBid, change.NewBid, change.CreatedAt.Time.Format("2006-01-02"), previous.Orders, current.Orders, previous.Revenue, current.Revenue, previous.ROAS, current.ROAS),
		Severity:    domain.SeverityMedium,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func campaignBidChangeRegressionAttentionItem(data *adsWorkspaceData, campaign domain.CampaignPerformanceSummary, period attentionPeriod, hasPeriod bool) (domain.AttentionItem, bool) {
	if data == nil || !hasPeriod || campaign.Status != "active" || campaign.PeriodCompare == nil {
		return domain.AttentionItem{}, false
	}
	change, ok := latestAppliedBidChangeForCampaign(data.bidChanges, campaign.ID, period.dateFrom, period.dateTo)
	if !ok {
		return domain.AttentionItem{}, false
	}

	compare := campaign.PeriodCompare
	current := compare.Current
	previous := compare.Previous
	if current.DataMode == "unavailable" || previous.DataMode == "unavailable" || previous.Orders == 0 || previous.Revenue <= 0 {
		return domain.AttentionItem{}, false
	}
	if compare.Trend != "declining" {
		return domain.AttentionItem{}, false
	}
	regressed := current.Orders < previous.Orders || current.Revenue < previous.Revenue
	if previous.ROAS > 0 && current.ROAS < previous.ROAS {
		regressed = true
	}
	if previous.DRR > 0 && current.DRR > previous.DRR {
		regressed = true
	}
	if !regressed {
		return domain.AttentionItem{}, false
	}

	severity := domain.SeverityHigh
	if previous.Orders >= 3 && current.Orders == 0 {
		severity = domain.SeverityCritical
	}
	if previous.DRR > 0 && current.DRR >= previous.DRR*1.5 {
		severity = domain.SeverityCritical
	}

	id := campaign.ID.String()
	return domain.AttentionItem{
		Type:        "campaign_bid_change_regressed",
		Title:       fmt.Sprintf("Кампания \"%s\" ухудшилась после изменения ставки", campaign.Name),
		Description: fmt.Sprintf("Applied изменение ставки %s: %d ₽ → %d ₽ от %s. Текущий период хуже предыдущего: заказы %d → %d, выручка %d ₽ → %d ₽, ROAS %.1f → %.1f, ДРР %.1f%% → %.1f%%. Проверьте причину и рассмотрите откат к %d ₽ через историю изменений.", change.Placement, change.OldBid, change.NewBid, change.CreatedAt.Time.Format("2006-01-02"), previous.Orders, current.Orders, previous.Revenue, current.Revenue, previous.ROAS, current.ROAS, previous.DRR, current.DRR, change.OldBid),
		Severity:    severity,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func latestAppliedBidChangeForCampaign(changes []sqlcgen.BidChange, campaignID uuid.UUID, dateFrom, dateTo time.Time) (sqlcgen.BidChange, bool) {
	for _, change := range changes {
		if change.WbStatus != "applied" || uuidFromPgtype(change.CampaignID) != campaignID || !change.CreatedAt.Valid {
			continue
		}
		if !dateInRange(change.CreatedAt.Time, dateFrom, dateTo) {
			continue
		}
		return change, true
	}
	return sqlcgen.BidChange{}, false
}

func campaignDRRLimitsFromStrategies(strategyRows []sqlcgen.Strategy, bindingRows []sqlcgen.StrategyBinding) map[uuid.UUID]campaignDRRLimit {
	if len(strategyRows) == 0 || len(bindingRows) == 0 {
		return nil
	}

	strategyByID := make(map[uuid.UUID]domain.Strategy, len(strategyRows))
	for _, row := range strategyRows {
		strategy := strategyFromSqlc(row)
		if !strategy.IsActive {
			continue
		}
		strategyByID[strategy.ID] = strategy
	}

	result := make(map[uuid.UUID]campaignDRRLimit)
	for _, binding := range bindingRows {
		if !binding.CampaignID.Valid {
			continue
		}
		strategy, ok := strategyByID[uuidFromPgtype(binding.StrategyID)]
		if !ok {
			continue
		}
		limit, source := configuredDRRLimit(strategy)
		if limit <= 0 {
			continue
		}
		campaignID := uuidFromPgtype(binding.CampaignID)
		current, exists := result[campaignID]
		if !exists || limit < current.Limit {
			result[campaignID] = campaignDRRLimit{
				StrategyID:   strategy.ID,
				StrategyName: strategy.Name,
				Limit:        limit,
				Source:       source,
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func configuredDRRLimit(strategy domain.Strategy) (float64, string) {
	switch strategy.Type {
	case domain.StrategyTypeAntiSliv:
		if strategy.Params.MaxACoS > 0 {
			return strategy.Params.MaxACoS, "max_acos"
		}
	case domain.StrategyTypeACoS:
		if strategy.Params.TargetACoS > 0 {
			return strategy.Params.TargetACoS, "target_acos"
		}
	}
	return 0, ""
}

func campaignCostSpikeAttentionItem(campaign domain.CampaignPerformanceSummary) (domain.AttentionItem, bool) {
	compare := campaign.PeriodCompare
	if compare == nil {
		return domain.AttentionItem{}, false
	}

	spikes := make([]string, 0, 2)
	maxRatio := 0.0
	if compare.Previous.CPC > 0 && compare.Current.CPC >= compare.Previous.CPC*1.5 && compare.Current.Clicks >= 10 && compare.Previous.Clicks >= 10 {
		ratio := compare.Current.CPC / compare.Previous.CPC
		maxRatio = maxFloat(maxRatio, ratio)
		spikes = append(spikes, fmt.Sprintf("CPC вырос с %.2f ₽ до %.2f ₽", compare.Previous.CPC, compare.Current.CPC))
	}
	if compare.Previous.CPM > 0 && compare.Current.CPM >= compare.Previous.CPM*1.5 && compare.Current.Impressions >= 1000 && compare.Previous.Impressions >= 1000 {
		ratio := compare.Current.CPM / compare.Previous.CPM
		maxRatio = maxFloat(maxRatio, ratio)
		spikes = append(spikes, fmt.Sprintf("CPM вырос с %.2f ₽ до %.2f ₽", compare.Previous.CPM, compare.Current.CPM))
	}
	if len(spikes) == 0 {
		return domain.AttentionItem{}, false
	}

	id := campaign.ID.String()
	severity := domain.SeverityHigh
	if maxRatio >= 2 {
		severity = domain.SeverityCritical
	}
	return domain.AttentionItem{
		Type:        "campaign_cost_spike",
		Title:       fmt.Sprintf("В кампании \"%s\" резко выросла стоимость трафика", campaign.Name),
		Description: strings.Join(spikes, "; ") + ". Проверьте ставки, слабые кластеры и свежесть WB-статистики перед масштабированием.",
		Severity:    severity,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func campaignDRRAttentionItem(campaign domain.CampaignPerformanceSummary) (domain.AttentionItem, bool) {
	metrics := campaign.Performance
	if campaign.Status != "active" || metrics.Orders == 0 || metrics.Revenue <= 0 || metrics.Spend <= 0 || metrics.DRR <= 35 {
		return domain.AttentionItem{}, false
	}

	id := campaign.ID.String()
	severity := domain.SeverityHigh
	if metrics.DRR >= 50 {
		severity = domain.SeverityCritical
	}

	return domain.AttentionItem{
		Type:        "campaign_drr_over_safe_threshold",
		Title:       fmt.Sprintf("Кампания \"%s\" вышла за безопасный ДРР", campaign.Name),
		Description: fmt.Sprintf("ДРР %.1f%% выше безопасного рекламного порога 35%% по подтверждённой статистике: расход %d ₽, выручка %d ₽, заказов %d. Проверьте unit economics и целевой ДРР перед масштабированием.", metrics.DRR, metrics.Spend, metrics.Revenue, metrics.Orders),
		Severity:    severity,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func campaignDRRLimitAttentionItem(data *adsWorkspaceData, campaign domain.CampaignPerformanceSummary) (domain.AttentionItem, bool) {
	if data == nil || campaign.Status != "active" {
		return domain.AttentionItem{}, false
	}
	limit, ok := data.campaignDRRLimits[campaign.ID]
	metrics := campaign.Performance
	if !ok || limit.Limit <= 0 || metrics.DataMode == "unavailable" || metrics.Orders == 0 || metrics.Revenue <= 0 || metrics.Spend <= 0 || metrics.DRR <= limit.Limit {
		return domain.AttentionItem{}, false
	}

	id := campaign.ID.String()
	severity := domain.SeverityHigh
	if metrics.DRR >= limit.Limit*1.5 {
		severity = domain.SeverityCritical
	}
	return domain.AttentionItem{
		Type:        "campaign_drr_over_configured_limit",
		Title:       fmt.Sprintf("Кампания \"%s\" превысила лимит ДРР", campaign.Name),
		Description: fmt.Sprintf("ДРР %.1f%% выше лимита %.1f%% из стратегии \"%s\" (%s). Подтверждённая статистика: расход %d ₽, выручка %d ₽, заказов %d.", metrics.DRR, limit.Limit, limit.StrategyName, limit.Source, metrics.Spend, metrics.Revenue, metrics.Orders),
		Severity:    severity,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func campaignNoStatsAttentionItem(campaign domain.CampaignPerformanceSummary) (domain.AttentionItem, bool) {
	if campaign.Status != "active" || campaign.Performance.DataMode != "unavailable" {
		return domain.AttentionItem{}, false
	}

	id := campaign.ID.String()
	return domain.AttentionItem{
		Type:        "campaign_no_stats",
		Title:       fmt.Sprintf("По кампании \"%s\" нет статистики", campaign.Name),
		Description: "Активная кампания есть в read-model, но за выбранный период нет подтверждённых строк рекламной статистики. Проверьте последний sync, права WB API и свежесть данных перед изменением ставок.",
		Severity:    domain.SeverityCritical,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func campaignLowCTRAttentionItem(campaign domain.CampaignPerformanceSummary) (domain.AttentionItem, bool) {
	metrics := campaign.Performance
	if campaign.Status != "active" || campaign.HealthStatus != "low_ctr" || metrics.DataMode == "unavailable" || metrics.Impressions == 0 {
		return domain.AttentionItem{}, false
	}

	id := campaign.ID.String()
	severity := domain.SeverityMedium
	if metrics.Impressions >= 5000 && metrics.Clicks == 0 {
		severity = domain.SeverityHigh
	}

	return domain.AttentionItem{
		Type:        "campaign_low_ctr",
		Title:       fmt.Sprintf("Кампания \"%s\" получает показы без кликов", campaign.Name),
		Description: fmt.Sprintf("Подтверждённая статистика WB: %d показов, %d кликов, CTR %.2f%%. Не повышайте ставку вслепую; проверьте запросы, главное фото, цену и релевантность карточки.", metrics.Impressions, metrics.Clicks, metrics.CTR),
		Severity:    severity,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func campaignBudgetUnderPaceGrowthAttentionItem(campaign domain.CampaignPerformanceSummary, products []domain.ProductAdsSummary) (domain.AttentionItem, bool) {
	metrics := campaign.Performance
	if campaign.Status != "active" || campaign.BudgetPace == nil || campaign.BudgetPace.State != "under_pace" {
		return domain.AttentionItem{}, false
	}
	if metrics.DataMode == "unavailable" || metrics.Orders == 0 || metrics.Revenue <= 0 || metrics.Spend <= 0 || metrics.DRR <= 0 || metrics.DRR > 35 {
		return domain.AttentionItem{}, false
	}

	scaleReadyProducts := countScaleReadyProductsForCampaign(campaign.ID, products)
	if scaleReadyProducts == 0 {
		return domain.AttentionItem{}, false
	}

	id := campaign.ID.String()
	return domain.AttentionItem{
		Type:        "campaign_budget_under_pace_growth",
		Title:       fmt.Sprintf("Кампания \"%s\" недорасходует бюджет при рабочей экономике рекламы", campaign.Name),
		Description: fmt.Sprintf("Расход за период: %d ₽ при плане %d ₽ (%0.0f%%). Подтверждённая статистика WB: %d заказов, выручка %d ₽, ДРР %.1f%%. Найдено товаров-кандидатов на рост с подтверждённым остатком: %d. Расширяйте показы только после проверки маржи и лимитов автопилота.", campaign.BudgetPace.ActualSpend, campaign.BudgetPace.PlannedSpend, campaign.BudgetPace.UtilizationPercent, metrics.Orders, metrics.Revenue, metrics.DRR, scaleReadyProducts),
		Severity:    domain.SeverityMedium,
		ActionLabel: "Открыть кампанию",
		ActionPath:  fmt.Sprintf("/ads-intelligence/campaigns?campaign_id=%s", id),
		SourceType:  "campaign",
		SourceID:    &id,
	}, true
}

func countScaleReadyProductsForCampaign(campaignID uuid.UUID, products []domain.ProductAdsSummary) int {
	count := 0
	for _, product := range products {
		if product.CampaignID == nil || *product.CampaignID != campaignID {
			continue
		}
		if product.Decision.Decision != "scale_candidate_partial" {
			continue
		}
		if product.StockEvidence == nil || product.StockEvidence.StockTotal <= stockAlertThreshold {
			continue
		}
		count++
	}
	return count
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func buildCampaignBudgetPace(dailyBudget *int64, actualSpend, todaySpend int64, dateFrom, dateTo, now time.Time) *domain.CampaignBudgetPaceSummary {
	if dailyBudget == nil || *dailyBudget <= 0 {
		return nil
	}
	periodDays := periodDayCount(dateFrom, dateTo)
	plannedSpend := *dailyBudget * int64(periodDays)
	if plannedSpend <= 0 {
		return nil
	}
	utilization := float64(actualSpend) / float64(plannedSpend) * 100
	state := "on_pace"
	reason := "Расход за период находится в пределах дневного бюджета."
	switch {
	case utilization >= 120:
		state = "over_pace"
		reason = "Расход за период существенно выше плана по дневному бюджету."
	case utilization >= 90:
		state = "near_limit"
		reason = "Расход за период близок к плану по дневному бюджету."
	case utilization <= 40:
		state = "under_pace"
		reason = "Расход за период заметно ниже плана по дневному бюджету."
	}

	projectedTodaySpend, projectedTodayUtilization := projectedTodayBudgetPace(*dailyBudget, todaySpend, dateFrom, dateTo, now)
	if projectedTodaySpend != nil && projectedTodayUtilization != nil {
		switch {
		case *projectedTodayUtilization >= 120:
			state = "over_pace"
			reason = fmt.Sprintf("По сегодняшнему подтверждённому расходу WB прогноз к концу дня: %d ₽ при дневном бюджете %d ₽.", *projectedTodaySpend, *dailyBudget)
		case state == "under_pace" && *projectedTodayUtilization >= 90:
			state = "near_limit"
			reason = fmt.Sprintf("Факт за период ниже плана, но сегодняшний темп прогнозирует %d ₽ к концу дня.", *projectedTodaySpend)
		}
	}

	return &domain.CampaignBudgetPaceSummary{
		State:                            state,
		PeriodDays:                       periodDays,
		DailyBudget:                      *dailyBudget,
		WeeklyBudget:                     *dailyBudget * 7,
		MonthlyBudget:                    *dailyBudget * 30,
		PlannedSpend:                     plannedSpend,
		ActualSpend:                      actualSpend,
		UtilizationPercent:               utilization,
		ProjectedTodaySpend:              projectedTodaySpend,
		ProjectedTodayUtilizationPercent: projectedTodayUtilization,
		Reason:                           reason,
	}
}

func projectedTodayBudgetPace(dailyBudget, todaySpend int64, dateFrom, dateTo, now time.Time) (*int64, *float64) {
	if dailyBudget <= 0 || todaySpend <= 0 {
		return nil, nil
	}
	today := normalizeStatDate(now)
	if today.Before(normalizeStatDate(dateFrom)) || today.After(normalizeStatDate(dateTo)) {
		return nil, nil
	}
	hoursElapsed := now.UTC().Sub(today).Hours()
	if hoursElapsed < 1 {
		return nil, nil
	}
	projectedSpend := int64(math.Round(float64(todaySpend) / hoursElapsed * 24))
	if projectedSpend <= 0 {
		return nil, nil
	}
	utilization := float64(projectedSpend) / float64(dailyBudget) * 100
	return &projectedSpend, &utilization
}

func buildCampaignBudgetRunout(latestBudget *domain.CampaignBudgetSummary, todaySpend int64, now time.Time) *domain.CampaignBudgetRunoutSummary {
	if latestBudget == nil || latestBudget.Total <= 0 || todaySpend <= 0 || latestBudget.CapturedAt.IsZero() {
		return nil
	}
	now = now.UTC()
	capturedAt := latestBudget.CapturedAt.UTC()
	if !normalizeStatDate(capturedAt).Equal(normalizeStatDate(now)) {
		return nil
	}

	dayStart := normalizeStatDate(now)
	hoursElapsed := now.Sub(dayStart).Hours()
	if hoursElapsed < 1 {
		return nil
	}
	hourlySpend := float64(todaySpend) / hoursElapsed
	if hourlySpend <= 0 {
		return nil
	}

	remainingBudget := latestBudget.Total / 100
	hoursToEmpty := float64(remainingBudget) / hourlySpend
	if remainingBudget <= 0 || hoursToEmpty <= 0 || hoursToEmpty > 3 {
		return nil
	}

	return &domain.CampaignBudgetRunoutSummary{
		State:           "will_end_soon",
		RemainingBudget: remainingBudget,
		SpendToday:      todaySpend,
		HoursElapsed:    hoursElapsed,
		HoursToEmpty:    hoursToEmpty,
		CapturedAt:      capturedAt,
		Reason:          "Свежий snapshot бюджета WB и сегодняшний расход показывают риск исчерпания бюджета в ближайшие 3 часа.",
	}
}

func buildProductStockRunoutForecast(stock *domain.ProductStockEvidence, business domain.ProductBusinessSummary, dateFrom, dateTo time.Time) *domain.ProductStockRunoutForecast {
	if stock == nil {
		return nil
	}

	periodDays := periodDayCount(dateFrom, dateTo)
	forecast := &domain.ProductStockRunoutForecast{
		State:      "sales_unavailable",
		StockTotal: stock.StockTotal,
		PeriodDays: periodDays,
		Source:     stock.Source + "+business_reports",
		CapturedAt: stock.CapturedAt,
		Reason:     "Нет подтверждённых бизнес-отчётов продаж за выбранный период; дата out-of-stock не прогнозируется.",
	}

	if business.DataMode != "reports" {
		return forecast
	}
	if stock.StockTotal <= 0 {
		days := 0.0
		forecast.State = "out_of_stock"
		forecast.DaysToEmpty = &days
		forecast.Reason = "Подтверждённый остаток равен 0; масштабирование рекламы нужно остановить до пополнения."
		return forecast
	}
	if business.Sales <= 0 {
		forecast.State = "no_recent_sales"
		forecast.Reason = "В подтверждённых бизнес-отчётах нет продаж за выбранный период; расход остатка не прогнозируется."
		return forecast
	}

	averageDailySales := float64(business.Sales) / float64(periodDays)
	if averageDailySales <= 0 {
		return forecast
	}
	daysToEmpty := float64(stock.StockTotal) / averageDailySales
	forecast.AverageDailySales = averageDailySales
	forecast.DaysToEmpty = &daysToEmpty
	forecast.State = "healthy"
	forecast.Reason = fmt.Sprintf("Прогноз рассчитан по подтверждённому остатку и %d продажам из WB business reports за %d дн.", business.Sales, periodDays)
	switch {
	case daysToEmpty <= 7:
		forecast.State = "runout_soon"
	case daysToEmpty <= 14:
		forecast.State = "watch"
	}
	return forecast
}

func buildProductDecisionScores(product domain.Product, metrics domain.AdsMetricsSummary, business domain.ProductBusinessSummary, stock *domain.ProductStockEvidence) domain.ProductDecisionScores {
	return domain.ProductDecisionScores{
		Advertising: buildAdvertisingScore(metrics),
		Readiness:   buildProductReadinessScore(product, metrics, business, stock),
		Growth:      buildGrowthPotentialScore(product, metrics, business, stock),
	}
}

func buildProductDecisionSummary(scores domain.ProductDecisionScores) domain.ProductDecisionSummary {
	missing := uniqueStrings(append(append([]string{}, scores.Advertising.MissingEvidence...), append(scores.Readiness.MissingEvidence, scores.Growth.MissingEvidence...)...))
	result := domain.ProductDecisionSummary{
		Decision:        "insufficient_evidence",
		DataMode:        decisionDataMode(scores),
		Reason:          "Недостаточно подтверждённых данных для итогового решения по товару.",
		MissingEvidence: missing,
	}

	advertising, advertisingOK := scoreValue(scores.Advertising)
	readiness, readinessOK := scoreValue(scores.Readiness)
	growth, growthOK := scoreValue(scores.Growth)
	if !advertisingOK || !readinessOK {
		return result
	}

	switch {
	case advertising >= 70 && readiness >= 70 && growthOK && growth >= 70:
		result.Decision = "scale_candidate_partial"
		result.Reason = "Реклама, готовность товара и потенциал роста выглядят сильными по доступным данным; проверьте недостающие evidence перед повышением ставок."
	case advertising < 40 && readiness < 50:
		result.Decision = "fix_product_readiness"
		result.Reason = "Рекламная эффективность и готовность товара низкие; сначала исправляйте карточку, оффер, остатки или репутацию."
	case advertising < 40 && readiness >= 60:
		result.Decision = "optimize_ads"
		result.Reason = "Товар выглядит достаточно готовым, но рекламная эффективность низкая; оптимизируйте кампании, ставки и запросы."
	case advertising >= 70 && readiness < 60:
		result.Decision = "ads_working_readiness_risk"
		result.Reason = "Реклама даёт результат, но готовность товара слабая или неполная; масштабируйте осторожно и закройте readiness-риски."
	default:
		result.Decision = "monitor"
		result.Reason = "Сильного итогового сигнала нет; продолжайте наблюдение и добирайте недостающие evidence."
	}
	return result
}

func buildAdvertisingScore(metrics domain.AdsMetricsSummary) domain.DecisionScoreSummary {
	score := domain.DecisionScoreSummary{DataMode: metrics.DataMode}
	if metrics.DataMode == "unavailable" {
		score.MissingEvidence = []string{"ad_stats"}
		return score
	}

	points, maxPoints := 0, 0
	if metrics.Spend > 0 && metrics.Revenue > 0 && metrics.DRR > 0 {
		maxPoints += 35
		score.Evidence = append(score.Evidence, "drr")
		switch {
		case metrics.DRR <= 20:
			points += 35
		case metrics.DRR <= 35:
			points += 28
		case metrics.DRR <= 50:
			points += 15
		default:
			points += 5
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "drr")
	}

	if metrics.Spend > 0 && metrics.Revenue > 0 && metrics.ROAS > 0 {
		maxPoints += 20
		score.Evidence = append(score.Evidence, "roas")
		switch {
		case metrics.ROAS >= 5:
			points += 20
		case metrics.ROAS >= 3:
			points += 14
		case metrics.ROAS >= 1:
			points += 7
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "roas")
	}

	if metrics.Impressions > 0 {
		maxPoints += 20
		score.Evidence = append(score.Evidence, "ctr")
		switch {
		case metrics.CTR >= 0.03:
			points += 20
		case metrics.CTR >= 0.01:
			points += 10
		default:
			points += 2
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "ctr")
	}

	if metrics.Clicks > 0 {
		maxPoints += 25
		score.Evidence = append(score.Evidence, "conversion_rate")
		switch {
		case metrics.ConversionRate >= 0.05:
			points += 25
		case metrics.ConversionRate >= 0.01:
			points += 12
		case metrics.Orders > 0:
			points += 8
		default:
			points += 2
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "conversion_rate")
	}

	score.Value = normalizedScore(points, maxPoints)
	score.DataMode = scoreDataMode(score.DataMode, score.Value, score.MissingEvidence)
	return score
}

func buildProductReadinessScore(product domain.Product, metrics domain.AdsMetricsSummary, business domain.ProductBusinessSummary, stock *domain.ProductStockEvidence) domain.DecisionScoreSummary {
	score := domain.DecisionScoreSummary{DataMode: "partial"}
	points, maxPoints := 0, 0

	if stock != nil {
		maxPoints += 25
		score.Evidence = append(score.Evidence, "stock")
		switch {
		case stock.StockTotal > stockAlertThreshold:
			points += 25
		case stock.StockTotal > 0:
			points += 10
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "stock")
	}

	if product.Rating != nil && *product.Rating > 0 {
		maxPoints += 20
		score.Evidence = append(score.Evidence, "rating")
		switch {
		case *product.Rating >= 4.6:
			points += 20
		case *product.Rating >= 4.2:
			points += 15
		case *product.Rating >= 4.0:
			points += 8
		default:
			points += 2
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "rating")
	}

	if product.ReviewsCount != nil {
		maxPoints += 15
		score.Evidence = append(score.Evidence, "reviews_count")
		switch {
		case *product.ReviewsCount >= 20:
			points += 15
		case *product.ReviewsCount >= 10:
			points += 10
		case *product.ReviewsCount > 0:
			points += 5
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "reviews_count")
	}

	if metrics.DataMode != "unavailable" && metrics.Clicks > 0 {
		maxPoints += 20
		score.Evidence = append(score.Evidence, "cart_rate")
		switch {
		case metrics.CartRate >= 0.1:
			points += 20
		case metrics.CartRate >= 0.03:
			points += 10
		default:
			points += 3
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "cart_rate")
	}

	if business.DataMode == "reports" && business.Orders > 0 {
		maxPoints += 20
		score.Evidence = append(score.Evidence, "buyout_rate")
		switch {
		case business.BuyoutRate >= 0.8:
			points += 20
		case business.BuyoutRate >= 0.6:
			points += 10
		default:
			points += 3
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "buyout_rate")
	}

	if business.SalesFunnelCartCount > 0 {
		maxPoints += 10
		score.Evidence = append(score.Evidence, "sales_funnel_carts")
		points += 10
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "sales_funnel_carts")
	}

	if business.SalesFunnelOpenToCartConversion != nil {
		maxPoints += 10
		score.Evidence = append(score.Evidence, "sales_funnel_open_to_cart")
		switch {
		case *business.SalesFunnelOpenToCartConversion >= 10:
			points += 10
		case *business.SalesFunnelOpenToCartConversion >= 3:
			points += 5
		default:
			points += 1
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "sales_funnel_open_to_cart")
	}

	if business.SalesFunnelCartToOrderConversion != nil {
		maxPoints += 10
		score.Evidence = append(score.Evidence, "sales_funnel_cart_to_order")
		switch {
		case *business.SalesFunnelCartToOrderConversion >= 20:
			points += 10
		case *business.SalesFunnelCartToOrderConversion >= 5:
			points += 5
		default:
			points += 1
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "sales_funnel_cart_to_order")
	}

	if business.WBCommissionDataMode == "wb_tariffs" {
		maxPoints += 5
		score.Evidence = append(score.Evidence, "wb_commission_tariffs")
		points += 5
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "wb_commission_tariffs")
	}

	score.MissingEvidence = append(score.MissingEvidence, "market_price")
	if pointsForCardContentEvidence(product) > 0 {
		maxPoints += 10
		score.Evidence = append(score.Evidence, "card_content")
		points += pointsForCardContentEvidence(product)
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "photo_content")
	}
	if business.MarginBeforeAds != nil {
		maxPoints += 15
		score.Evidence = append(score.Evidence, "unit_economics_margin")
		if *business.MarginBeforeAds > 0 {
			points += 15
		} else {
			points += 3
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "unit_economics_margin")
	}
	score.Value = normalizedScore(points, maxPoints)
	score.DataMode = scoreDataMode(score.DataMode, score.Value, score.MissingEvidence)
	return score
}

func pointsForCardContentEvidence(product domain.Product) int {
	if strings.TrimSpace(product.Title) == "" || product.ImageURL == nil || strings.TrimSpace(*product.ImageURL) == "" {
		return 0
	}
	points := 6
	if product.Brand != nil && strings.TrimSpace(*product.Brand) != "" {
		points += 2
	}
	if product.Category != nil && strings.TrimSpace(*product.Category) != "" {
		points += 2
	}
	return points
}

func buildGrowthPotentialScore(product domain.Product, metrics domain.AdsMetricsSummary, business domain.ProductBusinessSummary, stock *domain.ProductStockEvidence) domain.DecisionScoreSummary {
	score := domain.DecisionScoreSummary{DataMode: "partial"}
	points, maxPoints := 0, 0

	if metrics.DataMode != "unavailable" && metrics.Orders > 0 && metrics.Revenue > 0 && metrics.Spend > 0 && metrics.DRR > 0 {
		maxPoints += 35
		score.Evidence = append(score.Evidence, "orders_drr")
		switch {
		case metrics.DRR <= 20:
			points += 35
		case metrics.DRR <= 35:
			points += 25
		default:
			points += 5
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "orders_drr")
	}

	if stock != nil {
		maxPoints += 25
		score.Evidence = append(score.Evidence, "stock")
		if stock.StockTotal > stockAlertThreshold {
			points += 25
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "stock")
	}

	if metrics.DataMode != "unavailable" && metrics.AvgPosition > 0 {
		maxPoints += 15
		score.Evidence = append(score.Evidence, "avg_position")
		switch {
		case metrics.AvgPosition > 3:
			points += 15
		case metrics.AvgPosition > 1:
			points += 8
		default:
			points += 3
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "avg_position")
	}

	if product.Rating != nil && *product.Rating >= 4.2 && product.ReviewsCount != nil && *product.ReviewsCount >= 10 {
		maxPoints += 10
		score.Evidence = append(score.Evidence, "reputation")
		points += 10
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "reputation")
	}

	if metrics.DataMode != "unavailable" && metrics.Clicks > 0 && metrics.CartRate > 0 {
		maxPoints += 15
		score.Evidence = append(score.Evidence, "cart_rate")
		if metrics.CartRate >= 0.03 {
			points += 15
		} else {
			points += 5
		}
	} else {
		score.MissingEvidence = append(score.MissingEvidence, "cart_rate")
	}

	switch {
	case business.MaxAllowedDRR != nil:
		maxPoints += 20
		score.Evidence = append(score.Evidence, "max_allowed_drr")
		if metrics.DataMode != "unavailable" && metrics.DRR > 0 {
			switch {
			case metrics.DRR <= *business.MaxAllowedDRR:
				points += 20
			case metrics.DRR <= *business.MaxAllowedDRR*1.1:
				points += 8
			default:
				points += 2
			}
		} else {
			score.MissingEvidence = append(score.MissingEvidence, "drr_against_max_allowed")
		}
	case business.MarginBeforeAds != nil:
		maxPoints += 20
		score.Evidence = append(score.Evidence, "unit_economics_margin")
		if *business.MarginBeforeAds > 0 {
			points += 20
		} else {
			points += 2
		}
	default:
		score.MissingEvidence = append(score.MissingEvidence, "unit_economics_margin")
	}

	score.MissingEvidence = append(score.MissingEvidence, "budget_headroom", "winning_queries")
	score.Value = normalizedScore(points, maxPoints)
	score.DataMode = scoreDataMode(score.DataMode, score.Value, score.MissingEvidence)
	return score
}

func applyProductEconomics(business domain.ProductBusinessSummary, product domain.Product, economics domain.ProductEconomics) domain.ProductBusinessSummary {
	business.CostPrice = economics.CostPrice
	business.LogisticsCost = economics.LogisticsCost
	business.OtherCosts = economics.OtherCosts
	business.TaxRatePercent = economics.TaxRatePercent
	business.CommissionPercent = economics.CommissionPercent
	business.TargetMarginPercent = economics.TargetMarginPercent
	business.MaxAllowedDRR = economics.MaxAllowedDRR
	business.EconomicsSource = economics.Source
	business.EconomicsDataMode = "manual"

	if product.Price == nil ||
		economics.CostPrice == nil ||
		economics.LogisticsCost == nil ||
		economics.OtherCosts == nil ||
		economics.TaxRatePercent == nil ||
		economics.CommissionPercent == nil {
		return business
	}

	price := *product.Price
	if price <= 0 {
		return business
	}
	commission := int64(math.Round(float64(price) * *economics.CommissionPercent / 100))
	tax := int64(math.Round(float64(price) * *economics.TaxRatePercent / 100))
	margin := price - *economics.CostPrice - *economics.LogisticsCost - *economics.OtherCosts - commission - tax
	marginPercent := float64(margin) / float64(price) * 100
	business.MarginBeforeAds = &margin
	business.MarginBeforeAdsPercent = &marginPercent
	if business.MaxAllowedDRR == nil {
		business.MaxAllowedDRR = &marginPercent
	}
	if margin > 0 && business.Sales > 0 {
		marginTotal := margin * business.Sales
		profitAfterAds := marginTotal - business.AdSpend
		business.MarginBeforeAdsTotal = &marginTotal
		business.ProfitAfterAds = &profitAfterAds
	}
	if business.MarginBeforeAdsTotal != nil && *business.MarginBeforeAdsTotal > 0 && business.AdSpend > 0 {
		marginalDRR := float64(business.AdSpend) / float64(*business.MarginBeforeAdsTotal) * 100
		business.MarginalDRR = &marginalDRR
	}
	return business
}

func applyProductSalesFunnelEvidence(business domain.ProductBusinessSummary, rows []sqlcgen.ProductSalesFunnelPeriod, dateFrom, dateTo time.Time) domain.ProductBusinessSummary {
	if len(rows) == 0 {
		return business
	}

	normalizedFrom := normalizeStatDate(dateFrom)
	normalizedTo := normalizeStatDate(dateTo)
	var latestCapturedAt *time.Time
	for _, row := range rows {
		if !row.DateFrom.Valid || !row.DateTo.Valid {
			continue
		}
		if row.DateTo.Time.Before(normalizedFrom) || row.DateFrom.Time.After(normalizedTo) {
			continue
		}
		business.SalesFunnelOpenCount += row.OpenCount
		business.SalesFunnelCartCount += row.CartCount
		business.SalesFunnelOrderCount += row.OrderCount
		if business.SalesFunnelSource == "" {
			business.SalesFunnelSource = row.Source
		}
		if row.CapturedAt.Valid && (latestCapturedAt == nil || row.CapturedAt.Time.After(*latestCapturedAt)) {
			capturedAt := row.CapturedAt.Time
			latestCapturedAt = &capturedAt
		}
	}
	if business.SalesFunnelOpenCount > 0 || business.SalesFunnelCartCount > 0 || business.SalesFunnelOrderCount > 0 {
		business.SalesFunnelDataMode = "reports"
		business.SalesFunnelCapturedAt = latestCapturedAt
		if business.SalesFunnelOpenCount > 0 {
			value := float64(business.SalesFunnelCartCount) / float64(business.SalesFunnelOpenCount) * 100
			business.SalesFunnelOpenToCartConversion = &value
		}
		if business.SalesFunnelCartCount > 0 {
			value := float64(business.SalesFunnelOrderCount) / float64(business.SalesFunnelCartCount) * 100
			business.SalesFunnelCartToOrderConversion = &value
		}
	}
	return business
}

func productCommissionTariff(data *adsWorkspaceData, product domain.Product, key productCampaignKey) (sqlcgen.WBCommissionTariff, bool) {
	if data == nil || len(data.commissionTariffsBySubject) == 0 {
		return sqlcgen.WBCommissionTariff{}, false
	}
	if linkMeta, ok := data.campaignProductMeta[key]; ok && linkMeta.SubjectName != nil {
		if tariff, found := data.commissionTariffsBySubject[commissionTariffSubjectKey(*linkMeta.SubjectName)]; found {
			return tariff, true
		}
	}
	if product.Category != nil {
		if tariff, found := data.commissionTariffsBySubject[commissionTariffSubjectKey(*product.Category)]; found {
			return tariff, true
		}
	}
	return sqlcgen.WBCommissionTariff{}, false
}

func commissionTariffSubjectKey(subjectName string) string {
	return strings.ToLower(strings.TrimSpace(subjectName))
}

func applyProductCommissionTariffEvidence(business domain.ProductBusinessSummary, tariff sqlcgen.WBCommissionTariff) domain.ProductBusinessSummary {
	business.WBCommissionSubjectName = tariff.SubjectName
	business.WBCommissionMarketplacePercent = float8ToPtr(tariff.KGVPMarketplace)
	business.WBCommissionSupplierPercent = float8ToPtr(tariff.KGVPSupplier)
	business.WBCommissionPickupPercent = float8ToPtr(tariff.KGVPPickup)
	business.WBCommissionBookingPercent = float8ToPtr(tariff.KGVPBooking)
	business.WBCommissionSupplierExpressPercent = float8ToPtr(tariff.KGVPSupplierExpress)
	business.WBCommissionDataMode = "wb_tariffs"
	return business
}

func normalizedScore(points, maxPoints int) *int {
	if maxPoints <= 0 {
		return nil
	}
	value := int(float64(points) / float64(maxPoints) * 100)
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return &value
}

func scoreDataMode(current string, value *int, missing []string) string {
	if value == nil {
		return "unavailable"
	}
	if len(missing) > 0 || current == "" {
		return "partial"
	}
	return current
}

func scoreValue(score domain.DecisionScoreSummary) (int, bool) {
	if score.Value == nil {
		return 0, false
	}
	return *score.Value, true
}

func decisionDataMode(scores domain.ProductDecisionScores) string {
	if scores.Advertising.Value == nil && scores.Readiness.Value == nil && scores.Growth.Value == nil {
		return "unavailable"
	}
	if scores.Advertising.DataMode == "partial" || scores.Readiness.DataMode == "partial" || scores.Growth.DataMode == "partial" ||
		len(scores.Advertising.MissingEvidence)+len(scores.Readiness.MissingEvidence)+len(scores.Growth.MissingEvidence) > 0 {
		return "partial"
	}
	return "exact"
}

func uniqueStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func periodDayCount(dateFrom, dateTo time.Time) int {
	from := normalizeStatDate(dateFrom)
	to := normalizeStatDate(dateTo)
	if to.Before(from) {
		return 1
	}
	return int(to.Sub(from)/(24*time.Hour)) + 1
}

func productNeedsAttention(status string) bool {
	switch status {
	case "waste", "stop", "reduce_bid", "card_issue", "offer_issue", "low_ctr", "low_stock", "no_stock":
		return true
	default:
		return false
	}
}

func productAttentionType(status string) string {
	switch status {
	case "no_stock":
		return "product_no_stock"
	case "low_stock":
		return "product_low_stock"
	case "card_issue":
		return "product_card_issue"
	case "offer_issue":
		return "product_offer_issue"
	default:
		return "product_attention"
	}
}

func productAttentionSeverity(product domain.ProductAdsSummary) string {
	switch product.HealthStatus {
	case "no_stock":
		return domain.SeverityCritical
	case "low_stock", "stop", "reduce_bid":
		return domain.SeverityHigh
	default:
		return domain.SeverityMedium
	}
}

func (s *AdsReadService) productGrowthAttentionItem(product domain.ProductAdsSummary) (domain.AttentionItem, bool) {
	metrics := product.Performance
	if product.HealthStatus != "growth_candidate" || metrics.DataMode == "unavailable" || metrics.Orders == 0 || metrics.Revenue <= 0 || metrics.Spend <= 0 || metrics.DRR <= 0 {
		return domain.AttentionItem{}, false
	}
	if product.StockEvidence == nil || product.StockEvidence.StockTotal <= stockAlertThreshold {
		return domain.AttentionItem{}, false
	}
	economicsEvidence, ok := productScaleEconomicsEvidence(product.Business, metrics)
	if !ok {
		return domain.AttentionItem{}, false
	}

	id := product.ID.String()
	return domain.AttentionItem{
		Type:        "product_scale_candidate",
		Title:       fmt.Sprintf("Товар \"%s\" готов к проверке масштабирования", product.Title),
		Description: fmt.Sprintf("Рекламная статистика WB: %d заказов, расход %d ₽, выручка %d ₽, ДРР %.1f%%. Подтверждённый остаток: %d шт. Экономика товара: %s. Повышайте ставки только в пределах лимитов автопилота.", metrics.Orders, metrics.Spend, metrics.Revenue, metrics.DRR, product.StockEvidence.StockTotal, economicsEvidence),
		Severity:    domain.SeverityMedium,
		ActionLabel: "Открыть товар",
		ActionPath:  fmt.Sprintf("/ads-intelligence/products?product_id=%s", id),
		SourceType:  "product",
		SourceID:    &id,
	}, true
}

func productScaleEconomicsEvidence(business domain.ProductBusinessSummary, metrics domain.AdsMetricsSummary) (string, bool) {
	parts := make([]string, 0, 2)
	hasPositiveMargin := false
	if business.MarginBeforeAds != nil {
		if *business.MarginBeforeAds <= 0 {
			return "", false
		}
		hasPositiveMargin = true
		if business.MarginBeforeAdsPercent != nil {
			parts = append(parts, fmt.Sprintf("маржа до рекламы %d ₽ (%.1f%%)", *business.MarginBeforeAds, *business.MarginBeforeAdsPercent))
		} else {
			parts = append(parts, fmt.Sprintf("маржа до рекламы %d ₽", *business.MarginBeforeAds))
		}
	}
	if business.MaxAllowedDRR != nil {
		if metrics.DRR <= 0 || metrics.DRR > *business.MaxAllowedDRR {
			return "", false
		}
		parts = append(parts, fmt.Sprintf("максимальный допустимый ДРР %.1f%%", *business.MaxAllowedDRR))
	}
	if !hasPositiveMargin && business.MaxAllowedDRR == nil {
		return "", false
	}
	return strings.Join(parts, ", "), true
}

func productStockRunoutAttentionItem(product domain.ProductAdsSummary) (domain.AttentionItem, bool) {
	if product.StockRunout == nil || product.StockRunout.State != "runout_soon" || product.StockRunout.DaysToEmpty == nil {
		return domain.AttentionItem{}, false
	}

	id := product.ID.String()
	return domain.AttentionItem{
		Type:        "product_stock_runout",
		Title:       fmt.Sprintf("Товар \"%s\" скоро закончится", product.Title),
		Description: fmt.Sprintf("Подтверждённый остаток: %d шт.; средние реальные продажи %.1f шт./день за %d дн. Остатка хватит примерно на %.1f дн.", product.StockRunout.StockTotal, product.StockRunout.AverageDailySales, product.StockRunout.PeriodDays, *product.StockRunout.DaysToEmpty),
		Severity:    domain.SeverityHigh,
		ActionLabel: "Открыть товар",
		ActionPath:  fmt.Sprintf("/ads-intelligence/products?product_id=%s", id),
		SourceType:  "product",
		SourceID:    &id,
	}, true
}

func productReputationGuardrailAttentionItem(product domain.ProductAdsSummary) (domain.AttentionItem, bool) {
	if product.HealthStatus != "growth_candidate" || product.Performance.DataMode == "unavailable" {
		return domain.AttentionItem{}, false
	}

	ratingWeak := product.Rating != nil && *product.Rating > 0 && *product.Rating < 4.2
	reviewsWeak := product.ReviewsCount != nil && *product.ReviewsCount >= 0 && *product.ReviewsCount < 10
	if !ratingWeak && !reviewsWeak {
		return domain.AttentionItem{}, false
	}

	evidence := make([]string, 0, 2)
	severity := domain.SeverityMedium
	if ratingWeak {
		evidence = append(evidence, fmt.Sprintf("рейтинг %.1f", *product.Rating))
		if *product.Rating < 4.0 {
			severity = domain.SeverityHigh
		}
	}
	if reviewsWeak {
		evidence = append(evidence, fmt.Sprintf("%d отзывов", *product.ReviewsCount))
	}

	id := product.ID.String()
	return domain.AttentionItem{
		Type:        "product_reputation_guardrail",
		Title:       fmt.Sprintf("Товар \"%s\" лучше продвигать мягко", product.Title),
		Description: fmt.Sprintf("Карточка уже выглядит кандидатом на рост, но подтверждённые данные карточки показывают: %s. Не повышайте ставки резко; сначала проверьте отзывы, контент и цену.", strings.Join(evidence, ", ")),
		Severity:    severity,
		ActionLabel: "Открыть товар",
		ActionPath:  fmt.Sprintf("/ads-intelligence/products?product_id=%s", id),
		SourceType:  "product",
		SourceID:    &id,
	}, true
}

func queryConversionAttentionItem(query domain.QueryPerformanceSummary) (domain.AttentionItem, bool) {
	metrics := query.Performance
	if metrics.Clicks < 5 || metrics.Orders > 0 {
		return domain.AttentionItem{}, false
	}

	id := query.ID.String()
	item := domain.AttentionItem{
		ActionLabel: "Открыть запрос",
		ActionPath:  fmt.Sprintf("/ads-intelligence/phrases?phrase_id=%s", id),
		SourceType:  "query",
		SourceID:    &id,
	}

	if metrics.Atbs == 0 {
		item.Type = "query_clicks_without_carts"
		item.Title = fmt.Sprintf("Запрос \"%s\" кликают, но не добавляют в корзину", query.Keyword)
		item.Description = fmt.Sprintf("%d кликов и 0 корзин за период. Проверьте карточку, цену, фото, отзывы и релевантность запроса.", metrics.Clicks)
		item.Severity = "medium"
		if metrics.Clicks >= 20 || metrics.Spend >= 1000 {
			item.Severity = "high"
		}
		return item, true
	}

	item.Type = "query_carts_without_orders"
	item.Title = fmt.Sprintf("Запрос \"%s\" даёт корзины без заказов", query.Keyword)
	item.Description = fmt.Sprintf("%d корзин и 0 заказов за период. Проверьте цену, доставку, остатки, варианты и доверие к карточке.", metrics.Atbs)
	item.Severity = "medium"
	return item, true
}

func queryGrowthAttentionItem(query domain.QueryPerformanceSummary) (domain.AttentionItem, bool) {
	metrics := query.Performance
	if metrics.DataMode == "unavailable" || metrics.Orders == 0 || metrics.Revenue <= 0 || metrics.Spend <= 0 || metrics.DRR <= 0 || metrics.DRR > 35 {
		return domain.AttentionItem{}, false
	}

	id := query.ID.String()
	item := domain.AttentionItem{
		Severity:    domain.SeverityMedium,
		ActionLabel: "Открыть запрос",
		ActionPath:  fmt.Sprintf("/ads-intelligence/phrases?phrase_id=%s", id),
		SourceType:  "query",
		SourceID:    &id,
	}

	switch query.SignalCategory {
	case "seo_idea":
		item.Type = "query_seo_idea"
		item.Title = fmt.Sprintf("Запрос \"%s\" стоит проверить для SEO", query.Keyword)
		item.Description = fmt.Sprintf("Кластер дал %d заказов при ДРР %.1f%% по рекламной статистике WB. Это коммерческий SEO-сигнал; маржинальность и релевантность проверьте перед изменением карточки.", metrics.Orders, metrics.DRR)
		return item, true
	case "winner":
		item.Type = "query_winner"
		item.Title = fmt.Sprintf("Запрос \"%s\" даёт заказы с низким ДРР", query.Keyword)
		item.Description = fmt.Sprintf("Кластер дал %d заказов, расход %d ₽, выручка %d ₽, ДРР %.1f%%. Удерживайте его в приоритете; повышение ставки только после проверки остатков и unit economics.", metrics.Orders, metrics.Spend, metrics.Revenue, metrics.DRR)
		return item, true
	default:
		return domain.AttentionItem{}, false
	}
}

func queryDRRLimitAttentionItem(data *adsWorkspaceData, query domain.QueryPerformanceSummary) (domain.AttentionItem, bool) {
	metrics := query.Performance
	if metrics.DataMode == "unavailable" || metrics.Orders == 0 || metrics.Revenue <= 0 || metrics.Spend <= 0 || metrics.DRR <= 0 {
		return domain.AttentionItem{}, false
	}
	if query.CurrentBid == nil || *query.CurrentBid <= 0 {
		return domain.AttentionItem{}, false
	}

	limitValue := 35.0
	limitLabel := "безопасного порога"
	itemType := "query_drr_over_safe_threshold"
	if data != nil {
		if limit, ok := data.campaignDRRLimits[query.CampaignID]; ok && limit.Limit > 0 {
			limitValue = limit.Limit
			limitLabel = fmt.Sprintf("лимита %.1f%% из стратегии \"%s\" (%s)", limit.Limit, limit.StrategyName, limit.Source)
			itemType = "query_drr_over_configured_limit"
		}
	}
	if metrics.DRR <= limitValue {
		return domain.AttentionItem{}, false
	}

	id := query.ID.String()
	severity := domain.SeverityHigh
	if metrics.DRR >= limitValue*1.5 {
		severity = domain.SeverityCritical
	}
	return domain.AttentionItem{
		Type:        itemType,
		Title:       fmt.Sprintf("Запрос \"%s\" даёт дорогие заказы", query.Keyword),
		Description: fmt.Sprintf("ДРР %.1f%% выше %s. Подтверждённая статистика WB: %d заказов, расход %d ₽, выручка %d ₽, текущая ставка %d ₽. Снижайте ставку мягко и оставляйте в приоритете только кластеры с лучшим CPO.", metrics.DRR, limitLabel, metrics.Orders, metrics.Spend, metrics.Revenue, *query.CurrentBid),
		Severity:    severity,
		ActionLabel: "Открыть запрос",
		ActionPath:  fmt.Sprintf("/ads-intelligence/phrases?phrase_id=%s", id),
		SourceType:  "query",
		SourceID:    &id,
	}, true
}

func queryTopPositionReachedAttentionItem(query domain.QueryPerformanceSummary) (domain.AttentionItem, bool) {
	metrics := query.Performance
	if query.SignalCategory != "winner" || metrics.DataMode == "unavailable" {
		return domain.AttentionItem{}, false
	}
	if metrics.AvgPosition <= 0 || metrics.AvgPosition > 3 {
		return domain.AttentionItem{}, false
	}
	if metrics.Orders == 0 || metrics.Revenue <= 0 || metrics.Spend <= 0 || metrics.DRR <= 0 || metrics.DRR > 35 {
		return domain.AttentionItem{}, false
	}

	id := query.ID.String()
	return domain.AttentionItem{
		Type:        "query_top_position_reached",
		Title:       fmt.Sprintf("Запрос \"%s\" вошёл в топ-3 WB", query.Keyword),
		Description: fmt.Sprintf("Средняя позиция %.1f по статистике WB, %d заказов, расход %d ₽, выручка %d ₽, ДРР %.1f%%. Удерживайте запрос; повышение ставки только после проверки остатков и unit economics.", metrics.AvgPosition, metrics.Orders, metrics.Spend, metrics.Revenue, metrics.DRR),
		Severity:    domain.SeverityLow,
		ActionLabel: "Открыть запрос",
		ActionPath:  fmt.Sprintf("/ads-intelligence/phrases?phrase_id=%s", id),
		SourceType:  "query",
		SourceID:    &id,
	}, true
}

func queryLowDataGuardrailAttentionItem(query domain.QueryPerformanceSummary) (domain.AttentionItem, bool) {
	metrics := query.Performance
	if query.HealthStatus != "insufficient_data" || metrics.DataMode == "unavailable" || metrics.Orders > 0 {
		return domain.AttentionItem{}, false
	}
	if query.CurrentBid == nil || *query.CurrentBid <= 0 {
		return domain.AttentionItem{}, false
	}
	if metrics.Impressions == 0 && metrics.Clicks == 0 && metrics.Spend == 0 {
		return domain.AttentionItem{}, false
	}

	id := query.ID.String()
	return domain.AttentionItem{
		Type:        "query_low_data_guardrail",
		Title:       fmt.Sprintf("По запросу \"%s\" ещё мало данных", query.Keyword),
		Description: fmt.Sprintf("Подтверждённая статистика WB мала для резкого решения: %d показов, %d кликов, расход %d ₽, ставка %d ₽. Продолжите тест с лимитом и не меняйте ставку резко до набора данных.", metrics.Impressions, metrics.Clicks, metrics.Spend, *query.CurrentBid),
		Severity:    domain.SeverityLow,
		ActionLabel: "Открыть запрос",
		ActionPath:  fmt.Sprintf("/ads-intelligence/phrases?phrase_id=%s", id),
		SourceType:  "query",
		SourceID:    &id,
	}, true
}

func queryBidAttentionItem(data *adsWorkspaceData, query domain.QueryPerformanceSummary) (domain.AttentionItem, bool) {
	if query.CurrentBid == nil || *query.CurrentBid <= 0 || data == nil || len(data.bidSnapshotsByPhrase) == 0 {
		return domain.AttentionItem{}, false
	}
	snapshot, ok := data.bidSnapshotsByPhrase[query.ID]
	if !ok {
		return domain.AttentionItem{}, false
	}

	currentBid := *query.CurrentBid
	threshold := int64(0)
	itemType := ""
	title := ""
	description := ""
	severity := "medium"

	if snapshot.CpmMin > 0 && currentBid < snapshot.CpmMin {
		threshold = snapshot.CpmMin
		itemType = "query_bid_below_min"
		title = fmt.Sprintf("Ставка по запросу \"%s\" ниже минимальной", query.Keyword)
		description = fmt.Sprintf("Текущая ставка %d ниже минимальной ставки WB %d по последнему снимку ставок.", currentBid, threshold)
		severity = domain.SeverityCritical
	} else if snapshot.CompetitiveBid > 0 && currentBid < snapshot.CompetitiveBid {
		threshold = snapshot.CompetitiveBid
		itemType = "query_bid_below_competitive"
		title = fmt.Sprintf("Ставка по запросу \"%s\" ниже конкурентной", query.Keyword)
		description = fmt.Sprintf("Текущая ставка %d ниже конкурентной ставки WB %d по последнему снимку ставок.", currentBid, threshold)
	} else {
		return domain.AttentionItem{}, false
	}
	if snapshot.CapturedAt.Valid {
		description = fmt.Sprintf("%s Snapshot WB от %s.", description, snapshot.CapturedAt.Time.UTC().Format("2006-01-02 15:04"))
	}

	id := query.ID.String()
	return domain.AttentionItem{
		Type:        itemType,
		Title:       title,
		Description: description,
		Severity:    severity,
		ActionLabel: "Открыть запрос",
		ActionPath:  fmt.Sprintf("/ads-intelligence/phrases?phrase_id=%s", id),
		SourceType:  "query",
		SourceID:    &id,
	}, true
}

func overdueActiveRecommendation(recommendations []sqlcgen.Recommendation, now time.Time) (int, sqlcgen.Recommendation, bool) {
	count := 0
	var oldest sqlcgen.Recommendation
	for _, recommendation := range recommendations {
		if recommendation.Status != domain.RecommendationStatusActive || !recommendation.CreatedAt.Valid {
			continue
		}
		if now.Sub(recommendation.CreatedAt.Time) < domain.RecommendationOverdueAfter {
			continue
		}
		count++
		if !oldest.CreatedAt.Valid || recommendation.CreatedAt.Time.Before(oldest.CreatedAt.Time) {
			oldest = recommendation
		}
	}
	return count, oldest, count > 0
}

func countDecisionQueueBuckets(attention []domain.AttentionItem, recommendations []sqlcgen.Recommendation) map[string]int {
	if len(attention) == 0 && len(recommendations) == 0 {
		return nil
	}

	counts := make(map[string]int)
	for _, item := range attention {
		if bucket := attentionDecisionBucket(item.Type); bucket != "" {
			counts[bucket]++
		}
	}
	for _, rec := range recommendations {
		if rec.Status != "" && rec.Status != domain.RecommendationStatusActive {
			continue
		}
		if bucket := recommendationDigestBucket(rec.Type); bucket != "" {
			counts[bucket]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func countRecommendationTaskOwnerBuckets(recommendations []sqlcgen.Recommendation) map[string]int {
	if len(recommendations) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, rec := range recommendations {
		if rec.Status != "" && rec.Status != domain.RecommendationStatusActive {
			continue
		}
		if ownerRole := domain.RecommendationTaskOwnerRole(rec.Type); ownerRole != "" {
			counts[ownerRole]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func recommendationTaskCounts(recommendations []sqlcgen.Recommendation, now time.Time) (active int, overdue int) {
	for _, recommendation := range recommendations {
		if recommendation.Status != domain.RecommendationStatusActive {
			continue
		}
		active++
		if recommendation.CreatedAt.Valid && now.Sub(recommendation.CreatedAt.Time) >= domain.RecommendationOverdueAfter {
			overdue++
		}
	}
	return active, overdue
}

func attentionDecisionBucket(itemType string) string {
	switch itemType {
	case "campaign_budget_over_pace", "campaign_budget_runout_soon", "campaign_budget_empty", "campaign_low_budget", "campaign_spend_without_orders", "campaign_bid_change_regressed", "campaign_cost_spike", "campaign_drr_over_safe_threshold", "campaign_drr_over_configured_limit", "query_drr_over_safe_threshold", "query_drr_over_configured_limit":
		return "losses"
	case "campaign_budget_under_pace_growth", "campaign_bid_change_improved", "product_scale_candidate", "query_seo_idea", "query_winner", "query_top_position_reached", "query_bid_below_min", "query_bid_below_competitive":
		return "growth"
	case "missing_unit_economics", "product_no_stock", "product_low_stock", "product_card_issue", "product_offer_issue", "product_stock_runout", "product_reputation_guardrail", "query_clicks_without_carts", "query_carts_without_orders", "query_without_clicks", "campaign_low_ctr":
		return "card_tasks"
	case "missing_auto_sync", "sync_degraded", "wb_api_rate_limited", "wb_api_errors", "campaign_no_stats", "campaign_status_attention", "overdue_recommendations":
		return "api_risks"
	default:
		return ""
	}
}

func highestSpendProduct(products []domain.ProductAdsSummary, minSpend int64) (domain.ProductAdsSummary, bool) {
	var selected domain.ProductAdsSummary
	found := false
	for _, product := range products {
		if product.Performance.Spend < minSpend {
			continue
		}
		if !found || product.Performance.Spend > selected.Performance.Spend {
			selected = product
			found = true
		}
	}
	return selected, found
}

func highestSpendProductMissingEconomics(products []domain.ProductAdsSummary, minSpend int64) (domain.ProductAdsSummary, bool) {
	var selected domain.ProductAdsSummary
	found := false
	for _, product := range products {
		if product.Performance.Spend < minSpend || productBusinessHasEconomicsEvidence(product.Business) {
			continue
		}
		if !found || product.Performance.Spend > selected.Performance.Spend {
			selected = product
			found = true
		}
	}
	return selected, found
}

func productBusinessHasEconomicsEvidence(business domain.ProductBusinessSummary) bool {
	return business.MarginBeforeAds != nil ||
		business.MaxAllowedDRR != nil ||
		business.CostPrice != nil ||
		business.LogisticsCost != nil ||
		business.OtherCosts != nil ||
		business.TaxRatePercent != nil ||
		business.CommissionPercent != nil ||
		business.TargetMarginPercent != nil
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
		Evidence:        data.extensionEvidence.phraseEvidenceIndexedWithBid(phrase.ID, phrase.CurrentBid),
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
