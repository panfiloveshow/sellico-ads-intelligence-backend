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
	ListSearchQueries(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, search string, days int, limit, offset int32) ([]domain.OzonSearchQueryStat, int64, error)
	GetCPOOverview(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonCPOOverview, error)
	ListCPOOrders(ctx context.Context, workspaceID, cabinetID uuid.UUID, days int, limit, offset int32) ([]domain.OzonCPOOrder, int64, error)
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
	SetCPORate(ctx context.Context, workspaceID, cabinetID uuid.UUID, ratePct int) error
	SetCPOActive(ctx context.Context, workspaceID, cabinetID uuid.UUID, active bool) error
	GetBidLimits(ctx context.Context, workspaceID, cabinetID uuid.UUID) (json.RawMessage, error)
}

// ozonSyncEnqueuer enqueues an async ozon:sync_cabinet task.
type ozonSyncEnqueuer func(cabinetID uuid.UUID) error

// OzonHandler serves the /api/v1/ozon endpoints (phase 1 reads, phase 2
// writes, phase 3 AI autopilot — the latter attached via WithAI).
type OzonHandler struct {
	svc                ozonServicer
	actions            ozonActionsServicer
	enqueueSync        ozonSyncEnqueuer
	ai                 ozonAIServicer
	enqueueAIRun       ozonAIRunEnqueuer
	repricer           ozonRepricerServicer
	enqueueRepricerRun ozonRepricerRunEnqueuer
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
	pgSQLLimit, pgSQLOffset := pg.SQLLimitOffset()

	items, total, err := h.svc.ListCampaignsWithStats(r.Context(), workspaceID, cabinetID, pgSQLLimit, pgSQLOffset)
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
	pgSQLLimit, pgSQLOffset := pg.SQLLimitOffset()

	items, total, err := h.svc.ListPrices(r.Context(), workspaceID, cabinetID, r.URL.Query().Get("search"), pgSQLLimit, pgSQLOffset)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// ListSearchQueries handles GET /ozon/search-queries?cabinet_id=&sku=&search=&days=&page=
// — per-query aggregates from the phrases-report mirror.
func (h *OzonHandler) ListSearchQueries(w http.ResponseWriter, r *http.Request) {
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	var sku *int64
	if raw := r.URL.Query().Get("sku"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "sku must be an integer")
			return
		}
		sku = &parsed
	}
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 90 {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "days must be between 1 and 90")
			return
		}
		days = parsed
	}
	pg := pagination.Parse(r)
	pgSQLLimit, pgSQLOffset := pg.SQLLimitOffset()

	items, total, err := h.svc.ListSearchQueries(r.Context(), workspaceID, cabinetID, sku, r.URL.Query().Get("search"), days, pgSQLLimit, pgSQLOffset)
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
// cabinet_id is optional when campaign_id is given (campaign detail page).
func (h *OzonHandler) ListBidChanges(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing workspace id")
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
	cabinetID := uuid.Nil
	if raw := r.URL.Query().Get("cabinet_id"); raw != "" {
		parsed, err := parseNonNilUUID(raw)
		if err != nil {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid cabinet_id")
			return
		}
		cabinetID = parsed
	}
	if cabinetID == uuid.Nil && campaignID == nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cabinet_id or campaign_id is required")
		return
	}
	pg := pagination.Parse(r)
	pgSQLLimit, pgSQLOffset := pg.SQLLimitOffset()
	items, total, err := h.actions.ListBidChanges(r.Context(), workspaceID, cabinetID, campaignID, pgSQLLimit, pgSQLOffset)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// --- phase 2: CPO (search promo) ---

// CPOOverview handles GET /ozon/cpo/overview?cabinet_id= — the CPO promo
// summary (enabled flag, promo campaign, product count, 7-day stats).
func (h *OzonHandler) CPOOverview(w http.ResponseWriter, r *http.Request) {
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	overview, err := h.svc.GetCPOOverview(r.Context(), workspaceID, cabinetID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, overview)
}

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
	pgSQLLimit, pgSQLOffset := pg.SQLLimitOffset()
	items, total, err := h.actions.ListCPOProducts(r.Context(), workspaceID, cabinetID, pgSQLLimit, pgSQLOffset)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: total})
}

// ListCPOOrders handles GET /ozon/cpo/orders?cabinet_id=&page=&days= — the
// mirrored CPO promoted orders (all_sku_promo report) for the «Заказы
// продвижения» tab. days defaults to 7.
func (h *OzonHandler) ListCPOOrders(w http.ResponseWriter, r *http.Request) {
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, r.URL.Query().Get("cabinet_id"))
	if !ok {
		return
	}
	days := 7
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "days must be a positive integer")
			return
		}
		days = parsed
	}
	pg := pagination.Parse(r)
	pgSQLLimit, pgSQLOffset := pg.SQLLimitOffset()
	items, total, err := h.svc.ListCPOOrders(r.Context(), workspaceID, cabinetID, days, pgSQLLimit, pgSQLOffset)
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

// SetCPORate handles POST /ozon/cpo/rate — sets the whole-cabinet CPO rate
// (rate_pct must be one of 5, 7 or 9).
func (h *OzonHandler) SetCPORate(w http.ResponseWriter, r *http.Request) {
	if !h.requireActions(w) {
		return
	}
	var req struct {
		CabinetID string `json:"cabinet_id"`
		RatePct   int    `json:"rate_pct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	if req.RatePct != 5 && req.RatePct != 7 && req.RatePct != 9 {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "rate_pct must be one of 5, 7 or 9")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	if err := h.actions.SetCPORate(r.Context(), workspaceID, cabinetID, req.RatePct); err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]any{"status": "applied", "rate_pct": req.RatePct})
}

type ozonCPOActivateRequest struct {
	CabinetID string `json:"cabinet_id"`
}

func (h *OzonHandler) cpoActivate(w http.ResponseWriter, r *http.Request, active bool) {
	if !h.requireActions(w) {
		return
	}
	var req ozonCPOActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}
	workspaceID, cabinetID, ok := h.workspaceAndCabinet(w, r, req.CabinetID)
	if !ok {
		return
	}
	if err := h.actions.SetCPOActive(r.Context(), workspaceID, cabinetID, active); err != nil {
		writeAppError(w, err)
		return
	}
	status := "deactivated"
	if active {
		status = "activated"
	}
	dto.WriteJSON(w, http.StatusOK, map[string]any{"status": status})
}

// ActivateCPO handles POST /ozon/cpo/activate — turns the whole-cabinet CPO
// promo on (idempotent when already active).
func (h *OzonHandler) ActivateCPO(w http.ResponseWriter, r *http.Request) { h.cpoActivate(w, r, true) }

// DeactivateCPO handles POST /ozon/cpo/deactivate — turns the whole-cabinet CPO
// promo off.
func (h *OzonHandler) DeactivateCPO(w http.ResponseWriter, r *http.Request) {
	h.cpoActivate(w, r, false)
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
