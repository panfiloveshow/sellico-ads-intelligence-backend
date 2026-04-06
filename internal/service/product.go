package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type ProductListFilter struct {
	Title           string
	SellerCabinetID *uuid.UUID
}

// ProductService handles product read operations.
type ProductService struct {
	queries *sqlcgen.Queries
}

func NewProductService(queries *sqlcgen.Queries) *ProductService {
	return &ProductService{queries: queries}
}

func (s *ProductService) List(ctx context.Context, workspaceID uuid.UUID, filter ProductListFilter, limit, offset int32) ([]domain.Product, error) {
	rows, err := s.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID:           uuidToPgtype(workspaceID),
		Limit:                 limit,
		Offset:                offset,
		SellerCabinetIDFilter: uuidToPgtypePtr(filter.SellerCabinetID),
		TitleFilter:           textToPgtype(filter.Title),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list products")
	}

	result := make([]domain.Product, len(rows))
	for i, row := range rows {
		result[i] = productFromSqlc(row)
	}
	return result, nil
}

func (s *ProductService) Get(ctx context.Context, workspaceID, productID uuid.UUID) (*domain.Product, error) {
	row, err := s.queries.GetProductByID(ctx, uuidToPgtype(productID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get product")
	}
	if uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "product not found")
	}

	result := productFromSqlc(row)
	return &result, nil
}

func (s *ProductService) ListPositions(ctx context.Context, workspaceID, productID uuid.UUID, limit, offset int32) ([]domain.Position, error) {
	if _, err := s.Get(ctx, workspaceID, productID); err != nil {
		return nil, err
	}

	rows, err := s.queries.ListPositionsByProduct(ctx, sqlcgen.ListPositionsByProductParams{
		ProductID: uuidToPgtype(productID),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list product positions")
	}

	result := make([]domain.Position, len(rows))
	for i, row := range rows {
		result[i] = positionFromSqlc(row)
	}
	return result, nil
}

func (s *ProductService) ListRecommendations(ctx context.Context, workspaceID, productID uuid.UUID, filter RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	if _, err := s.Get(ctx, workspaceID, productID); err != nil {
		return nil, err
	}

	rows, err := s.queries.ListRecommendationsByWorkspace(ctx, sqlcgen.ListRecommendationsByWorkspaceParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           offset,
		CampaignIDFilter: uuidToPgtypePtr(filter.CampaignID),
		PhraseIDFilter:   uuidToPgtypePtr(filter.PhraseID),
		ProductIDFilter:  uuidToPgtypePtr(&productID),
		TypeFilter:       textToPgtype(filter.Type),
		SeverityFilter:   textToPgtype(filter.Severity),
		StatusFilter:     textToPgtype(filter.Status),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list product recommendations")
	}

	result := make([]domain.Recommendation, len(rows))
	for i, row := range rows {
		result[i] = recommendationFromSqlc(row)
	}
	return result, nil
}
