package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	adsReadEntityLimit = int32(10000)
	adsReadStatsLimit  = int32(50000)
)

type ProductSummaryFilter struct {
	SellerCabinetID *uuid.UUID
	Title           string
	View            string
}

type CampaignSummaryFilter struct {
	SellerCabinetID *uuid.UUID
	Status          string
	Name            string
	ProductID       *uuid.UUID
	View            string
}

type QuerySummaryFilter struct {
	SellerCabinetID *uuid.UUID
	CampaignID      *uuid.UUID
	ProductID       *uuid.UUID
	Search          string
	View            string
}

type cachedWorkspaceData struct {
	data     *adsWorkspaceData
	loadedAt time.Time
}

type AdsReadService struct {
	queries       *sqlcgen.Queries
	wbClient      WBSyncClient
	encryptionKey []byte
	logger        zerolog.Logger

	cacheMu   sync.RWMutex
	dataCache map[string]cachedWorkspaceData
}

func NewAdsReadService(queries *sqlcgen.Queries, wbClient WBSyncClient, encryptionKey []byte, logger zerolog.Logger) *AdsReadService {
	return &AdsReadService{
		queries:       queries,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "ads_read_service").Logger(),
		dataCache:     make(map[string]cachedWorkspaceData),
	}
}

type adsWorkspaceData struct {
	cabinets          map[uuid.UUID]domain.SellerCabinet
	campaigns         []domain.Campaign
	products          []domain.Product
	phrases           []domain.Phrase
	campaignStatsByID map[uuid.UUID][]domain.CampaignStat
	phraseStatsByID   map[uuid.UUID][]domain.PhraseStat
	campaignProducts  map[uuid.UUID][]domain.Product
	productCampaigns  map[uuid.UUID][]domain.Campaign
	campaignPhrases   map[uuid.UUID][]domain.Phrase
	lastAutoSync      *domain.SellerCabinetAutoSyncSummary
	extensionEvidence *workspaceExtensionEvidence
}

// OverviewFilter optionally scopes the overview dashboard to a single seller cabinet.
type OverviewFilter struct {
	SellerCabinetID *uuid.UUID
}

func (s *AdsReadService) Overview(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...OverviewFilter) (*domain.AdsOverview, error) {
	previousFrom, _ := previousPeriodRange(dateFrom, dateTo)
	// Load stats for both current AND previous period in one query (audit fix: SQL date filter)
	data, err := s.loadWorkspaceData(ctx, workspaceID, previousFrom, dateTo)
	if err != nil {
		return nil, err
	}
	_, previousTo := previousPeriodRange(dateFrom, dateTo)

	var cabinetFilter *uuid.UUID
	if len(filter) > 0 && filter[0].SellerCabinetID != nil {
		cabinetFilter = filter[0].SellerCabinetID
	}

	cabinets := s.buildCabinetSummaries(data)
	products := s.buildProductSummaries(data, dateFrom, dateTo, ProductSummaryFilter{SellerCabinetID: cabinetFilter})
	campaigns := s.buildCampaignSummaries(data, dateFrom, dateTo, CampaignSummaryFilter{SellerCabinetID: cabinetFilter})
	queries := s.buildQuerySummaries(data, dateFrom, dateTo, QuerySummaryFilter{SellerCabinetID: cabinetFilter})
	attention := s.buildAttentionItems(data, products, campaigns, queries)

	// Filter campaign stats to selected cabinet if specified
	filteredStats := data.campaignStatsByID
	if cabinetFilter != nil {
		filteredStats = filterCampaignStatsByCabinet(data, *cabinetFilter)
	}
	currentMetrics := aggregateWorkspaceMetrics(filteredStats, dateFrom, dateTo)
	previousMetrics := aggregateWorkspaceMetrics(filteredStats, previousFrom, previousTo)

	sortProductSummaries(products)
	sortCampaignSummaries(campaigns)
	sortQuerySummaries(queries)

	return &domain.AdsOverview{
		LastAutoSync:       data.lastAutoSync,
		PerformanceCompare: buildPeriodCompare(currentMetrics, previousMetrics),
		Evidence:           data.extensionEvidence.workspaceEvidence(domain.SourceAPI),
		Cabinets:           cabinets,
		Attention:          trimAttention(attention, 6),
		TopProducts:        trimProductSummaries(products, 6),
		TopCampaigns:       trimCampaignSummaries(campaigns, 6),
		TopQueries:         trimQuerySummaries(queries, 8),
		Totals: domain.AdsOverviewTotals{
			Cabinets:        len(cabinets),
			Products:        len(products),
			Campaigns:       len(campaigns),
			Queries:         len(queries),
			ActiveCampaigns: countActiveCampaigns(campaigns),
			AttentionItems:  len(attention),
		},
	}, nil
}

func (s *AdsReadService) ListProductSummaries(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ProductSummaryFilter) ([]domain.ProductAdsSummary, error) {
	data, err := s.loadWorkspaceData(ctx, workspaceID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	result := s.buildProductSummaries(data, dateFrom, dateTo, filter)
	sortProductSummaries(result)
	return result, nil
}

func (s *AdsReadService) GetProductSummary(ctx context.Context, workspaceID, productID uuid.UUID, dateFrom, dateTo time.Time) (*domain.ProductAdsSummary, error) {
	rows, err := s.ListProductSummaries(ctx, workspaceID, dateFrom, dateTo, ProductSummaryFilter{})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID == productID {
			result := row
			return &result, nil
		}
	}
	return nil, apperror.New(apperror.ErrNotFound, "product summary not found")
}

func (s *AdsReadService) ListCampaignSummaries(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter CampaignSummaryFilter) ([]domain.CampaignPerformanceSummary, error) {
	data, err := s.loadWorkspaceData(ctx, workspaceID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	result := s.buildCampaignSummaries(data, dateFrom, dateTo, filter)
	sortCampaignSummaries(result)
	return result, nil
}

func (s *AdsReadService) GetCampaignSummary(ctx context.Context, workspaceID, campaignID uuid.UUID, dateFrom, dateTo time.Time) (*domain.CampaignPerformanceSummary, error) {
	rows, err := s.ListCampaignSummaries(ctx, workspaceID, dateFrom, dateTo, CampaignSummaryFilter{})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID == campaignID {
			result := row
			return &result, nil
		}
	}
	return nil, apperror.New(apperror.ErrNotFound, "campaign summary not found")
}

func (s *AdsReadService) ListQuerySummaries(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter QuerySummaryFilter) ([]domain.QueryPerformanceSummary, error) {
	data, err := s.loadWorkspaceData(ctx, workspaceID, dateFrom, dateTo)
	if err != nil {
		return nil, err
	}
	result := s.buildQuerySummaries(data, dateFrom, dateTo, filter)
	sortQuerySummaries(result)
	return result, nil
}

func (s *AdsReadService) GetQuerySummary(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time) (*domain.QueryPerformanceSummary, error) {
	rows, err := s.ListQuerySummaries(ctx, workspaceID, dateFrom, dateTo, QuerySummaryFilter{})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID == phraseID {
			result := row
			return &result, nil
		}
	}
	return nil, apperror.New(apperror.ErrNotFound, "query summary not found")
}

func (s *AdsReadService) loadWorkspaceData(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time) (*adsWorkspaceData, error) {
	// Check cache first (30s TTL) — avoids re-loading for parallel frontend requests
	cacheKey := fmt.Sprintf("%s:%s:%s", workspaceID, dateFrom.Format("2006-01-02"), dateTo.Format("2006-01-02"))
	s.cacheMu.RLock()
	if cached, ok := s.dataCache[cacheKey]; ok && time.Since(cached.loadedAt) < 30*time.Second {
		s.cacheMu.RUnlock()
		return cached.data, nil
	}
	s.cacheMu.RUnlock()

	// Run ALL DB queries in parallel (was sequential — 11 queries × ~100ms each = 1.1s → now ~150ms)
	g, gctx := errgroup.WithContext(ctx)

	var cabinetRows []sqlcgen.SellerCabinet
	var campaignRows []sqlcgen.Campaign
	var productRows []sqlcgen.Product
	var phraseRows []sqlcgen.Phrase
	var campaignStatRows []sqlcgen.CampaignStat
	var phraseStatRows []sqlcgen.PhraseStat
	var extensionEvidence *workspaceExtensionEvidence
	var lastAutoSync *domain.SellerCabinetAutoSyncSummary

	g.Go(func() error {
		var err error
		cabinetRows, err = s.queries.ListSellerCabinetsByWorkspace(gctx, sqlcgen.ListSellerCabinetsByWorkspaceParams{
			WorkspaceID: uuidToPgtype(workspaceID), Limit: adsReadEntityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		campaignRows, err = s.queries.ListCampaignsByWorkspace(gctx, sqlcgen.ListCampaignsByWorkspaceParams{
			WorkspaceID: workspaceUUID(workspaceID), Limit: adsReadEntityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		productRows, err = s.queries.ListProductsByWorkspace(gctx, sqlcgen.ListProductsByWorkspaceParams{
			WorkspaceID: workspaceUUID(workspaceID), Limit: adsReadEntityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		phraseRows, err = s.queries.ListPhrasesByWorkspace(gctx, sqlcgen.ListPhrasesByWorkspaceParams{
			WorkspaceID: workspaceUUID(workspaceID), Limit: adsReadEntityLimit, Offset: 0,
		})
		return err
	})
	g.Go(func() error {
		var err error
		campaignStatRows, err = s.queries.ListCampaignStatsByWorkspaceDateRange(gctx, sqlcgen.ListCampaignStatsByWorkspaceDateRangeParams{
			WorkspaceID: workspaceUUID(workspaceID), DateFrom: dateFrom, DateTo: dateTo,
		})
		return err
	})
	g.Go(func() error {
		var err error
		phraseStatRows, err = s.queries.ListPhraseStatsByWorkspaceDateRange(gctx, sqlcgen.ListPhraseStatsByWorkspaceDateRangeParams{
			WorkspaceID: workspaceUUID(workspaceID), DateFrom: dateFrom, DateTo: dateTo,
		})
		return err
	})
	g.Go(func() error {
		ev, err := loadWorkspaceExtensionEvidence(gctx, s.queries, workspaceID, 5000) // reduced from 50K to 5K
		if err != nil {
			s.logger.Warn().Err(err).Str("workspace_id", workspaceID.String()).Msg("extension evidence load failed")
			return nil // non-fatal
		}
		extensionEvidence = ev
		return nil
	})
	g.Go(func() error {
		lastAutoSync = s.latestWorkspaceAutoSync(gctx, workspaceID)
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, apperror.New(apperror.ErrInternal, fmt.Sprintf("load workspace data: %v", err))
	}

	// Assemble data structure
	data := &adsWorkspaceData{
		cabinets:          make(map[uuid.UUID]domain.SellerCabinet, len(cabinetRows)),
		campaigns:         make([]domain.Campaign, 0, len(campaignRows)),
		products:          make([]domain.Product, 0, len(productRows)),
		phrases:           make([]domain.Phrase, 0, len(phraseRows)),
		campaignStatsByID: make(map[uuid.UUID][]domain.CampaignStat, len(campaignRows)),
		phraseStatsByID:   make(map[uuid.UUID][]domain.PhraseStat, len(phraseRows)),
		campaignProducts:  make(map[uuid.UUID][]domain.Product),
		productCampaigns:  make(map[uuid.UUID][]domain.Campaign),
		campaignPhrases:   make(map[uuid.UUID][]domain.Phrase),
		lastAutoSync:      lastAutoSync,
		extensionEvidence: &workspaceExtensionEvidence{},
	}
	if extensionEvidence != nil {
		data.extensionEvidence = extensionEvidence
	}

	for _, row := range cabinetRows {
		cabinet := sellerCabinetFromSqlc(row)
		cabinet.LastAutoSync = data.lastAutoSync
		data.cabinets[cabinet.ID] = cabinet
	}
	for _, row := range campaignRows {
		data.campaigns = append(data.campaigns, campaignFromSqlc(row))
	}
	for _, row := range productRows {
		data.products = append(data.products, productFromSqlc(row))
	}
	for _, row := range phraseRows {
		phrase := phraseFromSqlc(row)
		data.phrases = append(data.phrases, phrase)
		data.campaignPhrases[phrase.CampaignID] = append(data.campaignPhrases[phrase.CampaignID], phrase)
	}
	for _, row := range campaignStatRows {
		stat := campaignStatFromSqlc(row)
		data.campaignStatsByID[stat.CampaignID] = append(data.campaignStatsByID[stat.CampaignID], stat)
	}
	for _, row := range phraseStatRows {
		stat := phraseStatFromSqlc(row)
		data.phraseStatsByID[stat.PhraseID] = append(data.phraseStatsByID[stat.PhraseID], stat)
	}

	s.attachCampaignProducts(ctx, data, workspaceID)

	// Store in cache
	s.cacheMu.Lock()
	s.dataCache[cacheKey] = cachedWorkspaceData{data: data, loadedAt: time.Now()}
	// Evict old entries
	if len(s.dataCache) > 50 {
		for k, v := range s.dataCache {
			if time.Since(v.loadedAt) > 60*time.Second {
				delete(s.dataCache, k)
			}
		}
	}
	s.cacheMu.Unlock()

	return data, nil
}

// attachCampaignProducts links products to campaigns using local DB data only.
// Uses seller_cabinet_id as the join key (products and campaigns from the same cabinet are related).
// This avoids live WB API calls on every read request (audit fix: CRITICAL — was O(cabinets) HTTP calls per read).
func (s *AdsReadService) attachCampaignProducts(_ context.Context, data *adsWorkspaceData, _ uuid.UUID) {
	// Group products by cabinet for fast lookup
	productsByCabinet := make(map[uuid.UUID][]domain.Product)
	for _, product := range data.products {
		productsByCabinet[product.SellerCabinetID] = append(productsByCabinet[product.SellerCabinetID], product)
	}

	// Link campaigns to products in the same cabinet
	for _, campaign := range data.campaigns {
		cabinetProducts := productsByCabinet[campaign.SellerCabinetID]
		for _, product := range cabinetProducts {
			data.campaignProducts[campaign.ID] = append(data.campaignProducts[campaign.ID], product)
			data.productCampaigns[product.ID] = append(data.productCampaigns[product.ID], campaign)
		}
	}
}

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
		campaigns := dedupeCampaigns(data.productCampaigns[product.ID])
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

		relatedProducts := dedupeProducts(data.campaignProducts[campaign.ID])
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

		relatedProducts := dedupeProducts(data.campaignProducts[campaign.ID])
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
		result = append(result, s.buildQuerySummary(data, phrase, campaign, dedupeProducts(data.campaignProducts[campaign.ID]), dateFrom, dateTo))
	}

	sortQuerySummaries(result)
	return result
}

func (s *AdsReadService) buildQuerySummary(data *adsWorkspaceData, phrase domain.Phrase, campaign domain.Campaign, relatedProducts []domain.Product, dateFrom, dateTo time.Time) domain.QueryPerformanceSummary {
	cabinet := data.cabinets[campaign.SellerCabinetID]
	metrics := aggregatePhraseStats(data.phraseStatsByID[phrase.ID], dateFrom, dateTo)
	metrics.DataMode = "exact"
	previousFrom, previousTo := previousPeriodRange(dateFrom, dateTo)
	previousMetrics := aggregatePhraseStats(data.phraseStatsByID[phrase.ID], previousFrom, previousTo)
	previousMetrics.DataMode = "exact"
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
	if len(campaigns) == 0 {
		return domain.AdsMetricsSummary{DataMode: "unavailable"}, nil
	}

	metrics := domain.AdsMetricsSummary{}
	mode := "exact"
	for _, campaign := range campaigns {
		if len(data.campaignProducts[campaign.ID]) > 1 {
			mode = "shared"
		}
		campaignMetrics := aggregateCampaignStats(data.campaignStatsByID[campaign.ID], dateFrom, dateTo)
		metrics.Impressions += campaignMetrics.Impressions
		metrics.Clicks += campaignMetrics.Clicks
		metrics.Spend += campaignMetrics.Spend
		metrics.Orders += campaignMetrics.Orders
		metrics.Revenue += campaignMetrics.Revenue
	}
	metrics = finalizeMetrics(metrics, mode)
	if mode != "shared" {
		return metrics, nil
	}
	note := "Метрики товара рассчитаны по связанным кампаниям. Если в кампании несколько товаров, расход и выручка считаются как shared campaign data."
	return metrics, &note
}

// aggregateCampaignStats sums all campaign stats.
// Date filtering is already done at SQL level — no Go-level filtering needed (was causing double-filter bug).
func aggregateCampaignStats(stats []domain.CampaignStat, _, _ time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	for _, stat := range stats {
		result.Impressions += stat.Impressions
		result.Clicks += stat.Clicks
		result.Spend += stat.Spend
		if stat.Orders != nil {
			result.Orders += *stat.Orders
		}
		if stat.Revenue != nil {
			result.Revenue += *stat.Revenue
		}
	}
	return finalizeMetrics(result, "exact")
}

// aggregatePhraseStats sums all phrase stats.
// Date filtering is already done at SQL level.
func aggregatePhraseStats(stats []domain.PhraseStat, _, _ time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	for _, stat := range stats {
		result.Impressions += stat.Impressions
		result.Clicks += stat.Clicks
		result.Spend += stat.Spend
	}
	return finalizeMetrics(result, "exact")
}

// filterCampaignStatsByCabinet returns only campaign stats for campaigns belonging to the specified cabinet.
func filterCampaignStatsByCabinet(data *adsWorkspaceData, cabinetID uuid.UUID) map[uuid.UUID][]domain.CampaignStat {
	cabinetCampaigns := make(map[uuid.UUID]struct{})
	for _, c := range data.campaigns {
		if c.SellerCabinetID == cabinetID {
			cabinetCampaigns[c.ID] = struct{}{}
		}
	}
	filtered := make(map[uuid.UUID][]domain.CampaignStat, len(cabinetCampaigns))
	for campaignID, stats := range data.campaignStatsByID {
		if _, ok := cabinetCampaigns[campaignID]; ok {
			filtered[campaignID] = stats
		}
	}
	return filtered
}

func aggregateWorkspaceMetrics(statsByCampaign map[uuid.UUID][]domain.CampaignStat, dateFrom, dateTo time.Time) domain.AdsMetricsSummary {
	result := domain.AdsMetricsSummary{}
	for _, stats := range statsByCampaign {
		summary := aggregateCampaignStats(stats, dateFrom, dateTo)
		result.Impressions += summary.Impressions
		result.Clicks += summary.Clicks
		result.Spend += summary.Spend
		result.Orders += summary.Orders
		result.Revenue += summary.Revenue
	}
	return finalizeMetrics(result, "exact")
}

func finalizeMetrics(metrics domain.AdsMetricsSummary, mode string) domain.AdsMetricsSummary {
	metrics.DataMode = mode
	if metrics.Impressions > 0 {
		metrics.CTR = float64(metrics.Clicks) / float64(metrics.Impressions)
	}
	if metrics.Clicks > 0 {
		metrics.CPC = float64(metrics.Spend) / float64(metrics.Clicks)
		metrics.ConversionRate = float64(metrics.Orders) / float64(metrics.Clicks)
	}
	if metrics.Orders > 0 {
		metrics.CPO = float64(metrics.Spend) / float64(metrics.Orders)
	}
	if metrics.Spend > 0 {
		metrics.ROAS = float64(metrics.Revenue) / float64(metrics.Spend)
	}
	if metrics.Revenue > 0 {
		metrics.DRR = float64(metrics.Spend) / float64(metrics.Revenue) * 100
	}
	return metrics
}

func previousPeriodRange(dateFrom, dateTo time.Time) (time.Time, time.Time) {
	duration := dateTo.Sub(dateFrom) + 24*time.Hour
	previousTo := dateFrom.Add(-24 * time.Hour)
	previousFrom := previousTo.Add(-duration + 24*time.Hour)
	return previousFrom, previousTo
}

func buildPeriodCompare(current, previous domain.AdsMetricsSummary) *domain.AdsPeriodCompare {
	return &domain.AdsPeriodCompare{
		Current:  current,
		Previous: previous,
		Delta: domain.AdsMetricsDelta{
			Impressions:    current.Impressions - previous.Impressions,
			Clicks:         current.Clicks - previous.Clicks,
			Spend:          current.Spend - previous.Spend,
			Orders:         current.Orders - previous.Orders,
			Revenue:        current.Revenue - previous.Revenue,
			CTR:            current.CTR - previous.CTR,
			CPC:            current.CPC - previous.CPC,
			CPO:            current.CPO - previous.CPO,
			ROAS:           current.ROAS - previous.ROAS,
			ConversionRate: current.ConversionRate - previous.ConversionRate,
		},
		Trend: deriveMetricsTrend(current, previous),
	}
}

func deriveMetricsTrend(current, previous domain.AdsMetricsSummary) string {
	if current.Impressions == 0 && current.Clicks == 0 && current.Spend == 0 && current.Orders == 0 && current.Revenue == 0 {
		if previous.Impressions == 0 && previous.Clicks == 0 && previous.Spend == 0 && previous.Orders == 0 && previous.Revenue == 0 {
			return "flat"
		}
		return "declining"
	}
	if previous.Impressions == 0 && previous.Clicks == 0 && previous.Spend == 0 && previous.Orders == 0 && previous.Revenue == 0 {
		return "new"
	}

	revenueDelta := current.Revenue - previous.Revenue
	orderDelta := current.Orders - previous.Orders
	spendDelta := current.Spend - previous.Spend
	ctrDelta := current.CTR - previous.CTR

	switch {
	case revenueDelta > 0 && orderDelta >= 0 && spendDelta <= revenueDelta:
		return "improving"
	case revenueDelta < 0 || orderDelta < 0 || (spendDelta > 0 && revenueDelta <= 0):
		return "declining"
	case ctrDelta > 0.01 || ctrDelta < -0.01:
		return "volatile"
	default:
		return "flat"
	}
}

func buildCampaignRefs(campaigns []domain.Campaign) []domain.AdsEntityRef {
	result := make([]domain.AdsEntityRef, 0, len(campaigns))
	for _, campaign := range campaigns {
		wbID := campaign.WBCampaignID
		result = append(result, domain.AdsEntityRef{
			ID:    campaign.ID,
			Label: campaign.Name,
			WBID:  &wbID,
		})
	}
	return result
}

func buildProductRefs(products []domain.Product) []domain.AdsEntityRef {
	result := make([]domain.AdsEntityRef, 0, len(products))
	for _, product := range products {
		wbID := product.WBProductID
		result = append(result, domain.AdsEntityRef{
			ID:    product.ID,
			Label: product.Title,
			WBID:  &wbID,
		})
	}
	return result
}

func buildQuerySummaryRefs(items []domain.QueryPerformanceSummary) []domain.AdsEntityRef {
	result := make([]domain.AdsEntityRef, 0, len(items))
	for _, item := range items {
		wbID := item.WBClusterID
		result = append(result, domain.AdsEntityRef{
			ID:     item.ID,
			Label:  item.Keyword,
			WBID:   &wbID,
			Count:  item.ClusterSize,
			Source: item.SignalCategory,
		})
	}
	return result
}

func dedupeCampaigns(items []domain.Campaign) []domain.Campaign {
	seen := make(map[uuid.UUID]struct{}, len(items))
	result := make([]domain.Campaign, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}

func dedupeProducts(items []domain.Product) []domain.Product {
	seen := make(map[uuid.UUID]struct{}, len(items))
	result := make([]domain.Product, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}

func dedupePhrases(items []domain.Phrase) []domain.Phrase {
	seen := make(map[uuid.UUID]struct{}, len(items))
	result := make([]domain.Phrase, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		result = append(result, item)
	}
	return result
}

func containsProductID(products []domain.Product, productID uuid.UUID) bool {
	for _, product := range products {
		if product.ID == productID {
			return true
		}
	}
	return false
}

func sortProductSummaries(items []domain.ProductAdsSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Performance.Spend == items[j].Performance.Spend {
			return items[i].Title < items[j].Title
		}
		return items[i].Performance.Spend > items[j].Performance.Spend
	})
}

func sortCampaignSummaries(items []domain.CampaignPerformanceSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Performance.Spend == items[j].Performance.Spend {
			return items[i].Name < items[j].Name
		}
		return items[i].Performance.Spend > items[j].Performance.Spend
	})
}

func sortQuerySummaries(items []domain.QueryPerformanceSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].PriorityScore != items[j].PriorityScore {
			return items[i].PriorityScore > items[j].PriorityScore
		}
		leftRank := querySignalRank(items[i].SignalCategory)
		rightRank := querySignalRank(items[j].SignalCategory)
		if leftRank == rightRank {
			if items[i].Performance.Spend == items[j].Performance.Spend {
				if items[i].Performance.Impressions == items[j].Performance.Impressions {
					return items[i].Keyword < items[j].Keyword
				}
				return items[i].Performance.Impressions > items[j].Performance.Impressions
			}
			return items[i].Performance.Spend > items[j].Performance.Spend
		}
		return leftRank > rightRank
	})
}

func scoreQueryPriority(signalCategory string, metrics domain.AdsMetricsSummary, compare *domain.AdsPeriodCompare) int {
	score := 0

	switch signalCategory {
	case "waste":
		score += 80
	case "promising":
		score += 65
	case "high_volume":
		score += 50
	case "monitor":
		score += 20
	default:
		score += 10
	}

	if metrics.Spend >= 1000 {
		score += 20
	} else if metrics.Spend >= 500 {
		score += 10
	}
	if metrics.Impressions >= 1000 {
		score += 10
	} else if metrics.Impressions >= 300 {
		score += 5
	}
	if metrics.Clicks == 0 && metrics.Impressions >= 200 {
		score += 15
	}
	if compare != nil {
		switch compare.Trend {
		case "declining":
			score += 12
		case "improving":
			score += 8
		case "new":
			score += 6
		case "volatile":
			score += 4
		}
	}

	if score > 100 {
		return 100
	}
	return score
}

func matchesProductView(view, healthStatus string, compare *domain.AdsPeriodCompare) bool {
	switch view {
	case "", "all":
		return true
	case "scale":
		return healthStatus == "growing" || (compare != nil && (compare.Trend == "improving" || compare.Trend == "new"))
	case "save":
		return healthStatus == "waste" || healthStatus == "low_ctr" || (compare != nil && compare.Trend == "declining")
	case "watch":
		return healthStatus == "monitor" || healthStatus == "partial" || healthStatus == "insufficient_data"
	default:
		return true
	}
}

func matchesCampaignView(view, status, healthStatus string, compare *domain.AdsPeriodCompare) bool {
	switch view {
	case "", "all":
		return true
	case "profitable":
		return healthStatus == "growing" || (compare != nil && compare.Trend == "improving")
	case "waste":
		return healthStatus == "waste" || healthStatus == "low_ctr"
	case "stale":
		return healthStatus == "stale" || status == "paused" || (compare != nil && compare.Trend == "declining")
	case "watch":
		return healthStatus == "monitor" || healthStatus == "partial"
	default:
		return true
	}
}

func matchesQueryView(view string, summary domain.QueryPerformanceSummary) bool {
	switch view {
	case "", "all":
		return true
	case "priority":
		return summary.PriorityScore >= 40
	case "waste":
		return summary.SignalCategory == "waste"
	case "promising":
		return summary.SignalCategory == "promising"
	case "high_volume":
		return summary.SignalCategory == "high_volume"
	case "watch":
		return summary.SignalCategory == "monitor"
	default:
		return true
	}
}

func trimAttention(items []domain.AttentionItem, limit int) []domain.AttentionItem {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func trimProductSummaries(items []domain.ProductAdsSummary, limit int) []domain.ProductAdsSummary {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func trimCampaignSummaries(items []domain.CampaignPerformanceSummary, limit int) []domain.CampaignPerformanceSummary {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func trimQuerySummaries(items []domain.QueryPerformanceSummary, limit int) []domain.QueryPerformanceSummary {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func selectQuerySummariesBySignal(items []domain.QueryPerformanceSummary, signal string, limit int) []domain.QueryPerformanceSummary {
	filtered := make([]domain.QueryPerformanceSummary, 0, len(items))
	for _, item := range items {
		if item.SignalCategory == signal {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) <= limit {
		return filtered
	}
	return filtered[:limit]
}

func querySignalRank(value string) int {
	switch value {
	case "waste":
		return 4
	case "promising":
		return 3
	case "high_volume":
		return 2
	case "monitor":
		return 1
	default:
		return 0
	}
}

func classifyProductHealth(metrics domain.AdsMetricsSummary, campaignsCount, queriesCount int) (string, *string, *string) {
	switch {
	case metrics.DataMode == "unavailable" || campaignsCount == 0:
		return "insufficient_data", stringPtr("Товар пока не имеет устойчивой связки с кампаниями и рекламной статистикой."), stringPtr("Проверьте привязку товара к кампаниям и дождитесь следующего auto-sync.")
	case metrics.Spend >= 3000 && metrics.Orders == 0:
		return "waste", stringPtr(fmt.Sprintf("Товар потратил %d ₽ без заказов за выбранный период.", metrics.Spend)), stringPtr("Откройте связанные кампании и сократите расходные запросы.")
	case metrics.Impressions >= 1000 && metrics.Clicks == 0:
		return "low_ctr", stringPtr("Товар получает показы, но не собирает клики."), stringPtr("Проверьте релевантность запросов, фото и заголовок карточки.")
	case metrics.Orders >= 3 && metrics.Revenue > metrics.Spend*5:
		return "growing", stringPtr("Товар даёт заказы и окупает рекламный расход."), stringPtr("Масштабируйте сильные кампании и следите за маржинальностью.")
	case queriesCount == 0 && metrics.Spend > 0:
		return "partial", stringPtr("По товару есть расход, но запросный слой ещё не доехал полностью."), stringPtr("Откройте Jobs и проверьте полноту sync по normquery и phrase stats.")
	default:
		return "monitor", stringPtr("Товар участвует в рекламе и требует регулярного мониторинга."), stringPtr("Следите за расходом, заказами и сильными запросами.")
	}
}

func classifyCampaignHealth(metrics domain.AdsMetricsSummary, productsCount, queriesCount int, status string) (string, *string, *string) {
	switch {
	case status == "paused" && metrics.Spend > 0:
		return "stale", stringPtr("Кампания уже не активна, но остаётся важной в историческом анализе."), stringPtr("Решите, стоит ли вернуть кампанию в работу или оставить её как reference.")
	case metrics.Spend >= 2000 && metrics.Orders == 0:
		return "waste", stringPtr(fmt.Sprintf("Кампания потратила %d ₽ без заказов за период.", metrics.Spend)), stringPtr("Откройте проблемные запросы и сократите неэффективный расход.")
	case metrics.Impressions >= 1000 && metrics.Clicks == 0:
		return "low_ctr", stringPtr("Кампания получает показы, но почти не вовлекает трафик."), stringPtr("Проверьте тип кампании, запросы и креативную релевантность карточки.")
	case metrics.Orders >= 3 && metrics.Revenue > metrics.Spend*5:
		return "growing", stringPtr("Кампания даёт заказы и хорошую отдачу на рекламный расход."), stringPtr("Масштабируйте сильные запросы без потери marginal ROAS.")
	case productsCount == 0 || queriesCount == 0:
		return "partial", stringPtr("Кампанийный read-model пока не полностью связан с товарами или запросами."), stringPtr("Используйте Jobs как источник доверия к данным и дождитесь полного sync.")
	default:
		return "monitor", stringPtr("Кампания работает в штатном режиме и требует регулярного мониторинга."), stringPtr("Смотрите на сильные и расходные запросы за выбранный период.")
	}
}

func classifyQuerySignal(phrase domain.Phrase, metrics domain.AdsMetricsSummary) (string, string, *string, *string) {
	switch {
	case metrics.Spend >= 500 && metrics.Clicks >= 5 && metrics.CTR < 0.01:
		return "waste", "waste", stringPtr("Запрос уже тратит бюджет, но даёт слабое вовлечение."), stringPtr("Снизьте ставку или уберите запрос из активного приоритета.")
	case metrics.Impressions >= 200 && metrics.Clicks == 0:
		return "waste", "low_ctr", stringPtr("Запрос получает показы без кликов."), stringPtr("Проверьте релевантность карточки и не держите ставку выше необходимой.")
	case metrics.Clicks >= 10 && metrics.CTR >= 0.03:
		return "promising", "growing", stringPtr("Запрос стабильно собирает клики и выглядит перспективно."), stringPtr("Проверьте кампанию и усиливайте сильный спрос аккуратно.")
	case phrase.Count != nil && *phrase.Count >= 200:
		return "high_volume", "monitor", stringPtr("Это объёмный запросный кластер, за которым стоит следить отдельно."), stringPtr("Сверяйте CTR и расход, чтобы не сливать бюджет на объёме.")
	default:
		return "monitor", "monitor", stringPtr("Запрос пока не даёт сильного сигнала в одну сторону."), stringPtr("Оставьте запрос в наблюдении и сравните следующий период.")
	}
}

func freshnessStateFromSync(sync *domain.SellerCabinetAutoSyncSummary) string {
	if sync == nil || sync.FinishedAt == nil {
		return "unknown"
	}
	return freshnessState(*sync.FinishedAt)
}

func freshnessState(finishedAt time.Time) string {
	age := time.Since(finishedAt)
	switch {
	case age <= 24*time.Hour:
		return "fresh"
	case age <= 72*time.Hour:
		return "aging"
	default:
		return "stale"
	}
}

func countActiveCampaigns(items []domain.CampaignPerformanceSummary) int {
	count := 0
	for _, item := range items {
		if item.Status == "active" {
			count++
		}
	}
	return count
}

func attentionSeverityRank(value string) int {
	switch value {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func cabinetCampaignKey(cabinetID uuid.UUID, wbCampaignID int64) string {
	return cabinetID.String() + ":" + fmt.Sprintf("%d", wbCampaignID)
}

func cabinetProductKey(cabinetID uuid.UUID, wbProductID int64) string {
	return cabinetID.String() + ":" + fmt.Sprintf("%d", wbProductID)
}

func workspaceUUID(workspaceID uuid.UUID) pgtype.UUID {
	return uuidToPgtype(workspaceID)
}

func stringPtr(value string) *string {
	return &value
}

func (s *AdsReadService) latestWorkspaceAutoSync(ctx context.Context, workspaceID uuid.UUID) *domain.SellerCabinetAutoSyncSummary {
	rows, err := s.queries.ListJobRunsByWorkspace(ctx, sqlcgen.ListJobRunsByWorkspaceParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Limit:          1,
		Offset:         0,
		TaskTypeFilter: textToPgtype("wb:sync_workspace"),
	})
	if err != nil || len(rows) == 0 {
		return nil
	}

	row := rows[0]
	metadata := decodeJobRunMetadata(row.Metadata)
	resultState := metadataString(metadata, "result_state")
	summary := &domain.SellerCabinetAutoSyncSummary{
		JobRunID:       uuidFromPgtype(row.ID),
		Status:         row.Status,
		ResultState:    resultState,
		FreshnessState: "unknown",
		Cabinets:       metadataInt(metadata, "cabinets"),
		Campaigns:      metadataInt(metadata, "campaigns"),
		CampaignStats:  metadataInt(metadata, "campaign_stats"),
		Phrases:        metadataInt(metadata, "phrases"),
		PhraseStats:    metadataInt(metadata, "phrase_stats"),
		Products:       metadataInt(metadata, "products"),
		SyncIssues:     metadataInt(metadata, "sync_issues"),
	}
	if row.FinishedAt.Valid {
		finishedAt := row.FinishedAt.Time
		summary.FinishedAt = &finishedAt
		summary.FreshnessState = freshnessState(finishedAt)
	}
	return summary
}

type adsDecryptedCabinet struct {
	cabinet domain.SellerCabinet
	token   string
}

func (s *AdsReadService) listWorkspaceCabinets(ctx context.Context, workspaceID uuid.UUID) ([]adsDecryptedCabinet, error) {
	rows, err := s.queries.ListActiveSellerCabinetsByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list active seller cabinets")
	}

	result := make([]adsDecryptedCabinet, 0, len(rows))
	for _, row := range rows {
		cabinet := sellerCabinetFromSqlc(row)
		token, decryptErr := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
		if decryptErr != nil {
			s.logger.Warn().
				Err(decryptErr).
				Str("workspace_id", workspaceID.String()).
				Str("seller_cabinet_id", cabinet.ID.String()).
				Msg("skipping seller cabinet with undecryptable token in ads read")
			continue
		}
		result = append(result, adsDecryptedCabinet{
			cabinet: cabinet,
			token:   token,
		})
	}
	return result, nil
}
