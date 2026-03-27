package service

import (
	"context"
	"time"

	"github.com/google/uuid"

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

// PositionService handles position read operations.
type PositionService struct {
	queries *sqlcgen.Queries
}

func NewPositionService(queries *sqlcgen.Queries) *PositionService {
	return &PositionService{queries: queries}
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
