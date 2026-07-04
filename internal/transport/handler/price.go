package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
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
	ApplyManualBulk(ctx context.Context, actorID, workspaceID uuid.UUID, req domain.ManualPriceBulkRequest) (*domain.PriceBulkResult, error)
	ListChanges(ctx context.Context, workspaceID uuid.UUID, f domain.PriceChangeFilter) ([]domain.PriceChange, error)
	Rollback(ctx context.Context, actorID, workspaceID, changeID uuid.UUID) (*domain.PriceChange, error)
	ListUploadTasks(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.PriceUploadTask, error)
}

// repricerEnqueuer enqueues an async repricer run for a workspace.
type repricerEnqueuer func(workspaceID uuid.UUID) error

type PriceHandler struct {
	service    priceServicer
	enqueueRun repricerEnqueuer
}

func NewPriceHandler(service priceServicer, enqueueRun repricerEnqueuer) *PriceHandler {
	return &PriceHandler{service: service, enqueueRun: enqueueRun}
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
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(items))})
}

// TriggerSync refreshes current WB prices for the workspace (inline for v1).
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

// Bulk applies a manual bulk price change (explicit items or scope+adjustment).
func (h *PriceHandler) Bulk(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	actorID, _ := middleware.UserIDFromContext(r.Context())
	var req domain.ManualPriceBulkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	result, err := h.service.ApplyManualBulk(r.Context(), actorID, workspaceID, req)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, result)
}

// ListChanges returns price-change history (filters: wb_product_id, source, status).
func (h *PriceHandler) ListChanges(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	pg := pagination.Parse(r)
	f := domain.PriceChangeFilter{
		Source: r.URL.Query().Get("source"),
		Status: r.URL.Query().Get("status"),
		Limit:  int32(pg.PerPage),
		Offset: int32(pg.Offset()),
	}
	if nm := r.URL.Query().Get("wb_product_id"); nm != "" {
		if v, err := strconv.ParseInt(nm, 10, 64); err == nil {
			f.WBProductID = &v
		}
	}
	items, err := h.service.ListChanges(r.Context(), workspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(items))})
}

// Rollback reverts an applied price change.
func (h *PriceHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	actorID, _ := middleware.UserIDFromContext(r.Context())
	changeID, err := uuid.Parse(chi.URLParam(r, "changeId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid change id")
		return
	}
	change, err := h.service.Rollback(r.Context(), actorID, workspaceID, changeID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, change)
}

// ListUploadTasks returns recent price upload tasks with their statuses.
func (h *PriceHandler) ListUploadTasks(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	pg := pagination.Parse(r)
	items, err := h.service.ListUploadTasks(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(items))})
}

// Run enqueues an async repricer automation run for the workspace.
func (h *PriceHandler) Run(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	if h.enqueueRun == nil {
		dto.WriteError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "repricer runner not configured")
		return
	}
	if err := h.enqueueRun(workspaceID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "enqueued"})
}
