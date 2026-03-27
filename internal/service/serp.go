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

type SERPListFilter struct {
	Query    string
	Region   string
	DateFrom *time.Time
	DateTo   *time.Time
}

// SERPService handles SERP snapshot read operations.
type SERPService struct {
	queries *sqlcgen.Queries
}

func NewSERPService(queries *sqlcgen.Queries) *SERPService {
	return &SERPService{queries: queries}
}

func (s *SERPService) List(ctx context.Context, workspaceID uuid.UUID, filter SERPListFilter, limit, offset int32) ([]domain.SERPSnapshot, error) {
	rows, err := s.queries.ListSERPSnapshotsFiltered(ctx, sqlcgen.ListSERPSnapshotsFilteredParams{
		WorkspaceID:  uuidToPgtype(workspaceID),
		Limit:        limit,
		Offset:       offset,
		QueryFilter:  textToPgtype(filter.Query),
		RegionFilter: textToPgtype(filter.Region),
		DateFrom:     timePtrToPgtype(filter.DateFrom),
		DateTo:       timePtrToPgtype(filter.DateTo),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list serp snapshots")
	}

	result := make([]domain.SERPSnapshot, len(rows))
	for i, row := range rows {
		result[i] = serpSnapshotFromSqlc(row)
	}
	return result, nil
}

func (s *SERPService) Get(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPSnapshot, error) {
	row, err := s.queries.GetSERPSnapshotByID(ctx, uuidToPgtype(snapshotID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "serp snapshot not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get serp snapshot")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "serp snapshot not found")
	}

	result := serpSnapshotFromSqlc(row)
	return &result, nil
}

func (s *SERPService) ListItems(ctx context.Context, snapshotID uuid.UUID) ([]domain.SERPResultItem, error) {
	rows, err := s.queries.ListSERPResultItemsBySnapshot(ctx, uuidToPgtype(snapshotID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list serp result items")
	}

	result := make([]domain.SERPResultItem, len(rows))
	for i, row := range rows {
		result[i] = serpResultItemFromSqlc(row)
	}
	return result, nil
}
