package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
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

// ozonSyncEnqueuer enqueues an async ozon:sync_cabinet task.
type ozonSyncEnqueuer func(cabinetID uuid.UUID) error

// OzonHandler serves the read-only /api/v1/ozon endpoints.
type OzonHandler struct {
	svc         ozonServicer
	enqueueSync ozonSyncEnqueuer
}

// NewOzonHandler creates a new OzonHandler.
func NewOzonHandler(svc ozonServicer, enqueueSync ozonSyncEnqueuer) *OzonHandler {
	return &OzonHandler{svc: svc, enqueueSync: enqueueSync}
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
