package service

import (
	"context"
	"errors"
	"fmt"
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

const syncBatchLimit = int32(10000)

type WBSyncClient interface {
	ListCampaigns(ctx context.Context, token string) ([]wb.WBCampaignDTO, error)
	GetCampaignStats(ctx context.Context, token string, campaignIDs []int, dateFrom, dateTo string) ([]wb.WBCampaignStatDTO, error)
	ListSearchClusters(ctx context.Context, token string, campaignID int) ([]wb.WBSearchClusterDTO, error)
	GetSearchClusterStats(ctx context.Context, token string, campaignID int) ([]wb.WBSearchClusterStatDTO, error)
	ListProducts(ctx context.Context, token string) ([]wb.WBProductDTO, error)
}

type SyncSummary struct {
	Cabinets        int         `json:"cabinets"`
	Campaigns       int         `json:"campaigns"`
	CampaignStats   int         `json:"campaign_stats"`
	Phrases         int         `json:"phrases"`
	PhraseStats     int         `json:"phrase_stats"`
	Products        int         `json:"products"`
	SkippedCampaign int         `json:"skipped_campaigns"`
	Issues          []SyncIssue `json:"issues,omitempty"`
}

type SyncIssue struct {
	Stage    string `json:"stage"`
	EntityID string `json:"entity_id,omitempty"`
	Message  string `json:"message"`
}

func (s *SyncSummary) addIssue(stage, entityID, format string, args ...any) {
	s.Issues = append(s.Issues, SyncIssue{
		Stage:    stage,
		EntityID: entityID,
		Message:  fmt.Sprintf(format, args...),
	})
}

func (s *SyncSummary) merge(other SyncSummary) {
	s.Cabinets += other.Cabinets
	s.Campaigns += other.Campaigns
	s.CampaignStats += other.CampaignStats
	s.Phrases += other.Phrases
	s.PhraseStats += other.PhraseStats
	s.Products += other.Products
	s.SkippedCampaign += other.SkippedCampaign
	s.Issues = append(s.Issues, other.Issues...)
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

// SyncSingleCabinet syncs campaigns, stats, phrases, products for ONE specific cabinet.
func (s *SyncService) SyncSingleCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listSingleCabinet(ctx, cabinetID)
	if err != nil {
		return SyncSummary{}, err
	}
	if len(cabinets) == 0 {
		return SyncSummary{}, nil
	}

	summary := SyncSummary{Cabinets: 1}

	// Campaigns
	for _, cabinet := range cabinets {
		campaigns, fetchErr := s.wbClient.ListCampaigns(ctx, cabinet.token)
		if fetchErr != nil {
			summary.addIssue("campaigns.list", cabinet.cabinet.ID.String(), "list campaigns: %v", fetchErr)
			continue
		}
		activeWBIDs := make([]int64, 0, len(campaigns))
		for _, dto := range campaigns {
			campaign := wb.MapCampaignDTO(dto, workspaceID, cabinet.cabinet.ID)
			activeWBIDs = append(activeWBIDs, campaign.WBCampaignID)
			if _, upsertErr := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
				WorkspaceID:     uuidToPgtype(campaign.WorkspaceID),
				SellerCabinetID: uuidToPgtype(campaign.SellerCabinetID),
				WbCampaignID:    campaign.WBCampaignID,
				Name:            campaign.Name,
				Status:          campaign.Status,
				CampaignType:    int32(campaign.CampaignType),
				BidType:         campaign.BidType,
				PaymentType:     campaign.PaymentType,
				DailyBudget:     int64PtrToPgInt8(campaign.DailyBudget),
			}); upsertErr != nil {
				summary.addIssue("campaigns.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "upsert: %v", upsertErr)
				continue
			}
			summary.Campaigns++
		}
		// Stale cleanup for single cabinet sync
		if len(activeWBIDs) > 0 {
			if staleCount, staleErr := s.queries.MarkStaleCampaigns(ctx, uuidToPgtype(cabinet.cabinet.ID), activeWBIDs); staleErr != nil {
				summary.addIssue("campaigns.stale_cleanup", cabinet.cabinet.ID.String(), "mark stale: %v", staleErr)
			} else if staleCount > 0 {
				s.logger.Info().Int64("stale_campaigns", staleCount).Str("cabinet_id", cabinet.cabinet.ID.String()).Msg("marked stale campaigns as deleted")
			}
		}
		s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID))
	}

	// Stats — scoped to single cabinet (fix: audit CRITICAL #1)
	statsSummary, statsErr := s.syncCampaignStatsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
	if statsErr != nil {
		summary.addIssue("stats", cabinetID.String(), "sync stats: %v", statsErr)
	}
	summary.CampaignStats = statsSummary.CampaignStats

	// Phrases — scoped to single cabinet
	phrasesSummary, phrasesErr := s.syncPhrasesForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
	if phrasesErr != nil {
		summary.addIssue("phrases", cabinetID.String(), "sync phrases: %v", phrasesErr)
	}
	summary.Phrases = phrasesSummary.Phrases

	// Products — scoped to single cabinet
	productsSummary, productsErr := s.syncProductsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
	if productsErr != nil {
		summary.addIssue("products", cabinetID.String(), "sync products: %v", productsErr)
	}
	summary.Products = productsSummary.Products

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Str("cabinet_id", cabinetID.String()).
		Int("campaigns", summary.Campaigns).
		Int("stats", summary.CampaignStats).
		Int("phrases", summary.Phrases).
		Int("products", summary.Products).
		Msg("single cabinet sync completed")

	return summary, summary.Error()
}

func (s *SyncService) SyncCampaigns(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: len(cabinets)}
	for _, cabinet := range cabinets {
		campaigns, fetchErr := s.wbClient.ListCampaigns(ctx, cabinet.token)
		if fetchErr != nil {
			summary.addIssue("campaigns.list", cabinet.cabinet.ID.String(), "list campaigns: %v", fetchErr)
			continue
		}

		// Collect active WB IDs for stale cleanup
		activeWBIDs := make([]int64, 0, len(campaigns))
		for _, campaignDTO := range campaigns {
			campaign := wb.MapCampaignDTO(campaignDTO, workspaceID, cabinet.cabinet.ID)
			activeWBIDs = append(activeWBIDs, campaign.WBCampaignID)
			if _, upsertErr := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
				WorkspaceID:     uuidToPgtype(campaign.WorkspaceID),
				SellerCabinetID: uuidToPgtype(campaign.SellerCabinetID),
				WbCampaignID:    campaign.WBCampaignID,
				Name:            campaign.Name,
				Status:          campaign.Status,
				CampaignType:    int32(campaign.CampaignType),
				BidType:         campaign.BidType,
				PaymentType:     campaign.PaymentType,
				DailyBudget:     int64PtrToPgInt8(campaign.DailyBudget),
			}); upsertErr != nil {
				summary.addIssue("campaigns.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "failed to upsert campaign: %v", upsertErr)
				continue
			}
			summary.Campaigns++
		}

		// Stale data cleanup: mark campaigns not returned by WB as deleted (audit fix: HIGH #8)
		if len(activeWBIDs) > 0 {
			staleCount, staleErr := s.queries.MarkStaleCampaigns(ctx, uuidToPgtype(cabinet.cabinet.ID), activeWBIDs)
			if staleErr != nil {
				summary.addIssue("campaigns.stale_cleanup", cabinet.cabinet.ID.String(), "mark stale: %v", staleErr)
			} else if staleCount > 0 {
				s.logger.Info().Int64("stale_campaigns", staleCount).Str("cabinet_id", cabinet.cabinet.ID.String()).Msg("marked stale campaigns as deleted")
			}
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

	dateFrom := time.Now().UTC().AddDate(0, 0, -30).Format(exportDateLayout)
	dateTo := time.Now().UTC().Format(exportDateLayout)
	summary := SyncSummary{Cabinets: len(cabinets)}

	for _, cabinet := range cabinets {
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
			wbIDs = append(wbIDs, int(campaign.WbCampaignID))
			campaignByWBID[int(campaign.WbCampaignID)] = campaign
		}

		stats, fetchErr := s.wbClient.GetCampaignStats(ctx, cabinet.token, wbIDs, dateFrom, dateTo)
		if fetchErr != nil {
			summary.addIssue("campaign_stats.fetch", cabinet.cabinet.ID.String(), "get campaign stats: %v", fetchErr)
			continue
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
			}); upsertErr != nil {
				summary.addIssue("campaign_stats.upsert", stat.CampaignID.String(), "failed to upsert campaign stat: %v", upsertErr)
				continue
			}
			summary.CampaignStats++
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			summary.addIssue("seller_cabinet.sync_mark", cabinet.cabinet.ID.String(), "failed to update sync timestamp: %v", err)
		}
	}

	return summary, summary.Error()
}

func (s *SyncService) SyncPhrases(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: len(cabinets)}
	for _, cabinet := range cabinets {
		campaigns, fetchErr := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinet.cabinet.ID),
			Limit:           syncBatchLimit,
			Offset:          0,
		})
		if fetchErr != nil {
			return summary, apperror.New(apperror.ErrInternal, "failed to list campaigns for cabinet")
		}

		for _, campaign := range campaigns {
			// Skip non-active campaigns to avoid hitting WB rate limits (673 campaigns × 2 API calls each = 1346 calls)
			if campaign.Status != "active" && campaign.Status != "paused" {
				continue
			}
			clusters, clusterErr := s.wbClient.ListSearchClusters(ctx, cabinet.token, int(campaign.WbCampaignID))
			if clusterErr != nil {
				summary.addIssue("phrases.list_clusters", fmt.Sprintf("%d", campaign.WbCampaignID), "list search clusters: %v", clusterErr)
				continue
			}

			phraseIDsByCluster := make(map[int64]uuid.UUID, len(clusters))
			for _, clusterDTO := range clusters {
				phrase := wb.MapSearchClusterDTO(clusterDTO, uuidFromPgtype(campaign.ID), workspaceID)
				row, upsertErr := s.queries.UpsertPhrase(ctx, sqlcgen.UpsertPhraseParams{
					CampaignID:  uuidToPgtype(phrase.CampaignID),
					WorkspaceID: uuidToPgtype(phrase.WorkspaceID),
					WbClusterID: phrase.WBClusterID,
					Keyword:     phrase.Keyword,
					Count:       intPtrToPgInt4(phrase.Count),
					CurrentBid:  int64PtrToPgInt8(phrase.CurrentBid),
				})
				if upsertErr != nil {
					summary.addIssue("phrases.upsert", fmt.Sprintf("%d", clusterDTO.ClusterID), "failed to upsert phrase: %v", upsertErr)
					continue
				}
				phraseIDsByCluster[clusterDTO.ClusterID] = uuidFromPgtype(row.ID)
				summary.Phrases++
			}

			stats, statsErr := s.wbClient.GetSearchClusterStats(ctx, cabinet.token, int(campaign.WbCampaignID))
			if statsErr != nil {
				summary.addIssue("phrase_stats.fetch", fmt.Sprintf("%d", campaign.WbCampaignID), "get search cluster stats: %v", statsErr)
				continue
			}

			for _, statDTO := range stats {
				phraseID, ok := phraseIDsByCluster[statDTO.ClusterID]
				if !ok {
					summary.SkippedCampaign++
					continue
				}
				stat, mapErr := wb.MapSearchClusterStatDTO(statDTO, phraseID)
				if mapErr != nil {
					summary.addIssue("phrase_stats.map", fmt.Sprintf("%d", statDTO.ClusterID), "map phrase stat: %v", mapErr)
					continue
				}
				if _, upsertErr := s.queries.UpsertPhraseStat(ctx, sqlcgen.UpsertPhraseStatParams{
					PhraseID:    uuidToPgtype(stat.PhraseID),
					Date:        pgDate(stat.Date),
					Impressions: stat.Impressions,
					Clicks:      stat.Clicks,
					Spend:       stat.Spend,
				}); upsertErr != nil {
					summary.addIssue("phrase_stats.upsert", stat.PhraseID.String(), "failed to upsert phrase stat: %v", upsertErr)
					continue
				}
				summary.PhraseStats++
			}
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// syncCampaignStatsForCabinet syncs stats only for campaigns belonging to a specific cabinet.
func (s *SyncService) syncCampaignStatsForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) (SyncSummary, error) {
	campaigns, err := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Limit:           10000,
		Offset:          0,
	})
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: 1}

	// Collect campaign WB IDs for batch stats request
	campaignIDs := make([]int, 0, len(campaigns))
	campaignMap := make(map[int]sqlcgen.Campaign)
	for _, c := range campaigns {
		campaignIDs = append(campaignIDs, int(c.WbCampaignID))
		campaignMap[int(c.WbCampaignID)] = c
	}

	dateFrom := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	dateTo := time.Now().Format("2006-01-02")

	stats, fetchErr := s.wbClient.GetCampaignStats(ctx, token, campaignIDs, dateFrom, dateTo)
	if fetchErr != nil {
		summary.addIssue("stats.fetch", cabinetID.String(), "fetch stats: %v", fetchErr)
		return summary, fetchErr
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
		}); upsertErr != nil {
			continue
		}
		summary.CampaignStats++
	}
	return summary, summary.Error()
}

// syncPhrasesForCabinet syncs phrases only for campaigns belonging to a specific cabinet.
func (s *SyncService) syncPhrasesForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) (SyncSummary, error) {
	campaigns, err := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Limit:           10000,
		Offset:          0,
	})
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: 1}
	for _, campaign := range campaigns {
		// Skip non-active campaigns to avoid rate limits
		if campaign.Status != "active" && campaign.Status != "paused" {
			continue
		}
		clusters, fetchErr := s.wbClient.ListSearchClusters(ctx, token, int(campaign.WbCampaignID))
		if fetchErr != nil {
			summary.addIssue("phrases.fetch", fmt.Sprintf("%d", campaign.WbCampaignID), "fetch: %v", fetchErr)
			continue
		}
		for _, clusterDTO := range clusters {
			phrase := wb.MapSearchClusterDTO(clusterDTO, workspaceID, uuidFromPgtype(campaign.ID))
			if _, upsertErr := s.queries.UpsertPhrase(ctx, sqlcgen.UpsertPhraseParams{
				WorkspaceID: uuidToPgtype(workspaceID),
				CampaignID:  uuidToPgtype(phrase.CampaignID),
				WbClusterID: phrase.WBClusterID,
				Keyword:     phrase.Keyword,
				CurrentBid:  int64PtrToPgInt8(phrase.CurrentBid),
				Count:       int32PtrToPgInt4(phrase.Count),
			}); upsertErr != nil {
				continue
			}
			summary.Phrases++
		}
	}
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
