package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type seoServicer interface {
	AnalyzeWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error)
	GetAnalysis(ctx context.Context, productID uuid.UUID) (*domain.SEOAnalysis, error)
}

type SEOHandler struct {
	svc seoServicer
}

func NewSEOHandler(svc seoServicer) *SEOHandler {
	return &SEOHandler{svc: svc}
}

func (h *SEOHandler) AnalyzeAll(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	count, err := h.svc.AnalyzeWorkspace(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]int{"analyzed": count})
}

func (h *SEOHandler) GetProductAnalysis(w http.ResponseWriter, r *http.Request) {
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid product id")
		return
	}
	analysis, err := h.svc.GetAnalysis(r.Context(), productID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, analysis)
}
