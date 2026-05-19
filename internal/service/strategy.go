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

// StrategyService manages bid automation strategies.
type StrategyService struct {
	queries *sqlcgen.Queries
}

func NewStrategyService(queries *sqlcgen.Queries) *StrategyService {
	return &StrategyService{queries: queries}
}

func (s *StrategyService) Create(ctx context.Context, workspaceID uuid.UUID, input domain.Strategy) (*domain.Strategy, error) {
	if input.SellerCabinetID == uuid.Nil {
		return nil, apperror.New(apperror.ErrValidation, "seller_cabinet_id is required")
	}
	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(input.SellerCabinetID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if uuidFromPgtype(cabinet.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}

	paramsJSON, err := json.Marshal(input.Params)
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "invalid strategy params")
	}

	row, err := s.queries.CreateStrategy(ctx, sqlcgen.CreateStrategyParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(input.SellerCabinetID),
		Name:            input.Name,
		Type:            input.Type,
		Params:          paramsJSON,
		IsActive:        input.IsActive,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create strategy")
	}

	result := strategyFromSqlc(row)
	return &result, nil
}

func (s *StrategyService) Get(ctx context.Context, workspaceID, strategyID uuid.UUID) (*domain.Strategy, error) {
	row, err := s.queries.GetStrategyByIDAndWorkspace(ctx, sqlcgen.GetStrategyByIDAndWorkspaceParams{
		ID:          uuidToPgtype(strategyID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "strategy not found")
	}

	result := strategyFromSqlc(row)

	bindings, _ := s.queries.ListStrategyBindings(ctx, uuidToPgtype(strategyID))
	for _, b := range bindings {
		result.Bindings = append(result.Bindings, bindingFromSqlc(b))
	}

	return &result, nil
}

func (s *StrategyService) List(ctx context.Context, workspaceID uuid.UUID, sellerCabinetID *uuid.UUID, limit, offset int32) ([]domain.Strategy, error) {
	rows, err := s.queries.ListStrategiesByWorkspace(ctx, sqlcgen.ListStrategiesByWorkspaceParams{
		WorkspaceID:           uuidToPgtype(workspaceID),
		SellerCabinetIDFilter: nullableUUIDToPgtype(sellerCabinetID),
		Limit:                 limit,
		Offset:                offset,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list strategies")
	}

	result := make([]domain.Strategy, len(rows))
	for i, row := range rows {
		result[i] = strategyFromSqlc(row)
	}
	return result, nil
}

func (s *StrategyService) Update(ctx context.Context, workspaceID, strategyID uuid.UUID, input domain.Strategy) (*domain.Strategy, error) {
	paramsJSON, err := json.Marshal(input.Params)
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "invalid strategy params")
	}

	row, err := s.queries.UpdateStrategyInWorkspace(ctx, sqlcgen.UpdateStrategyInWorkspaceParams{
		ID:          uuidToPgtype(strategyID),
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        input.Name,
		Type:        input.Type,
		Params:      paramsJSON,
		IsActive:    input.IsActive,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrNotFound, "strategy not found")
		}
		return nil, apperror.New(apperror.ErrInternal, "failed to update strategy")
	}

	result := strategyFromSqlc(row)
	return &result, nil
}

func (s *StrategyService) Delete(ctx context.Context, workspaceID, strategyID uuid.UUID) error {
	return s.queries.DeleteStrategyInWorkspace(ctx, sqlcgen.DeleteStrategyInWorkspaceParams{
		ID:          uuidToPgtype(strategyID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
}

func (s *StrategyService) AttachBinding(ctx context.Context, workspaceID, strategyID uuid.UUID, campaignID, productID *uuid.UUID) (*domain.StrategyBinding, error) {
	row, err := s.queries.CreateStrategyBindingInWorkspace(ctx, sqlcgen.CreateStrategyBindingInWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		StrategyID:  uuidToPgtype(strategyID),
		CampaignID:  nullableUUIDToPgtype(campaignID),
		ProductID:   nullableUUIDToPgtype(productID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.New(apperror.ErrNotFound, "strategy or binding target not found")
		}
		return nil, apperror.New(apperror.ErrInternal, "failed to attach strategy")
	}

	result := bindingFromSqlc(row)
	return &result, nil
}

func (s *StrategyService) DetachBinding(ctx context.Context, workspaceID, bindingID uuid.UUID) error {
	return s.queries.DeleteStrategyBindingInWorkspace(ctx, sqlcgen.DeleteStrategyBindingInWorkspaceParams{
		ID:          uuidToPgtype(bindingID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
}

// ListActive returns all active strategies for a workspace (used by bid automation worker).
func (s *StrategyService) ListActive(ctx context.Context, workspaceID uuid.UUID) ([]domain.Strategy, error) {
	rows, err := s.queries.ListActiveStrategiesByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}

	result := make([]domain.Strategy, len(rows))
	for i, row := range rows {
		result[i] = strategyFromSqlc(row)
		bindings, _ := s.queries.ListStrategyBindings(ctx, uuidToPgtype(result[i].ID))
		for _, b := range bindings {
			result[i].Bindings = append(result[i].Bindings, bindingFromSqlc(b))
		}
	}
	return result, nil
}

func strategyFromSqlc(row sqlcgen.Strategy) domain.Strategy {
	s := domain.Strategy{
		ID:              uuidFromPgtype(row.ID),
		WorkspaceID:     uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		Name:            row.Name,
		Type:            row.Type,
		IsActive:        row.IsActive,
		CreatedAt:       row.CreatedAt.Time,
		UpdatedAt:       row.UpdatedAt.Time,
	}
	if len(row.Params) > 0 {
		_ = json.Unmarshal(row.Params, &s.Params)
	}
	return s
}

func bindingFromSqlc(row sqlcgen.StrategyBinding) domain.StrategyBinding {
	b := domain.StrategyBinding{
		ID:         uuidFromPgtype(row.ID),
		StrategyID: uuidFromPgtype(row.StrategyID),
		CreatedAt:  row.CreatedAt.Time,
	}
	if row.CampaignID.Valid {
		id := uuidFromPgtype(row.CampaignID)
		b.CampaignID = &id
	}
	if row.ProductID.Valid {
		id := uuidFromPgtype(row.ProductID)
		b.ProductID = &id
	}
	return b
}

func nullableUUIDToPgtype(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return uuidToPgtype(*id)
}
