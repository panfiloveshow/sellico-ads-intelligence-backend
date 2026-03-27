package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// campaignServicer is the interface the CampaignHandler depends on.
type campaignServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.Campaign, error)
	Get(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.Campaign, error)
	GetStats(ctx context.Context, campaignID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.CampaignStat, error)
	ListPhrases(ctx context.Context, campaignID uuid.UUID, limit, offset int32) ([]domain.Phrase, error)
}

// CampaignHandler handles campaign HTTP endpoints.
type CampaignHandler struct {
	svc campaignServicer
}

// NewCampaignHandler creates a new CampaignHandler.
func NewCampaignHandler(svc campaignServicer) *CampaignHandler {
	return &CampaignHandler{svc: svc}
}

// List handles GET /campaigns.
func (h *CampaignHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	pg := pagination.Parse(r)

	campaigns, err := h.svc.List(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.CampaignResponse, len(campaigns))
	for i, c := range campaigns {
		items[i] = dto.CampaignFromDomain(c)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// Get handles GET /campaigns/{id}.
func (h *CampaignHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	idStr := chi.URLParam(r, "id")
	campaignID, err := uuid.Parse(idStr)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign id")
		return
	}

	c, err := h.svc.Get(r.Context(), workspaceID, campaignID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.CampaignFromDomain(*c))
}

// GetStats handles GET /campaigns/{id}/stats.
func (h *CampaignHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	idStr := chi.URLParam(r, "id")
	campaignID, err := uuid.Parse(idStr)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign id")
		return
	}

	// Verify campaign belongs to workspace.
	if _, err := h.svc.Get(r.Context(), workspaceID, campaignID); err != nil {
		writeAppError(w, err)
		return
	}

	// Parse date range.
	dateFrom, dateTo := parseDateRange(r)

	pg := pagination.Parse(r)

	stats, err := h.svc.GetStats(r.Context(), campaignID, dateFrom, dateTo, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.CampaignStatResponse, len(stats))
	for i, s := range stats {
		items[i] = dto.CampaignStatFromDomain(s)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

// ListPhrases handles GET /campaigns/{id}/phrases.
func (h *CampaignHandler) ListPhrases(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	idStr := chi.URLParam(r, "id")
	campaignID, err := uuid.Parse(idStr)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign id")
		return
	}

	// Verify campaign belongs to workspace.
	if _, err := h.svc.Get(r.Context(), workspaceID, campaignID); err != nil {
		writeAppError(w, err)
		return
	}

	pg := pagination.Parse(r)

	phrases, err := h.svc.ListPhrases(r.Context(), campaignID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.PhraseResponse, len(phrases))
	for i, p := range phrases {
		items[i] = dto.PhraseFromDomain(p)
	}

	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

const dateLayout = "2006-01-02"

// parseDateRange extracts date_from and date_to query params.
// Defaults to last 30 days if not provided.
func parseDateRange(r *http.Request) (time.Time, time.Time) {
	now := time.Now().UTC()
	dateTo := now
	dateFrom := now.AddDate(0, 0, -30)

	if v := r.URL.Query().Get("date_from"); v != "" {
		if t, err := time.Parse(dateLayout, v); err == nil {
			dateFrom = t
		}
	}
	if v := r.URL.Query().Get("date_to"); v != "" {
		if t, err := time.Parse(dateLayout, v); err == nil {
			dateTo = t
		}
	}

	return dateFrom, dateTo
}
