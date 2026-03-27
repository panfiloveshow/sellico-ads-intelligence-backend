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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type mockPhraseService struct {
	getFn      func(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.Phrase, error)
	getStatsFn func(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.PhraseStat, error)
	listBidsFn func(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.BidSnapshot, error)
}

func (m *mockPhraseService) Get(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.Phrase, error) {
	return m.getFn(ctx, workspaceID, phraseID)
}

func (m *mockPhraseService) GetStats(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.PhraseStat, error) {
	return m.getStatsFn(ctx, workspaceID, phraseID, dateFrom, dateTo, limit, offset)
}

func (m *mockPhraseService) ListBids(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.BidSnapshot, error) {
	return m.listBidsFn(ctx, workspaceID, phraseID, dateFrom, dateTo, limit, offset)
}

func decodePhrase(t *testing.T, data interface{}) dto.PhraseResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.PhraseResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestPhraseGet_Success(t *testing.T) {
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now()
	mock := &mockPhraseService{
		getFn: func(_ context.Context, wsID, id uuid.UUID) (*domain.Phrase, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, phraseID, id)
			return &domain.Phrase{
				ID:          phraseID,
				CampaignID:  uuid.New(),
				WorkspaceID: workspaceID,
				WBClusterID: 123,
				Keyword:     "iphone case",
				CreatedAt:   now,
				UpdatedAt:   now,
			}, nil
		},
		getStatsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.PhraseStat, error) {
			return nil, nil
		},
		listBidsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
	}
	h := NewPhraseHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/phrases/"+phraseID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", phraseID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	phrase := decodePhrase(t, resp.Data)
	assert.Equal(t, "iphone case", phrase.Keyword)
}

func TestPhraseGetStats_Success(t *testing.T) {
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now()
	mock := &mockPhraseService{
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Phrase, error) { return nil, nil },
		getStatsFn: func(_ context.Context, wsID, id uuid.UUID, _, _ time.Time, _, _ int32) ([]domain.PhraseStat, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, phraseID, id)
			return []domain.PhraseStat{{
				ID:          uuid.New(),
				PhraseID:    phraseID,
				Date:        now,
				Impressions: 100,
				Clicks:      5,
				Spend:       250,
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, nil
		},
		listBidsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
	}
	h := NewPhraseHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/phrases/"+phraseID.String()+"/stats", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", phraseID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.GetStats(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
}

func TestPhraseListBids_InvalidID(t *testing.T) {
	h := NewPhraseHandler(&mockPhraseService{})
	req := httptest.NewRequest(http.MethodGet, "/phrases/bad-id/bids", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "bad-id")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.ListBids(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
