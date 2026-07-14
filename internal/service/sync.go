package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	syncBatchLimit             = int32(10000)
	campaignListFetchTimeout   = 150 * time.Second
	campaignBudgetFetchTimeout = 10 * time.Second
	businessReportFetchTimeout = 25 * time.Second
	wbAdvertStatsRequestDelay  = 20 * time.Second
	maxCampaignBudgetFailures  = 3
	// Budgets are a best-effort secondary signal. Cap the whole budget phase so a
	// slow WB budget endpoint on large accounts (hundreds of campaigns) can't eat
	// the entire 60-minute sync window and starve the primary data (stats/phrases/
	// products). Whatever isn't fetched this run refreshes on the next sync.
	campaignBudgetPhaseTimeout = 4 * time.Minute
)

const (
	SyncPhaseCampaigns       = "campaigns"
	SyncPhaseStats           = "stats"
	SyncPhasePhrases         = "phrases"
	SyncPhaseProducts        = "products"
	SyncPhaseBudgets         = "budgets"
	SyncPhaseFinance         = "finance"
	SyncPhaseSalesFunnel     = "sales_funnel"
	SyncPhaseBusinessReports = "business_reports"
	SyncPhaseTariffs         = "tariffs"
)

type WBSyncClient interface {
	ListPromotionCounts(ctx context.Context, token string) ([]wb.WBPromotionCountDTO, error)
	ListCampaigns(ctx context.Context, token string) ([]wb.WBCampaignDTO, error)
	GetCampaignStats(ctx context.Context, token string, campaignIDs []int, dateFrom, dateTo string) ([]wb.WBCampaignStatDTO, error)
	GetBalance(ctx context.Context, token string) (*wb.WBBalanceDTO, error)
	GetCampaignBudget(ctx context.Context, token string, wbCampaignID int64) (*wb.WBBudgetDTO, error)
	GetUPDDocuments(ctx context.Context, token string) ([]wb.WBFinanceDocumentDTO, error)
	GetPayments(ctx context.Context, token string) ([]wb.WBFinanceDocumentDTO, error)
	GetSupplierOrders(ctx context.Context, token, dateFrom string, flag int) ([]wb.WBOrderReportDTO, error)
	GetSupplierSales(ctx context.Context, token, dateFrom string, flag int) ([]wb.WBSaleReportDTO, error)
	ListSearchClusters(ctx context.Context, token string, campaignID int) ([]wb.WBSearchClusterDTO, error)
	ListSearchClustersWithNMIDs(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]wb.WBSearchClusterDTO, error)
	ListLegacyNormQueryClusters(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]wb.WBSearchClusterDTO, error)
	GetSearchClusterStats(ctx context.Context, token string, campaignID int) ([]wb.WBSearchClusterStatDTO, error)
	GetSearchClusterStatsWithNMIDs(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]wb.WBSearchClusterStatDTO, error)
	DebugNormQueryStats(ctx context.Context, token string, campaignID int, nmIDs []int64, dateFrom, dateTo string) (wb.WBNormQueryStatsDebug, error)
	ListProducts(ctx context.Context, token string) ([]wb.WBProductDTO, error)
	GetSalesFunnelProductsV3(ctx context.Context, token string, params wb.SalesFunnelParams) ([]wb.WBSalesFunnelProductDTO, error)
	GetCommissionTariffs(ctx context.Context, token string, locale string) ([]wb.WBCommissionTariffDTO, error)
}

type SyncSummary struct {
	Cabinets          int         `json:"cabinets"`
	Campaigns         int         `json:"campaigns"`
	CampaignStats     int         `json:"campaign_stats"`
	Phrases           int         `json:"phrases"`
	PhraseStats       int         `json:"phrase_stats"`
	Products          int         `json:"products"`
	ProductStats      int         `json:"product_stats"`
	CampaignBudgets   int         `json:"campaign_budgets"`
	AdBalances        int         `json:"ad_balances"`
	FinanceDocs       int         `json:"finance_docs"`
	SalesFunnel       int         `json:"sales_funnel"`
	BusinessOrders    int         `json:"business_orders"`
	BusinessSales     int         `json:"business_sales"`
	CommissionTariffs int         `json:"commission_tariffs"`
	SkippedCampaign   int         `json:"skipped_campaigns"`
	WBErrors          int         `json:"wb_errors"`
	RateLimited       int         `json:"rate_limited"`
	RateLimitEndpoint string      `json:"rate_limit_endpoint,omitempty"`
	RetryAfterSeconds int         `json:"retry_after_seconds,omitempty"`
	NextAllowedAt     *time.Time  `json:"next_allowed_at,omitempty"`
	DateFrom          string      `json:"date_from,omitempty"`
	DateTo            string      `json:"date_to,omitempty"`
	Issues            []SyncIssue `json:"issues,omitempty"`
}

type SyncIssue struct {
	Stage    string `json:"stage"`
	EntityID string `json:"entity_id,omitempty"`
	Message  string `json:"message"`
}

func (s *SyncSummary) addIssue(stage, entityID, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	s.Issues = append(s.Issues, SyncIssue{
		Stage:    stage,
		EntityID: entityID,
		Message:  message,
	})
	// Only count as a WB API error when it isn't a local persistence/parse failure —
	// otherwise a "campaigns.upsert"/"stats.map" DB error trips the WB-error guardrail
	// and disables bid automation for the whole workspace.
	// ponytail: stage-suffix heuristic; replace with a typed wb.APIError{StatusCode} when
	// the WB client stops flattening status codes into message strings.
	if isWBSyncIssue(message) && !isLocalPersistenceStage(stage) {
		s.WBErrors++
	}
	if isRateLimitIssue(message) {
		s.RateLimited++
	}
}

func (s *SyncSummary) addRateLimitIssue(stage, entityID, endpoint string, nextAllowedAt time.Time, retryAfterSeconds int, format string, args ...any) {
	s.addIssue(stage, entityID, format, args...)
	s.RateLimitEndpoint = endpoint
	s.RetryAfterSeconds = retryAfterSeconds
	next := nextAllowedAt.UTC()
	s.NextAllowedAt = &next
}

func (s *SyncSummary) merge(other SyncSummary) {
	s.Cabinets += other.Cabinets
	s.Campaigns += other.Campaigns
	s.CampaignStats += other.CampaignStats
	s.Phrases += other.Phrases
	s.PhraseStats += other.PhraseStats
	s.Products += other.Products
	s.ProductStats += other.ProductStats
	s.CampaignBudgets += other.CampaignBudgets
	s.AdBalances += other.AdBalances
	s.FinanceDocs += other.FinanceDocs
	s.SalesFunnel += other.SalesFunnel
	s.BusinessOrders += other.BusinessOrders
	s.BusinessSales += other.BusinessSales
	s.CommissionTariffs += other.CommissionTariffs
	s.SkippedCampaign += other.SkippedCampaign
	s.WBErrors += other.WBErrors
	s.RateLimited += other.RateLimited
	if other.RateLimitEndpoint != "" {
		s.RateLimitEndpoint = other.RateLimitEndpoint
	}
	if other.RetryAfterSeconds > 0 {
		s.RetryAfterSeconds = other.RetryAfterSeconds
	}
	if other.NextAllowedAt != nil {
		s.NextAllowedAt = other.NextAllowedAt
	}
	if other.DateFrom != "" {
		s.DateFrom = other.DateFrom
	}
	if other.DateTo != "" {
		s.DateTo = other.DateTo
	}
	s.Issues = append(s.Issues, other.Issues...)
}

func (s *SyncSummary) mergeRateLimitFields(other SyncSummary) {
	if other.RateLimitEndpoint != "" {
		s.RateLimitEndpoint = other.RateLimitEndpoint
	}
	if other.RetryAfterSeconds > 0 {
		s.RetryAfterSeconds = other.RetryAfterSeconds
	}
	if other.NextAllowedAt != nil {
		s.NextAllowedAt = other.NextAllowedAt
	}
	if other.DateFrom != "" {
		s.DateFrom = other.DateFrom
	}
	if other.DateTo != "" {
		s.DateTo = other.DateTo
	}
}

type cabinetSyncSnapshot struct {
	campaignRows []sqlcgen.Campaign
	nmIDsByWBID  map[int][]int64
}

func (s SyncSummary) Error() error {
	if len(s.Issues) == 0 {
		return nil
	}

	parts := make([]string, 0, minInt(len(s.Issues), 5))
	for _, issue := range s.Issues[:minInt(len(s.Issues), 5)] {
		prefix := issue.Stage
		if issue.EntityID != "" {
			prefix = fmt.Sprintf("%s[%s]", issue.Stage, issue.EntityID)
		}
		parts = append(parts, fmt.Sprintf("%s: %s", prefix, issue.Message))
	}

	message := strings.Join(parts, "; ")
	if len(s.Issues) > 5 {
		message = fmt.Sprintf("%s; and %d more issues", message, len(s.Issues)-5)
	}
	return errors.New(message)
}

// isLocalPersistenceStage reports whether a sync stage is a write/parse into our own
// storage (not a WB API call), so its failures must not be classified as WB errors.
func isLocalPersistenceStage(stage string) bool {
	seg := stage
	if i := strings.LastIndex(stage, "."); i >= 0 {
		seg = stage[i+1:]
	}
	switch seg {
	case "upsert", "map", "parse", "identity", "date", "skipped",
		"cleanup", "stale_cleanup", "sync_mark", "fallback":
		return true
	}
	return false
}

func isWBSyncIssue(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "wb") ||
		strings.Contains(lower, "wildberries") ||
		strings.Contains(lower, "advert-api") ||
		strings.Contains(lower, "content-api") ||
		strings.Contains(lower, "campaign") ||
		strings.Contains(lower, "stats") ||
		strings.Contains(lower, "products") ||
		strings.Contains(lower, "phrases")
}

func isRateLimitIssue(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "429") ||
		strings.Contains(lower, "rate limited") ||
		strings.Contains(lower, "too many requests")
}

func isWBAdvertStatsEligibleStatus(status string) bool {
	switch status {
	case "completed", "active", "paused":
		return true
	default:
		return false
	}
}

func wbAdvertStatsDateRange(now time.Time) (string, string) {
	location, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		location = time.FixedZone("MSK", 3*60*60)
	}
	nowMSK := now.In(location)
	today := time.Date(nowMSK.Year(), nowMSK.Month(), nowMSK.Day(), 0, 0, 0, 0, location)
	// WB fullstats is refreshed intraday. Persist today's real, provisional
	// counters as well as completed days; later syncs upsert the same date.
	// If WB omits today, no invented zero row is created and automation stays
	// fail-closed because it cannot prove current-day activity.
	return today.AddDate(0, 0, -30).Format(exportDateLayout), today.Format(exportDateLayout)
}

type SyncService struct {
	queries       *sqlcgen.Queries
	wbClient      WBSyncClient
	encryptionKey []byte
	logger        zerolog.Logger
}

func NewSyncService(queries *sqlcgen.Queries, wbClient WBSyncClient, encryptionKey []byte, logger zerolog.Logger) *SyncService {
	return &SyncService{
		queries:       queries,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "sync_service").Logger(),
	}
}

// SyncWorkspace runs the complete cabinet pipeline for every connected WB
// cabinet. It intentionally reuses SyncSingleCabinet so scheduled and manual
// syncs have identical phase coverage and partial-error semantics.
func (s *SyncService) SyncWorkspace(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}
	var summary SyncSummary
	for _, cabinet := range cabinets {
		cabinetSummary, _ := s.SyncSingleCabinet(ctx, workspaceID, cabinet.cabinet.ID)
		summary.merge(cabinetSummary)
	}
	return summary, summary.Error()
}

func (s *SyncService) SyncSingleCabinetPhase(ctx context.Context, workspaceID, cabinetID uuid.UUID, phase string) (SyncSummary, error) {
	cabinets, err := s.listSingleCabinet(ctx, cabinetID)
	if err != nil {
		return SyncSummary{}, err
	}
	if len(cabinets) == 0 {
		return SyncSummary{}, nil
	}
	cabinet := cabinets[0]

	switch phase {
	case SyncPhaseCampaigns:
		return s.syncCampaignsForCabinet(ctx, workspaceID, cabinet)
	case SyncPhaseStats:
		snapshot, err := s.loadCabinetSnapshotFromDB(ctx, workspaceID, cabinetID)
		if err != nil {
			return SyncSummary{}, err
		}
		return s.syncCampaignStatsForCabinet(ctx, workspaceID, cabinetID, cabinet.token, snapshot.campaignRows)
	case SyncPhasePhrases:
		snapshot, err := s.loadCabinetSnapshotFromDB(ctx, workspaceID, cabinetID)
		if err != nil {
			return SyncSummary{}, err
		}
		return s.syncPhrasesForCabinet(ctx, workspaceID, cabinetID, cabinet.token, snapshot.campaignRows, snapshot.nmIDsByWBID)
	case SyncPhaseProducts:
		return s.syncProductsForCabinet(ctx, workspaceID, cabinetID, cabinet.token)
	case SyncPhaseBudgets:
		snapshot, err := s.loadCabinetSnapshotFromDB(ctx, workspaceID, cabinetID)
		if err != nil {
			return SyncSummary{}, err
		}
		return s.syncCampaignBudgetsForCabinet(ctx, cabinetID, cabinet.token, snapshot.campaignRows)
	case SyncPhaseFinance:
		return s.syncAdFinanceForCabinet(ctx, cabinetID, cabinet.token)
	case SyncPhaseSalesFunnel:
		snapshot, err := s.loadCabinetSnapshotFromDB(ctx, workspaceID, cabinetID)
		if err != nil {
			return SyncSummary{}, err
		}
		return s.syncSalesFunnelProductsForCabinet(ctx, workspaceID, cabinetID, cabinet.token, snapshot)
	case SyncPhaseBusinessReports:
		return s.syncBusinessReportsForCabinet(ctx, workspaceID, cabinetID, cabinet.token)
	case SyncPhaseTariffs:
		return s.syncCommissionTariffsForCabinet(ctx, workspaceID, cabinetID, cabinet.token)
	default:
		return SyncSummary{}, apperror.New(apperror.ErrValidation, fmt.Sprintf("unknown sync phase %q", phase))
	}
}

// SyncSingleCabinet syncs all required data phases for one cabinet and records
// a cabinet-scoped readiness result. Consumers can therefore fail closed on
// partial/stale cabinets instead of trusting a workspace-wide timestamp.
func (s *SyncService) SyncSingleCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (SyncSummary, error) {
	startedAt := time.Now().UTC()
	beginErr := s.queries.BeginSellerCabinetSync(ctx, sqlcgen.BeginSellerCabinetSyncParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		WorkspaceID:     uuidToPgtype(workspaceID),
		StartedAt:       pgtype.Timestamptz{Time: startedAt, Valid: true},
	})

	summary, runErr := s.syncSingleCabinet(ctx, workspaceID, cabinetID)
	if runErr != nil && len(summary.Issues) == 0 {
		summary.addIssue("seller_cabinet.sync", cabinetID.String(), "sync failed: %v", runErr)
	}
	if runErr == nil && summary.Cabinets == 0 {
		summary.addIssue("seller_cabinet.sync", cabinetID.String(), "cabinet is unavailable for sync")
	}
	if beginErr != nil {
		summary.addIssue("seller_cabinet.readiness", cabinetID.String(), "failed to mark sync running: %v", beginErr)
	}
	status := cabinetSyncReadinessStatus(summary, runErr)
	lastError := ""
	if runErr != nil {
		lastError = runErr.Error()
	}
	dataThrough := pgtype.Date{}
	if summary.DateTo != "" {
		if parsed, parseErr := time.Parse(exportDateLayout, summary.DateTo); parseErr == nil {
			dataThrough = pgtype.Date{Time: parsed, Valid: true}
		}
	}
	completeErr := s.queries.CompleteSellerCabinetSync(ctx, sqlcgen.CompleteSellerCabinetSyncParams{
		SellerCabinetID:   uuidToPgtype(cabinetID),
		Status:            status,
		CompletedAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		DataThroughDate:   dataThrough,
		IssueCount:        boundedInt32(len(summary.Issues)),
		WBErrorCount:      boundedInt32(summary.WBErrors),
		RateLimited:       summary.RateLimited > 0,
		RetryAfterSeconds: pgtype.Int4{Int32: boundedInt32(summary.RetryAfterSeconds), Valid: summary.RetryAfterSeconds > 0},
		LastError:         pgtype.Text{String: lastError, Valid: lastError != ""},
	})
	if completeErr != nil {
		summary.addIssue("seller_cabinet.readiness", cabinetID.String(), "failed to persist sync readiness: %v", completeErr)
	}
	return summary, summary.Error()
}

func cabinetSyncReadinessStatus(summary SyncSummary, runErr error) string {
	if runErr == nil && len(summary.Issues) == 0 {
		return "ready"
	}
	if summary.Campaigns+summary.CampaignStats+summary.Phrases+summary.PhraseStats+
		summary.Products+summary.ProductStats+summary.CampaignBudgets+summary.AdBalances+
		summary.FinanceDocs+summary.SalesFunnel+summary.BusinessOrders+summary.BusinessSales+
		summary.CommissionTariffs > 0 {
		return "partial"
	}
	return "failed"
}

func (s *SyncService) syncSingleCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (SyncSummary, error) {
	s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("[sync] loading cabinet token")
	cabinets, err := s.listSingleCabinet(ctx, cabinetID)
	if err != nil {
		s.logger.Error().Err(err).Str("cabinet_id", cabinetID.String()).Msg("[sync] failed to load cabinet")
		return SyncSummary{}, err
	}
	if len(cabinets) == 0 {
		s.logger.Warn().Str("cabinet_id", cabinetID.String()).Msg("[sync] no cabinets found")
		return SyncSummary{}, nil
	}
	s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("[sync] cabinet token loaded, starting sync")

	summary := SyncSummary{Cabinets: 1}

	// Campaigns
	var snapshot cabinetSyncSnapshot
	for _, cabinet := range cabinets {
		if s.guardWBEndpoint(ctx, &summary, cabinet.cabinet.ID, wbEndpointAdverts, "campaigns.rate_limit") {
			continue
		}
		campaigns, fetchErr := s.listCampaigns(ctx, cabinet.token)
		if fetchErr != nil {
			s.recordWBRateLimitFromError(ctx, cabinet.cabinet.ID, wbEndpointAdverts, fetchErr)
			s.markSummaryRateLimitFromError(&summary, wbEndpointAdverts, fetchErr)
			summary.addIssue("campaigns.list", cabinet.cabinet.ID.String(), "list campaigns: %v", fetchErr)
			continue
		}
		activeWBIDs := make([]int64, 0, len(campaigns))
		snapshot.campaignRows = make([]sqlcgen.Campaign, 0, len(campaigns))
		snapshot.nmIDsByWBID = make(map[int][]int64, len(campaigns))
		for _, dto := range campaigns {
			campaign := wb.MapCampaignDTO(dto, workspaceID, cabinet.cabinet.ID)
			if dto.PartialError != "" {
				summary.addIssue("campaigns.enrichment", fmt.Sprintf("%d", campaign.WBCampaignID), "%s", dto.PartialError)
			}
			activeWBIDs = append(activeWBIDs, campaign.WBCampaignID)
			if len(dto.NMIDs) > 0 {
				snapshot.nmIDsByWBID[dto.AdvertID] = append([]int64(nil), dto.NMIDs...)
			}
			row, upsertErr := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
				WorkspaceID:              uuidToPgtype(campaign.WorkspaceID),
				SellerCabinetID:          uuidToPgtype(campaign.SellerCabinetID),
				WbCampaignID:             campaign.WBCampaignID,
				Name:                     campaign.Name,
				Status:                   campaign.Status,
				CampaignType:             int32(campaign.CampaignType),
				BidType:                  campaign.BidType,
				PaymentType:              campaign.PaymentType,
				DailyBudget:              int64PtrToPgInt8(campaign.DailyBudget),
				PlacementSearch:          boolPtrToPgBool(campaign.PlacementSearch),
				PlacementRecommendations: boolPtrToPgBool(campaign.PlacementRecommendations),
				WbCreatedAt:              timePtrToPgtype(campaign.WBCreatedAt),
				WbStartedAt:              timePtrToPgtype(campaign.WBStartedAt),
				WbUpdatedAt:              timePtrToPgtype(campaign.WBUpdatedAt),
				WbDeletedAt:              timePtrToPgtype(campaign.WBDeletedAt),
			})
			if upsertErr != nil {
				summary.addIssue("campaigns.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "upsert: %v", upsertErr)
				continue
			}
			if linked, linkErr := s.upsertCampaignProductLinks(ctx, workspaceID, cabinet.cabinet.ID, row, dto.NMIDs, dto.Products); linkErr != nil {
				summary.addIssue("campaign_products.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "link products: %v", linkErr)
			} else if linked > 0 {
				s.logger.Debug().Int("links", linked).Int64("campaign_id", campaign.WBCampaignID).Msg("campaign products linked")
			}
			snapshot.campaignRows = append(snapshot.campaignRows, row)
			summary.Campaigns++
		}
		// Stale cleanup for single cabinet sync
		if staleCount, staleErr := s.queries.MarkStaleCampaigns(ctx, uuidToPgtype(cabinet.cabinet.ID), activeWBIDs); staleErr != nil {
			summary.addIssue("campaigns.stale_cleanup", cabinet.cabinet.ID.String(), "mark stale: %v", staleErr)
		} else if staleCount > 0 {
			s.logger.Info().Int64("stale_campaigns", staleCount).Str("cabinet_id", cabinet.cabinet.ID.String()).Msg("marked stale campaigns as deleted")
		}
		s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID))
	}

	if summary.RateLimited > 0 && summary.Campaigns == 0 {
		fallbackSnapshot, fallbackErr := s.loadCabinetSnapshotFromDB(ctx, workspaceID, cabinetID)
		if fallbackErr != nil {
			summary.addIssue("campaigns.fallback", cabinetID.String(), "load stored campaigns: %v", fallbackErr)
		} else if len(fallbackSnapshot.campaignRows) > 0 {
			snapshot = fallbackSnapshot
			summary.Campaigns = len(snapshot.campaignRows)
			s.logger.Warn().
				Str("cabinet_id", cabinetID.String()).
				Int("campaigns", len(snapshot.campaignRows)).
				Msg("[sync] using stored campaign snapshot after WB rate limited campaign list")
		} else {
			summary.addIssue("stats.skipped", cabinetID.String(), "skip advertising stats because WB rate limited campaign list and no stored campaigns")
			summary.addIssue("phrases.skipped", cabinetID.String(), "skip phrases because WB rate limited campaign list and no stored campaign products")
			s.logger.Warn().
				Str("cabinet_id", cabinetID.String()).
				Msg("[sync] skipping stats and phrases because WB rate limited campaign list")
			productsSummary, productsErr := s.syncProductsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
			if productsErr != nil {
				summary.addIssue("products", cabinetID.String(), "sync products: %v", productsErr)
			}
			summary.Products = productsSummary.Products
			reportsSummary, reportsErr := s.syncBusinessReportsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
			if reportsErr != nil {
				summary.addIssue("business_reports", cabinetID.String(), "sync business reports: %v", reportsErr)
			}
			summary.BusinessOrders = reportsSummary.BusinessOrders
			summary.BusinessSales = reportsSummary.BusinessSales
			return summary, summary.Error()
		}
	}

	// Stats — scoped to single cabinet
	s.logger.Info().Str("cabinet_id", cabinetID.String()).Int("campaigns", summary.Campaigns).Msg("[sync] phase 2: syncing stats")
	statsSummary, statsErr := s.syncCampaignStatsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token, snapshot.campaignRows)
	if statsErr != nil {
		s.logger.Error().Err(statsErr).Msg("[sync] stats failed")
		summary.addIssue("stats", cabinetID.String(), "sync stats: %v", statsErr)
	}
	summary.CampaignStats = statsSummary.CampaignStats
	summary.ProductStats = statsSummary.ProductStats
	summary.mergeRateLimitFields(statsSummary)
	s.logger.Info().Int("campaign_stats", summary.CampaignStats).Msg("[sync] phase 2 done")

	s.logger.Info().Msg("[sync] phase 3: syncing phrases")
	phrasesSummary, phrasesErr := s.syncPhrasesForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token, snapshot.campaignRows, snapshot.nmIDsByWBID)
	if phrasesErr != nil {
		s.logger.Error().Err(phrasesErr).Msg("[sync] phrases failed")
		summary.addIssue("phrases", cabinetID.String(), "sync phrases: %v", phrasesErr)
	}
	summary.Phrases = phrasesSummary.Phrases
	summary.PhraseStats = phrasesSummary.PhraseStats
	summary.mergeRateLimitFields(phrasesSummary)

	s.logger.Info().Msg("[sync] phase 4: syncing products")
	productsSummary, productsErr := s.syncProductsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
	if productsErr != nil {
		s.logger.Error().Err(productsErr).Msg("[sync] products failed")
		summary.addIssue("products", cabinetID.String(), "sync products: %v", productsErr)
	}
	summary.Products = productsSummary.Products
	s.logger.Info().Int("phrases", summary.Phrases).Int("phrase_stats", summary.PhraseStats).Int("products", summary.Products).Msg("[sync] phase 3+4 done")

	s.logger.Info().Msg("[sync] phase 5: syncing campaign budgets")
	budgetSummary, budgetErr := s.syncCampaignBudgetsForCabinet(ctx, cabinetID, cabinets[0].token, snapshot.campaignRows)
	if budgetErr != nil {
		s.logger.Error().Err(budgetErr).Msg("[sync] campaign budgets failed")
		summary.addIssue("campaign_budgets", cabinetID.String(), "sync budgets: %v", budgetErr)
	}
	summary.CampaignBudgets = budgetSummary.CampaignBudgets
	summary.mergeRateLimitFields(budgetSummary)
	s.logger.Info().Int("campaign_budgets", summary.CampaignBudgets).Msg("[sync] campaign budgets done")

	s.logger.Info().Msg("[sync] phase 6: syncing advertising finance")
	financeSummary, financeErr := s.syncAdFinanceForCabinet(ctx, cabinetID, cabinets[0].token)
	if financeErr != nil {
		s.logger.Error().Err(financeErr).Msg("[sync] advertising finance failed")
		summary.addIssue("ad_finance", cabinetID.String(), "sync advertising finance: %v", financeErr)
	}
	summary.AdBalances = financeSummary.AdBalances
	summary.FinanceDocs = financeSummary.FinanceDocs
	summary.mergeRateLimitFields(financeSummary)

	s.logger.Info().Msg("[sync] phase 7: syncing sales funnel product carts")
	funnelSummary, funnelErr := s.syncSalesFunnelProductsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token, snapshot)
	if funnelErr != nil {
		s.logger.Error().Err(funnelErr).Msg("[sync] sales funnel products failed")
		summary.addIssue("sales_funnel_products", cabinetID.String(), "sync sales funnel products: %v", funnelErr)
	}
	summary.SalesFunnel = funnelSummary.SalesFunnel
	summary.mergeRateLimitFields(funnelSummary)

	s.logger.Info().Msg("[sync] phase 8: syncing business reports")
	reportsSummary, reportsErr := s.syncBusinessReportsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
	if reportsErr != nil {
		s.logger.Error().Err(reportsErr).Msg("[sync] business reports failed")
		summary.addIssue("business_reports", cabinetID.String(), "sync business reports: %v", reportsErr)
	}
	summary.BusinessOrders = reportsSummary.BusinessOrders
	summary.BusinessSales = reportsSummary.BusinessSales
	summary.mergeRateLimitFields(reportsSummary)

	s.logger.Info().Msg("[sync] phase 9: syncing WB commission tariffs")
	tariffsSummary, tariffsErr := s.syncCommissionTariffsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
	if tariffsErr != nil {
		s.logger.Error().Err(tariffsErr).Msg("[sync] commission tariffs failed")
		summary.addIssue("commission_tariffs", cabinetID.String(), "sync commission tariffs: %v", tariffsErr)
	}
	summary.CommissionTariffs = tariffsSummary.CommissionTariffs
	summary.mergeRateLimitFields(tariffsSummary)

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Str("cabinet_id", cabinetID.String()).
		Int("campaigns", summary.Campaigns).
		Int("stats", summary.CampaignStats).
		Int("product_stats", summary.ProductStats).
		Int("phrases", summary.Phrases).
		Int("products", summary.Products).
		Int("business_orders", summary.BusinessOrders).
		Int("business_sales", summary.BusinessSales).
		Int("commission_tariffs", summary.CommissionTariffs).
		Msg("single cabinet sync completed")

	return summary, summary.Error()
}

func (s *SyncService) syncCampaignsForCabinet(ctx context.Context, workspaceID uuid.UUID, cabinet decryptedCabinet) (SyncSummary, error) {
	summary := SyncSummary{Cabinets: 1}
	if s.guardWBEndpoint(ctx, &summary, cabinet.cabinet.ID, wbEndpointAdverts, "campaigns.rate_limit") {
		return summary, summary.Error()
	}

	campaigns, fetchErr := s.listCampaigns(ctx, cabinet.token)
	if fetchErr != nil {
		s.recordWBRateLimitFromError(ctx, cabinet.cabinet.ID, wbEndpointAdverts, fetchErr)
		s.markSummaryRateLimitFromError(&summary, wbEndpointAdverts, fetchErr)
		summary.addIssue("campaigns.list", cabinet.cabinet.ID.String(), "list campaigns: %v", fetchErr)
		return summary, summary.Error()
	}

	activeWBIDs := make([]int64, 0, len(campaigns))
	for _, dto := range campaigns {
		campaign := wb.MapCampaignDTO(dto, workspaceID, cabinet.cabinet.ID)
		if dto.PartialError != "" {
			summary.addIssue("campaigns.enrichment", fmt.Sprintf("%d", campaign.WBCampaignID), "%s", dto.PartialError)
		}
		activeWBIDs = append(activeWBIDs, campaign.WBCampaignID)
		row, upsertErr := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
			WorkspaceID:              uuidToPgtype(campaign.WorkspaceID),
			SellerCabinetID:          uuidToPgtype(campaign.SellerCabinetID),
			WbCampaignID:             campaign.WBCampaignID,
			Name:                     campaign.Name,
			Status:                   campaign.Status,
			CampaignType:             int32(campaign.CampaignType),
			BidType:                  campaign.BidType,
			PaymentType:              campaign.PaymentType,
			DailyBudget:              int64PtrToPgInt8(campaign.DailyBudget),
			PlacementSearch:          boolPtrToPgBool(campaign.PlacementSearch),
			PlacementRecommendations: boolPtrToPgBool(campaign.PlacementRecommendations),
			WbCreatedAt:              timePtrToPgtype(campaign.WBCreatedAt),
			WbStartedAt:              timePtrToPgtype(campaign.WBStartedAt),
			WbUpdatedAt:              timePtrToPgtype(campaign.WBUpdatedAt),
			WbDeletedAt:              timePtrToPgtype(campaign.WBDeletedAt),
		})
		if upsertErr != nil {
			summary.addIssue("campaigns.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "upsert: %v", upsertErr)
			continue
		}
		if linked, linkErr := s.upsertCampaignProductLinks(ctx, workspaceID, cabinet.cabinet.ID, row, dto.NMIDs, dto.Products); linkErr != nil {
			summary.addIssue("campaign_products.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "link products: %v", linkErr)
		} else if linked > 0 {
			s.logger.Debug().Int("links", linked).Int64("campaign_id", campaign.WBCampaignID).Msg("campaign products linked")
		}
		summary.Campaigns++
	}

	if staleCount, staleErr := s.queries.MarkStaleCampaigns(ctx, uuidToPgtype(cabinet.cabinet.ID), activeWBIDs); staleErr != nil {
		summary.addIssue("campaigns.stale_cleanup", cabinet.cabinet.ID.String(), "mark stale: %v", staleErr)
	} else if staleCount > 0 {
		s.logger.Info().Int64("stale_campaigns", staleCount).Str("cabinet_id", cabinet.cabinet.ID.String()).Msg("marked stale campaigns as deleted")
	}
	if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
		summary.addIssue("seller_cabinet.sync_mark", cabinet.cabinet.ID.String(), "failed to update sync timestamp: %v", err)
	}
	return summary, summary.Error()
}

func (s *SyncService) SyncCampaigns(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: len(cabinets)}
	for _, cabinet := range cabinets {
		if s.guardWBEndpoint(ctx, &summary, cabinet.cabinet.ID, wbEndpointAdverts, "campaigns.rate_limit") {
			continue
		}
		campaigns, fetchErr := s.listCampaigns(ctx, cabinet.token)
		if fetchErr != nil {
			s.recordWBRateLimitFromError(ctx, cabinet.cabinet.ID, wbEndpointAdverts, fetchErr)
			s.markSummaryRateLimitFromError(&summary, wbEndpointAdverts, fetchErr)
			summary.addIssue("campaigns.list", cabinet.cabinet.ID.String(), "list campaigns: %v", fetchErr)
			continue
		}

		// Collect active WB IDs for stale cleanup
		activeWBIDs := make([]int64, 0, len(campaigns))
		for _, campaignDTO := range campaigns {
			campaign := wb.MapCampaignDTO(campaignDTO, workspaceID, cabinet.cabinet.ID)
			if campaignDTO.PartialError != "" {
				summary.addIssue("campaigns.enrichment", fmt.Sprintf("%d", campaign.WBCampaignID), "%s", campaignDTO.PartialError)
			}
			activeWBIDs = append(activeWBIDs, campaign.WBCampaignID)
			row, upsertErr := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
				WorkspaceID:              uuidToPgtype(campaign.WorkspaceID),
				SellerCabinetID:          uuidToPgtype(campaign.SellerCabinetID),
				WbCampaignID:             campaign.WBCampaignID,
				Name:                     campaign.Name,
				Status:                   campaign.Status,
				CampaignType:             int32(campaign.CampaignType),
				BidType:                  campaign.BidType,
				PaymentType:              campaign.PaymentType,
				DailyBudget:              int64PtrToPgInt8(campaign.DailyBudget),
				PlacementSearch:          boolPtrToPgBool(campaign.PlacementSearch),
				PlacementRecommendations: boolPtrToPgBool(campaign.PlacementRecommendations),
				WbCreatedAt:              timePtrToPgtype(campaign.WBCreatedAt),
				WbStartedAt:              timePtrToPgtype(campaign.WBStartedAt),
				WbUpdatedAt:              timePtrToPgtype(campaign.WBUpdatedAt),
				WbDeletedAt:              timePtrToPgtype(campaign.WBDeletedAt),
			})
			if upsertErr != nil {
				summary.addIssue("campaigns.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "failed to upsert campaign: %v", upsertErr)
				continue
			}
			if linked, linkErr := s.upsertCampaignProductLinks(ctx, workspaceID, cabinet.cabinet.ID, row, campaignDTO.NMIDs, campaignDTO.Products); linkErr != nil {
				summary.addIssue("campaign_products.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "link products: %v", linkErr)
			} else if linked > 0 {
				s.logger.Debug().Int("links", linked).Int64("campaign_id", campaign.WBCampaignID).Msg("campaign products linked")
			}
			if campaign.Status == "active" || campaign.Status == "paused" || campaign.Status == "ready" {
				if budgetErr := s.syncCampaignBudget(ctx, cabinet.token, row); budgetErr != nil {
					summary.addIssue("campaign_budgets.fetch", fmt.Sprintf("%d", campaign.WBCampaignID), "budget: %v", budgetErr)
				} else {
					summary.CampaignBudgets++
				}
			}
			summary.Campaigns++
		}

		// Stale data cleanup: mark campaigns not returned by WB as deleted (audit fix: HIGH #8)
		staleCount, staleErr := s.queries.MarkStaleCampaigns(ctx, uuidToPgtype(cabinet.cabinet.ID), activeWBIDs)
		if staleErr != nil {
			summary.addIssue("campaigns.stale_cleanup", cabinet.cabinet.ID.String(), "mark stale: %v", staleErr)
		} else if staleCount > 0 {
			s.logger.Info().Int64("stale_campaigns", staleCount).Str("cabinet_id", cabinet.cabinet.ID.String()).Msg("marked stale campaigns as deleted")
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			summary.addIssue("seller_cabinet.sync_mark", cabinet.cabinet.ID.String(), "failed to update sync timestamp: %v", err)
		}
	}

	// Close orphaned recommendations for deleted campaigns
	if _, cleanupErr := s.queries.CloseOrphanedRecommendations(ctx, uuidToPgtype(workspaceID)); cleanupErr != nil {
		summary.addIssue("recommendations.cleanup", workspaceID.String(), "close orphaned: %v", cleanupErr)
	}

	return summary, summary.Error()
}

func (s *SyncService) SyncCampaignStats(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}

	dateFrom, dateTo := wbAdvertStatsDateRange(time.Now())
	summary := SyncSummary{Cabinets: len(cabinets), DateFrom: dateFrom, DateTo: dateTo}

	for _, cabinet := range cabinets {
		if s.guardWBEndpoint(ctx, &summary, cabinet.cabinet.ID, wbEndpointFullstats, "campaign_stats.rate_limit") {
			continue
		}
		campaigns, fetchErr := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinet.cabinet.ID),
			Limit:           syncBatchLimit,
			Offset:          0,
		})
		if fetchErr != nil {
			return summary, apperror.New(apperror.ErrInternal, "failed to list campaigns for cabinet")
		}
		if len(campaigns) == 0 {
			continue
		}

		wbIDs := make([]int, 0, len(campaigns))
		campaignByWBID := make(map[int]sqlcgen.Campaign, len(campaigns))
		for _, campaign := range campaigns {
			if !isWBAdvertStatsEligibleStatus(campaign.Status) {
				continue
			}
			wbIDs = append(wbIDs, int(campaign.WbCampaignID))
			campaignByWBID[int(campaign.WbCampaignID)] = campaign
		}
		if len(wbIDs) == 0 {
			continue
		}

		stats, fetchErr := s.wbClient.GetCampaignStats(ctx, cabinet.token, wbIDs, dateFrom, dateTo)
		if fetchErr != nil {
			s.recordWBRateLimitFromError(ctx, cabinet.cabinet.ID, wbEndpointFullstats, fetchErr)
			s.markSummaryRateLimitFromError(&summary, wbEndpointFullstats, fetchErr)
			summary.addIssue("campaign_stats.fetch", cabinet.cabinet.ID.String(), "get campaign stats: %v", fetchErr)
			if len(stats) == 0 {
				continue
			}
		}

		for _, statDTO := range stats {
			campaign, ok := campaignByWBID[statDTO.AdvertID]
			if !ok {
				summary.SkippedCampaign++
				continue
			}
			stat, mapErr := wb.MapCampaignStatDTO(statDTO, uuidFromPgtype(campaign.ID))
			if mapErr != nil {
				summary.addIssue("campaign_stats.map", fmt.Sprintf("%d", statDTO.AdvertID), "map campaign stat: %v", mapErr)
				continue
			}
			if _, upsertErr := s.queries.UpsertCampaignStat(ctx, sqlcgen.UpsertCampaignStatParams{
				CampaignID:  uuidToPgtype(stat.CampaignID),
				Date:        pgDate(stat.Date),
				Impressions: stat.Impressions,
				Clicks:      stat.Clicks,
				Spend:       stat.Spend,
				Orders:      int64PtrToPgInt8(stat.Orders),
				Revenue:     int64PtrToPgInt8(stat.Revenue),
				Atbs:        int64PtrToPgInt8(stat.Atbs),
				Canceled:    int64PtrToPgInt8(stat.Canceled),
				Shks:        int64PtrToPgInt8(stat.Shks),
			}); upsertErr != nil {
				summary.addIssue("campaign_stats.upsert", stat.CampaignID.String(), "failed to upsert campaign stat: %v", upsertErr)
				continue
			}
			summary.CampaignStats++
			if productStats, productStatsErr := s.upsertProductStatsFromCampaignStat(ctx, workspaceID, cabinet.cabinet.ID, campaign, statDTO); productStatsErr != nil {
				summary.addIssue("product_stats.upsert", fmt.Sprintf("%d", statDTO.AdvertID), "upsert product stats: %v", productStatsErr)
			} else {
				summary.ProductStats += productStats
			}
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			summary.addIssue("seller_cabinet.sync_mark", cabinet.cabinet.ID.String(), "failed to update sync timestamp: %v", err)
		}
	}

	return summary, summary.Error()
}

func (s *SyncService) upsertCampaignProductLinks(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaign sqlcgen.Campaign, nmIDs []int64, productSettings []wb.WBCampaignProductDTO) (int, error) {
	linked := 0
	settingsByNMID := make(map[int64]wb.WBCampaignProductDTO, len(productSettings))
	for _, setting := range productSettings {
		if setting.NmID != 0 {
			settingsByNMID[setting.NmID] = setting
		}
	}
	for _, nmID := range nmIDs {
		if nmID == 0 {
			continue
		}
		setting := settingsByNMID[nmID]
		productRow, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbProductID:     nmID,
			Title:           "",
			Brand:           pgtype.Text{},
			Category:        pgtype.Text{},
			ImageUrl:        pgtype.Text{},
			Price:           pgtype.Int8{},
		})
		if err != nil {
			return linked, err
		}
		if _, err := s.queries.UpsertCampaignProduct(ctx, sqlcgen.UpsertCampaignProductParams{
			CampaignID:         campaign.ID,
			ProductID:          productRow.ID,
			WorkspaceID:        uuidToPgtype(workspaceID),
			SellerCabinetID:    uuidToPgtype(cabinetID),
			WbCampaignID:       campaign.WbCampaignID,
			WbProductID:        nmID,
			SubjectName:        stringPtrToPgText(setting.SubjectName),
			BidSearch:          int64PtrToPgInt8(setting.BidSearch),
			BidRecommendations: int64PtrToPgInt8(setting.BidRecommendations),
		}); err != nil {
			return linked, err
		}
		linked++
	}
	return linked, nil
}

func (s *SyncService) upsertProductStatsFromCampaignStat(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaign sqlcgen.Campaign, statDTO wb.WBCampaignStatDTO) (int, error) {
	upserted := 0
	for _, productDTO := range statDTO.Products {
		productRow, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbProductID:     productDTO.NmID,
			Title:           productTitleFromStat(productDTO),
			Brand:           pgtype.Text{},
			Category:        pgtype.Text{},
			ImageUrl:        pgtype.Text{},
			Price:           pgtype.Int8{},
		})
		if err != nil {
			return upserted, err
		}
		if _, err := s.queries.UpsertCampaignProduct(ctx, sqlcgen.UpsertCampaignProductParams{
			CampaignID:      campaign.ID,
			ProductID:       productRow.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbCampaignID:    campaign.WbCampaignID,
			WbProductID:     productDTO.NmID,
		}); err != nil {
			return upserted, err
		}
		stat, err := wb.MapProductStatDTO(productDTO, uuidFromPgtype(productRow.ID), uuidFromPgtype(campaign.ID))
		if err != nil {
			return upserted, err
		}
		if _, err := s.queries.UpsertProductStat(ctx, sqlcgen.UpsertProductStatParams{
			ProductID:   uuidToPgtype(stat.ProductID),
			CampaignID:  uuidToPgtype(stat.CampaignID),
			Date:        pgDate(stat.Date),
			Impressions: stat.Impressions,
			Clicks:      stat.Clicks,
			Spend:       stat.Spend,
			Orders:      int64PtrToPgInt8(stat.Orders),
			Revenue:     int64PtrToPgInt8(stat.Revenue),
			Atbs:        int64PtrToPgInt8(stat.Atbs),
			Canceled:    int64PtrToPgInt8(stat.Canceled),
			Shks:        int64PtrToPgInt8(stat.Shks),
		}); err != nil {
			return upserted, err
		}
		upserted++
	}
	return upserted, nil
}

func productTitleFromStat(dto wb.WBProductStatDTO) string {
	if dto.Name != "" {
		return dto.Name
	}
	return ""
}

func (s *SyncService) syncCampaignBudgetsForCabinet(ctx context.Context, cabinetID uuid.UUID, token string, campaigns []sqlcgen.Campaign) (SyncSummary, error) {
	if campaigns == nil {
		var err error
		campaigns, err = s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Limit:           syncBatchLimit,
			Offset:          0,
		})
		if err != nil {
			return SyncSummary{}, err
		}
	}

	summary := SyncSummary{Cabinets: 1}
	if s.guardWBEndpoint(ctx, &summary, cabinetID, wbEndpointBudget, "campaign_budgets.rate_limit") {
		return summary, summary.Error()
	}

	phaseDeadline := time.Now().Add(campaignBudgetPhaseTimeout)
	consecutiveTransient := 0
	for _, campaign := range campaigns {
		if campaign.Status != "active" && campaign.Status != "paused" && campaign.Status != "ready" {
			continue
		}
		// Best-effort time cap: stop cleanly (no issue → sync stays "ok", not
		// "partial") once the budget phase has used its share of the window.
		if time.Now().After(phaseDeadline) {
			break
		}

		err := s.syncCampaignBudget(ctx, token, campaign)
		if err == nil {
			consecutiveTransient = 0
			summary.CampaignBudgets++
			continue
		}

		s.recordWBRateLimitFromError(ctx, cabinetID, wbEndpointBudget, err)
		s.markSummaryRateLimitFromError(&summary, wbEndpointBudget, err)

		// Access/rate-limit problems are real and actionable — surface them and stop
		// the whole phase (retrying every campaign would only deepen the rate limit).
		if campaignBudgetAccessFailure(err) {
			summary.addIssue("campaign_budgets.fetch", fmt.Sprintf("%d", campaign.WbCampaignID), "budget: %v", err)
			summary.addIssue("campaign_budgets.skipped", cabinetID.String(), "stopped budget sync after WB access/limit error")
			break
		}

		// Transient per-campaign failures (slow endpoint timeout, one-off 4xx) are
		// budget noise: skip silently so they don't flip the whole sync to "partial".
		// If they pile up consecutively the budget endpoint is degraded — stop early.
		consecutiveTransient++
		if consecutiveTransient >= maxCampaignBudgetFailures {
			break
		}
	}
	return summary, summary.Error()
}

// campaignBudgetAccessFailure reports whether a budget fetch error is an
// account-level access/limit problem worth surfacing and stopping on, as opposed
// to transient per-campaign slowness (timeouts) that should be silently skipped.
func campaignBudgetAccessFailure(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return isRateLimitIssue(lower) ||
		strings.Contains(lower, "401") ||
		strings.Contains(lower, "403") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "circuit breaker")
}

func (s *SyncService) syncCampaignBudget(ctx context.Context, token string, campaign sqlcgen.Campaign) error {
	budgetCtx, cancel := context.WithTimeout(ctx, campaignBudgetFetchTimeout)
	defer cancel()

	budget, err := s.wbClient.GetCampaignBudget(budgetCtx, token, campaign.WbCampaignID)
	if err != nil {
		return err
	}
	_, err = s.queries.UpsertCampaignBudget(ctx, sqlcgen.UpsertCampaignBudgetParams{
		CampaignID: uuidToPgtype(uuidFromPgtype(campaign.ID)),
		Cash:       rubToKopecks(budget.Cash),
		Netting:    rubToKopecks(budget.Netting),
		Total:      rubToKopecks(budget.Total),
		CapturedAt: pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Minute), Valid: true},
	})
	return err
}

func (s *SyncService) syncAdFinanceForCabinet(ctx context.Context, cabinetID uuid.UUID, token string) (SyncSummary, error) {
	summary := SyncSummary{Cabinets: 1}
	if s.guardWBEndpoint(ctx, &summary, cabinetID, wbEndpointAdFinance, "ad_finance.rate_limit") {
		return summary, summary.Error()
	}
	financeCtx, cancel := context.WithTimeout(ctx, campaignBudgetFetchTimeout)
	defer cancel()

	if balance, err := s.wbClient.GetBalance(financeCtx, token); err != nil {
		s.recordWBRateLimitFromError(ctx, cabinetID, wbEndpointAdFinance, err)
		s.markSummaryRateLimitFromError(&summary, wbEndpointAdFinance, err)
		summary.addIssue("ad_balance.fetch", cabinetID.String(), "balance: %v", err)
	} else if balance != nil {
		if err := s.queries.UpsertSellerAdBalance(ctx, sqlcgen.UpsertSellerAdBalanceParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Balance:         rubToKopecks(balance.Balance),
			Net:             rubToKopecks(balance.Net),
			Bonus:           rubToKopecks(balance.Bonus),
			CapturedAt:      pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Minute), Valid: true},
		}); err != nil {
			summary.addIssue("ad_balance.upsert", cabinetID.String(), "balance: %v", err)
		} else {
			summary.AdBalances++
		}
	}

	for docType, loader := range map[string]func(context.Context, string) ([]wb.WBFinanceDocumentDTO, error){
		"upd":     s.wbClient.GetUPDDocuments,
		"payment": s.wbClient.GetPayments,
	} {
		docs, err := loader(financeCtx, token)
		if err != nil {
			s.recordWBRateLimitFromError(ctx, cabinetID, wbEndpointAdFinance, err)
			s.markSummaryRateLimitFromError(&summary, wbEndpointAdFinance, err)
			summary.addIssue("ad_finance.fetch", docType, "%v", err)
			continue
		}
		for idx, doc := range docs {
			externalID := strings.TrimSpace(doc.ID)
			if externalID == "" {
				externalID = fmt.Sprintf("%s:%d:%d:%s", docType, doc.AdvertID, idx, doc.Date)
			}
			var documentDate pgtype.Timestamptz
			if parsed, err := parseReportDate(doc.Date); err == nil {
				documentDate = pgtype.Timestamptz{Time: parsed, Valid: true}
			}
			if err := s.queries.UpsertWBAdFinanceDocument(ctx, sqlcgen.UpsertWBAdFinanceDocumentParams{
				SellerCabinetID: uuidToPgtype(cabinetID),
				ExternalID:      externalID,
				DocumentType:    doc.Type,
				WBCampaignID:    doc.AdvertID,
				Amount:          rubToKopecks(doc.Sum),
				DocumentDate:    documentDate,
				Raw:             doc.Raw,
			}); err != nil {
				summary.addIssue("ad_finance.upsert", externalID, "%v", err)
				continue
			}
			summary.FinanceDocs++
		}
	}

	return summary, summary.Error()
}

func (s *SyncService) syncCommissionTariffsForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) (SyncSummary, error) {
	summary := SyncSummary{Cabinets: 1}
	if s.guardWBEndpoint(ctx, &summary, cabinetID, wbEndpointTariffs, "commission_tariffs.rate_limit") {
		return summary, summary.Error()
	}

	rows, err := s.wbClient.GetCommissionTariffs(ctx, token, "ru")
	if err != nil {
		s.recordWBRateLimitFromError(ctx, cabinetID, wbEndpointTariffs, err)
		s.markSummaryRateLimitFromError(&summary, wbEndpointTariffs, err)
		summary.addIssue("commission_tariffs.fetch", cabinetID.String(), "%v", err)
		return summary, summary.Error()
	}

	capturedAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	for _, row := range rows {
		if row.SubjectID <= 0 || strings.TrimSpace(row.SubjectName) == "" {
			continue
		}
		if err := s.queries.UpsertWBCommissionTariff(ctx, sqlcgen.UpsertWBCommissionTariffParams{
			WorkspaceID:         uuidToPgtype(workspaceID),
			SellerCabinetID:     uuidToPgtype(cabinetID),
			ParentID:            row.ParentID,
			ParentName:          strings.TrimSpace(row.ParentName),
			SubjectID:           row.SubjectID,
			SubjectName:         strings.TrimSpace(row.SubjectName),
			KGVPBooking:         float64PtrToPgtype(&row.KGVPBooking),
			KGVPPickup:          float64PtrToPgtype(&row.KGVPPickup),
			KGVPSupplier:        float64PtrToPgtype(&row.KGVPSupplier),
			KGVPSupplierExpress: float64PtrToPgtype(&row.KGVPSupplierExpress),
			KGVPMarketplace:     float64PtrToPgtype(&row.KGVPMarketplace),
			PaidStorageKGVP:     float64PtrToPgtype(&row.PaidStorageKGVP),
			Source:              "wb_tariffs_commission",
			CapturedAt:          capturedAt,
		}); err != nil {
			summary.addIssue("commission_tariffs.upsert", fmt.Sprintf("%d", row.SubjectID), "%v", err)
			continue
		}
		summary.CommissionTariffs++
	}

	return summary, summary.Error()
}

func (s *SyncService) syncSalesFunnelProductsForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string, snapshot cabinetSyncSnapshot) (SyncSummary, error) {
	dateFrom, dateTo := wb.SalesFunnelDefaultDateRange(time.Now())
	nmIDs := make([]int64, 0)
	for _, ids := range snapshot.nmIDsByWBID {
		nmIDs = append(nmIDs, ids...)
	}
	nmIDs = uniqueInt64s(nmIDs)
	if len(nmIDs) == 0 {
		links, err := s.queries.ListCampaignProductsByWorkspace(ctx, uuidToPgtype(workspaceID))
		if err != nil {
			return SyncSummary{}, err
		}
		for _, link := range links {
			if uuidFromPgtype(link.SellerCabinetID) == cabinetID && link.WbProductID != 0 {
				nmIDs = append(nmIDs, link.WbProductID)
			}
		}
		nmIDs = uniqueInt64s(nmIDs)
	}
	if len(nmIDs) == 0 {
		return SyncSummary{Cabinets: 1}, nil
	}

	summary := SyncSummary{Cabinets: 1}
	if s.guardWBEndpoint(ctx, &summary, cabinetID, wbEndpointAnalyticsFunnel, "sales_funnel_products.rate_limit") {
		return summary, summary.Error()
	}
	const funnelChunkSize = 100
	for start := 0; start < len(nmIDs); start += funnelChunkSize {
		end := start + funnelChunkSize
		if end > len(nmIDs) {
			end = len(nmIDs)
		}
		rows, err := s.wbClient.GetSalesFunnelProductsV3(ctx, token, wb.SalesFunnelParams{
			DateFrom: dateFrom,
			DateTo:   dateTo,
			NmIDs:    nmIDs[start:end],
		})
		if err != nil {
			s.recordWBRateLimitFromError(ctx, cabinetID, wbEndpointAnalyticsFunnel, err)
			s.markSummaryRateLimitFromError(&summary, wbEndpointAnalyticsFunnel, err)
			summary.addIssue("sales_funnel_products.fetch", cabinetID.String(), "%v", err)
			continue
		}
		for _, row := range rows {
			product, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
				WorkspaceID:     uuidToPgtype(workspaceID),
				SellerCabinetID: uuidToPgtype(cabinetID),
				WbProductID:     row.NmID,
				Title:           fallbackProductTitle("", row.NmID),
				Brand:           pgtype.Text{},
				Category:        pgtype.Text{},
				ImageUrl:        pgtype.Text{},
				Price:           pgtype.Int8{},
			})
			if err != nil {
				summary.addIssue("sales_funnel_products.product", fmt.Sprintf("%d", row.NmID), "%v", err)
				continue
			}
			fromDate, fromErr := time.Parse("2006-01-02", row.DateFrom)
			toDate, toErr := time.Parse("2006-01-02", row.DateTo)
			if fromErr != nil || toErr != nil {
				summary.addIssue("sales_funnel_products.date", fmt.Sprintf("%d", row.NmID), "invalid period %s..%s", row.DateFrom, row.DateTo)
				continue
			}
			if err := s.queries.UpsertProductSalesFunnelPeriod(ctx, sqlcgen.UpsertProductSalesFunnelPeriodParams{
				WorkspaceID:     uuidToPgtype(workspaceID),
				SellerCabinetID: uuidToPgtype(cabinetID),
				ProductID:       product.ID,
				WBProductID:     row.NmID,
				DateFrom:        pgDate(fromDate),
				DateTo:          pgDate(toDate),
				OpenCount:       row.OpenCount,
				CartCount:       row.CartCount,
				OrderCount:      row.OrderCount,
				CapturedAt:      pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			}); err != nil {
				summary.addIssue("sales_funnel_products.upsert", fmt.Sprintf("%d", row.NmID), "%v", err)
				continue
			}
			summary.SalesFunnel++
		}
	}

	return summary, summary.Error()
}

func (s *SyncService) listCampaigns(ctx context.Context, token string) ([]wb.WBCampaignDTO, error) {
	listCtx, cancel := context.WithTimeout(ctx, campaignListFetchTimeout)
	defer cancel()

	if counts, countErr := s.wbClient.ListPromotionCounts(listCtx, token); countErr != nil {
		s.logger.Warn().Err(countErr).Msg("WB promotion/count preflight failed")
	} else {
		s.logger.Info().Int("promotion_count_items", len(counts)).Msg("WB promotion/count preflight ok")
	}

	campaigns, err := s.wbClient.ListCampaigns(listCtx, token)
	if err != nil && errors.Is(listCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("rate limited guard timeout: %w", err)
	}
	return campaigns, err
}

func (s *SyncService) loadCabinetSnapshotFromDB(ctx context.Context, workspaceID, cabinetID uuid.UUID) (cabinetSyncSnapshot, error) {
	campaigns, err := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Limit:           syncBatchLimit,
		Offset:          0,
	})
	if err != nil {
		return cabinetSyncSnapshot{}, err
	}

	links, err := s.queries.ListCampaignProductsByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return cabinetSyncSnapshot{}, err
	}

	nmIDsByWBID := make(map[int][]int64, len(campaigns))
	for _, link := range links {
		if uuidFromPgtype(link.SellerCabinetID) != cabinetID {
			continue
		}
		wbCampaignID := int(link.WbCampaignID)
		wbProductID := link.WbProductID
		if wbCampaignID == 0 || wbProductID == 0 {
			continue
		}
		nmIDsByWBID[wbCampaignID] = append(nmIDsByWBID[wbCampaignID], wbProductID)
	}

	for wbCampaignID, nmIDs := range nmIDsByWBID {
		nmIDsByWBID[wbCampaignID] = uniqueInt64s(nmIDs)
	}

	return cabinetSyncSnapshot{
		campaignRows: campaigns,
		nmIDsByWBID:  nmIDsByWBID,
	}, nil
}

func uniqueInt64s(values []int64) []int64 {
	if len(values) < 2 {
		return values
	}
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

type productBusinessDaily struct {
	wbProductID     int64
	date            time.Time
	title           string
	brand           string
	category        string
	orders          int64
	canceledOrders  int64
	sales           int64
	returns         int64
	orderedRevenue  int64
	soldRevenue     int64
	returnedRevenue int64
}

func (s *SyncService) syncBusinessReportsForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) (SyncSummary, error) {
	dateFrom := time.Now().In(time.FixedZone("MSK", 3*60*60)).AddDate(0, 0, -30).Format("2006-01-02")
	summary := SyncSummary{Cabinets: 1}
	daily := make(map[string]*productBusinessDaily)
	hourly := make(map[hourlyOrdersKey]*hourlyOrdersAgg)

	ordersCtx, cancelOrders := context.WithTimeout(ctx, businessReportFetchTimeout)
	orders, ordersErr := s.wbClient.GetSupplierOrders(ordersCtx, token, dateFrom, 0)
	cancelOrders()
	if ordersErr != nil {
		if errors.Is(ordersCtx.Err(), context.DeadlineExceeded) {
			ordersErr = fmt.Errorf("rate limited guard timeout: %w", ordersErr)
		}
		summary.addIssue("business_orders.fetch", cabinetID.String(), "orders report: %v", ordersErr)
	} else {
		summary.BusinessOrders = len(orders)
		for _, row := range orders {
			if row.NmID == 0 {
				continue
			}
			date, err := parseReportDate(row.Date)
			if err != nil {
				summary.addIssue("business_orders.parse", fmt.Sprintf("%d", row.NmID), "date %q: %v", row.Date, err)
				continue
			}
			revenue := rubToKopecks(reportRevenue(row.FinishedPrice, row.PriceWithDisc, row.TotalPrice))
			item := dailyBusinessItem(daily, row.NmID, date, row.SupplierArticle, row.Brand, reportCategory(row.Category, row.Subject))
			item.orders++
			item.orderedRevenue += revenue
			if row.IsCancel {
				item.canceledOrders++
			}
			// Heatmap: same rows re-aggregated by MSK hour. Cancelled orders and
			// date-only rows (no time-of-day) are skipped.
			if !row.IsCancel {
				if ts, ok := parseReportDateTime(row.Date); ok {
					k := hourlyOrdersKey{nmID: row.NmID, date: ts.Format("2006-01-02"), hour: int16(ts.Hour())}
					agg := hourly[k]
					if agg == nil {
						agg = &hourlyOrdersAgg{}
						hourly[k] = agg
					}
					agg.orders++
					agg.units++
					agg.revenueKopecks += revenue
				}
			}
		}
	}

	salesCtx, cancelSales := context.WithTimeout(ctx, businessReportFetchTimeout)
	sales, salesErr := s.wbClient.GetSupplierSales(salesCtx, token, dateFrom, 0)
	cancelSales()
	if salesErr != nil {
		if errors.Is(salesCtx.Err(), context.DeadlineExceeded) {
			salesErr = fmt.Errorf("rate limited guard timeout: %w", salesErr)
		}
		summary.addIssue("business_sales.fetch", cabinetID.String(), "sales report: %v", salesErr)
	} else {
		summary.BusinessSales = len(sales)
		for _, row := range sales {
			if row.NmID == 0 {
				continue
			}
			date, err := parseReportDate(row.Date)
			if err != nil {
				summary.addIssue("business_sales.parse", fmt.Sprintf("%d", row.NmID), "date %q: %v", row.Date, err)
				continue
			}
			item := dailyBusinessItem(daily, row.NmID, date, row.SupplierArticle, row.Brand, reportCategory(row.Category, row.Subject))
			revenue := rubToKopecks(reportRevenue(row.FinishedPrice, row.PriceWithDisc, row.ForPay, row.TotalPrice))
			if strings.HasPrefix(strings.ToUpper(row.SaleID), "R") {
				item.returns++
				item.returnedRevenue += revenue
			} else {
				item.sales++
				item.soldRevenue += revenue
			}
		}
	}

	for _, item := range daily {
		product, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbProductID:     item.wbProductID,
			Title:           fallbackProductTitle(item.title, item.wbProductID),
			Brand:           textToPgtype(item.brand),
			Category:        textToPgtype(item.category),
			ImageUrl:        pgtype.Text{},
			Price:           pgtype.Int8{},
		})
		if err != nil {
			summary.addIssue("business_reports.product", fmt.Sprintf("%d", item.wbProductID), "upsert product: %v", err)
			continue
		}
		if _, err := s.queries.UpsertProductSalesDaily(ctx, sqlcgen.UpsertProductSalesDailyParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			ProductID:       product.ID,
			WbProductID:     item.wbProductID,
			Date:            pgDate(item.date),
			Orders:          item.orders,
			CanceledOrders:  item.canceledOrders,
			Sales:           item.sales,
			Returns:         item.returns,
			OrderedRevenue:  item.orderedRevenue,
			SoldRevenue:     item.soldRevenue,
			ReturnedRevenue: item.returnedRevenue,
			Source:          "wb_statistics",
			CapturedAt:      pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}); err != nil {
			summary.addIssue("business_reports.upsert", fmt.Sprintf("%d", item.wbProductID), "upsert daily sales: %v", err)
		}
	}

	for k, agg := range hourly {
		date, err := time.Parse("2006-01-02", k.date)
		if err != nil {
			continue
		}
		if err := s.queries.UpsertProductOrdersHourly(ctx, sqlcgen.UpsertProductOrdersHourlyParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbProductID:     k.nmID,
			Date:            pgDate(date),
			Hour:            k.hour,
			Orders:          agg.orders,
			Units:           agg.units,
			RevenueKopecks:  agg.revenueKopecks,
		}); err != nil {
			summary.addIssue("business_reports.hourly", fmt.Sprintf("%d", k.nmID), "upsert hourly orders: %v", err)
		}
	}
	if len(hourly) > 0 {
		if err := s.queries.DeleteOldProductOrdersHourly(ctx, uuidToPgtype(cabinetID)); err != nil {
			s.logger.Warn().Err(err).Msg("hourly orders retention cleanup failed")
		}
	}

	return summary, summary.Error()
}

// hourlyOrdersKey/hourlyOrdersAgg aggregate WB order rows into MSK hour buckets
// for the repricer orders heatmap.
type hourlyOrdersKey struct {
	nmID int64
	date string // 2006-01-02 (MSK)
	hour int16
}

type hourlyOrdersAgg struct {
	orders         int32
	units          int32
	revenueKopecks int64
}

func dailyBusinessItem(items map[string]*productBusinessDaily, nmID int64, date time.Time, title, brand, category string) *productBusinessDaily {
	key := fmt.Sprintf("%d:%s", nmID, date.Format("2006-01-02"))
	if item, ok := items[key]; ok {
		if item.title == "" {
			item.title = title
		}
		if item.brand == "" {
			item.brand = brand
		}
		if item.category == "" {
			item.category = category
		}
		return item
	}
	item := &productBusinessDaily{
		wbProductID: nmID,
		date:        date,
		title:       title,
		brand:       brand,
		category:    category,
	}
	items[key] = item
	return item
}

// mskLocation is the WB statistics timezone: order timestamps come as
// "2006-01-02T15:04:05" in Moscow time without a zone suffix.
var mskLocation = time.FixedZone("MSK", 3*60*60)

// parseReportDateTime extracts the full order timestamp (MSK). ok=false means
// the row has no time-of-day (date-only) — heatmap aggregation must skip it,
// otherwise every such order lands in a misleading 00:00 peak.
func parseReportDateTime(value string) (t time.Time, ok bool) {
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse("2006-01-02T15:04:05", value); err == nil {
		return parsed, true // already wall-clock MSK
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.In(mskLocation), true
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.In(mskLocation), true
	}
	return time.Time{}, false
}

func parseReportDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Truncate(24 * time.Hour), nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC().Truncate(24 * time.Hour), nil
	}
	if idx := strings.IndexByte(value, 'T'); idx > 0 {
		return time.Parse("2006-01-02", value[:idx])
	}
	return time.Time{}, fmt.Errorf("unsupported date format")
}

func reportRevenue(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func reportCategory(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func fallbackProductTitle(title string, nmID int64) string {
	if title != "" {
		return title
	}
	return ""
}

func (s *SyncService) SyncPhrases(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}

	dateFrom, dateTo := wbAdvertStatsDateRange(time.Now())
	summary := SyncSummary{Cabinets: len(cabinets), DateFrom: dateFrom, DateTo: dateTo}
	for _, cabinet := range cabinets {
		if s.guardWBEndpoint(ctx, &summary, cabinet.cabinet.ID, wbEndpointNormQueryStats, "phrase_stats.rate_limit") {
			continue
		}
		campaigns, fetchErr := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinet.cabinet.ID),
			Limit:           syncBatchLimit,
			Offset:          0,
		})
		if fetchErr != nil {
			return summary, apperror.New(apperror.ErrInternal, "failed to list campaigns for cabinet")
		}

		// Fetch WB campaigns ONCE per cabinet to get NMIDs
		var wbErr error
		var wbCampaigns []wb.WBCampaignDTO
		nmIDMap := make(map[int][]int64)
		if !s.guardWBEndpoint(ctx, &summary, cabinet.cabinet.ID, wbEndpointAdverts, "campaigns.rate_limit") {
			wbCampaigns, wbErr = s.listCampaigns(ctx, cabinet.token)
		}
		if wbErr == nil {
			for _, wbCamp := range wbCampaigns {
				nmIDMap[wbCamp.AdvertID] = wbCamp.NMIDs
			}
		} else {
			s.recordWBRateLimitFromError(ctx, cabinet.cabinet.ID, wbEndpointAdverts, wbErr)
			s.markSummaryRateLimitFromError(&summary, wbEndpointAdverts, wbErr)
		}

		for _, campaign := range campaigns {
			if !isWBAdvertStatsEligibleStatus(campaign.Status) {
				continue
			}
			nmIDs := nmIDMap[int(campaign.WbCampaignID)]
			if len(nmIDs) == 0 {
				continue
			}
			stats, statsErr := s.wbClient.GetSearchClusterStatsWithNMIDs(ctx, cabinet.token, int(campaign.WbCampaignID), nmIDs)
			if statsErr != nil {
				s.recordWBRateLimitFromError(ctx, cabinet.cabinet.ID, wbEndpointNormQueryStats, statsErr)
				s.markSummaryRateLimitFromError(&summary, wbEndpointNormQueryStats, statsErr)
				summary.addIssue("phrase_stats.fetch", fmt.Sprintf("%d", campaign.WbCampaignID), "get search cluster stats: %v", statsErr)
				continue
			}

			phraseIDsByNormQuery := make(map[string]uuid.UUID, len(stats))
			savedRowsCount := 0
			for _, statDTO := range stats {
				key := phraseIdentityKey(statDTO.NmID, statDTO.NormQuery)
				phraseID, ok := phraseIDsByNormQuery[key]
				if !ok {
					var upserted bool
					phraseID, upserted = s.upsertPhraseFromNormQueryStat(ctx, workspaceID, cabinet.cabinet.ID, uuidFromPgtype(campaign.ID), int(campaign.WbCampaignID), statDTO, &summary)
					if !upserted {
						summary.SkippedCampaign++
						continue
					}
					phraseIDsByNormQuery[key] = phraseID
				}
				stat, mapErr := wb.MapSearchClusterStatDTO(statDTO, phraseID)
				if mapErr != nil {
					summary.addIssue("phrase_stats.map", statDTO.NormQuery, "map phrase stat: %v", mapErr)
					continue
				}
				if _, upsertErr := s.queries.UpsertPhraseStat(ctx, sqlcgen.UpsertPhraseStatParams{
					PhraseID:    uuidToPgtype(stat.PhraseID),
					Date:        pgDate(stat.Date),
					Impressions: stat.Impressions,
					Clicks:      stat.Clicks,
					Spend:       stat.Spend,
					Atbs:        int64PtrToPgInt8(stat.Atbs),
					Orders:      int64PtrToPgInt8(stat.Orders),
					Cpc:         float64PtrToPgFloat8(stat.CPC),
					Cpm:         float64PtrToPgFloat8(stat.CPM),
					AvgPos:      float64PtrToPgFloat8(stat.AvgPos),
				}); upsertErr != nil {
					summary.addIssue("phrase_stats.upsert", stat.PhraseID.String(), "failed to upsert phrase stat: %v", upsertErr)
					continue
				}
				summary.PhraseStats++
				savedRowsCount++
			}
			s.logger.Info().
				Int64("advertId", campaign.WbCampaignID).
				Int("savedRowsCount", savedRowsCount).
				Msg("[NQ] saved rows")
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			summary.addIssue("seller_cabinet.sync_mark", cabinet.cabinet.ID.String(), "failed to update sync timestamp: %v", err)
		}
	}

	return summary, summary.Error()
}

func (s *SyncService) SyncProducts(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: len(cabinets)}
	for _, cabinet := range cabinets {
		products, fetchErr := s.wbClient.ListProducts(ctx, cabinet.token)
		if fetchErr != nil {
			summary.addIssue("products.list", cabinet.cabinet.ID.String(), "list products: %v", fetchErr)
			continue
		}

		for _, productDTO := range products {
			product := wb.MapProductDTO(productDTO, workspaceID, cabinet.cabinet.ID)
			if _, upsertErr := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
				WorkspaceID:     uuidToPgtype(product.WorkspaceID),
				SellerCabinetID: uuidToPgtype(product.SellerCabinetID),
				WbProductID:     product.WBProductID,
				Title:           product.Title,
				Brand:           textToPgtype(stringPtrToString(product.Brand)),
				Category:        textToPgtype(stringPtrToString(product.Category)),
				ImageUrl:        textToPgtype(stringPtrToString(product.ImageURL)),
				Price:           int64PtrToPgInt8(product.Price),
			}); upsertErr != nil {
				summary.addIssue("products.upsert", fmt.Sprintf("%d", product.WBProductID), "failed to upsert product: %v", upsertErr)
				continue
			}
			summary.Products++
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			summary.addIssue("seller_cabinet.sync_mark", cabinet.cabinet.ID.String(), "failed to update sync timestamp: %v", err)
		}
	}

	return summary, summary.Error()
}

type decryptedCabinet struct {
	cabinet domain.SellerCabinet
	token   string
}

// listCabinets returns cabinets to sync — if cabinetID is provided, only that one; otherwise all in workspace.
func (s *SyncService) listCabinets(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID) ([]decryptedCabinet, error) {
	if cabinetID != nil {
		return s.listSingleCabinet(ctx, *cabinetID)
	}
	return s.listWorkspaceCabinets(ctx, workspaceID)
}

func (s *SyncService) listSingleCabinet(ctx context.Context, cabinetID uuid.UUID) ([]decryptedCabinet, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	cabinet := sellerCabinetFromSqlc(row)
	token, err := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to decrypt cabinet token")
	}
	return []decryptedCabinet{{cabinet: cabinet, token: token}}, nil
}

func (s *SyncService) listWorkspaceCabinets(ctx context.Context, workspaceID uuid.UUID) ([]decryptedCabinet, error) {
	rows, err := s.queries.ListActiveSellerCabinetsByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list active seller cabinets")
	}

	result := make([]decryptedCabinet, 0, len(rows))
	for _, row := range rows {
		cabinet := sellerCabinetFromSqlc(row)
		token, decryptErr := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
		if decryptErr != nil {
			s.logger.Warn().
				Err(decryptErr).
				Str("workspace_id", workspaceID.String()).
				Str("seller_cabinet_id", cabinet.ID.String()).
				Str("source", cabinet.Source).
				Msg("skipping seller cabinet with undecryptable token")
			continue
		}
		result = append(result, decryptedCabinet{
			cabinet: cabinet,
			token:   token,
		})
	}
	return result, nil
}

func int64PtrToPgInt8(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func boolPtrToPgBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}

func float64PtrToPgFloat8(value *float64) pgtype.Float8 {
	if value == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *value, Valid: true}
}

func phraseIdentityKey(nmID int64, normQuery string) string {
	return fmt.Sprintf("%d:%s", nmID, strings.TrimSpace(normQuery))
}

func (s *SyncService) upsertPhraseFromNormQueryStat(ctx context.Context, workspaceID, cabinetID, campaignID uuid.UUID, wbCampaignID int, statDTO wb.WBSearchClusterStatDTO, summary *SyncSummary) (uuid.UUID, bool) {
	normQuery := strings.TrimSpace(statDTO.NormQuery)
	if statDTO.NmID <= 0 {
		summary.addIssue("phrases.identity", fmt.Sprintf("%d", wbCampaignID), "missing nmId for norm query %q", normQuery)
		return uuid.Nil, false
	}
	if normQuery == "" {
		summary.addIssue("phrases.identity", fmt.Sprintf("%d", wbCampaignID), "missing norm query for nmId %d", statDTO.NmID)
		return uuid.Nil, false
	}

	productRow, productErr := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(cabinetID),
		WbProductID:     statDTO.NmID,
		Title:           "",
		Brand:           pgtype.Text{},
		Category:        pgtype.Text{},
		ImageUrl:        pgtype.Text{},
		Price:           pgtype.Int8{},
	})
	if productErr != nil {
		summary.addIssue("phrases.product", fmt.Sprintf("%d", statDTO.NmID), "upsert product: %v", productErr)
		return uuid.Nil, false
	}

	count := int(statDTO.Views)
	if count == 0 {
		count = int(statDTO.Clicks)
	}
	productID := uuidFromPgtype(productRow.ID)
	wbProductID := statDTO.NmID
	row, upsertErr := s.queries.UpsertPhrase(ctx, sqlcgen.UpsertPhraseParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		CampaignID:  uuidToPgtype(campaignID),
		ProductID:   uuidToPgtypePtr(&productID),
		WbProductID: int64PtrToPgInt8(&wbProductID),
		WbClusterID: pgtype.Int8{},
		WbNormQuery: normQuery,
		Keyword:     normQuery,
		CurrentBid:  optionalInt64ToPgInt8(statDTO.CurrentBid),
		Count:       intPtrToPgInt4(&count),
	})
	if upsertErr != nil {
		summary.addIssue("phrases.upsert", normQuery, "failed to upsert phrase: %v", upsertErr)
		return uuid.Nil, false
	}
	summary.Phrases++
	return uuidFromPgtype(row.ID), true
}

func rubToKopecks(value float64) int64 {
	return int64(math.Round(value * 100))
}

func intPtrToPgInt4(value *int) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*value), Valid: true}
}

func int32PtrToPgInt4(value *int) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*value), Valid: true}
}

func stringPtrToPgText(value *string) pgtype.Text {
	if value == nil || *value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// syncCampaignStatsForCabinet syncs stats only for campaigns belonging to a specific cabinet.
func (s *SyncService) syncCampaignStatsForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string, campaigns []sqlcgen.Campaign) (SyncSummary, error) {
	var err error
	if campaigns == nil {
		campaigns, err = s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Limit:           10000,
			Offset:          0,
		})
		if err != nil {
			return SyncSummary{}, err
		}
	}

	dateFrom, dateTo := wbAdvertStatsDateRange(time.Now())
	summary := SyncSummary{Cabinets: 1, DateFrom: dateFrom, DateTo: dateTo}
	if s.guardWBEndpoint(ctx, &summary, cabinetID, wbEndpointFullstats, "stats.rate_limit") {
		return summary, summary.Error()
	}

	campaignIDs := make([]int, 0, len(campaigns))
	campaignMap := make(map[int]sqlcgen.Campaign)
	for _, c := range campaigns {
		if !isWBAdvertStatsEligibleStatus(c.Status) {
			continue
		}
		campaignIDs = append(campaignIDs, int(c.WbCampaignID))
		campaignMap[int(c.WbCampaignID)] = c
	}
	s.logger.Info().Int("stats_eligible_campaigns", len(campaignIDs)).Int("total_campaigns", len(campaigns)).Msg("[sync] stats: filtered to WB fullstats eligible campaigns")
	if len(campaignIDs) == 0 {
		return summary, summary.Error()
	}

	stats, fetchErr := s.wbClient.GetCampaignStats(ctx, token, campaignIDs, dateFrom, dateTo)
	if fetchErr != nil {
		s.recordWBRateLimitFromError(ctx, cabinetID, wbEndpointFullstats, fetchErr)
		s.markSummaryRateLimitFromError(&summary, wbEndpointFullstats, fetchErr)
		summary.addIssue("stats.fetch", cabinetID.String(), "fetch stats: %v", fetchErr)
		if len(stats) == 0 {
			return summary, fetchErr
		}
	}

	for _, statDTO := range stats {
		campaign, ok := campaignMap[statDTO.AdvertID]
		if !ok {
			continue
		}
		stat, mapErr := wb.MapCampaignStatDTO(statDTO, uuidFromPgtype(campaign.ID))
		if mapErr != nil {
			continue
		}
		if _, upsertErr := s.queries.UpsertCampaignStat(ctx, sqlcgen.UpsertCampaignStatParams{
			CampaignID:  uuidToPgtype(stat.CampaignID),
			Date:        pgtype.Date{Time: stat.Date, Valid: true},
			Impressions: stat.Impressions,
			Clicks:      stat.Clicks,
			Spend:       stat.Spend,
			Orders:      int64PtrToPgInt8(stat.Orders),
			Revenue:     int64PtrToPgInt8(stat.Revenue),
			Atbs:        int64PtrToPgInt8(stat.Atbs),
			Canceled:    int64PtrToPgInt8(stat.Canceled),
			Shks:        int64PtrToPgInt8(stat.Shks),
		}); upsertErr != nil {
			continue
		}
		summary.CampaignStats++
		if productStats, productStatsErr := s.upsertProductStatsFromCampaignStat(ctx, workspaceID, cabinetID, campaign, statDTO); productStatsErr != nil {
			summary.addIssue("product_stats.upsert", fmt.Sprintf("%d", statDTO.AdvertID), "upsert product stats: %v", productStatsErr)
		} else {
			summary.ProductStats += productStats
		}
	}
	return summary, summary.Error()
}

// syncPhrasesForCabinet syncs phrases only for campaigns belonging to a specific cabinet.
// Optimized: fetches ListCampaigns ONCE to get all NMIDs, then uses WithNMIDs methods.
func (s *SyncService) syncPhrasesForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string, campaigns []sqlcgen.Campaign, nmIDMap map[int][]int64) (SyncSummary, error) {
	var err error
	if campaigns == nil {
		campaigns, err = s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Limit:           10000,
			Offset:          0,
		})
		if err != nil {
			return SyncSummary{}, err
		}
	}
	if nmIDMap == nil {
		wbCampaigns, wbErr := s.listCampaigns(ctx, token)
		nmIDMap = make(map[int][]int64)
		if wbErr == nil {
			for _, wbCamp := range wbCampaigns {
				nmIDMap[wbCamp.AdvertID] = wbCamp.NMIDs
			}
		} else {
			s.logger.Warn().Err(wbErr).Msg("[sync] failed to fetch WB campaigns for NMIDs, will skip normquery")
		}
	}

	dateFrom, dateTo := wbAdvertStatsDateRange(time.Now())
	summary := SyncSummary{Cabinets: 1, DateFrom: dateFrom, DateTo: dateTo}
	if s.guardWBEndpoint(ctx, &summary, cabinetID, wbEndpointNormQueryStats, "phrase_stats.rate_limit") {
		return summary, summary.Error()
	}
	attemptedNormQueryRequests := 0
	for _, campaign := range campaigns {
		if !isWBAdvertStatsEligibleStatus(campaign.Status) {
			continue
		}
		wbCampaignID := int(campaign.WbCampaignID)
		campaignID := uuidFromPgtype(campaign.ID)
		nmIDs := nmIDMap[wbCampaignID]
		if len(nmIDs) == 0 {
			continue // No products linked — skip
		}

		// Rate-limit delay between campaigns for WB advert stats endpoints.
		if attemptedNormQueryRequests > 0 {
			if err := sleepWithContext(ctx, wbAdvertStatsRequestDelay); err != nil {
				return summary, err
			}
		}
		attemptedNormQueryRequests++

		// Sync phrase stats — rows and phrase identities come directly from /adv/v1/normquery/stats.
		// Legacy /adv/v0/normquery/list is intentionally not mixed into this flow:
		// it is management state (active/excluded), not advertising analytics, and it
		// can consume the same tight WB advertising limits before stats are collected.
		clusterStats, statsErr := s.wbClient.GetSearchClusterStatsWithNMIDs(ctx, token, wbCampaignID, nmIDs)
		if statsErr != nil {
			s.recordWBRateLimitFromError(ctx, cabinetID, wbEndpointNormQueryStats, statsErr)
			s.markSummaryRateLimitFromError(&summary, wbEndpointNormQueryStats, statsErr)
			summary.addIssue("phrase_stats.fetch", fmt.Sprintf("%d", wbCampaignID), "stats: %v", statsErr)
			if isRateLimitIssue(statsErr.Error()) {
				s.logger.Warn().
					Err(statsErr).
					Int("advertId", wbCampaignID).
					Msg("[NQ] stopping phrase sync after WB rate limit")
				break
			}
			continue
		}
		phraseIDsByNormQuery := make(map[string]uuid.UUID, len(clusterStats))
		savedRowsCount := 0
		for _, statDTO := range clusterStats {
			key := phraseIdentityKey(statDTO.NmID, statDTO.NormQuery)
			phraseID, ok := phraseIDsByNormQuery[key]
			if !ok {
				var upserted bool
				phraseID, upserted = s.upsertPhraseFromNormQueryStat(ctx, workspaceID, cabinetID, campaignID, wbCampaignID, statDTO, &summary)
				if !upserted {
					continue
				}
				phraseIDsByNormQuery[key] = phraseID
			}
			stat, mapErr := wb.MapSearchClusterStatDTO(statDTO, phraseID)
			if mapErr != nil {
				continue
			}
			if _, upsertErr := s.queries.UpsertPhraseStat(ctx, sqlcgen.UpsertPhraseStatParams{
				PhraseID:    uuidToPgtype(stat.PhraseID),
				Date:        pgtype.Date{Time: stat.Date, Valid: true},
				Impressions: stat.Impressions,
				Clicks:      stat.Clicks,
				Spend:       stat.Spend,
				Atbs:        int64PtrToPgInt8(stat.Atbs),
				Orders:      int64PtrToPgInt8(stat.Orders),
				Cpc:         float64PtrToPgFloat8(stat.CPC),
				Cpm:         float64PtrToPgFloat8(stat.CPM),
				AvgPos:      float64PtrToPgFloat8(stat.AvgPos),
			}); upsertErr != nil {
				continue
			}
			summary.PhraseStats++
			savedRowsCount++
		}
		s.logger.Info().
			Int("advertId", wbCampaignID).
			Int("savedRowsCount", savedRowsCount).
			Msg("[NQ] saved rows")
	}

	s.logger.Info().Int("phrases", summary.Phrases).Int("phrase_stats", summary.PhraseStats).Msg("[sync] phrases+stats done for cabinet")
	return summary, summary.Error()
}

// syncProductsForCabinet syncs products only for a specific cabinet.
func (s *SyncService) syncProductsForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) (SyncSummary, error) {
	products, err := s.wbClient.ListProducts(ctx, token)
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: 1}
	for _, productDTO := range products {
		product := wb.MapProductDTO(productDTO, workspaceID, cabinetID)
		if _, upsertErr := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbProductID:     product.WBProductID,
			Title:           product.Title,
			Brand:           stringPtrToPgText(product.Brand),
			Category:        stringPtrToPgText(product.Category),
			ImageUrl:        stringPtrToPgText(product.ImageURL),
			Price:           int64PtrToPgInt8(product.Price),
		}); upsertErr != nil {
			continue
		}
		summary.Products++
	}
	s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinetID))
	return summary, summary.Error()
}
