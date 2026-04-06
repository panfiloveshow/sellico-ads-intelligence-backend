package service

import (
	"context"

	"github.com/google/uuid"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// CountService provides total counts for list endpoints (pagination).
type CountService struct {
	queries *sqlcgen.Queries
}

// NewCountService creates a new CountService.
func NewCountService(queries *sqlcgen.Queries) *CountService {
	return &CountService{queries: queries}
}

// CountCampaigns returns total campaigns in workspace.
func (s *CountService) CountCampaigns(ctx context.Context, workspaceID uuid.UUID) int64 {
	count, _ := s.queries.CountCampaignsByWorkspace(ctx, uuidToPgtype(workspaceID))
	return count
}

// CountPhrases returns total phrases in workspace.
func (s *CountService) CountPhrases(ctx context.Context, workspaceID uuid.UUID) int64 {
	count, _ := s.queries.CountPhrasesByWorkspace(ctx, uuidToPgtype(workspaceID))
	return count
}

// CountProducts returns total products in workspace.
func (s *CountService) CountProducts(ctx context.Context, workspaceID uuid.UUID) int64 {
	count, _ := s.queries.CountProductsByWorkspace(ctx, uuidToPgtype(workspaceID))
	return count
}

// CountRecommendations returns total active recommendations in workspace.
func (s *CountService) CountRecommendations(ctx context.Context, workspaceID uuid.UUID) int64 {
	count, _ := s.queries.CountActiveRecommendationsByWorkspace(ctx, uuidToPgtype(workspaceID))
	return count
}

// CountExports returns total exports in workspace.
func (s *CountService) CountExports(ctx context.Context, workspaceID uuid.UUID) int64 {
	count, _ := s.queries.CountExportsByWorkspace(ctx, uuidToPgtype(workspaceID))
	return count
}

// CountJobRuns returns total job runs in workspace.
func (s *CountService) CountJobRuns(ctx context.Context, workspaceID uuid.UUID) int64 {
	count, _ := s.queries.CountJobRunsByWorkspace(ctx, uuidToPgtype(workspaceID))
	return count
}

// CountAuditLogs returns total audit logs in workspace.
func (s *CountService) CountAuditLogs(ctx context.Context, workspaceID uuid.UUID) int64 {
	count, _ := s.queries.CountAuditLogsByWorkspace(ctx, uuidToPgtype(workspaceID))
	return count
}
