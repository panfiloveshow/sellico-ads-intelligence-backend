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

type jobRunServicer interface {
	List(ctx context.Context, workspaceID uuid.UUID, filter service.JobRunListFilter, limit, offset int32) ([]domain.JobRun, error)
	Get(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*domain.JobRun, error)
	Retry(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*service.JobRunRetryResult, error)
}

// JobRunHandler handles job run monitoring endpoints.
type JobRunHandler struct {
	svc jobRunServicer
}

func NewJobRunHandler(svc jobRunServicer) *JobRunHandler {
	return &JobRunHandler{svc: svc}
}

func (h *JobRunHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	pg := pagination.Parse(r)
	jobRuns, err := h.svc.List(r.Context(), workspaceID, service.JobRunListFilter{
		TaskType: r.URL.Query().Get("task_type"),
		Status:   r.URL.Query().Get("status"),
	}, int32(pg.PerPage), int32(pg.Offset()))
	if err != nil {
		writeAppError(w, err)
		return
	}

	items := make([]dto.JobRunResponse, len(jobRuns))
	for i, item := range jobRuns {
		items[i] = dto.JobRunFromDomain(item)
	}
	dto.WriteJSONWithMeta(w, http.StatusOK, items, &envelope.Meta{
		Page:    pg.Page,
		PerPage: pg.PerPage,
		Total:   int64(len(items)),
	})
}

func (h *JobRunHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	jobRunID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid job run id")
		return
	}

	jobRun, err := h.svc.Get(r.Context(), workspaceID, jobRunID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusOK, dto.JobRunFromDomain(*jobRun))
}

func (h *JobRunHandler) Retry(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := middleware.WorkspaceIDFromContext(r.Context())
	if !ok {
		writeAppError(w, apperror.New(apperror.ErrValidation, "missing workspace id"))
		return
	}

	jobRunID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, apperror.ErrValidation.Code, "invalid job run id")
		return
	}

	result, err := h.svc.Retry(r.Context(), workspaceID, jobRunID)
	if err != nil {
		writeAppError(w, err)
		return
	}
	dto.WriteJSON(w, http.StatusAccepted, dto.JobRunRetryFromService(*result))
}
