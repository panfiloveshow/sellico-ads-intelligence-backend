package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type BidListFilter struct {
	PhraseID *uuid.UUID
	DateFrom *time.Time
	DateTo   *time.Time
}

type CreateBidSnapshotInput struct {
	PhraseID       uuid.UUID
	CompetitiveBid int64
	LeadershipBid  int64
	CPMMin         int64
	CapturedAt     *time.Time
}

// BidService handles bid snapshot read operations.
type BidService struct {
	queries  *sqlcgen.Queries
	enqueuer RecommendationJobEnqueuer
	logger   zerolog.Logger
}

func NewBidService(queries *sqlcgen.Queries, enqueuer RecommendationJobEnqueuer, logger zerolog.Logger) *BidService {
	return &BidService{
		queries:  queries,
		enqueuer: enqueuer,
		logger:   logger.With().Str("component", "bid_service").Logger(),
	}
}

func (s *BidService) Create(ctx context.Context, actorID, workspaceID uuid.UUID, input CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
	if err := s.ensurePhraseBelongsToWorkspace(ctx, workspaceID, input.PhraseID); err != nil {
		return nil, err
	}

	capturedAt := time.Now().UTC()
	if input.CapturedAt != nil {
		capturedAt = input.CapturedAt.UTC()
	}

	row, err := s.queries.CreateBidSnapshot(ctx, sqlcgen.CreateBidSnapshotParams{
		PhraseID:       uuidToPgtype(input.PhraseID),
		WorkspaceID:    uuidToPgtype(workspaceID),
		CompetitiveBid: input.CompetitiveBid,
		LeadershipBid:  input.LeadershipBid,
		CpmMin:         input.CPMMin,
		CapturedAt:     timePtrToPgtype(&capturedAt),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create bid snapshot")
	}

	meta, _ := json.Marshal(map[string]any{
		"phrase_id":       input.PhraseID,
		"competitive_bid": input.CompetitiveBid,
		"leadership_bid":  input.LeadershipBid,
		"cpm_min":         input.CPMMin,
	})
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "ingest_bid_snapshot",
		EntityType:  "bid_snapshot",
		EntityID:    row.ID,
		Metadata:    meta,
	})

	result := bidSnapshotFromSqlc(row)
	s.tryEnqueueRecommendationGeneration(workspaceID)
	return &result, nil
}

func (s *BidService) ListHistory(ctx context.Context, workspaceID uuid.UUID, filter BidListFilter, limit, offset int32) ([]domain.BidSnapshot, error) {
	if filter.PhraseID != nil {
		if err := s.ensurePhraseBelongsToWorkspace(ctx, workspaceID, *filter.PhraseID); err != nil {
			return nil, err
		}
	}

	rows, err := s.queries.ListBidSnapshotsByWorkspace(ctx, sqlcgen.ListBidSnapshotsByWorkspaceParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Limit:          limit,
		Offset:         offset,
		PhraseIDFilter: uuidToPgtypePtr(filter.PhraseID),
		DateFrom:       timePtrToPgtype(filter.DateFrom),
		DateTo:         timePtrToPgtype(filter.DateTo),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list bid history")
	}

	result := make([]domain.BidSnapshot, len(rows))
	for i, row := range rows {
		result[i] = bidSnapshotFromSqlc(row)
	}
	return result, nil
}

func (s *BidService) GetEstimate(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.BidSnapshot, error) {
	if err := s.ensurePhraseBelongsToWorkspace(ctx, workspaceID, phraseID); err != nil {
		return nil, err
	}

	row, err := s.queries.GetLatestBidSnapshot(ctx, uuidToPgtype(phraseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "bid estimate not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get bid estimate")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "bid estimate not found")
	}

	result := bidSnapshotFromSqlc(row)
	return &result, nil
}

func (s *BidService) ensurePhraseBelongsToWorkspace(ctx context.Context, workspaceID, phraseID uuid.UUID) error {
	phrase, err := s.queries.GetPhraseByID(ctx, uuidToPgtype(phraseID))
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrNotFound, "phrase not found")
	}
	if err != nil {
		return apperror.New(apperror.ErrInternal, "failed to get phrase")
	}
	if uuidFromPgtype(phrase.WorkspaceID) != workspaceID {
		return apperror.New(apperror.ErrNotFound, "phrase not found")
	}
	return nil
}

func (s *BidService) tryEnqueueRecommendationGeneration(workspaceID uuid.UUID) {
	if s.enqueuer == nil {
		return
	}
	status, err := s.enqueuer.EnqueueRecommendationGeneration(workspaceID)
	if err != nil {
		s.logger.Warn().Err(err).Str("workspace_id", workspaceID.String()).Msg("failed to enqueue recommendation generation after bid ingestion")
		return
	}
	s.logger.Debug().Str("workspace_id", workspaceID.String()).Str("queue_status", status).Msg("enqueued recommendation generation after bid ingestion")
}
