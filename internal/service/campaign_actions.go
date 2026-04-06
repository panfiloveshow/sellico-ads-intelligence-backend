package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// CampaignActionService handles campaign control (start/pause/stop) and bid management.
type CampaignActionService struct {
	queries       *sqlcgen.Queries
	wbClient      *wb.Client
	encryptionKey []byte
	logger        zerolog.Logger
}

func NewCampaignActionService(queries *sqlcgen.Queries, wbClient *wb.Client, encryptionKey []byte, logger zerolog.Logger) *CampaignActionService {
	return &CampaignActionService{
		queries:       queries,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "campaign_actions").Logger(),
	}
}

func (s *CampaignActionService) StartCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	token, wbCampaignID, err := s.resolveWBCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	return s.wbClient.StartCampaign(ctx, token, wbCampaignID)
}

func (s *CampaignActionService) PauseCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	token, wbCampaignID, err := s.resolveWBCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	return s.wbClient.PauseCampaign(ctx, token, wbCampaignID)
}

func (s *CampaignActionService) StopCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	token, wbCampaignID, err := s.resolveWBCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	return s.wbClient.StopCampaign(ctx, token, wbCampaignID)
}

func (s *CampaignActionService) SetBid(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, placement string, newBid int) (*domain.BidChange, error) {
	campaign, err := s.queries.GetCampaignByID(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return nil, err
	}

	// Get current bid (approximate from latest bid snapshot)
	oldBid := 0

	if err := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), 0, placement, newBid); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update bid in WB")
	}

	change, err := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: campaign.SellerCabinetID,
		CampaignID:      uuidToPgtype(campaignID),
		Placement:       placement,
		OldBid:          int32(oldBid),
		NewBid:          int32(newBid),
		Reason:          fmt.Sprintf("Manual bid set by user %s", actorID),
		Source:          domain.BidSourceManual,
		WbStatus:        "applied",
	})
	if err != nil {
		s.logger.Error().Err(err).Msg("failed to record bid change")
		return nil, apperror.New(apperror.ErrInternal, "bid applied but failed to record")
	}

	result := bidChangeFromSqlc(change)
	return &result, nil
}

func (s *CampaignActionService) ListBidHistory(ctx context.Context, campaignID uuid.UUID, limit, offset int32) ([]domain.BidChange, error) {
	rows, err := s.queries.ListBidChangesByCampaign(ctx, sqlcgen.ListBidChangesByCampaignParams{
		CampaignID: uuidToPgtype(campaignID),
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list bid history")
	}
	result := make([]domain.BidChange, len(rows))
	for i, row := range rows {
		result[i] = bidChangeFromSqlc(row)
	}
	return result, nil
}

func (s *CampaignActionService) ApplyRecommendation(ctx context.Context, workspaceID, recommendationID, actorID uuid.UUID) (*domain.BidChange, error) {
	rec, err := s.queries.GetRecommendationByID(ctx, uuidToPgtype(recommendationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "recommendation not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get recommendation")
	}

	recType := rec.Type
	if recType != "raise_bid" && recType != "lower_bid" {
		return nil, apperror.New(apperror.ErrValidation, "recommendation type is not applicable for automatic bid changes")
	}

	if !rec.CampaignID.Valid {
		return nil, apperror.New(apperror.ErrValidation, "recommendation has no linked campaign")
	}

	campaign, err := s.queries.GetCampaignByID(ctx, rec.CampaignID)
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "linked campaign not found")
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return nil, err
	}

	// Determine new bid from recommendation source_metrics
	var suggestedBid int
	if recType == "lower_bid" {
		suggestedBid = extractSuggestedBid(rec.SourceMetrics, "competitive_bid", 0.9)
	} else {
		suggestedBid = extractSuggestedBid(rec.SourceMetrics, "competitive_bid", 1.1)
	}
	if suggestedBid == 0 {
		return nil, apperror.New(apperror.ErrValidation, "cannot determine target bid from recommendation metrics")
	}

	oldBid := extractIntFromMetrics(rec.SourceMetrics, "current_bid")

	if err := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), 0, "search", suggestedBid); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to apply bid to WB")
	}

	change, err := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		SellerCabinetID:  campaign.SellerCabinetID,
		CampaignID:       rec.CampaignID,
		RecommendationID: uuidToPgtype(recommendationID),
		Placement:        "search",
		OldBid:           int32(oldBid),
		NewBid:           int32(suggestedBid),
		Reason:           fmt.Sprintf("Applied recommendation %s: %s", recType, rec.Title),
		Source:           domain.BidSourceRecommendation,
		WbStatus:         "applied",
	})
	if err != nil {
		s.logger.Error().Err(err).Msg("bid applied but failed to record change")
	}

	// Mark recommendation as completed
	s.queries.UpdateRecommendationStatus(ctx, sqlcgen.UpdateRecommendationStatusParams{
		ID:     uuidToPgtype(recommendationID),
		Status: "completed",
	})

	result := bidChangeFromSqlc(change)
	return &result, nil
}

func (s *CampaignActionService) resolveWBCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (string, int64, error) {
	campaign, err := s.queries.GetCampaignByID(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return "", 0, apperror.New(apperror.ErrNotFound, "campaign not found")
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return "", 0, err
	}

	return token, campaign.WbCampaignID, nil
}

func (s *CampaignActionService) decryptCabinetToken(ctx context.Context, cabinetID pgtype.UUID) (string, error) {
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, cabinetID)
	if err != nil {
		return "", apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	token, err := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
	if err != nil {
		return "", apperror.New(apperror.ErrInternal, "failed to decrypt cabinet token")
	}
	return token, nil
}

func extractSuggestedBid(metricsJSON []byte, key string, multiplier float64) int {
	val := extractIntFromMetrics(metricsJSON, key)
	if val == 0 {
		return 0
	}
	return int(float64(val) * multiplier)
}

func extractIntFromMetrics(metricsJSON []byte, key string) int {
	if len(metricsJSON) == 0 {
		return 0
	}
	var m map[string]any
	if err := json.Unmarshal(metricsJSON, &m); err != nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func bidChangeFromSqlc(row sqlcgen.BidChange) domain.BidChange {
	bc := domain.BidChange{
		ID:              uuidFromPgtype(row.ID),
		WorkspaceID:     uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		CampaignID:      uuidFromPgtype(row.CampaignID),
		Placement:       row.Placement,
		OldBid:          int(row.OldBid),
		NewBid:          int(row.NewBid),
		Reason:          row.Reason,
		Source:          row.Source,
		WBStatus:        row.WbStatus,
		CreatedAt:       row.CreatedAt.Time,
	}
	if row.ProductID.Valid {
		id := uuidFromPgtype(row.ProductID)
		bc.ProductID = &id
	}
	if row.PhraseID.Valid {
		id := uuidFromPgtype(row.PhraseID)
		bc.PhraseID = &id
	}
	if row.StrategyID.Valid {
		id := uuidFromPgtype(row.StrategyID)
		bc.StrategyID = &id
	}
	if row.RecommendationID.Valid {
		id := uuidFromPgtype(row.RecommendationID)
		bc.RecommendationID = &id
	}
	if row.Acos.Valid {
		bc.ACoS = &row.Acos.Float64
	}
	if row.Roas.Valid {
		bc.ROAS = &row.Roas.Float64
	}
	return bc
}
