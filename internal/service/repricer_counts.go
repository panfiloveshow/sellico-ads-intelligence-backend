package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func (s *RepricerService) CountCatalog(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID) (int64, error) {
	cabinet := pgtype.UUID{}
	if cabinetID != nil {
		cabinet = uuidToPgtype(*cabinetID)
	}
	return s.queries.CountProductCatalog(ctx, uuidToPgtype(workspaceID), cabinet)
}

func (s *RepricerService) CountChanges(ctx context.Context, workspaceID uuid.UUID, f domain.PriceChangeFilter) (int64, error) {
	arg := sqlcgen.ListPriceChangesParams{WorkspaceID: uuidToPgtype(workspaceID)}
	if f.SellerCabinetID != nil {
		arg.SellerCabinetID = uuidToPgtype(*f.SellerCabinetID)
	}
	if f.WBProductID != nil {
		arg.WbProductID = pgtype.Int8{Int64: *f.WBProductID, Valid: true}
	}
	if f.Source != "" {
		arg.Source = pgtype.Text{String: f.Source, Valid: true}
	}
	if f.Status != "" {
		arg.WbStatus = pgtype.Text{String: f.Status, Valid: true}
	}
	return s.queries.CountPriceChanges(ctx, arg)
}

func (s *RepricerService) CountUploadTasks(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID) (int64, error) {
	cabinet := pgtype.UUID{}
	if cabinetID != nil {
		cabinet = uuidToPgtype(*cabinetID)
	}
	return s.queries.CountPriceUploadTasks(ctx, uuidToPgtype(workspaceID), cabinet)
}

func (s *RepricerService) CountSchedules(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID, status string) (int64, error) {
	cabinet := pgtype.UUID{}
	if cabinetID != nil {
		cabinet = uuidToPgtype(*cabinetID)
	}
	statusFilter := pgtype.Text{}
	if status != "" {
		statusFilter = pgtype.Text{String: status, Valid: true}
	}
	return s.queries.CountPriceSchedules(ctx, uuidToPgtype(workspaceID), cabinet, statusFilter)
}
