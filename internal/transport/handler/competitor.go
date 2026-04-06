package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type competitorServicer interface {
	ListByProduct(ctx context.Context, productID uuid.UUID, limit, offset int32) ([]domain.Competitor, error)
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.Competitor, error)
	ExtractFromSERP(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

type CompetitorHandler struct {
	svc competitorServicer
}

func NewCompetitorHandler(svc competitorServicer) *CompetitorHandler {
	return &CompetitorHandler{svc: svc}
}

func (h *CompetitorHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	pg := pagination.Parse(r)

	productID, _ := parseOptionalUUIDQuery(r, "product_id")
	var competitors []domain.Competitor
	var err error
	if productID != nil {
		competitors, err = h.svc.ListByProduct(r.Context(), *productID, int32(pg.PerPage), int32(pg.Offset()))
	} else {
		competitors, err = h.svc.ListByWorkspace(r.Context(), workspaceID, int32(pg.PerPage), int32(pg.Offset()))
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, competitors, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(competitors))})
}

func (h *CompetitorHandler) ListByProduct(w http.ResponseWriter, r *http.Request) {
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid product id")
		return
	}
	pg := pagination.Parse(r)
	competitors, err := h.svc.ListByProduct(r.Context(), productID, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, competitors, &envelope.Meta{Page: pg.Page, PerPage: pg.PerPage, Total: int64(len(competitors))})
}

func (h *CompetitorHandler) Extract(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	count, err := h.svc.ExtractFromSERP(r.Context(), workspaceID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, map[string]int{"competitors_found": count})
}
