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
	Get(ctx context.Context, workspaceID, recommendationID uuid.UUID) (*domain.Recommendation, error)
	UpdateStatus(ctx context.Context, workspaceID, recommendationID uuid.UUID, status string) (*domain.Recommendation, error)
	TriggerGenerate(ctx context.Context, actorID, workspaceID uuid.UUID) (*service.WorkspaceTaskTriggerResult, error)
}

// RecommendationHandler handles recommendation endpoints.
type RecommendationHandler struct {
	svc     recommendationServicer
	counter ListCounter
}

func NewRecommendationHandler(svc recommendationServicer) *RecommendationHandler {
	return &RecommendationHandler{svc: svc}
}

func (h *RecommendationHandler) WithCounter(c ListCounter) *RecommendationHandler {
	h.counter = c
	return h
}

func (h *RecommendationHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	campaignID, err := parseOptionalUUIDQuery(r, "campaign_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid campaign_id")
		return
	}
	phraseID, err := parseOptionalUUIDQuery(r, "phrase_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid phrase_id")
		return
	}
	productID, err := parseOptionalUUIDQuery(r, "product_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product_id")
		return
	}

	pg := pagination.Parse(r)
	recommendations, err := h.svc.List(r.Context(), workspaceID, service.RecommendationListFilter{
		CampaignID: campaignID,
		PhraseID:   phraseID,
		ProductID:  productID,
		Type:       r.URL.Query().Get("type"),
		Severity:   r.URL.Query().Get("severity"),
		Status:     r.URL.Query().Get("status"),
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.RecommendationResponse, len(recommendations))
	for i, recommendation := range recommendations {
		items[i] = dto.RecommendationFromDomain(recommendation)
	}
	total := int64(len(items))
	if h.counter != nil {
		total = h.counter.CountRecommendations(r.Context(), workspaceID)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   total,
	})
}

func (h *RecommendationHandler) Get(w http.ResponseWriter, r *http.Request) {
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

	recommendation, err := h.svc.Get(r.Context(), workspaceID, recommendationID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusOK, dto.RecommendationFromDomain(*recommendation))
}

func (h *RecommendationHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	var req dto.UpdateRecommendationStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	h.updateStatus(w, r, req.Status)
}

func (h *RecommendationHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	h.updateStatus(w, r, domain.RecommendationStatusCompleted)
}

func (h *RecommendationHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	h.updateStatus(w, r, domain.RecommendationStatusDismissed)
}

func (h *RecommendationHandler) TriggerGenerate(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}

	result, err := h.svc.TriggerGenerate(r.Context(), userID, workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, dto.WorkspaceTaskTriggerFromService(*result))
}

func (h *RecommendationHandler) updateStatus(w http.ResponseWriter, r *http.Request, status string) {
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

	recommendation, err := h.svc.UpdateStatus(r.Context(), workspaceID, recommendationID, status)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.RecommendationFromDomain(*recommendation))
}
