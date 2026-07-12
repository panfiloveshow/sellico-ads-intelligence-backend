package handler

import (
	"context"
	"encoding/json"
	"fmt"
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
	CountCatalog(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID) (int64, error)
	ListCabinetsScope(ctx context.Context, workspaceID uuid.UUID) ([]domain.CabinetPricesScope, error)
	SyncPrices(ctx context.Context, workspaceID uuid.UUID) (int, error)
	ApplyManualBulk(ctx context.Context, actorID, workspaceID uuid.UUID, req domain.ManualPriceBulkRequest) (*domain.PriceBulkResult, error)
	ListChanges(ctx context.Context, workspaceID uuid.UUID, f domain.PriceChangeFilter) ([]domain.PriceChange, error)
	CountChanges(ctx context.Context, workspaceID uuid.UUID, f domain.PriceChangeFilter) (int64, error)
	Rollback(ctx context.Context, actorID, workspaceID, changeID uuid.UUID) (*domain.PriceChange, error)
	ListUploadTasks(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.PriceUploadTask, error)
	CountUploadTasks(ctx context.Context, workspaceID uuid.UUID) (int64, error)
	CreateSchedule(ctx context.Context, actorID, workspaceID uuid.UUID, in domain.PriceScheduleInput) (*domain.PriceScheduleEntry, error)
	ListSchedules(ctx context.Context, workspaceID uuid.UUID, status string, limit, offset int32) ([]domain.PriceScheduleEntry, error)
	CountSchedules(ctx context.Context, workspaceID uuid.UUID, status string) (int64, error)
	CancelSchedule(ctx context.Context, workspaceID, entryID uuid.UUID) error
	ListQuarantine(ctx context.Context, workspaceID uuid.UUID) ([]domain.PriceQuarantineGood, error)
	OrdersHeatmap(ctx context.Context, workspaceID, cabinetID uuid.UUID, wbProductID int64, from, to time.Time, metric string) (*domain.OrdersHeatmap, error)
	SetPause(ctx context.Context, workspaceID, cabinetID uuid.UUID, until *time.Time) error
	Health(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.RepricerHealth, error)
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
		id, err := parseNonNilUUID(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid seller_cabinet_id")
			return
		}
		cabinetID = &id
	}
	total, err := h.service.CountCatalog(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	items, err := h.service.ListCatalog(r.Context(), workspaceID, cabinetID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
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
	cabinetID, err := parseNonNilUUID(q.Get("seller_cabinet_id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "seller_cabinet_id is required")
		return
	}
	var nmID int64
	if raw := q.Get("wb_product_id"); raw != "" {
		nmID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || nmID <= 0 {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid wb_product_id")
			return
		}
	}
	metric := q.Get("metric")
	if !allowedValue(metric, "", domain.HeatmapMetricUnits, domain.HeatmapMetricOrders, domain.HeatmapMetricRevenue) {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "metric must be units, orders or revenue")
		return
	}
	to := time.Now()
	from := to.AddDate(0, 0, -30)
	if raw := q.Get("date_from"); raw != "" {
		parsed, perr := time.Parse("2006-01-02", raw)
		if perr != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "date_from must use YYYY-MM-DD")
			return
		}
		from = parsed
	}
	if raw := q.Get("date_to"); raw != "" {
		parsed, perr := time.Parse("2006-01-02", raw)
		if perr != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "date_to must use YYYY-MM-DD")
			return
		}
		to = parsed
	}
	if from.After(to) {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "date_from must be on or before date_to")
		return
	}
	hm, err := h.service.OrdersHeatmap(r.Context(), workspaceID, cabinetID, nmID, from, to, metric)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, hm)
}

// Health returns the repricer status summary for a cabinet.
func (h *PriceHandler) Health(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	cabinetID, err := parseNonNilUUID(r.URL.Query().Get("seller_cabinet_id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "seller_cabinet_id is required")
		return
	}
	res, err := h.service.Health(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, res)
}

// Pause freezes (or, with until=null, unfreezes) a cabinet's repricer auto-apply.
func (h *PriceHandler) Pause(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	var body struct {
		SellerCabinetID string     `json:"seller_cabinet_id"`
		Until           *time.Time `json:"until"` // null = unfreeze
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	cabinetID, err := parseNonNilUUID(body.SellerCabinetID)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "seller_cabinet_id is required")
		return
	}
	if err := h.service.SetPause(r.Context(), workspaceID, cabinetID, body.Until); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	if req.Scope != nil && req.Scope.SellerCabinetID != nil && *req.Scope.SellerCabinetID == uuid.Nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid seller_cabinet_id")
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
	q := r.URL.Query()
	source := q.Get("source")
	if !allowedValue(source, "", domain.PriceSourceStrategy, domain.PriceSourceManual, domain.PriceSourceRollback, domain.PriceSourceSchedule) {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid price change source")
		return
	}
	status := q.Get("status")
	if !allowedValue(status, "", domain.PriceStatusRecommended, domain.PriceStatusPending, domain.PriceStatusUploaded, domain.PriceStatusApplied, domain.PriceStatusFailed, domain.PriceStatusRolledBack) {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid price change status")
		return
	}
	f := domain.PriceChangeFilter{
		Source: source,
		Status: status,
		Limit:  int32(pg.PerPage),
		Offset: int32(pg.Offset()),
	}
	if nm := q.Get("wb_product_id"); nm != "" {
		v, err := strconv.ParseInt(nm, 10, 64)
		if err != nil || v <= 0 {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid wb_product_id")
			return
		}
		f.WBProductID = &v
	}
	total, err := h.service.CountChanges(r.Context(), workspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	items, err := h.service.ListChanges(r.Context(), workspaceID, f)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// Rollback reverts an applied price change.
func (h *PriceHandler) Rollback(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	actorID, _ := middleware.UserIDFromContext(r.Context())
	changeID, err := parseNonNilUUID(chi.URLParam(r, "changeId"))
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
	total, err := h.service.CountUploadTasks(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	items, err := h.service.ListUploadTasks(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
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
	if in.SellerCabinetID == uuid.Nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid seller_cabinet_id")
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
	status := r.URL.Query().Get("status")
	if !allowedValue(status, "", domain.PriceSchedulePlanned, domain.PriceScheduleExecuting, domain.PriceScheduleDone, domain.PriceScheduleFailed, domain.PriceScheduleCanceled) {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid price schedule status")
		return
	}
	pg := pagination.Parse(r)
	total, err := h.service.CountSchedules(r.Context(), workspaceID, status)
	if err != nil {
		writeAppError(w, err)
		return
	}
	items, err := h.service.ListSchedules(r.Context(), workspaceID, status, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// CancelSchedule cancels a planned schedule entry.
func (h *PriceHandler) CancelSchedule(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	entryID, err := parseNonNilUUID(chi.URLParam(r, "scheduleId"))
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

func parseNonNilUUID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("invalid uuid")
	}
	return id, nil
}

func allowedValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
