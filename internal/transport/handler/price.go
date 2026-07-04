package handler

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type priceServicer interface {
	ListPrices(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.ProductPrice, error)
	SyncPrices(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

type PriceHandler struct {
	service priceServicer
}

func NewPriceHandler(service priceServicer) *PriceHandler {
	return &PriceHandler{service: service}
}

func (h *PriceHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	pg := pagination.Parse(r)
	items, err := h.service.ListPrices(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// TriggerSync refreshes current WB prices for the workspace.
// ponytail: runs the WB sync inline for now; Phase 5 enqueues price:sync on the
// worker and this becomes a 202 Accepted.
func (h *PriceHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	count, err := h.service.SyncPrices(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]int{"synced": count})
}
