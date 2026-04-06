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

type mockProductService struct {
	listFn                func(ctx context.Context, workspaceID uuid.UUID, filter service.ProductListFilter, limit, offset int32) ([]domain.Product, error)
	getFn                 func(ctx context.Context, workspaceID, productID uuid.UUID) (*domain.Product, error)
	listPositionsFn       func(ctx context.Context, workspaceID, productID uuid.UUID, limit, offset int32) ([]domain.Position, error)
	listRecommendationsFn func(ctx context.Context, workspaceID, productID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

func (m *mockProductService) List(ctx context.Context, workspaceID uuid.UUID, filter service.ProductListFilter, limit, offset int32) ([]domain.Product, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, workspaceID, filter, limit, offset)
}

func (m *mockProductService) Get(ctx context.Context, workspaceID, productID uuid.UUID) (*domain.Product, error) {
	if m.getFn == nil {
		return nil, nil
	}
	return m.getFn(ctx, workspaceID, productID)
}

func (m *mockProductService) ListPositions(ctx context.Context, workspaceID, productID uuid.UUID, limit, offset int32) ([]domain.Position, error) {
	if m.listPositionsFn == nil {
		return nil, nil
	}
	return m.listPositionsFn(ctx, workspaceID, productID, limit, offset)
}

func (m *mockProductService) ListRecommendations(ctx context.Context, workspaceID, productID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	if m.listRecommendationsFn == nil {
		return nil, nil
	}
	return m.listRecommendationsFn(ctx, workspaceID, productID, filter, limit, offset)
}

func decodeProduct(t *testing.T, data interface{}) dto.ProductResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.ProductResponse
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestProductList_Success(t *testing.T) {
	workspaceID := uuid.New()
	sellerCabinetID := uuid.New()
	now := time.Now().UTC()
	h := NewProductHandler(&mockProductService{
		listFn: func(_ context.Context, wsID uuid.UUID, filter service.ProductListFilter, limit, offset int32) ([]domain.Product, error) {
			assert.Equal(t, workspaceID, wsID)
			require.NotNil(t, filter.SellerCabinetID)
			assert.Equal(t, sellerCabinetID, *filter.SellerCabinetID)
			assert.Equal(t, "case", filter.Title)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			price := int64(1999)
			return []domain.Product{{
				ID:              uuid.New(),
				WorkspaceID:     workspaceID,
				SellerCabinetID: sellerCabinetID,
				WBProductID:     1001,
				Title:           "Phone Case",
				Price:           &price,
				CreatedAt:       now,
				UpdatedAt:       now,
			}}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/products?seller_cabinet_id="+sellerCabinetID.String()+"&title=case", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	product := decodeProduct(t, items[0])
	assert.Equal(t, sellerCabinetID, product.SellerCabinetID)
	assert.Equal(t, "Phone Case", product.Title)
}

func TestProductList_InvalidSellerCabinetID(t *testing.T) {
	h := NewProductHandler(&mockProductService{
		listFn: func(context.Context, uuid.UUID, service.ProductListFilter, int32, int32) ([]domain.Product, error) {
			t.Fatal("list should not be called")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/products?seller_cabinet_id=bad-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestProductListRecommendations_Success(t *testing.T) {
	workspaceID := uuid.New()
	productID := uuid.New()
	now := time.Now().UTC()
	h := NewProductHandler(&mockProductService{
		listRecommendationsFn: func(_ context.Context, wsID, id uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, productID, id)
			assert.Equal(t, domain.RecommendationStatusActive, filter.Status)
			assert.Equal(t, domain.RecommendationTypePositionDrop, filter.Type)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Recommendation{{
				ID:          uuid.New(),
				WorkspaceID: workspaceID,
				ProductID:   &productID,
				Title:       "Position dropped in key region",
				Description: "desc",
				Type:        domain.RecommendationTypePositionDrop,
				Severity:    domain.SeverityHigh,
				Confidence:  0.91,
				Status:      domain.RecommendationStatusActive,
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/products/"+productID.String()+"/recommendations?status=active&type=position_drop", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", productID.String())
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
	require.NotNil(t, recommendation.ProductID)
	assert.Equal(t, productID, *recommendation.ProductID)
	assert.Equal(t, domain.RecommendationTypePositionDrop, recommendation.Type)
}

func TestProductListRecommendations_InvalidProductID(t *testing.T) {
	h := NewProductHandler(&mockProductService{
		listRecommendationsFn: func(context.Context, uuid.UUID, uuid.UUID, service.RecommendationListFilter, int32, int32) ([]domain.Recommendation, error) {
			t.Fatal("list recommendations should not be called")
			return nil, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/products/not-a-uuid/recommendations", nil)
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

func TestProductListRecommendations_NoWorkspace(t *testing.T) {
	h := NewProductHandler(&mockProductService{})

	req := httptest.NewRequest(http.MethodGet, "/products/"+uuid.New().String()+"/recommendations", nil)
	rec := httptest.NewRecorder()

	h.ListRecommendations(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
