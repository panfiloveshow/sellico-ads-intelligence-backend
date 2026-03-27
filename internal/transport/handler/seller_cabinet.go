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

// sellerCabinetServicer is the interface the SellerCabinetHandler depends on.
type sellerCabinetServicer interface {
	Create(ctx context.Context, workspaceID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error)
	List(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.SellerCabinet, error)
	Get(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error)
	Delete(ctx context.Context, actorID, workspaceID, cabinetID uuid.UUID) error
}

// SellerCabinetHandler handles seller cabinet HTTP endpoints.
type SellerCabinetHandler struct {
	svc sellerCabinetServicer
}

// NewSellerCabinetHandler creates a new SellerCabinetHandler.
func NewSellerCabinetHandler(svc sellerCabinetServicer) *SellerCabinetHandler {
	return &SellerCabinetHandler{svc: svc}
}

// Create handles POST /seller-cabinets.
func (h *SellerCabinetHandler) Create(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreateSellerCabinetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}

	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	sc, err := h.svc.Create(r.Context(), workspaceID, req.Name, req.APIToken)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusCreated, dto.SellerCabinetFromDomain(*sc))
}

// List handles GET /seller-cabinets.
func (h *SellerCabinetHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	pg := pagination.Parse(r)

	cabinets, err := h.svc.List(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.SellerCabinetResponse, len(cabinets))
	for i, sc := range cabinets {
		items[i] = dto.SellerCabinetFromDomain(sc)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// Get handles GET /seller-cabinets/{id}.
func (h *SellerCabinetHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	idStr := chi.URLParam(r, "id")
	cabinetID, err := uuid.Parse(idStr)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid cabinet id")
		return
	}

	sc, err := h.svc.Get(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.SellerCabinetFromDomain(*sc))
}

// Delete handles DELETE /seller-cabinets/{id}.
func (h *SellerCabinetHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

	idStr := chi.URLParam(r, "id")
	cabinetID, err := uuid.Parse(idStr)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid cabinet id")
		return
	}

	if err := h.svc.Delete(r.Context(), userID, workspaceID, cabinetID); err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, nil)
}
