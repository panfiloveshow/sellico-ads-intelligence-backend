package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/pagination"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type positionServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.PositionListFilter, limit, offset int32) ([]domain.Position, error)
	Aggregate(ctx context.Context, workspaceID, productID uuid.UUID, query, region string, dateFrom, dateTo time.Time) (*domain.PositionAggregate, error)
}

// PositionHandler handles position endpoints.
type PositionHandler struct {
	svc positionServicer
}

func NewPositionHandler(svc positionServicer) *PositionHandler {
	return &PositionHandler{svc: svc}
}

func (h *PositionHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	productID, err := parseOptionalUUIDQuery(r, "product_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product_id")
		return
	}
	dateFrom, dateTo := parseDateRangeWithDefault(r, 30)
	pg := pagination.Parse(r)

	positions, err := h.svc.List(r.Context(), workspaceID, service.PositionListFilter{
		ProductID: productID,
		Query:     r.URL.Query().Get("query"),
		Region:    r.URL.Query().Get("region"),
		DateFrom:  &dateFrom,
		DateTo:    &dateTo,
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.PositionResponse, len(positions))
	for i, position := range positions {
		items[i] = dto.PositionFromDomain(position)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

func (h *PositionHandler) Aggregate(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	productIDRaw := r.URL.Query().Get("product_id")
	query := r.URL.Query().Get("query")
	region := r.URL.Query().Get("region")
	if productIDRaw == "" || query == "" || region == "" {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "product_id, query and region are required")
		return
	}
	productID, err := uuid.Parse(productIDRaw)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product_id")
		return
	}

	dateFrom, dateTo := parseDateRangeWithDefault(r, 30)
	aggregate, err := h.svc.Aggregate(r.Context(), workspaceID, productID, query, region, dateFrom, dateTo)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.PositionAggregateFromDomain(*aggregate))
}
