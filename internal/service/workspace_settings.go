package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// WorkspaceSettingsService manages per-workspace settings (thresholds, notifications).
type WorkspaceSettingsService struct {
	queries *sqlcgen.Queries
}

// NewWorkspaceSettingsService creates a new WorkspaceSettingsService.
func NewWorkspaceSettingsService(queries *sqlcgen.Queries) *WorkspaceSettingsService {
	return &WorkspaceSettingsService{queries: queries}
}

// GetSettings returns the current settings for a workspace.
func (s *WorkspaceSettingsService) GetSettings(ctx context.Context, workspaceID uuid.UUID) (*domain.WorkspaceSettings, error) {
	raw, err := s.queries.GetWorkspaceSettings(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "workspace not found")
	}

	var settings domain.WorkspaceSettings
	if len(raw) > 0 && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to parse workspace settings")
		}
	}
	return &settings, nil
}

// UpdateSettings merges the provided settings into the workspace's JSONB column.
func (s *WorkspaceSettingsService) UpdateSettings(ctx context.Context, actorID, workspaceID uuid.UUID, input domain.WorkspaceSettings) (*domain.WorkspaceSettings, error) {
	raw, err := s.queries.GetWorkspaceSettings(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "workspace not found")
	}

	ws, err := s.queries.GetWorkspaceByID(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "workspace not found")
	}

	var existing domain.WorkspaceSettings
	if len(raw) > 0 && string(raw) != "{}" {
		_ = json.Unmarshal(raw, &existing)
	}
	_ = ws // used to get ID for update

	// Merge: only overwrite fields that are set in input
	if input.RecommendationThresholds != nil {
		existing.RecommendationThresholds = input.RecommendationThresholds
	}
	if input.Notifications != nil {
		existing.Notifications = input.Notifications
	}

	settingsJSON, err := json.Marshal(existing)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to serialize settings")
	}

	_, err = s.queries.UpdateWorkspaceSettings(ctx, sqlcgen.UpdateWorkspaceSettingsParams{
		ID:       ws.ID,
		Settings: settingsJSON,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update workspace settings")
	}

	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "update_workspace_settings",
		EntityType:  "workspace",
		EntityID:    ws.ID,
		Metadata:    settingsJSON,
	})

	return &existing, nil
}

// GetThresholds returns the effective recommendation thresholds (with defaults applied).
func (s *WorkspaceSettingsService) GetThresholds(ctx context.Context, workspaceID uuid.UUID) (*domain.RecommendationThresholds, error) {
	settings, err := s.GetSettings(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	merged := settings.RecommendationThresholds.Merged()
	return &merged, nil
}
