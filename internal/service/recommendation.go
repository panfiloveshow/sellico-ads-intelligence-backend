package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type RecommendationListFilter struct {
	Type     string
	Severity string
	Status   string
}

type RecommendationUpsertInput struct {
	WorkspaceID   uuid.UUID
	CampaignID    *uuid.UUID
	PhraseID      *uuid.UUID
	ProductID     *uuid.UUID
	Title         string
	Description   string
	Type          string
	Severity      string
	Confidence    float64
	SourceMetrics map[string]any
	NextAction    *string
}

// RecommendationService handles recommendation read/write operations.
type RecommendationService struct {
	queries *sqlcgen.Queries
}

func NewRecommendationService(queries *sqlcgen.Queries) *RecommendationService {
	return &RecommendationService{queries: queries}
}

func (s *RecommendationService) List(ctx context.Context, workspaceID uuid.UUID, filter RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	rows, err := s.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Limit:          limit,
		Offset:         offset,
		TypeFilter:     textToPgtype(filter.Type),
		SeverityFilter: textToPgtype(filter.Severity),
		StatusFilter:   textToPgtype(filter.Status),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list recommendations")
	}

	result := make([]domain.Recommendation, len(rows))
	for i, row := range rows {
		result[i] = recommendationFromSqlc(row)
	}
	return result, nil
}

func (s *RecommendationService) Get(ctx context.Context, workspaceID, recommendationID uuid.UUID) (*domain.Recommendation, error) {
	row, err := s.queries.GetRecommendationByID(ctx, uuidToPgtype(recommendationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "recommendation not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get recommendation")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "recommendation not found")
	}

	result := recommendationFromSqlc(row)
	return &result, nil
}

func (s *RecommendationService) UpdateStatus(ctx context.Context, workspaceID, recommendationID uuid.UUID, status string) (*domain.Recommendation, error) {
	if _, err := s.Get(ctx, workspaceID, recommendationID); err != nil {
		return nil, err
	}

	row, err := s.queries.UpdateRecommendationStatus(ctx, sqlcgen.UpdateRecommendationStatusParams{
		ID:     uuidToPgtype(recommendationID),
		Status: status,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update recommendation status")
	}

	result := recommendationFromSqlc(row)
	return &result, nil
}

func (s *RecommendationService) UpsertActive(ctx context.Context, input RecommendationUpsertInput) (*domain.Recommendation, error) {
	confidence, err := numericFromFloat64(input.Confidence)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to encode recommendation confidence")
	}
	sourceMetrics, err := json.Marshal(input.SourceMetrics)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to encode recommendation source metrics")
	}

	existing, err := s.queries.GetActiveRecommendation(ctx, sqlcgen.GetActiveRecommendationParams{
		WorkspaceID:      uuidToPgtype(input.WorkspaceID),
		Type:             input.Type,
		CampaignIDFilter: uuidToPgtypePtr(input.CampaignID),
		PhraseIDFilter:   uuidToPgtypePtr(input.PhraseID),
		ProductIDFilter:  uuidToPgtypePtr(input.ProductID),
	})
	if err == nil {
		row, updateErr := s.queries.UpdateRecommendationContent(ctx, sqlcgen.UpdateRecommendationContentParams{
			ID:            existing.ID,
			Title:         input.Title,
			Description:   input.Description,
			Severity:      input.Severity,
			Confidence:    confidence,
			SourceMetrics: sourceMetrics,
			NextAction:    textToPgtype(ptrStringValue(input.NextAction)),
		})
		if updateErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to refresh recommendation")
		}
		result := recommendationFromSqlc(row)
		return &result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrInternal, "failed to lookup active recommendation")
	}

	row, err := s.queries.CreateRecommendation(ctx, sqlcgen.CreateRecommendationParams{
		WorkspaceID:   uuidToPgtype(input.WorkspaceID),
		CampaignID:    uuidToPgtypePtr(input.CampaignID),
		PhraseID:      uuidToPgtypePtr(input.PhraseID),
		ProductID:     uuidToPgtypePtr(input.ProductID),
		Title:         input.Title,
		Description:   input.Description,
		Type:          input.Type,
		Severity:      input.Severity,
		Confidence:    confidence,
		SourceMetrics: sourceMetrics,
		NextAction:    textToPgtype(ptrStringValue(input.NextAction)),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create recommendation")
	}

	result := recommendationFromSqlc(row)
	return &result, nil
}

func ptrStringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nullableUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	value := id
	return &value
}

func nullableInt64(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}
