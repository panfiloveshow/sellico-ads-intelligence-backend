package service

import (
	"context"
	"fmt"
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
}

type SyncSummary struct {
	Cabinets        int `json:"cabinets"`
	Campaigns       int `json:"campaigns"`
	CampaignStats   int `json:"campaign_stats"`
	Phrases         int `json:"phrases"`
	PhraseStats     int `json:"phrase_stats"`
	SkippedCampaign int `json:"skipped_campaigns"`
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

func (s *SyncService) SyncCampaigns(ctx context.Context, workspaceID uuid.UUID) (SyncSummary, error) {
	cabinets, err := s.listWorkspaceCabinets(ctx, workspaceID)
	if err != nil {
		return SyncSummary{}, err
	}

	summary := SyncSummary{Cabinets: len(cabinets)}
	for _, cabinet := range cabinets {
		campaigns, fetchErr := s.wbClient.ListCampaigns(ctx, cabinet.token)
		if fetchErr != nil {
			return summary, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("list campaigns for cabinet %s: %v", cabinet.cabinet.ID.String(), fetchErr))
		}

		for _, campaignDTO := range campaigns {
			campaign := wb.MapCampaignDTO(campaignDTO, workspaceID, cabinet.cabinet.ID)
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
				return summary, apperror.New(apperror.ErrInternal, "failed to upsert campaign")
			}
			summary.Campaigns++
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			return summary, apperror.New(apperror.ErrInternal, "failed to update seller cabinet sync timestamp")
		}
	}

	return summary, nil
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
			return summary, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("get campaign stats for cabinet %s: %v", cabinet.cabinet.ID.String(), fetchErr))
		}

		for _, statDTO := range stats {
			campaign, ok := campaignByWBID[statDTO.AdvertID]
			if !ok {
				summary.SkippedCampaign++
				continue
			}
			stat, mapErr := wb.MapCampaignStatDTO(statDTO, uuidFromPgtype(campaign.ID))
			if mapErr != nil {
				return summary, apperror.New(apperror.ErrInternal, fmt.Sprintf("map campaign stat: %v", mapErr))
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
				return summary, apperror.New(apperror.ErrInternal, "failed to upsert campaign stat")
			}
			summary.CampaignStats++
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			return summary, apperror.New(apperror.ErrInternal, "failed to update seller cabinet sync timestamp")
		}
	}

	return summary, nil
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
			clusters, clusterErr := s.wbClient.ListSearchClusters(ctx, cabinet.token, int(campaign.WbCampaignID))
			if clusterErr != nil {
				return summary, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("list search clusters for campaign %d: %v", campaign.WbCampaignID, clusterErr))
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
					return summary, apperror.New(apperror.ErrInternal, "failed to upsert phrase")
				}
				phraseIDsByCluster[clusterDTO.ClusterID] = uuidFromPgtype(row.ID)
				summary.Phrases++
			}

			stats, statsErr := s.wbClient.GetSearchClusterStats(ctx, cabinet.token, int(campaign.WbCampaignID))
			if statsErr != nil {
				return summary, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("get search cluster stats for campaign %d: %v", campaign.WbCampaignID, statsErr))
			}

			for _, statDTO := range stats {
				phraseID, ok := phraseIDsByCluster[statDTO.ClusterID]
				if !ok {
					summary.SkippedCampaign++
					continue
				}
				stat, mapErr := wb.MapSearchClusterStatDTO(statDTO, phraseID)
				if mapErr != nil {
					return summary, apperror.New(apperror.ErrInternal, fmt.Sprintf("map phrase stat: %v", mapErr))
				}
				if _, upsertErr := s.queries.UpsertPhraseStat(ctx, sqlcgen.UpsertPhraseStatParams{
					PhraseID:    uuidToPgtype(stat.PhraseID),
					Date:        pgDate(stat.Date),
					Impressions: stat.Impressions,
					Clicks:      stat.Clicks,
					Spend:       stat.Spend,
				}); upsertErr != nil {
					return summary, apperror.New(apperror.ErrInternal, "failed to upsert phrase stat")
				}
				summary.PhraseStats++
			}
		}

		if err := s.queries.UpdateSellerCabinetLastSynced(ctx, uuidToPgtype(cabinet.cabinet.ID)); err != nil {
			return summary, apperror.New(apperror.ErrInternal, "failed to update seller cabinet sync timestamp")
		}
	}

	return summary, nil
}

type decryptedCabinet struct {
	cabinet domain.SellerCabinet
	token   string
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
			return nil, apperror.New(apperror.ErrDecryptionFail, "failed to decrypt seller cabinet token")
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
