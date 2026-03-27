package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type AuditLogListFilter struct {
	Action     string
	EntityType string
	UserID     *uuid.UUID
	DateFrom   *time.Time
	DateTo     *time.Time
}

// AuditLogService handles audit log read operations.
type AuditLogService struct {
	queries *sqlcgen.Queries
}

func NewAuditLogService(queries *sqlcgen.Queries) *AuditLogService {
	return &AuditLogService{queries: queries}
}

func (s *AuditLogService) List(ctx context.Context, workspaceID uuid.UUID, filter AuditLogListFilter, limit, offset int32) ([]domain.AuditLog, error) {
	rows, err := s.queries.ListAuditLogsFiltered(ctx, sqlcgen.ListAuditLogsFilteredParams{
		WorkspaceID:      uuidToPgtype(workspaceID),
		Limit:            limit,
		Offset:           offset,
		ActionFilter:     textToPgtype(filter.Action),
		EntityTypeFilter: textToPgtype(filter.EntityType),
		UserIDFilter:     uuidToPgtypePtr(filter.UserID),
		DateFrom:         timePtrToPgtype(filter.DateFrom),
		DateTo:           timePtrToPgtype(filter.DateTo),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list audit logs")
	}

	result := make([]domain.AuditLog, len(rows))
	for i, row := range rows {
		result[i] = auditLogFromSqlc(row)
	}
	return result, nil
}
