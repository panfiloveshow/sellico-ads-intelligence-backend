package handler

import (
	"context"
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

type productEventServicer interface {
	ListEvents(ctx context.Context, productID uuid.UUID, limit, offset int32) ([]domain.ProductEvent, error)
	ListEventsByWorkspace(ctx context.Context, workspaceID uuid.UUID, eventType string, limit, offset int32) ([]domain.ProductEvent, error)
}

type ProductEventHandler struct {
	svc productEventServicer
}

func NewProductEventHandler(svc productEventServicer) *ProductEventHandler {
	return &ProductEventHandler{svc: svc}
}

func (h *ProductEventHandler) ListByProduct(w http.ResponseWriter, r *http.Request) {
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid product id")
		return
	}
	pg := pagination.Parse(r)
	events, err := h.svc.ListEvents(r.Context(), productID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, events, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(events))})
}

func (h *ProductEventHandler) ListByWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	pg := pagination.Parse(r)
	eventType := r.URL.Query().Get("event_type")
	events, err := h.svc.ListEventsByWorkspace(r.Context(), workspaceID, eventType, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, events, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(events))})
}
