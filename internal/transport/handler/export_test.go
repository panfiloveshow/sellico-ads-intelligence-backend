package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

type mockExportService struct {
	createFn          func(ctx context.Context, userID, workspaceID uuid.UUID, entityType, format string, filters json.RawMessage) (*domain.Export, error)
	getFn             func(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error)
	prepareDownloadFn func(ctx context.Context, workspaceID, exportID uuid.UUID) (*service.ExportDownload, error)
}

func (m *mockExportService) Create(ctx context.Context, userID, workspaceID uuid.UUID, entityType, format string, filters json.RawMessage) (*domain.Export, error) {
	return m.createFn(ctx, userID, workspaceID, entityType, format, filters)
}

func (m *mockExportService) Get(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error) {
	return m.getFn(ctx, workspaceID, exportID)
}

func (m *mockExportService) PrepareDownload(ctx context.Context, workspaceID, exportID uuid.UUID) (*service.ExportDownload, error) {
	return m.prepareDownloadFn(ctx, workspaceID, exportID)
}

func decodeExport(t *testing.T, data interface{}) dto.ExportResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.ExportResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestExportCreate_Success(t *testing.T) {
	userID := uuid.New()
	workspaceID := uuid.New()
	exportID := uuid.New()
	now := time.Now()

	mock := &mockExportService{
		createFn: func(_ context.Context, gotUserID, gotWorkspaceID uuid.UUID, entityType, format string, filters json.RawMessage) (*domain.Export, error) {
			assert.Equal(t, userID, gotUserID)
			assert.Equal(t, workspaceID, gotWorkspaceID)
			assert.Equal(t, "products", entityType)
			assert.Equal(t, "csv", format)
			assert.JSONEq(t, `{"title":"boots"}`, string(filters))
			return &domain.Export{
				ID:          exportID,
				WorkspaceID: workspaceID,
				UserID:      userID,
				EntityType:  entityType,
				Format:      format,
				Status:      domain.ExportStatusPending,
				Filters:     filters,
				CreatedAt:   now,
				UpdatedAt:   now,
			}, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Export, error) {
			return nil, nil
		},
		prepareDownloadFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExportDownload, error) {
			return nil, nil
		},
	}
	h := NewExportHandler(mock)

	req := httptest.NewRequest(http.MethodPost, "/exports", jsonBody(t, dto.CreateExportRequest{
		EntityType: "products",
		Format:     "csv",
		Filters:    json.RawMessage(`{"title":"boots"}`),
	}))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeEnvelope(t, rec)
	exportResp := decodeExport(t, resp.Data)
	assert.Equal(t, exportID, exportResp.ID)
	assert.Equal(t, domain.ExportStatusPending, exportResp.Status)
}

func TestExportGet_Success(t *testing.T) {
	workspaceID := uuid.New()
	exportID := uuid.New()
	now := time.Now()

	mock := &mockExportService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, string, string, json.RawMessage) (*domain.Export, error) {
			return nil, nil
		},
		getFn: func(_ context.Context, gotWorkspaceID, gotExportID uuid.UUID) (*domain.Export, error) {
			assert.Equal(t, workspaceID, gotWorkspaceID)
			assert.Equal(t, exportID, gotExportID)
			return &domain.Export{
				ID:          exportID,
				WorkspaceID: workspaceID,
				UserID:      uuid.New(),
				EntityType:  "products",
				Format:      "csv",
				Status:      domain.ExportStatusCompleted,
				CreatedAt:   now,
				UpdatedAt:   now,
			}, nil
		},
		prepareDownloadFn: func(context.Context, uuid.UUID, uuid.UUID) (*service.ExportDownload, error) {
			return nil, nil
		},
	}
	h := NewExportHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/exports/"+exportID.String(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", exportID.String())
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	exportResp := decodeExport(t, resp.Data)
	assert.Equal(t, exportID, exportResp.ID)
	assert.Equal(t, domain.ExportStatusCompleted, exportResp.Status)
}

func TestExportDownload_Success(t *testing.T) {
	workspaceID := uuid.New()
	exportID := uuid.New()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "export.csv")
	require.NoError(t, os.WriteFile(filePath, []byte("id,name\n1,boots\n"), 0o644))

	mock := &mockExportService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, string, string, json.RawMessage) (*domain.Export, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Export, error) {
			return nil, nil
		},
		prepareDownloadFn: func(_ context.Context, gotWorkspaceID, gotExportID uuid.UUID) (*service.ExportDownload, error) {
			assert.Equal(t, workspaceID, gotWorkspaceID)
			assert.Equal(t, exportID, gotExportID)
			return &service.ExportDownload{
				Path:        filePath,
				FileName:    "export.csv",
				ContentType: "text/csv; charset=utf-8",
			}, nil
		},
	}
	h := NewExportHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/exports/"+exportID.String()+"/download", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", exportID.String())
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "attachment; filename=\"export.csv\"", rec.Header().Get("Content-Disposition"))
	assert.Contains(t, rec.Body.String(), "boots")
}
