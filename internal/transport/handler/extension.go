package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type extensionServicer interface {
	UpsertSession(ctx context.Context, userID, workspaceID uuid.UUID, extensionVersion string) (*domain.ExtensionSession, error)
	CreateContextEvent(ctx context.Context, userID, workspaceID uuid.UUID, url, pageType string) (*domain.ExtensionContextEvent, error)
	Version() string
}

// ExtensionHandler handles browser extension endpoints.
type ExtensionHandler struct {
	svc extensionServicer
}

func NewExtensionHandler(svc extensionServicer) *ExtensionHandler {
	return &ExtensionHandler{svc: svc}
}

func (h *ExtensionHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreateExtensionSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	session, err := h.svc.UpsertSession(r.Context(), userID, workspaceID, req.ExtensionVersion)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExtensionSessionFromDomain(*session))
}

func (h *ExtensionHandler) CreateContext(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.ExtensionContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	if _, err := h.svc.CreateContextEvent(r.Context(), userID, workspaceID, req.URL, req.PageType); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, nil)
}

func (h *ExtensionHandler) Version(w http.ResponseWriter, _ *http.Request) {
	dto.WriteJSON(w, http.StatusOK, dto.ExtensionVersionResponse{Version: h.svc.Version()})
}
