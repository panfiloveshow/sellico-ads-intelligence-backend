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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// ozonRepricerServicer is the Ozon repricer surface (phase 4 audit/manual +
// phase 5 calendar/pause/health).
type ozonRepricerServicer interface {
	ListPriceChanges(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, limit, offset int32) ([]domain.OzonPriceChange, int64, error)
	Rollback(ctx context.Context, workspaceID, changeID uuid.UUID) (*domain.OzonPriceChange, error)
	ApplyManual(ctx context.Context, workspaceID, cabinetID uuid.UUID, items []service.OzonManualPriceInput, userID uuid.UUID) ([]domain.OzonPriceChange, error)
	ResolveWorkspaceCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) error
	CreateSchedule(ctx context.Context, workspaceID, cabinetID uuid.UUID, in domain.OzonPriceScheduleInput) (*domain.OzonPriceScheduleEntry, error)
	ListSchedules(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, limit, offset int32) ([]domain.OzonPriceScheduleEntry, int64, error)
	CancelSchedule(ctx context.Context, workspaceID, entryID uuid.UUID) error
	SetPause(ctx context.Context, workspaceID, cabinetID uuid.UUID, hours int) (*time.Time, error)
	Health(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonRepricerHealth, error)
}

// ozonRepricerRunEnqueuer enqueues an async ozon:repricer_run for a workspace.
type ozonRepricerRunEnqueuer func(workspaceID uuid.UUID) error

// WithRepricer attaches the phase-4 repricer surface to the handler.
func (h *OzonHandler) WithRepricer(repricer ozonRepricerServicer, enqueueRun ozonRepricerRunEnqueuer) *OzonHandler {
	h.repricer = repricer
	h.enqueueRepricerRun = enqueueRun
	return h
}

func (h *OzonHandler) requireRepricer(w http.ResponseWriter) bool {
	if h.repricer == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "NOT_CONFIGURED", "ozon repricer is not configured")
		return false
	}
	return true
}

// ListPriceChanges handles GET /ozon/price-changes?cabinet_id=&sku=&page=.
func (h *OzonHandler) ListPriceChanges(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	var sku *int64
	if raw := r.URL.Query().Get("sku"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "sku must be a positive integer")
			return
		}
		sku = &parsed
	}
	pg := pagination.Parse(r)
	items, total, err := h.repricer.ListPriceChanges(r.Context(), workspaceID, cabinetID, sku, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// RollbackPriceChange handles POST /ozon/price-changes/{id}/rollback.
func (h *OzonHandler) RollbackPriceChange(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	changeID, err := parseNonNilUUID(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid price change id")
		return
	}
	change, err := h.repricer.Rollback(r.Context(), workspaceID, changeID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, change)
}

// BulkPrices handles POST /ozon/prices/bulk {cabinet_id, items}.
func (h *OzonHandler) BulkPrices(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	var req struct {
		CabinetID string                         `json:"cabinet_id"`
		Items     []service.OzonManualPriceInput `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id")
		return
	}
	changes, err := h.repricer.ApplyManual(r.Context(), workspaceID, cabinetID, req.Items, userID)
	if err != nil {
		// Per-item failures are reported in the rows; only request-level
		// errors (validation, tenancy, transport) reach here with nil rows.
		if changes == nil {
			writeAppError(w, err)
			return
		}
	}
	dto.WriteJSON(w, http.StatusOK, changes)
}

// CreatePriceSchedule handles POST /ozon/price-schedules {cabinet_id, sku,
// scheduled_price_rub, revert_price_rub?, starts_at, ends_at?}.
func (h *OzonHandler) CreatePriceSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	var req struct {
		CabinetID string `json:"cabinet_id"`
		domain.OzonPriceScheduleInput
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	entry, err := h.repricer.CreateSchedule(r.Context(), workspaceID, cabinetID, req.OzonPriceScheduleInput)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, entry)
}

// ListPriceSchedules handles GET /ozon/price-schedules?cabinet_id=&status=&page=.
func (h *OzonHandler) ListPriceSchedules(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !allowedValue(status, "", domain.OzonScheduleStatusPending, domain.OzonScheduleStatusApplied, domain.OzonScheduleStatusReverted, domain.OzonScheduleStatusCancelled, domain.OzonScheduleStatusFailed) {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid schedule status")
		return
	}
	pg := pagination.Parse(r)
	items, total, err := h.repricer.ListSchedules(r.Context(), workspaceID, cabinetID, status, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// CancelPriceSchedule handles DELETE /ozon/price-schedules/{id} (pending
// entries only → cancelled).
func (h *OzonHandler) CancelPriceSchedule(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	entryID, err := parseNonNilUUID(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid schedule id")
		return
	}
	if err := h.repricer.CancelSchedule(r.Context(), workspaceID, entryID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// PauseRepricer handles POST /ozon/prices/pause {cabinet_id, hours} — freezes
// the cabinet's Ozon repricer automation (hours=0 unfreezes).
func (h *OzonHandler) PauseRepricer(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	var req struct {
		CabinetID string `json:"cabinet_id"`
		Hours     int    `json:"hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	until, err := h.repricer.SetPause(r.Context(), workspaceID, cabinetID, req.Hours)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]*time.Time{"paused_until": until})
}

// RepricerHealth handles GET /ozon/repricer/health?cabinet_id=.
func (h *OzonHandler) RepricerHealth(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	health, err := h.repricer.Health(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, health)
}

// TriggerRepricerRun handles POST /ozon/repricer/run {cabinet_id} — validates
// the cabinet and enqueues an ozon:repricer_run for its workspace.
func (h *OzonHandler) TriggerRepricerRun(w http.ResponseWriter, r *http.Request) {
	if !h.requireRepricer(w) {
		return
	}
	var req struct {
		CabinetID string `json:"cabinet_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	if err := h.repricer.ResolveWorkspaceCabinet(r.Context(), workspaceID, cabinetID); err != nil {
		writeAppError(w, err)
		return
	}
	if h.enqueueRepricerRun == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "NOT_CONFIGURED", "ozon repricer worker is not configured")
		return
	}
	if err := h.enqueueRepricerRun(workspaceID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, map[string]string{
		"status":     "enqueued",
		"cabinet_id": cabinetID.String(),
	})
}
