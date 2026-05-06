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
	ListSearchClustersWithNMIDs(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]wb.WBSearchClusterDTO, error)
	GetSearchClusterStats(ctx context.Context, token string, campaignID int) ([]wb.WBSearchClusterStatDTO, error)
	GetSearchClusterStatsWithNMIDs(ctx context.Context, token string, campaignID int, nmIDs []int64) ([]wb.WBSearchClusterStatDTO, error)
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
			row, upsertErr := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
				WorkspaceID:     uuidToPgtype(campaign.WorkspaceID),
				SellerCabinetID: uuidToPgtype(campaign.SellerCabinetID),
				WbCampaignID:    campaign.WBCampaignID,
				Name:            campaign.Name,
				Status:          campaign.Status,
				CampaignType:    int32(campaign.CampaignType),
				BidType:         campaign.BidType,
				PaymentType:     campaign.PaymentType,
				DailyBudget:     int64PtrToPgInt8(campaign.DailyBudget),
			})
			if upsertErr != nil {
				summary.addIssue("campaigns.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "upsert: %v", upsertErr)
				continue
			}
			if linked, linkErr := s.upsertCampaignProductLinks(ctx, workspaceID, cabinet.cabinet.ID, row, dto.NMIDs); linkErr != nil {
				summary.addIssue("campaign_products.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "link products: %v", linkErr)
			} else if linked > 0 {
				s.logger.Debug().Int("links", linked).Int64("campaign_id", campaign.WBCampaignID).Msg("campaign products linked")
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

	// Stats — scoped to single cabinet
	s.logger.Info().Str("cabinet_id", cabinetID.String()).Int("campaigns", summary.Campaigns).Msg("[sync] phase 2: syncing stats")
	statsSummary, statsErr := s.syncCampaignStatsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
	if statsErr != nil {
		s.logger.Error().Err(statsErr).Msg("[sync] stats failed")
		summary.addIssue("stats", cabinetID.String(), "sync stats: %v", statsErr)
	}
	summary.CampaignStats = statsSummary.CampaignStats
	s.logger.Info().Int("campaign_stats", summary.CampaignStats).Msg("[sync] phase 2 done")

	// Phrases + Products — run in PARALLEL (different WB APIs, no rate conflict)
	s.logger.Info().Msg("[sync] phase 3+4: syncing phrases and products in parallel")
	var phrasesSummary, productsSummary SyncSummary
	var phrasesErr, productsErr error

	syncWg := make(chan struct{}, 2)
	go func() {
		phrasesSummary, phrasesErr = s.syncPhrasesForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
		if phrasesErr != nil {
			s.logger.Error().Err(phrasesErr).Msg("[sync] phrases failed")
		}
		syncWg <- struct{}{}
	}()
	go func() {
		productsSummary, productsErr = s.syncProductsForCabinet(ctx, workspaceID, cabinetID, cabinets[0].token)
		if productsErr != nil {
			s.logger.Error().Err(productsErr).Msg("[sync] products failed")
		}
		syncWg <- struct{}{}
	}()
	<-syncWg
	<-syncWg

	if phrasesErr != nil {
		summary.addIssue("phrases", cabinetID.String(), "sync phrases: %v", phrasesErr)
	}
	summary.Phrases = phrasesSummary.Phrases
	if productsErr != nil {
		summary.addIssue("products", cabinetID.String(), "sync products: %v", productsErr)
	}
	summary.Products = productsSummary.Products
	s.logger.Info().Int("phrases", summary.Phrases).Int("products", summary.Products).Msg("[sync] phase 3+4 done")

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
			row, upsertErr := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
				WorkspaceID:     uuidToPgtype(campaign.WorkspaceID),
				SellerCabinetID: uuidToPgtype(campaign.SellerCabinetID),
				WbCampaignID:    campaign.WBCampaignID,
				Name:            campaign.Name,
				Status:          campaign.Status,
				CampaignType:    int32(campaign.CampaignType),
				BidType:         campaign.BidType,
				PaymentType:     campaign.PaymentType,
				DailyBudget:     int64PtrToPgInt8(campaign.DailyBudget),
			})
			if upsertErr != nil {
				summary.addIssue("campaigns.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "failed to upsert campaign: %v", upsertErr)
				continue
			}
			if linked, linkErr := s.upsertCampaignProductLinks(ctx, workspaceID, cabinet.cabinet.ID, row, campaignDTO.NMIDs); linkErr != nil {
				summary.addIssue("campaign_products.upsert", fmt.Sprintf("%d", campaign.WBCampaignID), "link products: %v", linkErr)
			} else if linked > 0 {
				s.logger.Debug().Int("links", linked).Int64("campaign_id", campaign.WBCampaignID).Msg("campaign products linked")
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
			// Only active/paused — skip 90% of inactive campaigns to avoid rate limits
			if campaign.Status != "active" && campaign.Status != "paused" {
				continue
			}
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
			if productStatsErr := s.upsertProductStatsFromCampaignStat(ctx, workspaceID, cabinet.cabinet.ID, campaign, statDTO); productStatsErr != nil {
				summary.addIssue("product_stats.upsert", fmt.Sprintf("%d", statDTO.AdvertID), "upsert product stats: %v", productStatsErr)
			}
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			summary.addIssue("seller_cabinet.sync_mark", cabinet.cabinet.ID.String(), "failed to update sync timestamp: %v", err)
		}
	}

	return summary, summary.Error()
}

func (s *SyncService) upsertCampaignProductLinks(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaign sqlcgen.Campaign, nmIDs []int64) (int, error) {
	linked := 0
	for _, nmID := range nmIDs {
		if nmID == 0 {
			continue
		}
		productRow, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbProductID:     nmID,
			Title:           fmt.Sprintf("Артикул %d", nmID),
			Brand:           pgtype.Text{},
			Category:        pgtype.Text{},
			ImageUrl:        pgtype.Text{},
			Price:           pgtype.Int8{},
		})
		if err != nil {
			return linked, err
		}
		if _, err := s.queries.UpsertCampaignProduct(ctx, sqlcgen.UpsertCampaignProductParams{
			CampaignID:      campaign.ID,
			ProductID:       productRow.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbCampaignID:    campaign.WbCampaignID,
			WbProductID:     nmID,
		}); err != nil {
			return linked, err
		}
		linked++
	}
	return linked, nil
}

func (s *SyncService) upsertProductStatsFromCampaignStat(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaign sqlcgen.Campaign, statDTO wb.WBCampaignStatDTO) error {
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
			return err
		}
		if _, err := s.queries.UpsertCampaignProduct(ctx, sqlcgen.UpsertCampaignProductParams{
			CampaignID:      campaign.ID,
			ProductID:       productRow.ID,
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbCampaignID:    campaign.WbCampaignID,
			WbProductID:     productDTO.NmID,
		}); err != nil {
			return err
		}
		stat, err := wb.MapProductStatDTO(productDTO, uuidFromPgtype(productRow.ID), uuidFromPgtype(campaign.ID))
		if err != nil {
			return err
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
		}); err != nil {
			return err
		}
	}
	return nil
}

func productTitleFromStat(dto wb.WBProductStatDTO) string {
	if dto.Name != "" {
		return dto.Name
	}
	return fmt.Sprintf("Артикул %d", dto.NmID)
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

		// Fetch WB campaigns ONCE per cabinet to get NMIDs
		wbCampaigns, wbErr := s.wbClient.ListCampaigns(ctx, cabinet.token)
		nmIDMap := make(map[int][]int64)
		if wbErr == nil {
			for _, wbCamp := range wbCampaigns {
				nmIDMap[wbCamp.AdvertID] = wbCamp.NMIDs
			}
		}

		for _, campaign := range campaigns {
			if campaign.Status != "active" && campaign.Status != "paused" {
				continue
			}
			nmIDs := nmIDMap[int(campaign.WbCampaignID)]
			if len(nmIDs) == 0 {
				continue
			}
			clusters, clusterErr := s.wbClient.ListSearchClustersWithNMIDs(ctx, cabinet.token, int(campaign.WbCampaignID), nmIDs)
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

	// Only fetch stats for active/paused campaigns (673 → ~83, saves 90% API calls)
	campaignIDs := make([]int, 0, len(campaigns))
	campaignMap := make(map[int]sqlcgen.Campaign)
	for _, c := range campaigns {
		if c.Status != "active" && c.Status != "paused" {
			continue
		}
		campaignIDs = append(campaignIDs, int(c.WbCampaignID))
		campaignMap[int(c.WbCampaignID)] = c
	}
	s.logger.Info().Int("active_campaigns", len(campaignIDs)).Int("total_campaigns", len(campaigns)).Msg("[sync] stats: filtered to active campaigns")

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
		if productStatsErr := s.upsertProductStatsFromCampaignStat(ctx, workspaceID, cabinetID, campaign, statDTO); productStatsErr != nil {
			summary.addIssue("product_stats.upsert", fmt.Sprintf("%d", statDTO.AdvertID), "upsert product stats: %v", productStatsErr)
		}
	}
	return summary, summary.Error()
}

// syncPhrasesForCabinet syncs phrases only for campaigns belonging to a specific cabinet.
// Optimized: fetches ListCampaigns ONCE to get all NMIDs, then uses WithNMIDs methods.
func (s *SyncService) syncPhrasesForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) (SyncSummary, error) {
	campaigns, err := s.queries.ListCampaignsBySellerCabinet(ctx, sqlcgen.ListCampaignsBySellerCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Limit:           10000,
		Offset:          0,
	})
	if err != nil {
		return SyncSummary{}, err
	}

	// Fetch all WB campaigns ONCE to get NMIDs mapping (saves N extra API calls)
	wbCampaigns, wbErr := s.wbClient.ListCampaigns(ctx, token)
	nmIDMap := make(map[int][]int64)
	if wbErr == nil {
		for _, wbCamp := range wbCampaigns {
			nmIDMap[wbCamp.AdvertID] = wbCamp.NMIDs
		}
	} else {
		s.logger.Warn().Err(wbErr).Msg("[sync] failed to fetch WB campaigns for NMIDs, will skip normquery")
	}

	summary := SyncSummary{Cabinets: 1}
	for _, campaign := range campaigns {
		// Skip non-active campaigns to avoid rate limits
		if campaign.Status != "active" && campaign.Status != "paused" {
			continue
		}
		wbCampaignID := int(campaign.WbCampaignID)
		campaignID := uuidFromPgtype(campaign.ID)
		nmIDs := nmIDMap[wbCampaignID]
		if len(nmIDs) == 0 {
			continue // No products linked — skip
		}

		// Rate-limit delay between campaigns (normquery API shares fullstats limit)
		if summary.Phrases > 0 {
			if err := sleepWithContext(ctx, time.Second); err != nil {
				return summary, err
			}
		}

		clusters, fetchErr := s.wbClient.ListSearchClustersWithNMIDs(ctx, token, wbCampaignID, nmIDs)
		if fetchErr != nil {
			summary.addIssue("phrases.fetch", fmt.Sprintf("%d", wbCampaignID), "fetch: %v", fetchErr)
			continue
		}

		phraseIDsByCluster := make(map[int64]uuid.UUID, len(clusters))
		for _, clusterDTO := range clusters {
			// FIX: correct argument order — (dto, campaignID, workspaceID)
			phrase := wb.MapSearchClusterDTO(clusterDTO, campaignID, workspaceID)
			row, upsertErr := s.queries.UpsertPhrase(ctx, sqlcgen.UpsertPhraseParams{
				WorkspaceID: uuidToPgtype(workspaceID),
				CampaignID:  uuidToPgtype(campaignID),
				WbClusterID: phrase.WBClusterID,
				Keyword:     phrase.Keyword,
				CurrentBid:  int64PtrToPgInt8(phrase.CurrentBid),
				Count:       int32PtrToPgInt4(phrase.Count),
			})
			if upsertErr != nil {
				s.logger.Warn().Err(upsertErr).Int64("cluster_id", clusterDTO.ClusterID).Msg("upsert phrase failed")
				continue
			}
			phraseIDsByCluster[clusterDTO.ClusterID] = uuidFromPgtype(row.ID)
			summary.Phrases++
		}

		// Sync phrase stats — uses WithNMIDs to avoid extra ListCampaigns call
		clusterStats, statsErr := s.wbClient.GetSearchClusterStatsWithNMIDs(ctx, token, wbCampaignID, nmIDs)
		if statsErr != nil {
			summary.addIssue("phrase_stats.fetch", fmt.Sprintf("%d", wbCampaignID), "stats: %v", statsErr)
			continue
		}
		for _, statDTO := range clusterStats {
			phraseID, ok := phraseIDsByCluster[statDTO.ClusterID]
			if !ok {
				continue
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
			}); upsertErr != nil {
				continue
			}
			summary.PhraseStats++
		}
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
