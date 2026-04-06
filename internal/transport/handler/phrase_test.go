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

type mockPhraseService struct {
	listFn                func(ctx context.Context, workspaceID uuid.UUID, filter service.PhraseListFilter, limit, offset int32) ([]domain.Phrase, error)
	getFn                 func(ctx context.Context, workspaceID, phraseID uuid.UUID) (*domain.Phrase, error)
	getStatsFn            func(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.PhraseStat, error)
	listBidsFn            func(ctx context.Context, workspaceID, phraseID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.BidSnapshot, error)
	listRecommendationsFn func(ctx context.Context, workspaceID, phraseID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

func (m *mockPhraseService) List(ctx context.Context, workspaceID uuid.UUID, filter service.PhraseListFilter, limit, offset int32) ([]domain.Phrase, error) {
	return m.listFn(ctx, workspaceID, filter, limit, offset)
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

func (m *mockPhraseService) ListRecommendations(ctx context.Context, workspaceID, phraseID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	return m.listRecommendationsFn(ctx, workspaceID, phraseID, filter, limit, offset)
}

func decodePhrase(t *testing.T, data interface{}) dto.PhraseResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.PhraseResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestPhraseList_Success(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now()
	mock := &mockPhraseService{
		listFn: func(_ context.Context, wsID uuid.UUID, filter service.PhraseListFilter, limit, offset int32) ([]domain.Phrase, error) {
			assert.Equal(t, workspaceID, wsID)
			require.NotNil(t, filter.CampaignID)
			assert.Equal(t, campaignID, *filter.CampaignID)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Phrase{{
				ID:          uuid.New(),
				CampaignID:  uuid.New(),
				WorkspaceID: workspaceID,
				WBClusterID: 123,
				Keyword:     "iphone case",
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Phrase, error) { return nil, nil },
		getStatsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.PhraseStat, error) {
			return nil, nil
		},
		listBidsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		listRecommendationsFn: func(context.Context, uuid.UUID, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
			return nil, nil
		},
	}
	h := NewPhraseHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/phrases?campaign_id="+campaignID.String(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	phrase := decodePhrase(t, items[0])
	assert.Equal(t, "iphone case", phrase.Keyword)
}

func TestPhraseList_InvalidCampaignID(t *testing.T) {
	h := NewPhraseHandler(&mockPhraseService{
		listFn: func(context.Context, uuid.UUID, service.PhraseListFilter, int32, int32) ([]domain.Phrase, error) {
			t.Fatal("list should not be called")
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Phrase, error) { return nil, nil },
		getStatsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.PhraseStat, error) {
			return nil, nil
		},
		listBidsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		listRecommendationsFn: func(context.Context, uuid.UUID, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/phrases?campaign_id=bad-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPhraseGet_Success(t *testing.T) {
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now()
	mock := &mockPhraseService{
		listFn: func(context.Context, uuid.UUID, service.PhraseListFilter, int32, int32) ([]domain.Phrase, error) {
			return nil, nil
		},
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
		listRecommendationsFn: func(context.Context, uuid.UUID, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
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
		listFn: func(context.Context, uuid.UUID, service.PhraseListFilter, int32, int32) ([]domain.Phrase, error) {
			return nil, nil
		},
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
		listRecommendationsFn: func(context.Context, uuid.UUID, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
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
	h := NewPhraseHandler(&mockPhraseService{
		listFn: func(context.Context, uuid.UUID, service.PhraseListFilter, int32, int32) ([]domain.Phrase, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Phrase, error) { return nil, nil },
		getStatsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.PhraseStat, error) {
			return nil, nil
		},
		listBidsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		listRecommendationsFn: func(context.Context, uuid.UUID, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
			return nil, nil
		},
	})
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

func TestPhraseListRecommendations_Success(t *testing.T) {
	workspaceID := uuid.New()
	phraseID := uuid.New()
	now := time.Now().UTC()
	h := NewPhraseHandler(&mockPhraseService{
		listFn: func(context.Context, uuid.UUID, service.PhraseListFilter, int32, int32) ([]domain.Phrase, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Phrase, error) { return nil, nil },
		getStatsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.PhraseStat, error) {
			return nil, nil
		},
		listBidsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		listRecommendationsFn: func(_ context.Context, wsID, id uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, phraseID, id)
			assert.Equal(t, domain.RecommendationStatusActive, filter.Status)
			assert.Equal(t, domain.RecommendationTypeBidAdjustment, filter.Type)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Recommendation{{
				ID:          uuid.New(),
				WorkspaceID: workspaceID,
				PhraseID:    &phraseID,
				Title:       "Competitive bid opportunity",
				Description: "desc",
				Type:        domain.RecommendationTypeBidAdjustment,
				Severity:    domain.SeverityMedium,
				Confidence:  0.88,
				Status:      domain.RecommendationStatusActive,
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/phrases/"+phraseID.String()+"/recommendations?status=active&type=bid_adjustment", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", phraseID.String())
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.ListRecommendations(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	recommendation := decodeRecommendation(t, items[0])
	require.NotNil(t, recommendation.PhraseID)
	assert.Equal(t, phraseID, *recommendation.PhraseID)
	assert.Equal(t, domain.RecommendationTypeBidAdjustment, recommendation.Type)
}

func TestPhraseListRecommendations_InvalidPhraseID(t *testing.T) {
	h := NewPhraseHandler(&mockPhraseService{
		listFn: func(context.Context, uuid.UUID, service.PhraseListFilter, int32, int32) ([]domain.Phrase, error) {
			return nil, nil
		},
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Phrase, error) { return nil, nil },
		getStatsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.PhraseStat, error) {
			return nil, nil
		},
		listBidsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time, int32, int32) ([]domain.BidSnapshot, error) {
			return nil, nil
		},
		listRecommendationsFn: func(context.Context, uuid.UUID, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
			t.Fatal("list recommendations should not be called")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/phrases/not-a-uuid/recommendations", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "not-a-uuid")
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.ListRecommendations(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
