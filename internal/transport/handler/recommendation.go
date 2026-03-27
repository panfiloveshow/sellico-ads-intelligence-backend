package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type recommendationServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
	UpdateStatus(ctx context.Context, workspaceID, recommendationID uuid.UUID, status string) (*domain.Recommendation, error)
}

// RecommendationHandler handles recommendation endpoints.
type RecommendationHandler struct {
	svc recommendationServicer
}

func NewRecommendationHandler(svc recommendationServicer) *RecommendationHandler {
	return &RecommendationHandler{svc: svc}
}

func (h *RecommendationHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	pg := pagination.Parse(r)
	recommendations, err := h.svc.List(r.Context(), workspaceID, service.RecommendationListFilter{
		Type:     r.URL.Query().Get("type"),
		Severity: r.URL.Query().Get("severity"),
		Status:   r.URL.Query().Get("status"),
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.RecommendationResponse, len(recommendations))
	for i, recommendation := range recommendations {
		items[i] = dto.RecommendationFromDomain(recommendation)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

func (h *RecommendationHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	recommendationID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid recommendation id")
		return
	}

	var req dto.UpdateRecommendationStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	recommendation, err := h.svc.UpdateStatus(r.Context(), workspaceID, recommendationID, req.Status)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.RecommendationFromDomain(*recommendation))
}
