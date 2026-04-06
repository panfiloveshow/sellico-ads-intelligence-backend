package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type PositionListFilter struct {
	ProductID *uuid.UUID
	Query     string
	Region    string
	DateFrom  *time.Time
	DateTo    *time.Time
}

type PositionTargetListFilter struct {
	ProductID  *uuid.UUID
	Query      string
	Region     string
	ActiveOnly bool
}

type CreatePositionInput struct {
	ProductID uuid.UUID
	Query     string
	Region    string
	Position  int
	Page      int
	Source    string
	CheckedAt *time.Time
}

type CreatePositionTrackingTargetInput struct {
	ProductID uuid.UUID
	Query     string
	Region    string
}

// PositionService handles position read operations.
type PositionService struct {
	queries  *sqlcgen.Queries
	enqueuer RecommendationJobEnqueuer
	logger   zerolog.Logger
}

func NewPositionService(queries *sqlcgen.Queries, enqueuer RecommendationJobEnqueuer, logger zerolog.Logger) *PositionService {
	return &PositionService{
		queries:  queries,
		enqueuer: enqueuer,
		logger:   logger.With().Str("component", "position_service").Logger(),
	}
}

func (s *PositionService) CreateTrackingTarget(ctx context.Context, actorID, workspaceID uuid.UUID, input CreatePositionTrackingTargetInput) (*domain.PositionTrackingTarget, error) {
	product, err := s.queries.GetProductByID(ctx, uuidToPgtype(input.ProductID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get product")
	}
	if uuidFromPgtype(product.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}

	latestPositions, err := s.queries.ListPositionsFiltered(ctx, sqlcgen.ListPositionsFilteredParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		Limit:           1,
		Offset:          0,
		ProductIDFilter: uuidToPgtypePtr(&input.ProductID),
		QueryFilter:     textToPgtype(input.Query),
		RegionFilter:    textToPgtype(input.Region),
		DateFrom:        timePtrToPgtype(nil),
		DateTo:          timePtrToPgtype(nil),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to inspect latest position snapshot")
	}

	baselinePosition := pgtype.Int4{}
	baselineCheckedAt := pgtype.Timestamptz{}
	if len(latestPositions) > 0 {
		baselinePosition = pgtype.Int4{Int32: latestPositions[0].Position, Valid: true}
		baselineCheckedAt = latestPositions[0].CheckedAt
	}

	row, err := s.queries.CreatePositionTrackingTarget(ctx, sqlcgen.CreatePositionTrackingTargetParams{
		WorkspaceID:       uuidToPgtype(workspaceID),
		ProductID:         uuidToPgtype(input.ProductID),
		Query:             input.Query,
		Region:            input.Region,
		BaselinePosition:  baselinePosition,
		BaselineCheckedAt: baselineCheckedAt,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create position tracking target")
	}

	meta, _ := json.Marshal(map[string]any{
		"product_id": input.ProductID,
		"query":      input.Query,
		"region":     input.Region,
	})
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "create_position_tracking_target",
		EntityType:  "position_tracking_target",
		EntityID:    row.ID,
		Metadata:    meta,
	})

	target := positionTrackingTargetFromSqlc(row)
	target.ProductTitle = product.Title
	if len(latestPositions) > 0 {
		latest := positionFromSqlc(latestPositions[0])
		target.LatestPosition = &latest.Position
		target.LatestPage = &latest.Page
		target.LatestCheckedAt = &latest.CheckedAt
		target.SampleCount = 1
		delta, candidate, severity := computePositionAlert(target.BaselinePosition, target.LatestPosition)
		target.Delta = delta
		target.AlertCandidate = candidate
		target.AlertSeverity = severity
	}

	return &target, nil
}

func (s *PositionService) Create(ctx context.Context, actorID, workspaceID uuid.UUID, input CreatePositionInput) (*domain.Position, error) {
	product, err := s.queries.GetProductByID(ctx, uuidToPgtype(input.ProductID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get product")
	}
	if uuidFromPgtype(product.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}

	checkedAt := time.Now().UTC()
	if input.CheckedAt != nil {
		checkedAt = input.CheckedAt.UTC()
	}

	row, err := s.queries.CreatePosition(ctx, sqlcgen.CreatePositionParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		ProductID:   uuidToPgtype(input.ProductID),
		Query:       input.Query,
		Region:      input.Region,
		Position:    int32(input.Position),
		Page:        int32(input.Page),
		Source:      input.Source,
		CheckedAt:   timePtrToPgtype(&checkedAt),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create position snapshot")
	}

	meta, _ := json.Marshal(map[string]any{
		"product_id": input.ProductID,
		"query":      input.Query,
		"region":     input.Region,
		"source":     input.Source,
	})
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "ingest_position_snapshot",
		EntityType:  "position",
		EntityID:    row.ID,
		Metadata:    meta,
	})

	result := positionFromSqlc(row)
	s.tryEnqueueRecommendationGeneration(workspaceID)
	return &result, nil
}

func (s *PositionService) ListTrackingTargets(ctx context.Context, workspaceID uuid.UUID, filter PositionTargetListFilter, limit, offset int32) ([]domain.PositionTrackingTarget, error) {
	rows, err := s.queries.ListPositionTrackingTargetsFiltered(ctx, sqlcgen.ListPositionTrackingTargetsFilteredParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		Limit:           limit,
		Offset:          offset,
		ProductIDFilter: uuidToPgtypePtr(filter.ProductID),
		QueryFilter:     textToPgtype(filter.Query),
		RegionFilter:    textToPgtype(filter.Region),
		ActiveFilter:    boolToPgtype(filter.ActiveOnly),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list position tracking targets")
	}

	productTitles := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		productID := uuidFromPgtype(row.ProductID)
		if _, ok := productTitles[productID]; ok {
			continue
		}
		product, prodErr := s.queries.GetProductByID(ctx, row.ProductID)
		if prodErr == nil {
			productTitles[productID] = product.Title
		}
	}

	result := make([]domain.PositionTrackingTarget, 0, len(rows))
	for _, row := range rows {
		target := positionTrackingTargetFromSqlc(row)
		target.ProductTitle = productTitles[target.ProductID]

		positions, posErr := s.queries.ListPositionsFiltered(ctx, sqlcgen.ListPositionsFilteredParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			Limit:           1000,
			Offset:          0,
			ProductIDFilter: uuidToPgtypePtr(&target.ProductID),
			QueryFilter:     textToPgtype(target.Query),
			RegionFilter:    textToPgtype(target.Region),
			DateFrom:        timePtrToPgtype(nil),
			DateTo:          timePtrToPgtype(nil),
		})
		if posErr == nil && len(positions) > 0 {
			latest := positionFromSqlc(positions[0])
			target.LatestPosition = &latest.Position
			target.LatestPage = &latest.Page
			target.LatestCheckedAt = &latest.CheckedAt
			target.SampleCount = len(positions)
			delta, candidate, severity := computePositionAlert(target.BaselinePosition, target.LatestPosition)
			target.Delta = delta
			target.AlertCandidate = candidate
			target.AlertSeverity = severity
		}

		result = append(result, target)
	}

	return result, nil
}

func (s *PositionService) List(ctx context.Context, workspaceID uuid.UUID, filter PositionListFilter, limit, offset int32) ([]domain.Position, error) {
	rows, err := s.queries.ListPositionsFiltered(ctx, sqlcgen.ListPositionsFilteredParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		Limit:           limit,
		Offset:          offset,
		ProductIDFilter: uuidToPgtypePtr(filter.ProductID),
		QueryFilter:     textToPgtype(filter.Query),
		RegionFilter:    textToPgtype(filter.Region),
		DateFrom:        timePtrToPgtype(filter.DateFrom),
		DateTo:          timePtrToPgtype(filter.DateTo),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list positions")
	}

	result := make([]domain.Position, len(rows))
	for i, row := range rows {
		result[i] = positionFromSqlc(row)
	}
	return result, nil
}

func (s *PositionService) Aggregate(ctx context.Context, workspaceID, productID uuid.UUID, query, region string, dateFrom, dateTo time.Time) (*domain.PositionAggregate, error) {
	product, err := s.queries.GetProductByID(ctx, uuidToPgtype(productID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}
	if uuidFromPgtype(product.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}

	rows, err := s.List(ctx, workspaceID, PositionListFilter{
		ProductID: &productID,
		Query:     query,
		Region:    region,
		DateFrom:  &dateFrom,
		DateTo:    &dateTo,
	}, 1000, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &domain.PositionAggregate{
			ProductID:   productID,
			Query:       query,
			Region:      region,
			Average:     0,
			DateFrom:    dateFrom,
			DateTo:      dateTo,
			SampleCount: 0,
		}, nil
	}

	var total int
	for _, row := range rows {
		total += row.Position
	}

	return &domain.PositionAggregate{
		ProductID:   productID,
		Query:       query,
		Region:      region,
		Average:     float64(total) / float64(len(rows)),
		DateFrom:    dateFrom,
		DateTo:      dateTo,
		SampleCount: len(rows),
	}, nil
}

func computePositionAlert(baseline, latest *int) (*int, bool, string) {
	if baseline == nil || latest == nil {
		return nil, false, ""
	}

	delta := *latest - *baseline
	if delta <= 0 {
		return &delta, false, ""
	}
	if delta >= 10 {
		return &delta, true, domain.SeverityHigh
	}
	if delta >= 5 {
		return &delta, true, domain.SeverityMedium
	}
	return &delta, false, ""
}

func boolToPgtype(value bool) pgtype.Bool {
	return pgtype.Bool{Bool: value, Valid: true}
}

func (s *PositionService) tryEnqueueRecommendationGeneration(workspaceID uuid.UUID) {
	if s.enqueuer == nil {
		return
	}
	status, err := s.enqueuer.EnqueueRecommendationGeneration(workspaceID)
	if err != nil {
		s.logger.Warn().Err(err).Str("workspace_id", workspaceID.String()).Msg("failed to enqueue recommendation generation after position ingestion")
		return
	}
	s.logger.Debug().Str("workspace_id", workspaceID.String()).Str("queue_status", status).Msg("enqueued recommendation generation after position ingestion")
}
