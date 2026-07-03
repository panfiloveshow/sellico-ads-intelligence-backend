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
	economics     UnitEconomicsReadinessProvider
	logger        zerolog.Logger
}

type CampaignActionOption func(*CampaignActionService)

type CreateCampaignActionInput struct {
	SellerCabinetID uuid.UUID
	Name            string
	NMIDs           []int64
	BidType         string
	PaymentType     string
	PlacementTypes  []string
}

func WithCampaignActionUnitEconomicsReadinessProvider(provider UnitEconomicsReadinessProvider) CampaignActionOption {
	return func(s *CampaignActionService) {
		s.economics = provider
	}
}

func NewCampaignActionService(queries *sqlcgen.Queries, wbClient *wb.Client, encryptionKey []byte, logger zerolog.Logger, opts ...CampaignActionOption) *CampaignActionService {
	svc := &CampaignActionService{
		queries:       queries,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "campaign_actions").Logger(),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func normalizeCreateCampaignActionInput(input CreateCampaignActionInput) (CreateCampaignActionInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return input, apperror.New(apperror.ErrValidation, "campaign name is required")
	}
	if len([]rune(input.Name)) > 100 {
		return input, apperror.New(apperror.ErrValidation, "campaign name must be 100 characters or fewer")
	}
	if input.SellerCabinetID == uuid.Nil {
		return input, apperror.New(apperror.ErrValidation, "seller cabinet id is required")
	}

	seenNMIDs := make(map[int64]struct{}, len(input.NMIDs))
	nmIDs := make([]int64, 0, len(input.NMIDs))
	for _, nmID := range input.NMIDs {
		if nmID <= 0 {
			return input, apperror.New(apperror.ErrValidation, "nm_ids must contain positive WB product IDs")
		}
		if _, ok := seenNMIDs[nmID]; ok {
			continue
		}
		seenNMIDs[nmID] = struct{}{}
		nmIDs = append(nmIDs, nmID)
	}
	if len(nmIDs) == 0 {
		return input, apperror.New(apperror.ErrValidation, "at least one WB product ID is required")
	}
	if len(nmIDs) > 50 {
		return input, apperror.New(apperror.ErrValidation, "campaign can include up to 50 WB product IDs")
	}
	input.NMIDs = nmIDs

	input.BidType = strings.TrimSpace(input.BidType)
	if input.BidType == "" {
		input.BidType = "manual"
	}
	switch input.BidType {
	case "manual", "unified":
	default:
		return input, apperror.New(apperror.ErrValidation, "bid_type must be manual or unified")
	}

	input.PaymentType = strings.TrimSpace(input.PaymentType)
	if input.PaymentType == "" {
		input.PaymentType = "cpm"
	}
	switch input.PaymentType {
	case "cpm", "cpc":
	default:
		return input, apperror.New(apperror.ErrValidation, "payment_type must be cpm or cpc")
	}

	seenPlacements := make(map[string]struct{}, len(input.PlacementTypes))
	placements := make([]string, 0, len(input.PlacementTypes))
	for _, placement := range input.PlacementTypes {
		placement = strings.TrimSpace(placement)
		if placement == "" {
			continue
		}
		switch placement {
		case "search", "recommendations":
		default:
			return input, apperror.New(apperror.ErrValidation, "placement_types must contain search or recommendations")
		}
		if _, ok := seenPlacements[placement]; ok {
			continue
		}
		seenPlacements[placement] = struct{}{}
		placements = append(placements, placement)
	}
	if input.BidType == "manual" && len(placements) == 0 {
		placements = []string{"search"}
	}
	if input.BidType == "unified" {
		placements = nil
	}
	input.PlacementTypes = placements

	return input, nil
}

func (s *CampaignActionService) CreateCampaign(ctx context.Context, workspaceID, actorID uuid.UUID, input CreateCampaignActionInput) (*domain.Campaign, error) {
	normalized, err := normalizeCreateCampaignActionInput(input)
	if err != nil {
		return nil, err
	}

	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(normalized.SellerCabinetID))
	if err != nil || uuidFromPgtype(cabinet.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err := s.ensureWBEndpointAvailable(ctx, cabinet.ID, wbEndpointCampaignActions); err != nil {
		return nil, err
	}
	token, err := s.decryptCabinetToken(ctx, cabinet.ID)
	if err != nil {
		return nil, err
	}

	request := wb.CreateCampaignRequest{
		Name:        normalized.Name,
		NMIDs:       normalized.NMIDs,
		BidType:     normalized.BidType,
		PaymentType: normalized.PaymentType,
	}
	if normalized.BidType == "manual" {
		request.PlacementTypes = normalized.PlacementTypes
	}
	wbCampaignID, err := s.wbClient.CreateCampaign(ctx, token, request)
	s.recordActionRateLimitFromError(ctx, cabinet.ID, wbEndpointCampaignActions, err)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create campaign in WB")
	}

	row, err := s.queries.UpsertCampaign(ctx, sqlcgen.UpsertCampaignParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: cabinet.ID,
		WbCampaignID:    wbCampaignID,
		Name:            normalized.Name,
		Status:          "ready",
		CampaignType:    9,
		BidType:         normalized.BidType,
		PaymentType:     normalized.PaymentType,
		DailyBudget:     pgtype.Int8{},
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "campaign created in WB but failed to save local campaign")
	}
	s.recordWBBidAction(ctx, workspaceID, row, actorID, "campaign_create", 0, wbCampaignID, "", "manual campaign create", nil)
	result := campaignFromSqlc(row)
	return &result, nil
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

func (s *CampaignActionService) RenameCampaign(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return apperror.New(apperror.ErrValidation, "campaign name is required")
	}
	if len([]rune(name)) > 100 {
		return apperror.New(apperror.ErrValidation, "campaign name must be 100 characters or fewer")
	}

	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "rename"); err != nil {
		return err
	}

	oldName := campaign.Name
	err = s.wbClient.RenameCampaign(ctx, token, campaign.WbCampaignID, name)
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordCampaignControlAudit(ctx, workspaceID, actorID, campaign, "campaign_rename", oldName, name, err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to rename campaign in WB")
	}
	if _, updateErr := s.queries.UpdateCampaign(ctx, sqlcgen.UpdateCampaignParams{
		ID:          campaign.ID,
		Name:        name,
		Status:      campaign.Status,
		BidType:     campaign.BidType,
		PaymentType: campaign.PaymentType,
		DailyBudget: campaign.DailyBudget,
	}); updateErr != nil {
		return apperror.New(apperror.ErrInternal, "campaign renamed in WB but failed to update local campaign")
	}
	return nil
}

func (s *CampaignActionService) DeleteCampaign(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID) error {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "delete"); err != nil {
		return err
	}

	err = s.wbClient.DeleteCampaign(ctx, token, campaign.WbCampaignID)
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordCampaignControlAudit(ctx, workspaceID, actorID, campaign, "campaign_delete", campaign.Status, "deleted", err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to delete campaign in WB")
	}
	if _, updateErr := s.queries.UpdateCampaign(ctx, sqlcgen.UpdateCampaignParams{
		ID:          campaign.ID,
		Name:        campaign.Name,
		Status:      "deleted",
		BidType:     campaign.BidType,
		PaymentType: campaign.PaymentType,
		DailyBudget: campaign.DailyBudget,
	}); updateErr != nil {
		return apperror.New(apperror.ErrInternal, "campaign deleted in WB but failed to update local campaign")
	}
	return nil
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
	if err := s.ensureBidIncreaseReadiness(ctx, workspaceID, campaign, oldBid, newBid); err != nil {
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

func (s *CampaignActionService) RollbackBidChange(ctx context.Context, workspaceID, campaignID, bidChangeID, actorID uuid.UUID) (*domain.BidChange, error) {
	row, err := s.queries.GetBidChangeByIDAndWorkspace(ctx, sqlcgen.GetBidChangeByIDAndWorkspaceParams{
		ID:          uuidToPgtype(bidChangeID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "bid change not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load bid change")
	}
	if err := validateBidChangeRollbackTarget(row, campaignID); err != nil {
		return nil, err
	}

	campaign, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
		ID:          row.CampaignID,
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return nil, err
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "bid"); err != nil {
		return nil, err
	}
	if row.PhraseID.Valid {
		return s.rollbackClusterBidChange(ctx, workspaceID, campaign, row, token, bidChangeID, actorID)
	}

	currentBid, ok, err := currentBidForRollback(ctx, s.queries, campaignID, row.ProductID)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load current bid")
	}
	if !ok {
		return nil, apperror.New(apperror.ErrValidation, "current bid is unavailable or ambiguous; sync real bid data before rollback")
	}
	if currentBid != int(row.NewBid) {
		return nil, apperror.New(apperror.ErrValidation, fmt.Sprintf("current bid %d no longer matches changed bid %d; rollback would overwrite a newer real bid", currentBid, row.NewBid))
	}

	nmID, err := s.rollbackTargetNMID(ctx, row.ProductID)
	if err != nil {
		return nil, err
	}
	rollbackBid := int(row.OldBid)
	reason := fmt.Sprintf("rollback bid change %s to previous bid %d", bidChangeID, rollbackBid)
	if err := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), nmID, row.Placement, rollbackBid); err != nil {
		s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
		s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, row.ProductID, nmID, actorID, "rollback_bid", int64(currentBid), int64(rollbackBid), "", reason, err)
		return nil, apperror.New(apperror.ErrInternal, "failed to rollback bid in WB")
	}
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, row.ProductID, nmID, actorID, "rollback_bid", int64(currentBid), int64(rollbackBid), "", reason, nil)

	change, err := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: campaign.SellerCabinetID,
		CampaignID:      row.CampaignID,
		ProductID:       row.ProductID,
		Placement:       row.Placement,
		OldBid:          int32(currentBid),
		NewBid:          int32(rollbackBid),
		Reason:          reason,
		Source:          domain.BidSourceManual,
		WbStatus:        "applied",
	})
	if err != nil {
		s.logger.Error().Err(err).Msg("bid rollback applied but failed to record change")
		return nil, apperror.New(apperror.ErrInternal, "bid rollback applied but failed to record")
	}

	result := bidChangeFromSqlc(change)
	return &result, nil
}

func (s *CampaignActionService) rollbackClusterBidChange(ctx context.Context, workspaceID uuid.UUID, campaign sqlcgen.Campaign, row sqlcgen.BidChange, token string, bidChangeID, actorID uuid.UUID) (*domain.BidChange, error) {
	if campaign.PaymentType != domain.PaymentTypeCPM || campaign.BidType != domain.BidTypeManual {
		return nil, apperror.New(apperror.ErrValidation, "cluster bid rollback is available only for manual CPM campaigns")
	}
	phrase, err := s.loadRollbackPhrase(ctx, workspaceID, row)
	if err != nil {
		return nil, err
	}
	currentBid, ok := currentClusterBidForRollback(phrase)
	if !ok {
		return nil, apperror.New(apperror.ErrValidation, "current cluster bid is unavailable; sync real cluster bid data before rollback")
	}
	if currentBid != int(row.NewBid) {
		return nil, apperror.New(apperror.ErrValidation, fmt.Sprintf("current cluster bid %d no longer matches changed bid %d; rollback would overwrite a newer real bid", currentBid, row.NewBid))
	}
	nmID, normQuery, err := phraseClusterActionTarget(phrase, nil)
	if err != nil {
		return nil, err
	}

	rollbackBid := int(row.OldBid)
	reason := fmt.Sprintf("rollback cluster bid change %s to previous bid %d", bidChangeID, rollbackBid)
	err = s.wbClient.SetClusterBids(ctx, token, campaign.WbCampaignID, []wb.ClusterBidItem{{
		NMID:      nmID,
		NormQuery: normQuery,
		Bid:       rollbackBid,
	}})
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, phrase.ProductID, nmID, actorID, "rollback_cluster_bid", int64(currentBid), int64(rollbackBid), normQuery, reason, err)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to rollback cluster bid in WB")
	}

	change, err := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: campaign.SellerCabinetID,
		CampaignID:      row.CampaignID,
		ProductID:       phrase.ProductID,
		PhraseID:        row.PhraseID,
		Placement:       row.Placement,
		OldBid:          int32(currentBid),
		NewBid:          int32(rollbackBid),
		Reason:          reason,
		Source:          domain.BidSourceManual,
		WbStatus:        "applied",
	})
	if err != nil {
		s.logger.Error().Err(err).Msg("cluster bid rollback applied but failed to record change")
		return nil, apperror.New(apperror.ErrInternal, "cluster bid rollback applied but failed to record")
	}

	result := bidChangeFromSqlc(change)
	return &result, nil
}

func (s *CampaignActionService) loadRollbackPhrase(ctx context.Context, workspaceID uuid.UUID, row sqlcgen.BidChange) (sqlcgen.Phrase, error) {
	phrase, err := s.queries.GetPhraseByID(ctx, row.PhraseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.Phrase{}, apperror.New(apperror.ErrNotFound, "rollback phrase not found")
	}
	if err != nil {
		return sqlcgen.Phrase{}, apperror.New(apperror.ErrInternal, "failed to load rollback phrase")
	}
	if uuidFromPgtype(phrase.WorkspaceID) != workspaceID || !phrase.CampaignID.Valid || uuidFromPgtype(phrase.CampaignID) != uuidFromPgtype(row.CampaignID) {
		return sqlcgen.Phrase{}, apperror.New(apperror.ErrNotFound, "rollback phrase not found")
	}
	return phrase, nil
}

func currentClusterBidForRollback(phrase sqlcgen.Phrase) (int, bool) {
	if !phrase.CurrentBid.Valid || phrase.CurrentBid.Int64 <= 0 {
		return 0, false
	}
	return int(phrase.CurrentBid.Int64), true
}

func validateBidChangeRollbackTarget(row sqlcgen.BidChange, campaignID uuid.UUID) error {
	if !row.CampaignID.Valid || uuidFromPgtype(row.CampaignID) != campaignID {
		return apperror.New(apperror.ErrValidation, "bid change does not belong to this campaign")
	}
	if row.WbStatus != "applied" {
		return apperror.New(apperror.ErrValidation, "only applied bid changes can be rolled back")
	}
	if row.OldBid <= 0 || row.NewBid <= 0 || row.OldBid == row.NewBid {
		return apperror.New(apperror.ErrValidation, "bid change has no previous bid to restore")
	}
	return nil
}

func currentBidForRollback(ctx context.Context, queries *sqlcgen.Queries, campaignID uuid.UUID, productID pgtype.UUID) (int, bool, error) {
	if !productID.Valid {
		return currentBidFromCampaignPhrases(ctx, queries, campaignID)
	}
	phrases, err := queries.ListPhrasesByCampaign(ctx, sqlcgen.ListPhrasesByCampaignParams{
		CampaignID: uuidToPgtype(campaignID),
		Limit:      1000,
		Offset:     0,
	})
	if err != nil {
		return 0, false, err
	}

	var currentBid int
	for _, phrase := range phrases {
		if !phrase.ProductID.Valid || uuidFromPgtype(phrase.ProductID) != uuidFromPgtype(productID) {
			continue
		}
		if !phrase.CurrentBid.Valid || phrase.CurrentBid.Int64 <= 0 {
			continue
		}
		bid := int(phrase.CurrentBid.Int64)
		if currentBid == 0 {
			currentBid = bid
			continue
		}
		if currentBid != bid {
			return 0, false, nil
		}
	}
	if currentBid == 0 {
		return 0, false, nil
	}
	return currentBid, true, nil
}

func (s *CampaignActionService) rollbackTargetNMID(ctx context.Context, productID pgtype.UUID) (int64, error) {
	if !productID.Valid {
		return 0, nil
	}
	product, err := s.queries.GetProductByID(ctx, productID)
	if err != nil {
		return 0, apperror.New(apperror.ErrValidation, "rollback product target is unavailable; sync real product data before rollback")
	}
	if product.WbProductID <= 0 {
		return 0, apperror.New(apperror.ErrValidation, "rollback product target has no real WB product id")
	}
	return product.WbProductID, nil
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
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, pgtype.UUID{}, nmID, actorID, "cluster_bid", 0, int64(newBid), normQuery, "manual cluster bid change", err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to update cluster bid in WB")
	}
	return nil
}

func (s *CampaignActionService) DeleteClusterBid(ctx context.Context, workspaceID, campaignID, actorID uuid.UUID, nmID int64, normQuery string, currentBid int) error {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return err
	}
	if campaign.PaymentType != "cpm" || campaign.BidType != "manual" {
		return apperror.New(apperror.ErrValidation, "cluster bid deletion is available only for manual CPM campaigns")
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "cluster_bid"); err != nil {
		return err
	}
	if nmID <= 0 || strings.TrimSpace(normQuery) == "" || currentBid <= 0 {
		return apperror.New(apperror.ErrValidation, "nm_id, norm_query and current_bid are required")
	}
	err = s.wbClient.DeleteClusterBids(ctx, token, campaign.WbCampaignID, []wb.ClusterBidItem{{
		NMID:      nmID,
		NormQuery: normQuery,
		Bid:       currentBid,
	}})
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, pgtype.UUID{}, nmID, actorID, "cluster_bid_delete", int64(currentBid), 0, normQuery, "manual cluster bid deletion", err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to delete cluster bid in WB")
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
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, pgtype.UUID{}, nmID, actorID, "cluster_minus", 0, 0, normQuery, "manual cluster minus", err)
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to set cluster minus in WB")
	}
	if err := s.recordLocalMinusPhrase(ctx, campaign.ID, normQuery); err != nil {
		return apperror.New(apperror.ErrInternal, "cluster minus applied but failed to record local minus phrase")
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
	if err := s.ensureBudgetDepositReadiness(ctx, campaign, amount); err != nil {
		return err
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

func (s *CampaignActionService) ensureBudgetDepositReadiness(ctx context.Context, campaign sqlcgen.Campaign, amount int64) error {
	balance, err := s.queries.GetLatestSellerAdBalance(ctx, campaign.SellerCabinetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrValidation, "sync WB advertising balance before depositing campaign budget")
	}
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to load WB advertising balance")
	}
	if reason := budgetDepositGuardrailReason(balance, amount, time.Now()); reason != "" {
		return apperror.New(apperror.ErrValidation, reason)
	}
	return nil
}

func budgetDepositGuardrailReason(balance sqlcgen.SellerAdBalance, amountRub int64, now time.Time) string {
	if amountRub <= 0 {
		return "amount must be positive"
	}
	if !balance.CapturedAt.Valid {
		return "latest WB advertising balance snapshot is unavailable"
	}
	if now.Sub(balance.CapturedAt.Time) > 24*time.Hour {
		return fmt.Sprintf("latest WB advertising balance snapshot is stale: %s", balance.CapturedAt.Time.Format("2006-01-02 15:04"))
	}
	amountKopecks := amountRub * 100
	if amountRub > 0 && amountKopecks/100 != amountRub {
		return "amount is too large"
	}
	if balance.Balance < amountKopecks {
		return fmt.Sprintf("deposit amount %d ₽ exceeds latest WB advertising balance %.2f ₽", amountRub, float64(balance.Balance)/100)
	}
	return ""
}

func (s *CampaignActionService) GetClusterMinus(ctx context.Context, workspaceID, campaignID uuid.UUID, nmID int64) ([]string, error) {
	campaign, token, err := s.resolveCampaignWithToken(ctx, workspaceID, campaignID)
	if err != nil {
		return nil, err
	}
	if campaign.PaymentType != "cpm" || campaign.BidType != "manual" {
		return nil, apperror.New(apperror.ErrValidation, "cluster minus is available only for manual CPM campaigns")
	}
	if err := s.ensureWBEndpointAvailable(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions); err != nil {
		return nil, err
	}
	if nmID <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "nm_id is required")
	}
	phrases, err := s.wbClient.GetClusterMinus(ctx, token, campaign.WbCampaignID, nmID)
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get cluster minus phrases from WB")
	}
	return phrases, nil
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
	case "delete":
		if status != "ready" {
			return apperror.New(apperror.ErrValidation, "campaign can be deleted only from ready status")
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
	if reason := wbEndpointRateLimitBlockReason(endpointKey, limit, time.Now().UTC()); reason != "" {
		return apperror.New(apperror.ErrRateLimited, reason)
	}
	return nil
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
		s.enrichBidChangeOutcome(ctx, &result[i])
	}
	return result, nil
}

func (s *CampaignActionService) enrichBidChangeOutcome(ctx context.Context, change *domain.BidChange) {
	if change == nil || change.WBStatus != "applied" || change.CreatedAt.IsZero() {
		return
	}
	baselineDate := normalizeStatDate(change.CreatedAt)
	outcomeDate := baselineDate.AddDate(0, 0, 1)
	rows, err := s.queries.GetCampaignStatsByDateRange(ctx, sqlcgen.GetCampaignStatsByDateRangeParams{
		CampaignID: uuidToPgtype(change.CampaignID),
		Date:       pgtype.Date{Time: baselineDate, Valid: true},
		Date_2:     pgtype.Date{Time: outcomeDate, Valid: true},
		Limit:      10,
		Offset:     0,
	})
	if err != nil {
		s.logger.Warn().Err(err).Str("bid_change_id", change.ID.String()).Msg("failed to load bid change outcome stats")
		return
	}
	change.Outcome = bidChangeOutcomeFromStats(change.CreatedAt, rows)
}

func bidChangeOutcomeFromStats(changeTime time.Time, rows []sqlcgen.CampaignStat) *domain.BidChangeOutcome {
	if changeTime.IsZero() || len(rows) == 0 {
		return nil
	}
	baselineDate := normalizeStatDate(changeTime)
	outcomeDate := baselineDate.AddDate(0, 0, 1)
	stats := make([]domain.CampaignStat, 0, len(rows))
	for _, row := range rows {
		stats = append(stats, campaignStatFromSqlc(row))
	}
	baseline := aggregateCampaignStats(stats, baselineDate, baselineDate)
	outcome := aggregateCampaignStats(stats, outcomeDate, outcomeDate)
	if baseline.DataMode == "unavailable" || outcome.DataMode == "unavailable" {
		return nil
	}
	compare := buildPeriodCompare(outcome, baseline)
	return &domain.BidChangeOutcome{
		DataMode:     "exact",
		Window:       "change_day_to_next_day",
		BaselineDate: baselineDate.Format("2006-01-02"),
		OutcomeDate:  outcomeDate.Format("2006-01-02"),
		Baseline:     baseline,
		Outcome:      outcome,
		Delta:        compare.Delta,
		Trend:        compare.Trend,
	}
}

func (s *CampaignActionService) ApplyRecommendation(ctx context.Context, workspaceID, recommendationID, actorID uuid.UUID) (*domain.Recommendation, error) {
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
	if rec.Status != domain.RecommendationStatusActive {
		return nil, apperror.New(apperror.ErrValidation, "recommendation is not active")
	}

	switch recType {
	case domain.RecommendationTypeRaiseBid, domain.RecommendationTypeLowerBid:
		return s.applyBidRecommendation(ctx, workspaceID, recommendationID, actorID, rec)
	case domain.RecommendationTypeAddMinusPhrase:
		return s.applyAddMinusPhraseRecommendation(ctx, workspaceID, recommendationID, actorID, rec)
	default:
		return nil, apperror.New(apperror.ErrValidation, "recommendation type is not applicable for automatic actions")
	}
}

func (s *CampaignActionService) applyBidRecommendation(ctx context.Context, workspaceID, recommendationID, actorID uuid.UUID, rec sqlcgen.Recommendation) (*domain.Recommendation, error) {
	if !rec.CampaignID.Valid && rec.PhraseID.Valid {
		return s.applyPhraseBidRecommendation(ctx, workspaceID, recommendationID, actorID, rec)
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
	if rec.Type == domain.RecommendationTypeLowerBid {
		suggestedBid = extractSuggestedBid(rec.SourceMetrics, "competitive_bid", 0.9)
	} else {
		suggestedBid = extractSuggestedBid(rec.SourceMetrics, "competitive_bid", 1.1)
	}
	if suggestedBid == 0 {
		return nil, apperror.New(apperror.ErrValidation, "cannot determine target bid from recommendation metrics")
	}

	oldBid := extractIntFromMetrics(rec.SourceMetrics, "current_bid")
	if oldBid <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "current bid is unavailable; sync real bid data before applying recommendation")
	}

	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "bid"); err != nil {
		return nil, err
	}
	if err := s.ensureBidIncreaseReadiness(ctx, workspaceID, campaign, oldBid, suggestedBid); err != nil {
		return nil, err
	}
	if err := s.wbClient.UpdateCampaignBid(ctx, token, campaign.WbCampaignID, int(campaign.CampaignType), 0, "search", suggestedBid); err != nil {
		s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
		s.recordWBBidAction(ctx, workspaceID, campaign, actorID, "recommendation_bid", int64(oldBid), int64(suggestedBid), "", fmt.Sprintf("apply recommendation %s: %s", rec.Type, rec.Title), err)
		return nil, apperror.New(apperror.ErrInternal, "failed to apply bid to WB")
	}
	s.recordWBBidAction(ctx, workspaceID, campaign, actorID, "recommendation_bid", int64(oldBid), int64(suggestedBid), "", fmt.Sprintf("apply recommendation %s: %s", rec.Type, rec.Title), nil)

	if _, err := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		SellerCabinetID:  campaign.SellerCabinetID,
		CampaignID:       rec.CampaignID,
		RecommendationID: uuidToPgtype(recommendationID),
		Placement:        "search",
		OldBid:           int32(oldBid),
		NewBid:           int32(suggestedBid),
		Reason:           fmt.Sprintf("Applied recommendation %s: %s", rec.Type, rec.Title),
		Source:           domain.BidSourceRecommendation,
		WbStatus:         "applied",
	}); err != nil {
		s.logger.Error().Err(err).Msg("bid applied but failed to record change")
		return nil, apperror.New(apperror.ErrInternal, "bid applied but failed to record")
	}

	return s.completeRecommendation(ctx, workspaceID, recommendationID)
}

func (s *CampaignActionService) applyPhraseBidRecommendation(ctx context.Context, workspaceID, recommendationID, actorID uuid.UUID, rec sqlcgen.Recommendation) (*domain.Recommendation, error) {
	phrase, err := s.loadRecommendationPhrase(ctx, workspaceID, rec)
	if err != nil {
		return nil, err
	}

	campaign, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
		ID:          phrase.CampaignID,
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "linked campaign not found")
	}
	if campaign.PaymentType != domain.PaymentTypeCPM || campaign.BidType != domain.BidTypeManual {
		return nil, apperror.New(apperror.ErrValidation, "cluster bids are available only for manual CPM campaigns")
	}

	oldBid := extractIntFromMetrics(rec.SourceMetrics, "current_bid")
	if oldBid <= 0 && phrase.CurrentBid.Valid {
		oldBid = int(phrase.CurrentBid.Int64)
	}
	if oldBid <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "current phrase bid is unavailable; sync real bid data before applying recommendation")
	}

	var suggestedBid int
	if rec.Type == domain.RecommendationTypeLowerBid {
		suggestedBid = extractSuggestedBid(rec.SourceMetrics, "competitive_bid", 0.9)
	} else {
		suggestedBid = extractSuggestedBid(rec.SourceMetrics, "competitive_bid", 1.1)
	}
	if suggestedBid == 0 {
		return nil, apperror.New(apperror.ErrValidation, "cannot determine target bid from recommendation metrics")
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "cluster_bid"); err != nil {
		return nil, err
	}
	if err := s.ensureBidIncreaseReadiness(ctx, workspaceID, campaign, oldBid, suggestedBid); err != nil {
		return nil, err
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return nil, err
	}
	nmID, normQuery, err := phraseClusterActionTarget(phrase, rec.SourceMetrics)
	if err != nil {
		return nil, err
	}

	err = s.wbClient.SetClusterBids(ctx, token, campaign.WbCampaignID, []wb.ClusterBidItem{{
		NMID:      nmID,
		NormQuery: normQuery,
		Bid:       suggestedBid,
	}})
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, phrase.ProductID, nmID, actorID, "recommendation_cluster_bid", int64(oldBid), int64(suggestedBid), normQuery, fmt.Sprintf("apply recommendation %s: %s", rec.Type, rec.Title), err)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to apply cluster bid to WB")
	}

	if _, err := s.queries.CreateBidChange(ctx, sqlcgen.CreateBidChangeParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		SellerCabinetID:  campaign.SellerCabinetID,
		CampaignID:       phrase.CampaignID,
		ProductID:        phrase.ProductID,
		PhraseID:         rec.PhraseID,
		RecommendationID: uuidToPgtype(recommendationID),
		Placement:        "search",
		OldBid:           int32(oldBid),
		NewBid:           int32(suggestedBid),
		Reason:           fmt.Sprintf("Applied phrase recommendation %s: %s", rec.Type, rec.Title),
		Source:           domain.BidSourceRecommendation,
		WbStatus:         "applied",
	}); err != nil {
		s.logger.Error().Err(err).Msg("cluster bid applied but failed to record change")
		return nil, apperror.New(apperror.ErrInternal, "cluster bid applied but failed to record")
	}

	return s.completeRecommendation(ctx, workspaceID, recommendationID)
}

func (s *CampaignActionService) applyAddMinusPhraseRecommendation(ctx context.Context, workspaceID, recommendationID, actorID uuid.UUID, rec sqlcgen.Recommendation) (*domain.Recommendation, error) {
	if !rec.PhraseID.Valid {
		return nil, apperror.New(apperror.ErrValidation, "recommendation has no linked phrase")
	}

	phrase, err := s.loadRecommendationPhrase(ctx, workspaceID, rec)
	if err != nil {
		return nil, err
	}

	campaignID := phrase.CampaignID
	if rec.CampaignID.Valid {
		campaignID = rec.CampaignID
	}
	campaign, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
		ID:          campaignID,
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "linked campaign not found")
	}

	if campaign.PaymentType != domain.PaymentTypeCPM || campaign.BidType != domain.BidTypeManual {
		return nil, apperror.New(apperror.ErrValidation, "cluster minus is available only for manual CPM campaigns")
	}
	if reason := clusterMinusCPOGuardrailReason(rec.SourceMetrics); reason != "" {
		return nil, apperror.New(apperror.ErrValidation, reason)
	}
	if err := s.preflightCampaignAction(ctx, campaign, wbEndpointCampaignActions, "cluster_minus"); err != nil {
		return nil, err
	}

	token, err := s.decryptCabinetToken(ctx, campaign.SellerCabinetID)
	if err != nil {
		return nil, err
	}

	nmID, normQuery, err := phraseClusterActionTarget(phrase, rec.SourceMetrics)
	if err != nil {
		return nil, err
	}

	err = s.wbClient.SetClusterMinus(ctx, token, campaign.WbCampaignID, []wb.ClusterMinusItem{{
		NMID:      nmID,
		NormQuery: normQuery,
	}})
	s.recordActionRateLimitFromError(ctx, campaign.SellerCabinetID, wbEndpointCampaignActions, err)
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, phrase.ProductID, nmID, actorID, "cluster_minus", 0, 0, normQuery, fmt.Sprintf("apply recommendation %s: %s", rec.Type, rec.Title), err)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to set cluster minus in WB")
	}
	if err := s.recordLocalMinusPhrase(ctx, campaign.ID, normQuery); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "cluster minus applied but failed to record local minus phrase")
	}

	return s.completeRecommendation(ctx, workspaceID, recommendationID)
}

func clusterMinusCPOGuardrailReason(sourceMetrics []byte) string {
	spend := extractFloat64FromMetrics(sourceMetrics, "spend")
	if spend <= 0 {
		return "cluster spend evidence is unavailable; sync real phrase stats before applying minus"
	}
	targetCPO := extractFloat64FromMetrics(sourceMetrics, "target_cpo")
	if targetCPO <= 0 {
		targetCPO = extractFloat64FromMetrics(sourceMetrics, "campaign_poor_cpo")
	}
	if targetCPO <= 0 {
		targetCPO = extractFloat64FromMetrics(sourceMetrics, "max_cpo")
	}
	if targetCPO <= 0 {
		return "target CPO evidence is unavailable; configure CPO threshold before applying cluster minus"
	}
	minSpend := targetCPO * 1.5
	if spend < minSpend {
		return fmt.Sprintf("cluster spend %.0f is below 1.5x target CPO %.0f; do not apply minus yet", spend, targetCPO)
	}
	return ""
}

func (s *CampaignActionService) loadRecommendationPhrase(ctx context.Context, workspaceID uuid.UUID, rec sqlcgen.Recommendation) (sqlcgen.Phrase, error) {
	if !rec.PhraseID.Valid {
		return sqlcgen.Phrase{}, apperror.New(apperror.ErrValidation, "recommendation has no linked phrase")
	}
	phrase, err := s.queries.GetPhraseByID(ctx, rec.PhraseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcgen.Phrase{}, apperror.New(apperror.ErrNotFound, "linked phrase not found")
	}
	if err != nil {
		return sqlcgen.Phrase{}, apperror.New(apperror.ErrInternal, "failed to get linked phrase")
	}
	if uuidFromPgtype(phrase.WorkspaceID) != workspaceID {
		return sqlcgen.Phrase{}, apperror.New(apperror.ErrNotFound, "linked phrase not found")
	}
	return phrase, nil
}

func phraseClusterActionTarget(phrase sqlcgen.Phrase, sourceMetrics []byte) (int64, string, error) {
	nmID := int64(0)
	if phrase.WbProductID.Valid {
		nmID = phrase.WbProductID.Int64
	}
	if nmID <= 0 {
		nmID = extractInt64FromMetrics(sourceMetrics, "wb_product_id")
	}
	normQuery := strings.TrimSpace(phrase.WbNormQuery)
	if normQuery == "" {
		normQuery = strings.TrimSpace(extractStringFromMetrics(sourceMetrics, "wb_norm_query"))
	}
	if normQuery == "" {
		normQuery = strings.TrimSpace(phrase.Keyword)
	}
	if nmID <= 0 || normQuery == "" {
		return 0, "", apperror.New(apperror.ErrValidation, "recommendation lacks real nm_id or norm_query for cluster action")
	}
	return nmID, normQuery, nil
}

func (s *CampaignActionService) recordLocalMinusPhrase(ctx context.Context, campaignID pgtype.UUID, normQuery string) error {
	if strings.TrimSpace(normQuery) == "" {
		return nil
	}
	_, err := s.queries.CreateMinusPhrase(ctx, campaignID, strings.TrimSpace(normQuery))
	return err
}

func (s *CampaignActionService) completeRecommendation(ctx context.Context, workspaceID, recommendationID uuid.UUID) (*domain.Recommendation, error) {
	row, err := s.queries.UpdateRecommendationStatusInWorkspace(ctx, sqlcgen.UpdateRecommendationStatusInWorkspaceParams{
		ID:          uuidToPgtype(recommendationID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Status:      domain.RecommendationStatusCompleted,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to mark recommendation as completed")
	}
	result := recommendationFromSqlc(row)
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

func (s *CampaignActionService) ensureBidIncreaseReadiness(ctx context.Context, workspaceID uuid.UUID, campaign sqlcgen.Campaign, oldBid, newBid int) error {
	if newBid <= oldBid {
		return nil
	}

	products, err := s.productsForCampaignReadiness(ctx, campaign.ID)
	if err != nil {
		s.logger.Warn().
			Err(err).
			Int64("wb_campaign_id", campaign.WbCampaignID).
			Msg("bid increase blocked because campaign products could not be loaded")
		return apperror.New(apperror.ErrValidation, "real product stock could not be loaded; sync campaign products before increasing bids")
	}
	if len(products) == 0 {
		return apperror.New(apperror.ErrValidation, "campaign has no real linked products; sync campaign products before increasing bids")
	}

	productIDs := make([]uuid.UUID, 0, len(products))
	wbProductIDs := make([]int64, 0, len(products))
	for _, product := range products {
		productID := uuidFromPgtype(product.ID)
		productIDs = append(productIDs, productID)
		wbProductIDs = append(wbProductIDs, product.WbProductID)

		ev := latestProductStockEvidence(ctx, s.queries, product.ID)
		if !ev.OK {
			return apperror.New(apperror.ErrValidation, "real product stock evidence is unavailable; sync product stock before increasing bids")
		}
		// Quantity unknown (delivery_data confirms presence but not count) → presence
		// alone is enough; only a confirmed zero stock blocks the increase.
		if ev.QuantityKnown && ev.Stock <= 0 {
			return apperror.New(apperror.ErrValidation, "one or more linked products have no confirmed stock; increase is blocked")
		}
		if reason := productReputationBidIncreaseBlockReason(product); reason != "" {
			return apperror.New(apperror.ErrValidation, reason)
		}
	}

	if s.economics == nil {
		return apperror.New(apperror.ErrValidation, "unit economics readiness provider is not configured; increase is blocked")
	}
	readiness, err := s.economics.CheckBidIncreaseReadiness(ctx, UnitEconomicsReadinessInput{
		WorkspaceID:     workspaceID,
		SellerCabinetID: uuidFromPgtype(campaign.SellerCabinetID),
		ProductIDs:      productIDs,
		WBProductIDs:    wbProductIDs,
	})
	if err != nil {
		s.logger.Warn().
			Err(err).
			Int64("wb_campaign_id", campaign.WbCampaignID).
			Msg("bid increase blocked because unit economics readiness could not be loaded")
		return apperror.New(apperror.ErrValidation, "unit economics readiness could not be loaded; increase is blocked")
	}
	if !readiness.AllowsBidIncrease() {
		return apperror.New(apperror.ErrValidation, readiness.BlockReason())
	}
	return nil
}

func (s *CampaignActionService) productsForCampaignReadiness(ctx context.Context, campaignID pgtype.UUID) ([]sqlcgen.Product, error) {
	links, err := s.queries.ListCampaignProductsByCampaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}

	products := make([]sqlcgen.Product, 0, len(links))
	for _, link := range links {
		if !link.ProductID.Valid {
			continue
		}
		product, err := s.queries.GetProductByID(ctx, link.ProductID)
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	return products, nil
}

func extractSuggestedBid(metricsJSON []byte, key string, multiplier float64) int {
	val := extractIntFromMetrics(metricsJSON, key)
	if val == 0 {
		return 0
	}
	return int(float64(val) * multiplier)
}

func extractIntFromMetrics(metricsJSON []byte, key string) int {
	return int(extractInt64FromMetrics(metricsJSON, key))
}

func extractFloat64FromMetrics(metricsJSON []byte, key string) float64 {
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
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func extractInt64FromMetrics(metricsJSON []byte, key string) int64 {
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
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	default:
		return 0
	}
}

func extractStringFromMetrics(metricsJSON []byte, key string) string {
	if len(metricsJSON) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(metricsJSON, &m); err != nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (s *CampaignActionService) recordWBBidAction(ctx context.Context, workspaceID uuid.UUID, campaign sqlcgen.Campaign, actorID uuid.UUID, actionType string, oldBid, newBid int64, normQuery, reason string, actionErr error) {
	s.recordWBBidActionWithTarget(ctx, workspaceID, campaign, pgtype.UUID{}, 0, actorID, actionType, oldBid, newBid, normQuery, reason, actionErr)
}

func (s *CampaignActionService) recordCampaignControlAudit(ctx context.Context, workspaceID, actorID uuid.UUID, campaign sqlcgen.Campaign, action, oldValue, newValue string, actionErr error) {
	status := "applied"
	if actionErr != nil {
		status = "failed"
	}
	metadata := map[string]any{
		"action_type":    action,
		"status":         status,
		"campaign_id":    uuidFromPgtype(campaign.ID),
		"wb_campaign_id": campaign.WbCampaignID,
		"old_value":      oldValue,
		"new_value":      newValue,
	}
	if actionErr != nil {
		metadata["error"] = actionErr.Error()
	}
	payload, _ := json.Marshal(metadata)
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "wb_campaign_action",
		EntityType:  "campaign",
		EntityID:    campaign.ID,
		Metadata:    payload,
	})
}

func (s *CampaignActionService) recordWBBidActionWithTarget(ctx context.Context, workspaceID uuid.UUID, campaign sqlcgen.Campaign, productID pgtype.UUID, wbProductID int64, actorID uuid.UUID, actionType string, oldBid, newBid int64, normQuery, reason string, actionErr error) {
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
		ProductID:       productID,
		WBCampaignID:    campaign.WbCampaignID,
		WBProductID:     wbProductID,
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

	metadata, _ := json.Marshal(campaignActionAuditMetadata(campaign, productID, wbProductID, actionType, oldBid, newBid, normQuery, reason, status, actionErr))
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      createdBy,
		Action:      "wb_bid_action",
		EntityType:  "campaign",
		EntityID:    campaign.ID,
		Metadata:    metadata,
	})
}

func campaignActionAuditMetadata(campaign sqlcgen.Campaign, productID pgtype.UUID, wbProductID int64, actionType string, oldBid, newBid int64, normQuery, reason, status string, actionErr error) map[string]any {
	metadata := map[string]any{
		"action_type":    actionType,
		"status":         status,
		"campaign_id":    uuidFromPgtype(campaign.ID),
		"wb_campaign_id": campaign.WbCampaignID,
		"reason":         reason,
	}
	if oldBid != 0 {
		metadata["old_bid"] = oldBid
	}
	if newBid != 0 {
		metadata["new_bid"] = newBid
	}
	if strings.TrimSpace(normQuery) != "" {
		metadata["norm_query"] = strings.TrimSpace(normQuery)
	}
	if productID.Valid {
		metadata["product_id"] = uuidFromPgtype(productID)
	}
	if wbProductID != 0 {
		metadata["wb_product_id"] = wbProductID
	}
	if actionErr != nil {
		metadata["error"] = actionErr.Error()
	}
	return metadata
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
	if bc.WBStatus == "applied" && bc.OldBid > 0 && bc.NewBid > 0 && bc.OldBid != bc.NewBid {
		bc.CanRollback = true
		rollbackBid := bc.OldBid
		bc.RollbackBid = &rollbackBid
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
	bc.DecisionContext = bidChangeDecisionContext(bc)
	return bc
}

func bidChangeDecisionContext(change domain.BidChange) *domain.BidChangeDecisionContext {
	context := &domain.BidChangeDecisionContext{
		ActorType: actorTypeFromBidSource(change.Source),
		Reason:    strings.TrimSpace(change.Reason),
		DataMode:  "reason_only",
	}
	switch {
	case change.ACoS != nil:
		context.PrimaryMetric = "acos"
		context.PrimaryMetricValue = change.ACoS
		context.DataMode = "metric_evidence"
	case change.ROAS != nil:
		context.PrimaryMetric = "roas"
		context.PrimaryMetricValue = change.ROAS
		context.DataMode = "metric_evidence"
	default:
		context.MissingEvidence = []string{"primary_metric"}
	}
	if context.Reason == "" {
		context.MissingEvidence = append(context.MissingEvidence, "reason")
	}
	return context
}

func actorTypeFromBidSource(source string) string {
	switch source {
	case domain.BidSourceStrategy:
		return "autopilot"
	case domain.BidSourceRecommendation:
		return "recommendation"
	case domain.BidSourceManual:
		return "user"
	default:
		return strings.TrimSpace(source)
	}
}
