package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "start"); err != nil {
		return err
	}
	err = s.wbClient.StartCampaign(ctx, token, campaign.WbCampaignID)
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidAction(ctx, workspaceID, campaign, uuid.Nil, "campaign_start", 0, 0, "", "manual campaign start", err)
	return err
}

func (s *CampaignActionService) PauseCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "pause"); err != nil {
		return err
	}
	err = s.wbClient.PauseCampaign(ctx, token, campaign.WbCampaignID)
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidAction(ctx, workspaceID, campaign, uuid.Nil, "campaign_pause", 0, 0, "", "manual campaign pause", err)
	return err
}

func (s *CampaignActionService) StopCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "stop"); err != nil {
		return err
	}
	err = s.wbClient.StopCampaign(ctx, token, campaign.WbCampaignID)
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidAction(ctx, workspaceID, campaign, uuid.Nil, "campaign_stop", 0, 0, "", "manual campaign stop", err)
	return err
}

func (s *CampaignActionService) SetBid(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, placement string, newBid int) (*domain.BidChange, error) {
	campaign, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return nil, err
	}

	oldBid, ok, bidErr := currentBidFromCampaignPhrases(ctx, s.queries, campaignID)
	if bidErr != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load current bid")
	}
	if !ok {
		return nil, apperror.New(apperror.ErrValidation, "current bid is unavailable or ambiguous; sync real bid data before changing bids")
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "bid"); err != nil {
		return nil, err
	}

	if err := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), 0, placement, newBid); err != nil {
		s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
		s.recordWBBidAction(ctx, workspaceID, campaign, actorID, "product_bid", int64(oldBid), int64(newBid), "", fmt.Sprintf("manual %s bid change", placement), err)
		return nil, apperror.New(apperror.ErrInternal, "failed to update bid in WB")
	}
	s.recordWBBidAction(ctx, workspaceID, campaign, actorID, "product_bid", int64(oldBid), int64(newBid), "", fmt.Sprintf("manual %s bid change", placement), nil)

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

func (s *CampaignActionService) SetClusterBid(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, nmID int64, normQuery string, newBid int) error {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if campaign.PaymentType != "cpm" || campaign.BidType != "manual" {
		return apperror.New(apperror.ErrValidation, "cluster bids are available only for manual CPM campaigns")
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "cluster_bid"); err != nil {
		return err
	}
	if nmID <= 0 || strings.TrimSpace(normQuery) == "" || newBid <= 0 {
		return apperror.New(apperror.ErrValidation, "nm_id, norm_query and new_bid are required")
	}
	err = s.wbClient.SetClusterBids(ctx, token, campaign.WbCampaignID, []wb.ClusterBidItem{{
		NMID:      nmID,
		NormQuery: normQuery,
		Bid:       newBid,
	}})
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidAction(ctx, workspaceID, campaign, actorID, "cluster_bid", 0, int64(newBid), normQuery, "manual cluster bid change", err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to update cluster bid in WB")
	}
	return nil
}

func (s *CampaignActionService) SetClusterMinus(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, nmID int64, normQuery string) error {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if campaign.PaymentType != "cpm" || campaign.BidType != "manual" {
		return apperror.New(apperror.ErrValidation, "cluster minus is available only for manual CPM campaigns")
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "cluster_minus"); err != nil {
		return err
	}
	if strings.TrimSpace(normQuery) == "" {
		return apperror.New(apperror.ErrValidation, "norm_query is required")
	}
	err = s.wbClient.SetClusterMinus(ctx, token, campaign.WbCampaignID, []wb.ClusterMinusItem{{
		NMID:      nmID,
		NormQuery: normQuery,
	}})
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidAction(ctx, workspaceID, campaign, actorID, "cluster_minus", 0, 0, normQuery, "manual cluster minus", err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to set cluster minus in WB")
	}
	return nil
}

func (s *CampaignActionService) DepositBudget(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, amount int64) error {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if amount <= 0 {
		return apperror.New(apperror.ErrValidation, "amount must be positive")
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointBudget, "budget_deposit"); err != nil {
		return err
	}
	err = s.wbClient.DepositCampaignBudget(ctx, token, campaign.WbCampaignID, amount)
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointBudget, err)
	s.recordWBBidAction(ctx, workspaceID, campaign, actorID, "budget_deposit", 0, amount, "", "manual budget deposit", err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to deposit campaign budget in WB")
	}
	return nil
}

func (s *CampaignActionService) GetMinimumBids(ctx context.Context, workspaceID, campaignID uuid.UUID, nmIDs []int) ([]wb.WBMinimumBidDTO, error) {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return nil, err
	}
	return s.wbClient.GetMinimumBids(ctx, token, int(campaign.WbCampaignID), nmIDs)
}

func (s *CampaignActionService) preflightCampaignAction(ctx context.Context, campaign sqlcgen.Campaign, endpointKey, action string) error {
	if err := s.ensureWBEndpointAvailable(ctx, campaign.SellerCabinetID, endpointKey); err != nil {
		return err
	}
	status := strings.ToLower(strings.TrimSpace(campaign.Status))
	switch action {
	case "start":
		if status != "paused" && status != "ready" {
			return apperror.New(apperror.ErrValidation, "campaign can be started only from paused or ready status")
		}
	case "pause":
		if status != "active" {
			return apperror.New(apperror.ErrValidation, "only active campaigns can be paused")
		}
	case "stop":
		if status != "active" && status != "paused" && status != "ready" {
			return apperror.New(apperror.ErrValidation, "campaign cannot be stopped from current status")
		}
	case "bid", "cluster_bid", "cluster_minus", "budget_deposit":
		if status != "active" && status != "paused" && status != "ready" {
			return apperror.New(apperror.ErrValidation, "campaign action is unavailable for current status")
		}
	}
	return nil
}

func (s *CampaignActionService) ensureWBEndpointAvailable(ctx context.Context, sellerCabinetID pgtype.UUID, endpointKey string) error {
	limit, err := s.queries.GetWBAPIRateLimit(ctx, sellerCabinetID, endpointKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		s.logger.Warn().Err(err).Str("endpoint", endpointKey).Msg("failed to read WB action rate limit")
		return nil
	}
	if !limit.NextAllowedAt.Valid {
		return nil
	}
	next := limit.NextAllowedAt.Time.UTC()
	if !next.After(time.Now().UTC()) {
		return nil
	}
	return apperror.New(apperror.ErrRateLimited, fmt.Sprintf("WB ограничил действие до %s", next.Format(time.RFC3339)))
}

func (s *CampaignActionService) recordActionRateLimitFromError(ctx context.Context, sellerCabinetID pgtype.UUID, endpointKey string, err error) {
	if err == nil || !isRateLimitIssue(err.Error()) {
		return
	}
	delay := wbEndpointFallbackDelay(endpointKey)
	next := time.Now().UTC().Add(delay)
	lastError := strings.TrimSpace(err.Error())
	if len(lastError) > 500 {
		lastError = lastError[:500]
	}
	if upsertErr := s.queries.UpsertWBAPIRateLimit(ctx, sqlcgen.UpsertWBAPIRateLimitParams{
		SellerCabinetID:   sellerCabinetID,
		EndpointKey:       endpointKey,
		NextAllowedAt:     pgtype.Timestamptz{Time: next, Valid: true},
		RetryAfterSeconds: int32(delay.Seconds()),
		LastStatus:        429,
		LastError:         pgtype.Text{String: lastError, Valid: lastError != ""},
	}); upsertErr != nil {
		s.logger.Warn().Err(upsertErr).Str("endpoint", endpointKey).Msg("failed to persist WB action rate limit")
	}
}

func (s *CampaignActionService) ListBidHistory(ctx context.Context, workspaceID, campaignID uuid.UUID, limit, offset int32) ([]domain.BidChange, error) {
	rows, err := s.queries.ListBidChangesByCampaignAndWorkspace(ctx, sqlcgen.ListBidChangesByCampaignAndWorkspaceParams{
		CampaignID:  uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       limit,
		Offset:      offset,
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
	rec, err := s.queries.GetRecommendationByIDAndWorkspace(ctx, sqlcgen.GetRecommendationByIDAndWorkspaceParams{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
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

	campaign, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
		ID:          rec.CampaignID,
		WorkspaceID: uuidToPgtype(workspaceID),
	})
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

	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "bid"); err != nil {
		return nil, err
	}
	if err := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), 0, "search", suggestedBid); err != nil {
		s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
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
	s.queries.UpdateRecommendationStatusInWorkspace(ctx, sqlcgen.UpdateRecommendationStatusInWorkspaceParams{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Status:      "completed",
	})

	result := bidChangeFromSqlc(change)
	return &result, nil
}

func (s *CampaignActionService) resolveWBCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (string, int64, error) {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return "", 0, err
	}
	return token, campaign.WbCampaignID, nil
}

func (s *CampaignActionService) resolveCampaignWithToken(ctx context.Context, workspaceID, campaignID uuid.UUID) (sqlcgen.Campaign, string, error) {
	campaign, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return sqlcgen.Campaign{}, "", apperror.New(apperror.ErrNotFound, "campaign not found")
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return sqlcgen.Campaign{}, "", err
	}

	return campaign, token, nil
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

func (s *CampaignActionService) recordWBBidAction(ctx context.Context, workspaceID uuid.UUID, campaign sqlcgen.Campaign, actorID uuid.UUID, actionType string, oldBid, newBid int64, normQuery, reason string, actionErr error) {
	status := "applied"
	var response []byte
	if actionErr != nil {
		status = "failed"
		response, _ = json.Marshal(map[string]string{"error": actionErr.Error()})
	}
	var createdBy pgtype.UUID
	if actorID != uuid.Nil {
		createdBy = uuidToPgtype(actorID)
	}
	var oldBidValue, newBidValue pgtype.Int8
	if oldBid != 0 {
		oldBidValue = pgtype.Int8{Int64: oldBid, Valid: true}
	}
	if newBid != 0 {
		newBidValue = pgtype.Int8{Int64: newBid, Valid: true}
	}
	var normQueryValue pgtype.Text
	if strings.TrimSpace(normQuery) != "" {
		normQueryValue = pgtype.Text{String: strings.TrimSpace(normQuery), Valid: true}
	}
	var reasonValue pgtype.Text
	if reason != "" {
		reasonValue = pgtype.Text{String: reason, Valid: true}
	}
	if err := s.queries.CreateWBBidAction(ctx, sqlcgen.CreateWBBidActionParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: campaign.SellerCabinetID,
		CampaignID:      campaign.ID,
		WBCampaignID:    campaign.WbCampaignID,
		NormQuery:       normQueryValue,
		ActionType:      actionType,
		OldBid:          oldBidValue,
		NewBid:          newBidValue,
		Reason:          reasonValue,
		Status:          status,
		WBResponse:      response,
		CreatedBy:       createdBy,
	}); err != nil {
		s.logger.Warn().Err(err).Str("action_type", actionType).Msg("failed to record wb bid action")
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
