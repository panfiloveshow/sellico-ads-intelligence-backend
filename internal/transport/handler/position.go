package handler

import (
	"context"
	"encoding/json"
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
	CreateTrackingTarget(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreatePositionTrackingTargetInput) (*domain.PositionTrackingTarget, error)
	ListTrackingTargets(ctx context.Context, workspaceID uuid.UUID, filter service.PositionTargetListFilter, limit, offset int32) ([]domain.PositionTrackingTarget, error)
	Create(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreatePositionInput) (*domain.Position, error)
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

func (h *PositionHandler) CreateTarget(w http.ResponseWriter, r *http.Request) {
	actorID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreatePositionTrackingTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	target, err := h.svc.CreateTrackingTarget(r.Context(), actorID, workspaceID, service.CreatePositionTrackingTargetInput{
		ProductID: req.ProductID,
		Query:     req.Query,
		Region:    req.Region,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusCreated, dto.PositionTrackingTargetFromDomain(*target))
}

func (h *PositionHandler) ListTargets(w http.ResponseWriter, r *http.Request) {
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
	pg := pagination.Parse(r)
	targets, err := h.svc.ListTrackingTargets(r.Context(), workspaceID, service.PositionTargetListFilter{
		ProductID:  productID,
		Query:      r.URL.Query().Get("query"),
		Region:     r.URL.Query().Get("region"),
		ActiveOnly: r.URL.Query().Get("active_only") != "false",
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.PositionTrackingTargetResponse, len(targets))
	for i, target := range targets {
		items[i] = dto.PositionTrackingTargetFromDomain(target)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

func (h *PositionHandler) Create(w http.ResponseWriter, r *http.Request) {
	actorID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreatePositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	position, err := h.svc.Create(r.Context(), actorID, workspaceID, service.CreatePositionInput{
		ProductID: req.ProductID,
		Query:     req.Query,
		Region:    req.Region,
		Position:  req.Position,
		Page:      req.Page,
		Source:    req.Source,
		CheckedAt: req.CheckedAt,
	})
	if err != nil {
		writeAppError(w, err)
		return
	}

	dto.WriteJSON(w, http.StatusCreated, dto.PositionFromDomain(*position))
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
