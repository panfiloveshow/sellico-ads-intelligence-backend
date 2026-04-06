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

type strategyServicer interface {
	Create(ctx context.Context, workspaceID uuid.UUID, input domain.Strategy) (*domain.Strategy, error)
	Get(ctx context.Context, strategyID uuid.UUID) (*domain.Strategy, error)
	List(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.Strategy, error)
	Update(ctx context.Context, strategyID uuid.UUID, input domain.Strategy) (*domain.Strategy, error)
	Delete(ctx context.Context, strategyID uuid.UUID) error
	AttachBinding(ctx context.Context, strategyID uuid.UUID, campaignID, productID *uuid.UUID) (*domain.StrategyBinding, error)
	DetachBinding(ctx context.Context, bindingID uuid.UUID) error
}

type StrategyHandler struct {
	svc strategyServicer
}

func NewStrategyHandler(svc strategyServicer) *StrategyHandler {
	return &StrategyHandler{svc: svc}
}

func (h *StrategyHandler) Create(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var input domain.Strategy
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	strategy, err := h.svc.Create(r.Context(), workspaceID, input)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, strategy)
}

func (h *StrategyHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	strategy, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, strategy)
}

func (h *StrategyHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	pg := pagination.Parse(r)
	strategies, err := h.svc.List(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, strategies, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(strategies)),
	})
}

func (h *StrategyHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	var input domain.Strategy
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	strategy, err := h.svc.Update(r.Context(), id, input)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, strategy)
}

func (h *StrategyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type attachRequest struct {
	CampaignID *uuid.UUID `json:"campaign_id"`
	ProductID  *uuid.UUID `json:"product_id"`
}

func (h *StrategyHandler) Attach(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid strategy id")
		return
	}

	var req attachRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	binding, err := h.svc.AttachBinding(r.Context(), id, req.CampaignID, req.ProductID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, binding)
}

func (h *StrategyHandler) Detach(w http.ResponseWriter, r *http.Request) {
	bindingID, err := uuid.Parse(chi.URLParam(r, "bindingId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid binding id")
		return
	}

	if err := h.svc.DetachBinding(r.Context(), bindingID); err != nil {
		writeAppError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
