package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type priceServicer interface {
	ListCatalog(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID, limit, offset int32) ([]domain.ProductCatalogItem, error)
	ListCabinetsScope(ctx context.Context, workspaceID uuid.UUID) ([]domain.CabinetPricesScope, error)
	SyncPrices(ctx context.Context, workspaceID uuid.UUID) (int, error)
	ApplyManualBulk(ctx context.Context, actorID, workspaceID uuid.UUID, req domain.ManualPriceBulkRequest) (*domain.PriceBulkResult, error)
	ListChanges(ctx context.Context, workspaceID uuid.UUID, f domain.PriceChangeFilter) ([]domain.PriceChange, error)
	Rollback(ctx context.Context, actorID, workspaceID, changeID uuid.UUID) (*domain.PriceChange, error)
	ListUploadTasks(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.PriceUploadTask, error)
	CreateSchedule(ctx context.Context, actorID, workspaceID uuid.UUID, in domain.PriceScheduleInput) (*domain.PriceScheduleEntry, error)
	ListSchedules(ctx context.Context, workspaceID uuid.UUID, status string, limit, offset int32) ([]domain.PriceScheduleEntry, error)
	CancelSchedule(ctx context.Context, workspaceID, entryID uuid.UUID) error
	ListQuarantine(ctx context.Context, workspaceID uuid.UUID) ([]domain.PriceQuarantineGood, error)
	OrdersHeatmap(ctx context.Context, workspaceID, cabinetID uuid.UUID, wbProductID int64, from, to time.Time, metric string) (*domain.OrdersHeatmap, error)
}

// repricerEnqueuer enqueues an async repricer run for a workspace.
type repricerEnqueuer func(workspaceID uuid.UUID) error

type PriceHandler struct {
	service     priceServicer
	enqueueRun  repricerEnqueuer
	enqueueSync repricerEnqueuer
}

func NewPriceHandler(service priceServicer, enqueueRun, enqueueSync repricerEnqueuer) *PriceHandler {
	return &PriceHandler{service: service, enqueueRun: enqueueRun, enqueueSync: enqueueSync}
}

func (h *PriceHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	pg := pagination.Parse(r)
	var cabinetID *uuid.UUID
	if raw := r.URL.Query().Get("seller_cabinet_id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			cabinetID = &id
		}
	}
	items, err := h.service.ListCatalog(r.Context(), workspaceID, cabinetID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(items))})
}

// Heatmap returns the 7×24 orders matrix (day-of-week × MSK hour) for planning
// intraday price timers. seller_cabinet_id is required; wb_product_id optional.
func (h *PriceHandler) Heatmap(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	q := r.URL.Query()
	cabinetID, err := uuid.Parse(q.Get("seller_cabinet_id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "seller_cabinet_id is required")
		return
	}
	var nmID int64
	if raw := q.Get("wb_product_id"); raw != "" {
		nmID, _ = strconv.ParseInt(raw, 10, 64)
	}
	to := time.Now()
	from := to.AddDate(0, 0, -30)
	if raw := q.Get("date_from"); raw != "" {
		if parsed, perr := time.Parse("2006-01-02", raw); perr == nil {
			from = parsed
		}
	}
	if raw := q.Get("date_to"); raw != "" {
		if parsed, perr := time.Parse("2006-01-02", raw); perr == nil {
			to = parsed
		}
	}
	hm, err := h.service.OrdersHeatmap(r.Context(), workspaceID, cabinetID, nmID, from, to, q.Get("metric"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, hm)
}

// CabinetsStatus returns each cabinet's prices-scope status so the UI can warn
// when a token lacks the "Цены и скидки" category.
func (h *PriceHandler) CabinetsStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	items, err := h.service.ListCabinetsScope(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, items)
}

// TriggerSync enqueues an async WB price refresh (respects rate limits; avoids
// the request timeout that inline syncing hit for large cabinets).
func (h *PriceHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	if h.enqueueSync == nil {
		count, err := h.service.SyncPrices(r.Context(), workspaceID)
		if err != nil {
			writeAppError(w, err)
			return
		}
		dto.WriteJSON(w, http.StatusOK, map[string]int{"synced": count})
		return
	}
	if err := h.enqueueSync(workspaceID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "enqueued"})
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

// CreateSchedule plans a future price change (optionally with auto-revert).
func (h *PriceHandler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	actorID, _ := middleware.UserIDFromContext(r.Context())
	var in domain.PriceScheduleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	entry, err := h.service.CreateSchedule(r.Context(), actorID, workspaceID, in)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, entry)
}

// ListSchedules returns planned/executed schedule entries (calendar feed).
func (h *PriceHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	pg := pagination.Parse(r)
	items, err := h.service.ListSchedules(r.Context(), workspaceID, r.URL.Query().Get("status"), int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(items))})
}

// CancelSchedule cancels a planned schedule entry.
func (h *PriceHandler) CancelSchedule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	entryID, err := uuid.Parse(chi.URLParam(r, "scheduleId"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid schedule id")
		return
	}
	if err := h.service.CancelSchedule(r.Context(), workspaceID, entryID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "canceled"})
}

// ListQuarantine returns products currently held in WB price quarantine.
func (h *PriceHandler) ListQuarantine(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	items, err := h.service.ListQuarantine(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, items)
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
