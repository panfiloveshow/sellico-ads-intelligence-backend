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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type productServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.ProductListFilter, limit, offset int32) ([]domain.Product, error)
	Get(ctx context.Context, workspaceID, productID uuid.UUID) (*domain.Product, error)
	ListPositions(ctx context.Context, workspaceID, productID uuid.UUID, limit, offset int32) ([]domain.Position, error)
	ListRecommendations(ctx context.Context, workspaceID, productID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

// ProductHandler handles product HTTP endpoints.
type ProductHandler struct {
	svc     productServicer
	counter ListCounter
}

func NewProductHandler(svc productServicer) *ProductHandler {
	return &ProductHandler{svc: svc}
}

func (h *ProductHandler) WithCounter(c ListCounter) *ProductHandler {
	h.counter = c
	return h
}

func (h *ProductHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	sellerCabinetID, err := parseOptionalUUIDQuery(r, "seller_cabinet_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid seller_cabinet_id")
		return
	}

	pg := pagination.Parse(r)
	products, err := h.svc.List(r.Context(), workspaceID, service.ProductListFilter{
		Title:           r.URL.Query().Get("title"),
		SellerCabinetID: sellerCabinetID,
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.ProductResponse, len(products))
	for i, product := range products {
		items[i] = dto.ProductFromDomain(product)
	}
	total := int64(len(items))
	if h.counter != nil {
		total = h.counter.CountProducts(r.Context(), workspaceID)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   total,
	})
}

func (h *ProductHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product id")
		return
	}

	product, err := h.svc.Get(r.Context(), workspaceID, productID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.ProductFromDomain(*product))
}

func (h *ProductHandler) ListPositions(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product id")
		return
	}

	pg := pagination.Parse(r)
	positions, err := h.svc.ListPositions(r.Context(), workspaceID, productID, int32(pg.PerPage), int32(pg.Offset()))
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

func (h *ProductHandler) ListRecommendations(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	productID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid product id")
		return
	}

	pg := pagination.Parse(r)
	recommendations, err := h.svc.ListRecommendations(r.Context(), workspaceID, productID, service.RecommendationListFilter{
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
