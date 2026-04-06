package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type mockJobRunService struct {
	listFn  func(ctx context.Context, workspaceID uuid.UUID, filter service.JobRunListFilter, limit, offset int32) ([]domain.JobRun, error)
	getFn   func(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*domain.JobRun, error)
	retryFn func(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*service.JobRunRetryResult, error)
}

func (m *mockJobRunService) List(ctx context.Context, workspaceID uuid.UUID, filter service.JobRunListFilter, limit, offset int32) ([]domain.JobRun, error) {
	return m.listFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockJobRunService) Get(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*domain.JobRun, error) {
	return m.getFn(ctx, workspaceID, jobRunID)
}

func (m *mockJobRunService) Retry(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*service.JobRunRetryResult, error) {
	return m.retryFn(ctx, workspaceID, jobRunID)
}

func decodeJobRun(t *testing.T, data interface{}) dto.JobRunResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.JobRunResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestJobRunList_Success(t *testing.T) {
	workspaceID := uuid.New()
	now := time.Now().UTC()
	mock := &mockJobRunService{
		listFn: func(_ context.Context, actualWorkspaceID uuid.UUID, filter service.JobRunListFilter, _, _ int32) ([]domain.JobRun, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			assert.Equal(t, domain.JobStatusCompleted, filter.Status)
			assert.Equal(t, "wb:sync_products", filter.TaskType)
			return []domain.JobRun{{
				ID:          uuid.New(),
				WorkspaceID: &workspaceID,
				TaskType:    "wb:sync_products",
				Status:      domain.JobStatusCompleted,
				StartedAt:   now,
				CreatedAt:   now,
			}}, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.JobRun, error) {
			return nil, nil
		},
		retryFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.JobRunRetryResult, error) {
			return nil, nil
		},
	}
	h := NewJobRunHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/job-runs?task_type=wb:sync_products&status=completed", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item := decodeJobRun(t, items[0])
	assert.Equal(t, "wb:sync_products", item.TaskType)
	assert.Equal(t, domain.JobStatusCompleted, item.Status)
}

func TestJobRunGet_Success(t *testing.T) {
	workspaceID := uuid.New()
	jobRunID := uuid.New()
	now := time.Now().UTC()
	mock := &mockJobRunService{
		listFn: func(context.Context, uuid.UUID, service.JobRunListFilter, int32, int32) ([]domain.JobRun, error) {
			return nil, nil
		},
		getFn: func(_ context.Context, actualWorkspaceID, actualJobRunID uuid.UUID) (*domain.JobRun, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			assert.Equal(t, jobRunID, actualJobRunID)
			return &domain.JobRun{
				ID:          jobRunID,
				WorkspaceID: &workspaceID,
				TaskType:    "recommendation:generate",
				Status:      domain.JobStatusCompleted,
				StartedAt:   now,
				CreatedAt:   now,
			}, nil
		},
		retryFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.JobRunRetryResult, error) {
			return nil, nil
		},
	}
	h := NewJobRunHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/job-runs/"+jobRunID.String(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", jobRunID.String())
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	item := decodeJobRun(t, resp.Data)
	assert.Equal(t, "recommendation:generate", item.TaskType)
}

func TestJobRunGet_InvalidUUID(t *testing.T) {
	mock := &mockJobRunService{
		listFn: func(context.Context, uuid.UUID, service.JobRunListFilter, int32, int32) ([]domain.JobRun, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.JobRun, error) {
			return nil, nil
		},
		retryFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.JobRunRetryResult, error) {
			return nil, nil
		},
	}
	h := NewJobRunHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/job-runs/not-a-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "not-a-uuid")
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestJobRunRetry_Success(t *testing.T) {
	workspaceID := uuid.New()
	jobRunID := uuid.New()
	mock := &mockJobRunService{
		listFn: func(context.Context, uuid.UUID, service.JobRunListFilter, int32, int32) ([]domain.JobRun, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.JobRun, error) {
			return nil, nil
		},
		retryFn: func(_ context.Context, actualWorkspaceID, actualJobRunID uuid.UUID) (*service.JobRunRetryResult, error) {
			assert.Equal(t, workspaceID, actualWorkspaceID)
			assert.Equal(t, jobRunID, actualJobRunID)
			return &service.JobRunRetryResult{
				OriginalJobRunID: jobRunID,
				TaskType:         "wb:sync_products",
				Status:           "enqueued",
				WorkspaceID:      workspaceID,
			}, nil
		},
	}
	h := NewJobRunHandler(mock)

	req := httptest.NewRequest(http.MethodPost, "/job-runs/"+jobRunID.String()+"/retry", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", jobRunID.String())
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.Retry(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	resp := decodeEnvelope(t, rec)
	raw, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var result dto.JobRunRetryResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, "wb:sync_products", result.TaskType)
	assert.Equal(t, "enqueued", result.Status)
	assert.Equal(t, workspaceID, result.WorkspaceID)
}
