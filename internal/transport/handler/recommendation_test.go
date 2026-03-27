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

type mockRecommendationService struct {
	listFn         func(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
	updateStatusFn func(ctx context.Context, workspaceID, recommendationID uuid.UUID, status string) (*domain.Recommendation, error)
}

func (m *mockRecommendationService) List(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	return m.listFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockRecommendationService) UpdateStatus(ctx context.Context, workspaceID, recommendationID uuid.UUID, status string) (*domain.Recommendation, error) {
	return m.updateStatusFn(ctx, workspaceID, recommendationID, status)
}

func decodeRecommendation(t *testing.T, data interface{}) dto.RecommendationResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.RecommendationResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestRecommendationList_Success(t *testing.T) {
	workspaceID := uuid.New()
	now := time.Now()
	mock := &mockRecommendationService{
		listFn: func(_ context.Context, wsID uuid.UUID, filter service.RecommendationListFilter, _, _ int32) ([]domain.Recommendation, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, "active", filter.Status)
			return []domain.Recommendation{{
				ID:          uuid.New(),
				WorkspaceID: workspaceID,
				Title:       "High impressions with zero clicks",
				Description: "desc",
				Type:        domain.RecommendationTypeLowCTR,
				Severity:    domain.SeverityHigh,
				Confidence:  0.84,
				Status:      domain.RecommendationStatusActive,
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, nil
		},
		updateStatusFn: func(context.Context, uuid.UUID, uuid.UUID, string) (*domain.Recommendation, error) {
			return nil, nil
		},
	}
	h := NewRecommendationHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/recommendations?status=active", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item := decodeRecommendation(t, items[0])
	assert.Equal(t, "High impressions with zero clicks", item.Title)
}

func TestRecommendationUpdateStatus_Success(t *testing.T) {
	workspaceID := uuid.New()
	recommendationID := uuid.New()
	now := time.Now()
	mock := &mockRecommendationService{
		listFn: func(context.Context, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
			return nil, nil
		},
		updateStatusFn: func(_ context.Context, wsID, recID uuid.UUID, status string) (*domain.Recommendation, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, recommendationID, recID)
			assert.Equal(t, domain.RecommendationStatusCompleted, status)
			return &domain.Recommendation{
				ID:          recommendationID,
				WorkspaceID: workspaceID,
				Title:       "High impressions with zero clicks",
				Description: "desc",
				Type:        domain.RecommendationTypeLowCTR,
				Severity:    domain.SeverityHigh,
				Confidence:  0.84,
				Status:      status,
				CreatedAt:   now,
				UpdatedAt:   now,
			}, nil
		},
	}
	h := NewRecommendationHandler(mock)

	body := jsonBody(t, dto.UpdateRecommendationStatusRequest{Status: domain.RecommendationStatusCompleted})
	req := httptest.NewRequest(http.MethodPatch, "/recommendations/"+recommendationID.String(), body)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", recommendationID.String())
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.UpdateStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	item := decodeRecommendation(t, resp.Data)
	assert.Equal(t, domain.RecommendationStatusCompleted, item.Status)
}
