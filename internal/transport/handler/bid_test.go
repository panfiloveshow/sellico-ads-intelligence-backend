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

type mockBidService struct {
	createFn      func(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreateBidSnapshotInput) (*domain.BidSnapshot, error)
	listHistoryFn func(ctx context.Context, workspaceID uuid.UUID, filter service.BidListFilter, limit, offset int32) ([]domain.BidSnapshot, error)
	getEstimateFn func(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.BidSnapshot, error)
}

func (m *mockBidService) Create(ctx context.Context, actorID, workspaceID uuid.UUID, input service.CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
	return m.createFn(ctx, actorID, workspaceID, input)
}

func (m *mockBidService) ListHistory(ctx context.Context, workspaceID uuid.UUID, filter service.BidListFilter, limit, offset int32) ([]domain.BidSnapshot, error) {
	return m.listHistoryFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockBidService) GetEstimate(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.BidSnapshot, error) {
	return m.getEstimateFn(ctx, workspaceID, phraseID)
}

func decodeBidSnapshot(t *testing.T, data interface{}) dto.BidSnapshotResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.BidSnapshotResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func decodeBidEstimate(t *testing.T, data interface{}) dto.BidEstimateResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.BidEstimateResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestBidListHistory_Success(t *testing.T) {
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now().UTC()
	mock := &mockBidService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
			return nil, nil
		},
		listHistoryFn: func(_ context.Context, wsID uuid.UUID, filter service.BidListFilter, limit, offset int32) ([]domain.BidSnapshot, error) {
			assert.Equal(t, workspaceID, wsID)
			require.NotNil(t, filter.PhraseID)
			assert.Equal(t, phraseID, *filter.PhraseID)
			require.NotNil(t, filter.DateFrom)
			require.NotNil(t, filter.DateTo)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.BidSnapshot{{
				ID:             uuid.New(),
				PhraseID:       phraseID,
				WorkspaceID:    workspaceID,
				CompetitiveBid: 120,
				LeadershipBid:  180,
				CPMMin:         90,
				CapturedAt:     now,
				CreatedAt:      now,
			}}, nil
		},
		getEstimateFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.BidSnapshot, error) {
			return nil, nil
		},
	}
	h := NewBidHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/bids/history?phrase_id="+phraseID.String(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.ListHistory(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item := decodeBidSnapshot(t, items[0])
	assert.Equal(t, int64(120), item.CompetitiveBid)
}

func TestBidListHistory_InvalidPhraseID(t *testing.T) {
	mock := &mockBidService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
			return nil, nil
		},
		listHistoryFn: func(context.Context, uuid.UUID, service.BidListFilter, int32, int32) ([]domain.BidSnapshot, error) {
			t.Fatal("list history should not be called")
			return nil, nil
		},
		getEstimateFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.BidSnapshot, error) {
			return nil, nil
		},
	}
	h := NewBidHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/bids/history?phrase_id=bad-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	h.ListHistory(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBidGetEstimate_Success(t *testing.T) {
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now().UTC()
	mock := &mockBidService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
			return nil, nil
		},
		listHistoryFn: func(context.Context, uuid.UUID, service.BidListFilter, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		getEstimateFn: func(_ context.Context, wsID, pID uuid.UUID) (*domain.BidSnapshot, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, phraseID, pID)
			return &domain.BidSnapshot{
				PhraseID:       phraseID,
				WorkspaceID:    workspaceID,
				CompetitiveBid: 150,
				LeadershipBid:  210,
				CPMMin:         100,
				CapturedAt:     now,
			}, nil
		},
	}
	h := NewBidHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/bids/estimates?phrase_id="+phraseID.String(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.GetEstimate(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	item := decodeBidEstimate(t, resp.Data)
	assert.Equal(t, int64(150), item.CompetitiveBid)
	assert.Equal(t, phraseID, item.PhraseID)
}

func TestBidGetEstimate_MissingPhraseID(t *testing.T) {
	mock := &mockBidService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
			return nil, nil
		},
		listHistoryFn: func(context.Context, uuid.UUID, service.BidListFilter, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		getEstimateFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.BidSnapshot, error) {
			t.Fatal("get estimate should not be called")
			return nil, nil
		},
	}
	h := NewBidHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/bids/estimates", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	h.GetEstimate(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBidCreate_Success(t *testing.T) {
	actorID := uuid.New()
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now().UTC()
	mock := &mockBidService{
		createFn: func(_ context.Context, aID, wsID uuid.UUID, input service.CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
			assert.Equal(t, actorID, aID)
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, phraseID, input.PhraseID)
			assert.Equal(t, int64(120), input.CompetitiveBid)
			return &domain.BidSnapshot{
				ID:             uuid.New(),
				PhraseID:       phraseID,
				WorkspaceID:    workspaceID,
				CompetitiveBid: input.CompetitiveBid,
				LeadershipBid:  input.LeadershipBid,
				CPMMin:         input.CPMMin,
				CapturedAt:     now,
				CreatedAt:      now,
			}, nil
		},
		listHistoryFn: func(context.Context, uuid.UUID, service.BidListFilter, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		getEstimateFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.BidSnapshot, error) {
			return nil, nil
		},
	}
	h := NewBidHandler(mock)

	body := jsonBody(t, dto.CreateBidSnapshotRequest{
		PhraseID:       phraseID,
		CompetitiveBid: 120,
		LeadershipBid:  180,
		CPMMin:         90,
	})
	req := httptest.NewRequest(http.MethodPost, "/bids/history", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, actorID)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeEnvelope(t, rec)
	item := decodeBidSnapshot(t, resp.Data)
	assert.Equal(t, phraseID, item.PhraseID)
	assert.Equal(t, int64(180), item.LeadershipBid)
}

func TestBidCreate_ValidationError(t *testing.T) {
	mock := &mockBidService{
		createFn: func(context.Context, uuid.UUID, uuid.UUID, service.CreateBidSnapshotInput) (*domain.BidSnapshot, error) {
			t.Fatal("create should not be called")
			return nil, nil
		},
		listHistoryFn: func(context.Context, uuid.UUID, service.BidListFilter, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		getEstimateFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.BidSnapshot, error) {
			return nil, nil
		},
	}
	h := NewBidHandler(mock)

	body := jsonBody(t, dto.CreateBidSnapshotRequest{})
	req := httptest.NewRequest(http.MethodPost, "/bids/history", body)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New())
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
