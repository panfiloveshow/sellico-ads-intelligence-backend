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

type mockCampaignService struct {
	listFn                func(ctx context.Context, workspaceID uuid.UUID, filter service.CampaignListFilter, limit, offset int32) ([]domain.Campaign, error)
	getFn                 func(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.Campaign, error)
	getStatsFn            func(ctx context.Context, campaignID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.CampaignStat, error)
	listPhrasesFn         func(ctx context.Context, campaignID uuid.UUID, limit, offset int32) ([]domain.Phrase, error)
	listRecommendationsFn func(ctx context.Context, workspaceID, campaignID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

func (m *mockCampaignService) List(ctx context.Context, workspaceID uuid.UUID, filter service.CampaignListFilter, limit, offset int32) ([]domain.Campaign, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockCampaignService) Get(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.Campaign, error) {
	if m.getFn == nil {
		return nil, nil
	}
	return m.getFn(ctx, workspaceID, campaignID)
}

func (m *mockCampaignService) GetStats(ctx context.Context, campaignID uuid.UUID, dateFrom, dateTo time.Time, limit, offset int32) ([]domain.CampaignStat, error) {
	if m.getStatsFn == nil {
		return nil, nil
	}
	return m.getStatsFn(ctx, campaignID, dateFrom, dateTo, limit, offset)
}

func (m *mockCampaignService) ListPhrases(ctx context.Context, campaignID uuid.UUID, limit, offset int32) ([]domain.Phrase, error) {
	if m.listPhrasesFn == nil {
		return nil, nil
	}
	return m.listPhrasesFn(ctx, campaignID, limit, offset)
}

func (m *mockCampaignService) ListRecommendations(ctx context.Context, workspaceID, campaignID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	if m.listRecommendationsFn == nil {
		return nil, nil
	}
	return m.listRecommendationsFn(ctx, workspaceID, campaignID, filter, limit, offset)
}

func decodeCampaignRecommendation(t *testing.T, data interface{}) dto.RecommendationResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.RecommendationResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func decodeCampaign(t *testing.T, data interface{}) dto.CampaignResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.CampaignResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestCampaignList_Success(t *testing.T) {
	workspaceID := uuid.New()
	sellerCabinetID := uuid.New()
	now := time.Now().UTC()
	mock := &mockCampaignService{
		listFn: func(_ context.Context, wsID uuid.UUID, filter service.CampaignListFilter, limit, offset int32) ([]domain.Campaign, error) {
			assert.Equal(t, workspaceID, wsID)
			require.NotNil(t, filter.SellerCabinetID)
			assert.Equal(t, sellerCabinetID, *filter.SellerCabinetID)
			assert.Equal(t, "active", filter.Status)
			assert.Equal(t, "Campaign", filter.Name)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Campaign{{
				ID:              uuid.New(),
				WorkspaceID:     workspaceID,
				SellerCabinetID: sellerCabinetID,
				WBCampaignID:    42,
				Name:            "Campaign One",
				Status:          "active",
				CampaignType:    1,
				BidType:         "auto",
				PaymentType:     "cpm",
				CreatedAt:       now,
				UpdatedAt:       now,
			}}, nil
		},
	}
	h := NewCampaignHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/campaigns?seller_cabinet_id="+sellerCabinetID.String()+"&status=active&name=Campaign", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item := decodeCampaign(t, items[0])
	assert.Equal(t, sellerCabinetID, item.SellerCabinetID)
	assert.Equal(t, "Campaign One", item.Name)
}

func TestCampaignList_NameOnlyFilter(t *testing.T) {
	workspaceID := uuid.New()
	mock := &mockCampaignService{
		listFn: func(_ context.Context, wsID uuid.UUID, filter service.CampaignListFilter, limit, offset int32) ([]domain.Campaign, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Nil(t, filter.SellerCabinetID)
			assert.Equal(t, "", filter.Status)
			assert.Equal(t, "Search", filter.Name)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Campaign{}, nil
		},
	}
	h := NewCampaignHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/campaigns?name=Search", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCampaignList_InvalidSellerCabinetID(t *testing.T) {
	h := NewCampaignHandler(&mockCampaignService{
		listFn: func(context.Context, uuid.UUID, service.CampaignListFilter, int32, int32) ([]domain.Campaign, error) {
			t.Fatal("list should not be called")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/campaigns?seller_cabinet_id=bad-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCampaignListRecommendations_Success(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	now := time.Now().UTC()
	mock := &mockCampaignService{
		listRecommendationsFn: func(_ context.Context, wsID, cID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, campaignID, cID)
			assert.Equal(t, domain.RecommendationStatusActive, filter.Status)
			assert.Equal(t, domain.RecommendationTypeLowCTR, filter.Type)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Recommendation{{
				ID:          uuid.New(),
				WorkspaceID: workspaceID,
				CampaignID:  &campaignID,
				Title:       "High impressions with zero clicks",
				Description: "desc",
				Type:        domain.RecommendationTypeLowCTR,
				Severity:    domain.SeverityHigh,
				Confidence:  0.92,
				Status:      domain.RecommendationStatusActive,
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, nil
		},
	}
	h := NewCampaignHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/campaigns/"+campaignID.String()+"/recommendations?status=active&type=low_ctr", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", campaignID.String())
		return rctx
	}()))
	rec := httptest.NewRecorder()

	h.ListRecommendations(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item := decodeCampaignRecommendation(t, items[0])
	assert.Equal(t, campaignID, *item.CampaignID)
	assert.Equal(t, domain.RecommendationTypeLowCTR, item.Type)
}

func TestCampaignListRecommendations_InvalidCampaignID(t *testing.T) {
	h := NewCampaignHandler(&mockCampaignService{})

	req := httptest.NewRequest(http.MethodGet, "/campaigns/not-a-uuid/recommendations", nil)
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

func TestCampaignListRecommendations_NoWorkspace(t *testing.T) {
	h := NewCampaignHandler(&mockCampaignService{})

	req := httptest.NewRequest(http.MethodGet, "/campaigns/"+uuid.New().String()+"/recommendations", nil)
	rec := httptest.NewRecorder()

	h.ListRecommendations(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
