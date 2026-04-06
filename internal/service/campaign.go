package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// CampaignService handles campaign read operations.
type CampaignService struct {
	queries *sqlcgen.Queries
}

type CampaignListFilter struct {
	SellerCabinetID *uuid.UUID
	Status          string
	Name            string
}

// NewCampaignService creates a new CampaignService.
func NewCampaignService(queries *sqlcgen.Queries) *CampaignService {
	return &CampaignService{queries: queries}
}

// List returns campaigns for a workspace with pagination.
func (s *CampaignService) List(ctx context.Context, workspaceID uuid.UUID, filter CampaignListFilter, limit, offset int32) ([]domain.Campaign, error) {
	rows, err := s.queries.ListCampaignsByWorkspace(ctx, sqlcgen.ListCampaignsByWorkspaceParams{
		WorkspaceID:           uuidToPgtype(workspaceID),
		SellerCabinetIDFilter: uuidToPgtypePtr(filter.SellerCabinetID),
		StatusFilter:          textToPgtype(filter.Status),
		NameFilter:            textToPgtype(filter.Name),
		Limit:                 limit,
		Offset:                offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list campaigns")
	}

	result := make([]domain.Campaign, len(rows))
	for i, row := range rows {
		result[i] = campaignFromSqlc(row)
	}
	return result, nil
}

// Get returns a single campaign by ID, verifying workspace ownership.
func (s *CampaignService) Get(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.Campaign, error) {
	c, err := s.queries.GetCampaignByID(ctx, uuidToPgtype(campaignID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get campaign")
	}

	if uuidFromPgtype(c.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}

	result := campaignFromSqlc(c)
	return &result, nil
}

// GetStats returns campaign statistics filtered by date range with pagination.
func (s *CampaignService) GetStats(ctx context.Context, campaignID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.CampaignStat, error) {
	rows, err := s.queries.GetCampaignStatsByDateRange(ctx, sqlcgen.GetCampaignStatsByDateRangeParams{
		CampaignID: uuidToPgtype(campaignID),
		Date:       pgtype.Date{Time: dateFrom, Valid: true},
		Date_2:     pgtype.Date{Time: dateTo, Valid: true},
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get campaign stats")
	}

	result := make([]domain.CampaignStat, len(rows))
	for i, row := range rows {
		result[i] = campaignStatFromSqlc(row)
	}
	return result, nil
}

// ListPhrases returns phrases for a campaign with pagination.
func (s *CampaignService) ListPhrases(ctx context.Context, campaignID uuid.UUID, limit, offset int32) ([]domain.Phrase, error) {
	rows, err := s.queries.ListPhrasesByCampaign(ctx, sqlcgen.ListPhrasesByCampaignParams{
		CampaignID: uuidToPgtype(campaignID),
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list phrases")
	}

	result := make([]domain.Phrase, len(rows))
	for i, row := range rows {
		result[i] = phraseFromSqlc(row)
	}
	return result, nil
}

// ListRecommendations returns recommendations for a campaign with pagination.
func (s *CampaignService) ListRecommendations(ctx context.Context, workspaceID, campaignID uuid.UUID, filter RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	if _, err := s.Get(ctx, workspaceID, campaignID); err != nil {
		return nil, err
	}

	rows, err := s.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           offset,
		CampaignIDFilter: uuidToPgtypePtr(&campaignID),
		PhraseIDFilter:   uuidToPgtypePtr(filter.PhraseID),
		ProductIDFilter:  uuidToPgtypePtr(filter.ProductID),
		TypeFilter:       textToPgtype(filter.Type),
		SeverityFilter:   textToPgtype(filter.Severity),
		StatusFilter:     textToPgtype(filter.Status),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list campaign recommendations")
	}

	result := make([]domain.Recommendation, len(rows))
	for i, row := range rows {
		result[i] = recommendationFromSqlc(row)
	}
	return result, nil
}

// --- sqlc → domain mappers ---

func campaignFromSqlc(c sqlcgen.Campaign) domain.Campaign {
	result := domain.Campaign{
		ID:              uuidFromPgtype(c.ID),
		WorkspaceID:     uuidFromPgtype(c.WorkspaceID),
		SellerCabinetID: uuidFromPgtype(c.SellerCabinetID),
		WBCampaignID:    c.WbCampaignID,
		Name:            c.Name,
		Status:          c.Status,
		CampaignType:    int(c.CampaignType),
		BidType:         c.BidType,
		PaymentType:     c.PaymentType,
		CreatedAt:       c.CreatedAt.Time,
		UpdatedAt:       c.UpdatedAt.Time,
	}
	if c.DailyBudget.Valid {
		v := c.DailyBudget.Int64
		result.DailyBudget = &v
	}
	return result
}

func campaignStatFromSqlc(s sqlcgen.CampaignStat) domain.CampaignStat {
	result := domain.CampaignStat{
		ID:          uuidFromPgtype(s.ID),
		CampaignID:  uuidFromPgtype(s.CampaignID),
		Date:        s.Date.Time,
		Impressions: s.Impressions,
		Clicks:      s.Clicks,
		Spend:       s.Spend,
		CreatedAt:   s.CreatedAt.Time,
		UpdatedAt:   s.UpdatedAt.Time,
	}
	if s.Orders.Valid {
		v := s.Orders.Int64
		result.Orders = &v
	}
	if s.Revenue.Valid {
		v := s.Revenue.Int64
		result.Revenue = &v
	}
	return result
}

func phraseFromSqlc(p sqlcgen.Phrase) domain.Phrase {
	result := domain.Phrase{
		ID:          uuidFromPgtype(p.ID),
		CampaignID:  uuidFromPgtype(p.CampaignID),
		WorkspaceID: uuidFromPgtype(p.WorkspaceID),
		WBClusterID: p.WbClusterID,
		Keyword:     p.Keyword,
		CreatedAt:   p.CreatedAt.Time,
		UpdatedAt:   p.UpdatedAt.Time,
	}
	if p.Count.Valid {
		v := int(p.Count.Int32)
		result.Count = &v
	}
	if p.CurrentBid.Valid {
		v := p.CurrentBid.Int64
		result.CurrentBid = &v
	}
	return result
}
