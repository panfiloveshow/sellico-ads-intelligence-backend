package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// PhraseService handles phrase read operations.
type PhraseService struct {
	queries *sqlcgen.Queries
}

type PhraseListFilter struct {
	CampaignID *uuid.UUID
}

func NewPhraseService(queries *sqlcgen.Queries) *PhraseService {
	return &PhraseService{queries: queries}
}

func (s *PhraseService) List(ctx context.Context, workspaceID uuid.UUID, filter PhraseListFilter, limit, offset int32) ([]domain.Phrase, error) {
	rows, err := s.queries.ListPhrasesByWorkspace(ctx, sqlcgen.ListPhrasesByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		CampaignIDFilter: uuidToPgtypePtr(filter.CampaignID),
		Limit:            limit,
		Offset:           offset,
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

func (s *PhraseService) Get(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.Phrase, error) {
	row, err := s.queries.GetPhraseByID(ctx, uuidToPgtype(phraseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "phrase not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get phrase")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "phrase not found")
	}

	result := phraseFromSqlc(row)
	return &result, nil
}

func (s *PhraseService) GetStats(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.PhraseStat, error) {
	if _, err := s.Get(ctx, workspaceID, phraseID); err != nil {
		return nil, err
	}

	rows, err := s.queries.GetPhraseStatsByDateRange(ctx, sqlcgen.GetPhraseStatsByDateRangeParams{
		PhraseID: uuidToPgtype(phraseID),
		Date:     pgDate(dateFrom),
		Date_2:   pgDate(dateTo),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get phrase stats")
	}

	result := make([]domain.PhraseStat, len(rows))
	for i, row := range rows {
		result[i] = phraseStatFromSqlc(row)
	}
	return result, nil
}

func (s *PhraseService) ListBids(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.BidSnapshot, error) {
	if _, err := s.Get(ctx, workspaceID, phraseID); err != nil {
		return nil, err
	}

	rows, err := s.queries.ListBidSnapshotsByPhrase(ctx, sqlcgen.ListBidSnapshotsByPhraseParams{
		PhraseID: uuidToPgtype(phraseID),
		Limit:    limit,
		Offset:   offset,
		DateFrom: timePtrToPgtype(&dateFrom),
		DateTo:   timePtrToPgtype(&dateTo),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list phrase bids")
	}

	result := make([]domain.BidSnapshot, len(rows))
	for i, row := range rows {
		result[i] = bidSnapshotFromSqlc(row)
	}
	return result, nil
}

func (s *PhraseService) ListRecommendations(ctx context.Context, workspaceID, phraseID uuid.UUID, filter RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	if _, err := s.Get(ctx, workspaceID, phraseID); err != nil {
		return nil, err
	}

	rows, err := s.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           offset,
		CampaignIDFilter: uuidToPgtypePtr(filter.CampaignID),
		PhraseIDFilter:   uuidToPgtypePtr(&phraseID),
		ProductIDFilter:  uuidToPgtypePtr(filter.ProductID),
		TypeFilter:       textToPgtype(filter.Type),
		SeverityFilter:   textToPgtype(filter.Severity),
		StatusFilter:     textToPgtype(filter.Status),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list phrase recommendations")
	}

	result := make([]domain.Recommendation, len(rows))
	for i, row := range rows {
		result[i] = recommendationFromSqlc(row)
	}
	return result, nil
}
