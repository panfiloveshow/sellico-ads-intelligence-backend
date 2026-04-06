package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// SettingsServicer defines the interface for workspace settings operations.
type SettingsServicer interface {
	GetSettings(ctx context.Context, workspaceID uuid.UUID) (*domain.WorkspaceSettings, error)
	UpdateSettings(ctx context.Context, actorID, workspaceID uuid.UUID, input domain.WorkspaceSettings) (*domain.WorkspaceSettings, error)
	GetThresholds(ctx context.Context, workspaceID uuid.UUID) (*domain.RecommendationThresholds, error)
}

// WorkspaceSettingsHandler handles workspace settings HTTP endpoints.
type WorkspaceSettingsHandler struct {
	service SettingsServicer
}

// NewWorkspaceSettingsHandler creates a new WorkspaceSettingsHandler.
func NewWorkspaceSettingsHandler(service SettingsServicer) *WorkspaceSettingsHandler {
	return &WorkspaceSettingsHandler{service: service}
}

// GetSettings handles GET /api/v1/settings.
func (h *WorkspaceSettingsHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := r.Context().Value(middleware.WorkspaceIDKey).(uuid.UUID)
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace context")
		return
	}

	settings, err := h.service.GetSettings(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, settings)
}

// UpdateSettings handles PUT /api/v1/settings.
func (h *WorkspaceSettingsHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(uuid.UUID)
	if !ok {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user context")
		return
	}

	workspaceID, ok := r.Context().Value(middleware.WorkspaceIDKey).(uuid.UUID)
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace context")
		return
	}

	var input domain.WorkspaceSettings
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	settings, err := h.service.UpdateSettings(r.Context(), userID, workspaceID, input)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, settings)
}

// GetThresholds handles GET /api/v1/settings/thresholds.
func (h *WorkspaceSettingsHandler) GetThresholds(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := r.Context().Value(middleware.WorkspaceIDKey).(uuid.UUID)
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace context")
		return
	}

	thresholds, err := h.service.GetThresholds(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, thresholds)
}
