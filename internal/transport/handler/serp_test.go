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

type mockSERPService struct {
	createFn    func(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreateSERPSnapshotInput) (*domain.SERPSnapshot, []domain.SERPResultItem, error)
	listFn      func(ctx context.Context, workspaceID uuid.UUID, filter service.SERPListFilter, limit, offset int32) ([]domain.SERPSnapshot, error)
	getFn       func(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPSnapshot, error)
	compareFn   func(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPComparison, error)
	listItemsFn func(ctx context.Context, snapshotID uuid.UUID) ([]domain.SERPResultItem, error)
}

func (m *mockSERPService) Create(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreateSERPSnapshotInput) (*domain.SERPSnapshot, []domain.SERPResultItem, error) {
	return m.createFn(ctx, actorID, workspaceID, input)
}

func (m *mockSERPService) List(ctx context.Context, workspaceID uuid.UUID, filter service.SERPListFilter, limit, offset int32) ([]domain.SERPSnapshot, error) {
	return m.listFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockSERPService) Get(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPSnapshot, error) {
	return m.getFn(ctx, workspaceID, snapshotID)
}

func (m *mockSERPService) Compare(ctx context.Context, workspaceID, snapshotID uuid.UUID) (*domain.SERPComparison, error) {
	return m.compareFn(ctx, workspaceID, snapshotID)
}

func (m *mockSERPService) ListItems(ctx context.Context, snapshotID uuid.UUID) ([]domain.SERPResultItem, error) {
	return m.listItemsFn(ctx, snapshotID)
}

func decodeSERPSnapshotDetail(t *testing.T, data interface{}) dto.SERPSnapshotDetailResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.SERPSnapshotDetailResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestSERPCreate_Success(t *testing.T) {
	actorID := uuid.New()
	workspaceID := uuid.New()
	now := time.Now().UTC()
	mock := &mockSERPService{
		createFn: func(_ context.Context, aID, wsID uuid.UUID, input service.CreateSERPSnapshotInput) (*domain.SERPSnapshot, []domain.SERPResultItem, error) {
			assert.Equal(t, actorID, aID)
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, "iphone", input.Query)
			require.Len(t, input.Items, 1)

			snapshotID := uuid.New()
			return &domain.SERPSnapshot{
					ID:           snapshotID,
					WorkspaceID:  workspaceID,
					Query:        input.Query,
					Region:       input.Region,
					TotalResults: input.TotalResults,
					ScannedAt:    now,
					CreatedAt:    now,
				}, []domain.SERPResultItem{{
					ID:          uuid.New(),
					SnapshotID:  snapshotID,
					Position:    1,
					WBProductID: 123,
					Title:       "Item",
					CreatedAt:   now,
				}}, nil
		},
		listFn: func(context.Context, uuid.UUID, service.SERPListFilter, int32, int32) ([]domain.SERPSnapshot, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.SERPSnapshot, error) {
			return nil, nil
		},
		compareFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.SERPComparison, error) {
			return nil, nil
		},
		listItemsFn: func(context.Context, uuid.UUID) ([]domain.SERPResultItem, error) {
			return nil, nil
		},
	}
	h := NewSERPHandler(mock)

	body := jsonBody(t, dto.CreateSERPSnapshotRequest{
		Query:        "iphone",
		Region:       "msk",
		TotalResults: 100,
		Items: []dto.CreateSERPResultItemRequest{{
			Position:    1,
			WBProductID: 123,
			Title:       "Item",
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/serp-snapshots", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, actorID)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeEnvelope(t, rec)
	item := decodeSERPSnapshotDetail(t, resp.Data)
	assert.Equal(t, "iphone", item.Query)
	require.Len(t, item.Items, 1)
	assert.Equal(t, int64(123), item.Items[0].WBProductID)
}

func TestSERPCreate_ValidationError(t *testing.T) {
	mock := &mockSERPService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateSERPSnapshotInput) (*domain.SERPSnapshot, []domain.SERPResultItem, error) {
			t.Fatal("create should not be called")
			return nil, nil, nil
		},
		listFn: func(context.Context, uuid.UUID, service.SERPListFilter, int32, int32) ([]domain.SERPSnapshot, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.SERPSnapshot, error) {
			return nil, nil
		},
		compareFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.SERPComparison, error) {
			return nil, nil
		},
		listItemsFn: func(context.Context, uuid.UUID) ([]domain.SERPResultItem, error) {
			return nil, nil
		},
	}
	h := NewSERPHandler(mock)

	body := jsonBody(t, dto.CreateSERPSnapshotRequest{})
	req := httptest.NewRequest(http.MethodPost, "/serp-snapshots", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New())
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
