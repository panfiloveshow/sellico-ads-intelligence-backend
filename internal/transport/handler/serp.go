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

type serpServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.SERPListFilter, limit, offset int32) ([]domain.SERPSnapshot, error)
	Get(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPSnapshot, error)
	ListItems(ctx context.Context, snapshotID uuid.UUID) ([]domain.SERPResultItem, error)
}

// SERPHandler handles SERP snapshot endpoints.
type SERPHandler struct {
	svc serpServicer
}

func NewSERPHandler(svc serpServicer) *SERPHandler {
	return &SERPHandler{svc: svc}
}

func (h *SERPHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	dateFrom, dateTo := parseDateRangeWithDefault(r, 30)
	pg := pagination.Parse(r)
	snapshots, err := h.svc.List(r.Context(), workspaceID, service.SERPListFilter{
		Query:    r.URL.Query().Get("query"),
		Region:   r.URL.Query().Get("region"),
		DateFrom: &dateFrom,
		DateTo:   &dateTo,
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.SERPSnapshotResponse, len(snapshots))
	for i, snapshot := range snapshots {
		items[i] = dto.SERPSnapshotFromDomain(snapshot)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

func (h *SERPHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}
	snapshotID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid serp snapshot id")
		return
	}

	snapshot, err := h.svc.Get(r.Context(), workspaceID, snapshotID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	items, err := h.svc.ListItems(r.Context(), snapshotID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.SERPSnapshotDetailFromDomain(*snapshot, items))
}
