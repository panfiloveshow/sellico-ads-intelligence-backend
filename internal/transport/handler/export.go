package handler

import (
	"context"
	"encoding/json"
	"fmt"
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

type exportServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.ExportListFilter, limit, offset int32) ([]domain.Export, error)
	Create(ctx context.Context, userID, workspaceID uuid.UUID, entityType, format string, filters json.RawMessage) (*domain.Export, error)
	Get(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error)
	PrepareDownload(ctx context.Context, workspaceID, exportID uuid.UUID) (*service.ExportDownload, error)
}

type ExportHandler struct {
	svc     exportServicer
	counter ListCounter
}

func NewExportHandler(svc exportServicer) *ExportHandler {
	return &ExportHandler{svc: svc}
}

func (h *ExportHandler) WithCounter(c ListCounter) *ExportHandler {
	h.counter = c
	return h
}

func (h *ExportHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	userID, err := parseOptionalUUIDQuery(r, "user_id")
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid user_id")
		return
	}
	pg := pagination.Parse(r)
	exports, err := h.svc.List(r.Context(), workspaceID, service.ExportListFilter{
		UserID:     userID,
		EntityType: r.URL.Query().Get("entity_type"),
		Format:     r.URL.Query().Get("format"),
		Status:     r.URL.Query().Get("status"),
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.ExportResponse, len(exports))
	for i, exportTask := range exports {
		items[i] = dto.ExportFromDomain(exportTask)
	}
	total := int64(len(items))
	if h.counter != nil {
		total = h.counter.CountExports(r.Context(), workspaceID)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   total,
	})
}

func (h *ExportHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrUnauthorized, "authentication required"))
		return
	}
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	var req dto.CreateExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid request body")
		return
	}
	if errs := req.Validate(); len(errs) > 0 {
		dto.WriteValidationError(w, errs)
		return
	}

	exportTask, err := h.svc.Create(r.Context(), userID, workspaceID, req.EntityType, req.Format, req.Filters)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusCreated, dto.ExportFromDomain(*exportTask))
}

func (h *ExportHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	exportID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid export id")
		return
	}

	exportTask, err := h.svc.Get(r.Context(), workspaceID, exportID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.ExportFromDomain(*exportTask))
}

func (h *ExportHandler) Download(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	exportID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid export id")
		return
	}

	download, err := h.svc.PrepareDownload(r.Context(), workspaceID, exportID)
	if err != nil {
		writeAppError(w, err)
		return
	}

	w.Header().Set("Content-Type", download.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", download.FileName))
	http.ServeFile(w, r, download.Path)
}
