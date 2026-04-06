package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type mockPositionService struct {
	createTargetFn func(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreatePositionTrackingTargetInput) (*domain.PositionTrackingTarget, error)
	listTargetsFn  func(ctx context.Context, workspaceID uuid.UUID, filter service.PositionTargetListFilter, limit, offset int32) ([]domain.PositionTrackingTarget, error)
	createFn       func(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreatePositionInput) (*domain.Position, error)
	listFn         func(ctx context.Context, workspaceID uuid.UUID, filter service.PositionListFilter, limit, offset int32) ([]domain.Position, error)
	aggregateFn    func(ctx context.Context, workspaceID, productID uuid.UUID, query, region string, dateFrom, dateTo time.Time) (*domain.PositionAggregate, error)
}

func (m *mockPositionService) CreateTrackingTarget(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreatePositionTrackingTargetInput) (*domain.PositionTrackingTarget, error) {
	return m.createTargetFn(ctx, actorID, workspaceID, input)
}

func (m *mockPositionService) ListTrackingTargets(ctx context.Context, workspaceID uuid.UUID, filter service.PositionTargetListFilter, limit, offset int32) ([]domain.PositionTrackingTarget, error) {
	return m.listTargetsFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockPositionService) Create(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreatePositionInput) (*domain.Position, error) {
	return m.createFn(ctx, actorID, workspaceID, input)
}

func (m *mockPositionService) List(ctx context.Context, workspaceID uuid.UUID, filter service.PositionListFilter, limit, offset int32) ([]domain.Position, error) {
	return m.listFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockPositionService) Aggregate(ctx context.Context, workspaceID, productID uuid.UUID, query, region string, dateFrom, dateTo time.Time) (*domain.PositionAggregate, error) {
	return m.aggregateFn(ctx, workspaceID, productID, query, region, dateFrom, dateTo)
}

func decodePosition(t *testing.T, data interface{}) dto.PositionResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.PositionResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestPositionCreate_Success(t *testing.T) {
	actorID := uuid.New()
	workspaceID := uuid.New()
	productID := uuid.New()
	now := time.Now().UTC()
	mock := &mockPositionService{
		createFn: func(_ context.Context, aID, wsID uuid.UUID, input service.CreatePositionInput) (*domain.Position, error) {
			assert.Equal(t, actorID, aID)
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, productID, input.ProductID)
			assert.Equal(t, "iphone", input.Query)
			return &domain.Position{
				ID:          uuid.New(),
				WorkspaceID: workspaceID,
				ProductID:   productID,
				Query:       input.Query,
				Region:      input.Region,
				Position:    input.Position,
				Page:        input.Page,
				Source:      input.Source,
				CheckedAt:   now,
				CreatedAt:   now,
			}, nil
		},
		listFn: func(context.Context, uuid.UUID, service.PositionListFilter, int32, int32) ([]domain.Position, error) {
			return nil, nil
		},
		createTargetFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreatePositionTrackingTargetInput) (*domain.PositionTrackingTarget, error) {
			return nil, nil
		},
		listTargetsFn: func(context.Context, uuid.UUID, service.PositionTargetListFilter, int32, int32) ([]domain.PositionTrackingTarget, error) {
			return nil, nil
		},
		aggregateFn: func(context.Context, uuid.UUID, uuid.UUID, string, string, time.Time, time.Time) (*domain.PositionAggregate, error) {
			return nil, nil
		},
	}
	h := NewPositionHandler(mock)

	body := jsonBody(t, dto.CreatePositionRequest{
		ProductID: productID,
		Query:     "iphone",
		Region:    "msk",
		Position:  3,
		Page:      1,
		Source:    "manual",
	})
	req := httptest.NewRequest(http.MethodPost, "/positions", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, actorID)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeEnvelope(t, rec)
	item := decodePosition(t, resp.Data)
	assert.Equal(t, productID, item.ProductID)
	assert.Equal(t, "msk", item.Region)
}

func TestPositionCreate_ValidationError(t *testing.T) {
	mock := &mockPositionService{
		createTargetFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreatePositionTrackingTargetInput) (*domain.PositionTrackingTarget, error) {
			return nil, nil
		},
		listTargetsFn: func(context.Context, uuid.UUID, service.PositionTargetListFilter, int32, int32) ([]domain.PositionTrackingTarget, error) {
			return nil, nil
		},
		createFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreatePositionInput) (*domain.Position, error) {
			t.Fatal("create should not be called")
			return nil, nil
		},
		listFn: func(context.Context, uuid.UUID, service.PositionListFilter, int32, int32) ([]domain.Position, error) {
			return nil, nil
		},
		aggregateFn: func(context.Context, uuid.UUID, uuid.UUID, string, string, time.Time, time.Time) (*domain.PositionAggregate, error) {
			return nil, nil
		},
	}
	h := NewPositionHandler(mock)

	body := jsonBody(t, dto.CreatePositionRequest{})
	req := httptest.NewRequest(http.MethodPost, "/positions", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New())
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
