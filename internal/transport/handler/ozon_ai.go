package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// ozonAIServicer is the AI autopilot surface (phase 3).
type ozonAIServicer interface {
	ListRuns(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.AIRun, int64, error)
	ListDecisions(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, runID *uuid.UUID, limit, offset int32) ([]domain.AIDecision, int64, error)
	CheckManualRunAllowed(ctx context.Context, workspaceID, cabinetID uuid.UUID) error
	ApproveDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error)
	RejectDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error)
	ApproveDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult
	RejectDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult
	GetImpact(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIImpactSummary, error)
	GetLatestWeeklyReport(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonAIWeeklyReport, error)
	GetReadiness(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIReadiness, error)
}

// ozonAIRunEnqueuer enqueues an async ozon:ai_run task (trigger 'manual').
type ozonAIRunEnqueuer func(cabinetID uuid.UUID) error

// WithAI attaches the phase-3 AI autopilot surface to the handler.
func (h *OzonHandler) WithAI(ai ozonAIServicer, enqueueRun ozonAIRunEnqueuer) *OzonHandler {
	h.ai = ai
	h.enqueueAIRun = enqueueRun
	return h
}

func (h *OzonHandler) requireAI(w http.ResponseWriter) bool {
	if h.ai == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "NOT_CONFIGURED", "ozon ai autopilot is not configured")
		return false
	}
	return true
}

// AIListRuns handles GET /ozon/ai/runs?cabinet_id=&page=.
func (h *OzonHandler) AIListRuns(w http.ResponseWriter, r *http.Request) {
	if !h.requireAI(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	pg := pagination.Parse(r)
	items, total, err := h.ai.ListRuns(r.Context(), workspaceID, cabinetID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// AITriggerRun handles POST /ozon/ai/run {cabinet_id} — 409 when a run is
// already in flight for the cabinet.
func (h *OzonHandler) AITriggerRun(w http.ResponseWriter, r *http.Request) {
	if !h.requireAI(w) {
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
	if err := h.ai.CheckManualRunAllowed(r.Context(), workspaceID, cabinetID); err != nil {
		writeAppError(w, err)
		return
	}
	if h.enqueueAIRun == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "NOT_CONFIGURED", "ozon ai worker is not configured")
		return
	}
	if err := h.enqueueAIRun(cabinetID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, map[string]string{
		"status":     "enqueued",
		"cabinet_id": cabinetID.String(),
	})
}

// AIListDecisions handles GET /ozon/ai/decisions?cabinet_id=&status=&run_id=&page=.
func (h *OzonHandler) AIListDecisions(w http.ResponseWriter, r *http.Request) {
	if !h.requireAI(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	var runID *uuid.UUID
	if raw := r.URL.Query().Get("run_id"); raw != "" {
		parsed, err := parseNonNilUUID(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid run_id")
			return
		}
		runID = &parsed
	}
	pg := pagination.Parse(r)
	items, total, err := h.ai.ListDecisions(r.Context(), workspaceID, cabinetID, r.URL.Query().Get("status"), runID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

func (h *OzonHandler) aiDecisionAction(w http.ResponseWriter, r *http.Request, approve bool) {
	if !h.requireAI(w) {
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id")
		return
	}
	decisionID, err := parseNonNilUUID(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid decision id")
		return
	}
	var decision *domain.AIDecision
	if approve {
		decision, err = h.ai.ApproveDecision(r.Context(), workspaceID, decisionID, userID)
	} else {
		decision, err = h.ai.RejectDecision(r.Context(), workspaceID, decisionID, userID)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, decision)
}

// AIImpact handles GET /ozon/ai/impact?cabinet_id= — the 30-day «ИИ
// заработал/сэкономил» aggregate over applied decisions.
func (h *OzonHandler) AIImpact(w http.ResponseWriter, r *http.Request) {
	if !h.requireAI(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	summary, err := h.ai.GetImpact(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, summary)
}

// AIApproveDecision handles POST /ozon/ai/decisions/{id}/approve.
func (h *OzonHandler) AIApproveDecision(w http.ResponseWriter, r *http.Request) {
	h.aiDecisionAction(w, r, true)
}

// AIRejectDecision handles POST /ozon/ai/decisions/{id}/reject.
func (h *OzonHandler) AIRejectDecision(w http.ResponseWriter, r *http.Request) {
	h.aiDecisionAction(w, r, false)
}

// aiDecisionsBatch handles POST /ozon/ai/decisions/approve-batch and
// /reject-batch: {ids:[]} → per-id results {id, ok, error}.
func (h *OzonHandler) aiDecisionsBatch(w http.ResponseWriter, r *http.Request, approve bool) {
	if !h.requireAI(w) {
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id")
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "ids must not be empty")
		return
	}
	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, raw := range req.IDs {
		parsed, err := parseNonNilUUID(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid decision id: "+raw)
			return
		}
		ids = append(ids, parsed)
	}
	var results []domain.AIDecisionBatchResult
	if approve {
		results = h.ai.ApproveDecisionsBatch(r.Context(), workspaceID, ids, userID)
	} else {
		results = h.ai.RejectDecisionsBatch(r.Context(), workspaceID, ids, userID)
	}
	dto.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

// AIApproveDecisionsBatch handles POST /ozon/ai/decisions/approve-batch.
func (h *OzonHandler) AIApproveDecisionsBatch(w http.ResponseWriter, r *http.Request) {
	h.aiDecisionsBatch(w, r, true)
}

// AIRejectDecisionsBatch handles POST /ozon/ai/decisions/reject-batch.
func (h *OzonHandler) AIRejectDecisionsBatch(w http.ResponseWriter, r *http.Request) {
	h.aiDecisionsBatch(w, r, false)
}

// AIWeeklyReport handles GET /ozon/ai/weekly-report?cabinet_id= — the latest
// plain-Russian weekly recap for a cabinet (null when none exists yet).
func (h *OzonHandler) AIWeeklyReport(w http.ResponseWriter, r *http.Request) {
	if !h.requireAI(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	report, err := h.ai.GetLatestWeeklyReport(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, report)
}

// AIReadiness handles GET /ozon/ai/readiness?cabinet_id= — the shadow →
// next-level readiness stat (null when the cabinet has no active AI strategy).
func (h *OzonHandler) AIReadiness(w http.ResponseWriter, r *http.Request) {
	if !h.requireAI(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	readiness, err := h.ai.GetReadiness(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, readiness)
}
