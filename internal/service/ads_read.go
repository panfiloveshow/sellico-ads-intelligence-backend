package service

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/sync/singleflight"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// Defaults applied when AdsReadService is constructed without explicit limits
// (e.g. legacy callers, tests). Production callers override these via
// config.AdsReadEntityLimit / AdsReadStatsLimit so ops can tune without a
// code change. The values were tuned during the OOM incident — see
// commit 15b161a — and represent a known-safe ceiling for a 1G-RSS api.
const (
	defaultAdsReadEntityLimit = int32(5000)
	defaultAdsReadStatsLimit  = int32(20000)
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

	entityLimit int32
	statsLimit  int32

	cacheMu   sync.RWMutex
	dataCache map[string]cachedWorkspaceData
	loadGroup singleflight.Group
}

// AdsReadOption configures non-required AdsReadService settings.
type AdsReadOption func(*AdsReadService)

// WithAdsReadLimits overrides the per-query entity / stats caps. Pass 0 for
// either to keep the default. Used by main() to wire env-driven config without
// breaking existing test callers of NewAdsReadService.
func WithAdsReadLimits(entityLimit, statsLimit int) AdsReadOption {
	return func(s *AdsReadService) {
		if entityLimit > 0 {
			s.entityLimit = int32(entityLimit)
		}
		if statsLimit > 0 {
			s.statsLimit = int32(statsLimit)
		}
	}
}

func NewAdsReadService(queries *sqlcgen.Queries, wbClient WBSyncClient, encryptionKey []byte, logger zerolog.Logger, opts ...AdsReadOption) *AdsReadService {
	s := &AdsReadService{
		queries:       queries,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "ads_read_service").Logger(),
		dataCache:     make(map[string]cachedWorkspaceData),
		entityLimit:   defaultAdsReadEntityLimit,
		statsLimit:    defaultAdsReadStatsLimit,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type adsWorkspaceData struct {
	cabinets           map[uuid.UUID]domain.SellerCabinet
	campaigns          []domain.Campaign
	products           []domain.Product
	phrases            []domain.Phrase
	campaignStatsByID  map[uuid.UUID][]domain.CampaignStat
	productStatsByID   map[uuid.UUID][]domain.ProductStat
	phraseStatsByID    map[uuid.UUID][]domain.PhraseStat
	campaignProductIDs map[uuid.UUID][]uuid.UUID
	productCampaignIDs map[uuid.UUID][]uuid.UUID
	campaignPhrases    map[uuid.UUID][]domain.Phrase
	lastAutoSync       *domain.SellerCabinetAutoSyncSummary
	extensionEvidence  *workspaceExtensionEvidence
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
