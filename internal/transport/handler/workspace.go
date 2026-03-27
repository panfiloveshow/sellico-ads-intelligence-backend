package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// workspaceServicer is the interface the WorkspaceHandler depends on.
type workspaceServicer interface {
	Create(ctx context.Context, userID uuid.UUID, name, slug string) (*domain.Workspace, error)
	List(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]domain.Workspace, error)
	Get(ctx context.Context, workspaceID uuid.UUID) (*domain.Workspace, error)
	InviteMember(ctx context.Context, workspaceID uuid.UUID, email, role string) (*domain.WorkspaceMember, error)
	UpdateMemberRole(ctx context.Context, actorID, workspaceID, memberID uuid.UUID, newRole string) (*domain.WorkspaceMember, error)
	RemoveMember(ctx context.Context, workspaceID, memberID uuid.UUID) error
}

// WorkspaceHandler handles workspace HTTP endpoints.
type WorkspaceHandler struct {
	svc workspaceServicer
}

// NewWorkspaceHandler creates a new WorkspaceHandler.
func NewWorkspaceHandler(svc workspaceServicer) *WorkspaceHandler {
	return &WorkspaceHandler{svc: svc}
}

// Create handles POST /workspaces.
func (h *WorkspaceHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}

	var req dto.CreateWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}

	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	ws, err := h.svc.Create(r.Context(), userID, req.Name, req.Slug)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusCreated, dto.WorkspaceFromDomain(*ws))
}

// List handles GET /workspaces.
func (h *WorkspaceHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}

	pg := pagination.Parse(r)

	workspaces, err := h.svc.List(r.Context(), userID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.WorkspaceResponse, len(workspaces))
	for i, ws := range workspaces {
		items[i] = dto.WorkspaceFromDomain(ws)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// Get handles GET /workspaces/{workspaceId}.
func (h *WorkspaceHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	ws, err := h.svc.Get(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.WorkspaceFromDomain(*ws))
}

// InviteMember handles POST /workspaces/{workspaceId}/members.
func (h *WorkspaceHandler) InviteMember(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.InviteMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}

	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	member, err := h.svc.InviteMember(r.Context(), workspaceID, req.Email, req.Role)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusCreated, dto.WorkspaceMemberFromDomain(*member))
}

// UpdateMemberRole handles PATCH /workspaces/{workspaceId}/members/{memberId}.
func (h *WorkspaceHandler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}

	memberIDStr := chi.URLParam(r, "memberId")
	memberID, err := uuid.Parse(memberIDStr)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid member id")
		return
	}

	var req dto.UpdateMemberRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}

	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	member, err := h.svc.UpdateMemberRole(r.Context(), userID, workspaceID, memberID, req.Role)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.WorkspaceMemberFromDomain(*member))
}

// RemoveMember handles DELETE /workspaces/{workspaceId}/members/{memberId}.
func (h *WorkspaceHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	memberIDStr := chi.URLParam(r, "memberId")
	memberID, err := uuid.Parse(memberIDStr)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid member id")
		return
	}

	if err := h.svc.RemoveMember(r.Context(), workspaceID, memberID); err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, nil)
}
