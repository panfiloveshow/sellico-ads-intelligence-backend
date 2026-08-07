package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// ozonServicer is the read surface the OzonHandler depends on (phase 1).
type ozonServicer interface {
	ResolveOzonCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error)
	ListCampaignsWithStats(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCampaignWithStats, int64, error)
	GetCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.OzonCampaign, []domain.OzonCampaignProduct, error)
	ListCampaignStats(ctx context.Context, workspaceID, campaignID uuid.UUID, from, to time.Time) ([]domain.OzonCampaignStat, error)
	ListPrices(ctx context.Context, workspaceID, cabinetID uuid.UUID, search string, limit, offset int32) ([]domain.OzonProductPrice, int64, error)
}

// ozonActionsServicer is the write/management surface (phase 2).
type ozonActionsServicer interface {
	ActivateCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error
	DeactivateCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error
	UpdateBudget(ctx context.Context, workspaceID, campaignID uuid.UUID, dailyRub, weeklyRub *int64) error
	SetProductBids(ctx context.Context, workspaceID, campaignID uuid.UUID, items []service.OzonBidInput) ([]domain.OzonBidChange, error)
	GetCompetitiveBids(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]ozon.CompetitiveBid, error)
	ListBidChanges(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaignID *uuid.UUID, limit, offset int32) ([]domain.OzonBidChange, int64, error)
	ListCPOProducts(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCPOProduct, int64, error)
	EnableCPO(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error
	DisableCPO(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error
	SetCPOBids(ctx context.Context, workspaceID, cabinetID uuid.UUID, bids []service.OzonCPOBidInput) error
	GetBidLimits(ctx context.Context, workspaceID, cabinetID uuid.UUID) (json.RawMessage, error)
}

// ozonSyncEnqueuer enqueues an async ozon:sync_cabinet task.
type ozonSyncEnqueuer func(cabinetID uuid.UUID) error

// OzonHandler serves the /api/v1/ozon endpoints (phase 1 reads, phase 2
// writes, phase 3 AI autopilot — the latter attached via WithAI).
type OzonHandler struct {
	svc          ozonServicer
	actions      ozonActionsServicer
	enqueueSync  ozonSyncEnqueuer
	ai           ozonAIServicer
	enqueueAIRun ozonAIRunEnqueuer
}

// NewOzonHandler creates a new OzonHandler.
func NewOzonHandler(svc ozonServicer, actions ozonActionsServicer, enqueueSync ozonSyncEnqueuer) *OzonHandler {
	return &OzonHandler{svc: svc, actions: actions, enqueueSync: enqueueSync}
}

// ListCampaigns handles GET /ozon/campaigns?cabinet_id=.
func (h *OzonHandler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	cabinetID, err := parseNonNilUUID(r.URL.Query().Get("cabinet_id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cabinet_id is required")
		return
	}
	pg := pagination.Parse(r)

	items, total, err := h.svc.ListCampaignsWithStats(r.Context(), workspaceID, cabinetID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// GetCampaign handles GET /ozon/campaigns/{id} (campaign + products with bids).
func (h *OzonHandler) GetCampaign(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	campaignID, err := parseNonNilUUID(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid campaign id")
		return
	}

	campaign, products, err := h.svc.GetCampaign(r.Context(), workspaceID, campaignID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]any{
		"campaign": campaign,
		"products": products,
	})
}

// CampaignStats handles GET /ozon/campaigns/{id}/stats?from=&to=.
func (h *OzonHandler) CampaignStats(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	campaignID, err := parseNonNilUUID(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid campaign id")
		return
	}

	to := time.Now().UTC()
	from := to.AddDate(0, 0, -14)
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, perr := time.Parse("2006-01-02", raw)
		if perr != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "from must use YYYY-MM-DD")
			return
		}
		from = parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, perr := time.Parse("2006-01-02", raw)
		if perr != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "to must use YYYY-MM-DD")
			return
		}
		to = parsed
	}
	if from.After(to) {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "from must be on or before to")
		return
	}

	stats, err := h.svc.ListCampaignStats(r.Context(), workspaceID, campaignID, from, to)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, stats)
}

// ListPrices handles GET /ozon/prices?cabinet_id=&search=&page=.
func (h *OzonHandler) ListPrices(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	cabinetID, err := parseNonNilUUID(r.URL.Query().Get("cabinet_id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cabinet_id is required")
		return
	}
	pg := pagination.Parse(r)

	items, total, err := h.svc.ListPrices(r.Context(), workspaceID, cabinetID, r.URL.Query().Get("search"), int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// TriggerSync handles POST /ozon/sync?cabinet_id= — enqueues an async
// campaigns+stats+prices sync for one cabinet.
func (h *OzonHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return
	}
	cabinetID, err := parseNonNilUUID(r.URL.Query().Get("cabinet_id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cabinet_id is required")
		return
	}

	// Tenancy + marketplace gate before anything hits the queue.
	if _, err := h.svc.ResolveOzonCabinet(r.Context(), workspaceID, cabinetID); err != nil {
		writeAppError(w, err)
		return
	}
	if h.enqueueSync == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "NOT_CONFIGURED", "ozon sync worker is not configured")
		return
	}
	if err := h.enqueueSync(cabinetID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, map[string]string{
		"status":     "enqueued",
		"cabinet_id": cabinetID.String(),
	})
}

// --- phase 2: campaign management ---

func (h *OzonHandler) requireActions(w http.ResponseWriter) bool {
	if h.actions == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, "NOT_CONFIGURED", "ozon campaign actions are not configured")
		return false
	}
	return true
}

func (h *OzonHandler) workspaceAndCampaign(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return uuid.Nil, uuid.Nil, false
	}
	campaignID, err := parseNonNilUUID(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid campaign id")
		return uuid.Nil, uuid.Nil, false
	}
	return workspaceID, campaignID, true
}

func (h *OzonHandler) workspaceAndCabinet(w http.ResponseWriter, r *http.Request, cabinetRaw string) (uuid.UUID, uuid.UUID, bool) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
		return uuid.Nil, uuid.Nil, false
	}
	cabinetID, err := parseNonNilUUID(cabinetRaw)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cabinet_id is required")
		return uuid.Nil, uuid.Nil, false
	}
	return workspaceID, cabinetID, true
}

// ActivateCampaign handles POST /ozon/campaigns/{id}/activate.
func (h *OzonHandler) ActivateCampaign(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, campaignID, ok := h.workspaceAndCampaign(w, r)
	if !ok {
		return
	}
	if err := h.actions.ActivateCampaign(r.Context(), workspaceID, campaignID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "activated"})
}

// DeactivateCampaign handles POST /ozon/campaigns/{id}/deactivate.
func (h *OzonHandler) DeactivateCampaign(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, campaignID, ok := h.workspaceAndCampaign(w, r)
	if !ok {
		return
	}
	if err := h.actions.DeactivateCampaign(r.Context(), workspaceID, campaignID); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}

// UpdateBudget handles PATCH /ozon/campaigns/{id}/budget.
func (h *OzonHandler) UpdateBudget(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, campaignID, ok := h.workspaceAndCampaign(w, r)
	if !ok {
		return
	}
	var req struct {
		DailyBudgetRub  *int64 `json:"daily_budget_rub"`
		WeeklyBudgetRub *int64 `json:"weekly_budget_rub"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	if err := h.actions.UpdateBudget(r.Context(), workspaceID, campaignID, req.DailyBudgetRub, req.WeeklyBudgetRub); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// SetProductBids handles PUT /ozon/campaigns/{id}/products/bids.
func (h *OzonHandler) SetProductBids(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, campaignID, ok := h.workspaceAndCampaign(w, r)
	if !ok {
		return
	}
	var req struct {
		Items []service.OzonBidInput `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	changes, err := h.actions.SetProductBids(r.Context(), workspaceID, campaignID, req.Items)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, changes)
}

// CompetitiveBids handles GET /ozon/campaigns/{id}/bids/competitive.
func (h *OzonHandler) CompetitiveBids(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, campaignID, ok := h.workspaceAndCampaign(w, r)
	if !ok {
		return
	}
	bids, err := h.actions.GetCompetitiveBids(r.Context(), workspaceID, campaignID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if bids == nil {
		bids = []ozon.CompetitiveBid{}
	}
	dto.WriteJSON(w, http.StatusOK, bids)
}

// ListBidChanges handles GET /ozon/bid-changes?cabinet_id=&campaign_id=&page=.
func (h *OzonHandler) ListBidChanges(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	var campaignID *uuid.UUID
	if raw := r.URL.Query().Get("campaign_id"); raw != "" {
		parsed, err := parseNonNilUUID(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid campaign_id")
			return
		}
		campaignID = &parsed
	}
	pg := pagination.Parse(r)
	items, total, err := h.actions.ListBidChanges(r.Context(), workspaceID, cabinetID, campaignID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// --- phase 2: CPO (search promo) ---

// ListCPOProducts handles GET /ozon/cpo/products?cabinet_id=&page=.
func (h *OzonHandler) ListCPOProducts(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	pg := pagination.Parse(r)
	items, total, err := h.actions.ListCPOProducts(r.Context(), workspaceID, cabinetID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

type ozonCPOToggleRequest struct {
	CabinetID string  `json:"cabinet_id"`
	SKUs      []int64 `json:"skus"`
}

func (h *OzonHandler) cpoToggle(w http.ResponseWriter, r *http.Request, enable bool) {
	if !h.requireActions(w) {
		return
	}
	var req ozonCPOToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	var err error
	status := "disabled"
	if enable {
		status = "enabled"
		err = h.actions.EnableCPO(r.Context(), workspaceID, cabinetID, req.SKUs)
	} else {
		err = h.actions.DisableCPO(r.Context(), workspaceID, cabinetID, req.SKUs)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]any{"status": status, "skus": len(req.SKUs)})
}

// EnableCPO handles POST /ozon/cpo/enable.
func (h *OzonHandler) EnableCPO(w http.ResponseWriter, r *http.Request) { h.cpoToggle(w, r, true) }

// DisableCPO handles POST /ozon/cpo/disable.
func (h *OzonHandler) DisableCPO(w http.ResponseWriter, r *http.Request) { h.cpoToggle(w, r, false) }

// SetCPOBids handles POST /ozon/cpo/bids.
func (h *OzonHandler) SetCPOBids(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	var req struct {
		CabinetID string                    `json:"cabinet_id"`
		Bids      []service.OzonCPOBidInput `json:"bids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	if err := h.actions.SetCPOBids(r.Context(), workspaceID, cabinetID, req.Bids); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]any{"status": "applied", "bids": len(req.Bids)})
}

// BidLimits handles GET /ozon/limits?cabinet_id= (passthrough, cached 1h).
func (h *OzonHandler) BidLimits(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	payload, err := h.actions.GetBidLimits(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, payload)
}
