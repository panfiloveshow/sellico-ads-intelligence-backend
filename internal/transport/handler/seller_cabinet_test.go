package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock ---

type mockSellerCabinetService struct {
	createFn        func(ctx context.Context, workspaceID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error)
	listFn          func(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, filter service.SellerCabinetListFilter, limit, offset int32) ([]domain.SellerCabinet, error)
	getFn           func(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*domain.SellerCabinet, error)
	listCampaignsFn func(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Campaign, error)
	listProductsFn  func(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Product, error)
	deleteFn        func(ctx context.Context, actorID uuid.UUID, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) error
	triggerSyncFn   func(ctx context.Context, actorID uuid.UUID, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*service.SyncTriggerResult, error)
}

func (m *mockSellerCabinetService) Create(ctx context.Context, workspaceID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error) {
	return m.createFn(ctx, workspaceID, name, apiToken)
}
func (m *mockSellerCabinetService) List(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, filter service.SellerCabinetListFilter, limit, offset int32) ([]domain.SellerCabinet, error) {
	return m.listFn(ctx, token, workspaceRef, workspaceID, filter, limit, offset)
}
func (m *mockSellerCabinetService) Get(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*domain.SellerCabinet, error) {
	return m.getFn(ctx, token, workspaceRef, workspaceID, cabinetRef)
}
func (m *mockSellerCabinetService) ListCampaigns(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Campaign, error) {
	return m.listCampaignsFn(ctx, token, workspaceRef, workspaceID, cabinetRef, limit, offset)
}
func (m *mockSellerCabinetService) ListProducts(ctx context.Context, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Product, error) {
	return m.listProductsFn(ctx, token, workspaceRef, workspaceID, cabinetRef, limit, offset)
}
func (m *mockSellerCabinetService) Delete(ctx context.Context, actorID uuid.UUID, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) error {
	return m.deleteFn(ctx, actorID, token, workspaceRef, workspaceID, cabinetRef)
}
func (m *mockSellerCabinetService) TriggerSellerCabinetSync(ctx context.Context, actorID uuid.UUID, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*service.SyncTriggerResult, error) {
	return m.triggerSyncFn(ctx, actorID, token, workspaceRef, workspaceID, cabinetRef)
}

// --- helpers ---

func decodeSellerCabinet(t *testing.T, data interface{}) dto.SellerCabinetResponse {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var sc dto.SellerCabinetResponse
	require.NoError(t, json.Unmarshal(b, &sc))
	return sc
}

func decodeSellerCabinetCampaign(t *testing.T, data interface{}) dto.CampaignResponse {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.CampaignResponse
	require.NoError(t, json.Unmarshal(b, &result))
	return result
}

func decodeSellerCabinetProduct(t *testing.T, data interface{}) dto.ProductResponse {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var result dto.ProductResponse
	require.NoError(t, json.Unmarshal(b, &result))
	return result
}

// --- Create tests ---

func TestCreateSellerCabinet_Success(t *testing.T) {
	now := time.Now()
	cabinetID := uuid.New()
	workspaceID := uuid.New()

	mock := &mockSellerCabinetService{
		createFn: func(_ context.Context, wsID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, "My Cabinet", name)
			assert.Equal(t, "wb-token-123", apiToken)
			return &domain.SellerCabinet{
				ID:             cabinetID,
				WorkspaceID:    workspaceID,
				Name:           "My Cabinet",
				EncryptedToken: "encrypted-secret-value",
				Status:         "active",
				CreatedAt:      now,
				UpdatedAt:      now,
			}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	body := jsonBody(t, dto.CreateSellerCabinetRequest{Name: "My Cabinet", APIToken: "wb-token-123"})
	req := httptest.NewRequest(http.MethodPost, "/seller-cabinets", body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	sc := decodeSellerCabinet(t, resp.Data)
	assert.Equal(t, cabinetID.String(), sc.ID)
	assert.Equal(t, workspaceID, sc.WorkspaceID)
	assert.Equal(t, "My Cabinet", sc.Name)
	assert.Equal(t, "active", sc.Status)

	// Security: encrypted_token must NOT appear in response
	assert.NotContains(t, rec.Body.String(), "encrypted_token")
}

func TestCreateSellerCabinet_ValidationError(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{})

	body := jsonBody(t, dto.CreateSellerCabinetRequest{})
	req := httptest.NewRequest(http.MethodPost, "/seller-cabinets", body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.NotEmpty(t, resp.Errors)
	fields := make(map[string]bool)
	for _, e := range resp.Errors {
		fields[e.Field] = true
	}
	assert.True(t, fields["name"], "expected validation error for name")
	assert.True(t, fields["api_token"], "expected validation error for api_token")
}

func TestCreateSellerCabinet_InvalidToken(t *testing.T) {
	mock := &mockSellerCabinetService{
		createFn: func(_ context.Context, _ uuid.UUID, _, _ string) (*domain.SellerCabinet, error) {
			return nil, apperror.New(apperror.ErrValidation, "invalid WB API token")
		},
	}
	h := NewSellerCabinetHandler(mock)

	body := jsonBody(t, dto.CreateSellerCabinetRequest{Name: "Cabinet", APIToken: "bad-token"})
	req := httptest.NewRequest(http.MethodPost, "/seller-cabinets", body)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "VALIDATION_ERROR", resp.Errors[0].Code)
}

func TestCreateSellerCabinet_NoWorkspace(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{})

	body := jsonBody(t, dto.CreateSellerCabinetRequest{Name: "Cabinet", APIToken: "token"})
	req := httptest.NewRequest(http.MethodPost, "/seller-cabinets", body)
	// No workspace_id in context
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "VALIDATION_ERROR", resp.Errors[0].Code)
}

// --- List tests ---

func TestListSellerCabinets_Success(t *testing.T) {
	now := time.Now()
	workspaceID := uuid.New()
	sc1 := domain.SellerCabinet{ID: uuid.New(), WorkspaceID: workspaceID, Name: "Cabinet 1", EncryptedToken: "secret1", Status: "active", CreatedAt: now, UpdatedAt: now}
	sc2 := domain.SellerCabinet{ID: uuid.New(), WorkspaceID: workspaceID, Name: "Cabinet 2", EncryptedToken: "secret2", Status: "active", CreatedAt: now, UpdatedAt: now}

	mock := &mockSellerCabinetService{
		listFn: func(_ context.Context, _ string, _ string, wsID uuid.UUID, filter service.SellerCabinetListFilter, limit, offset int32) ([]domain.SellerCabinet, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, "active", filter.Status)
			return []domain.SellerCabinet{sc1, sc2}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets?status=active", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	assert.NotNil(t, resp.Meta)

	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	assert.Len(t, items, 2)

	// Security: no encrypted_token in response
	assert.NotContains(t, rec.Body.String(), "encrypted_token")
}

func TestListSellerCabinets_Empty(t *testing.T) {
	mock := &mockSellerCabinetService{
		listFn: func(_ context.Context, _ string, _ string, _ uuid.UUID, filter service.SellerCabinetListFilter, _, _ int32) ([]domain.SellerCabinet, error) {
			assert.Equal(t, "", filter.Status)
			return []domain.SellerCabinet{}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)

	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	assert.Len(t, items, 0)
}

func TestListSellerCabinets_NoEncryptedToken(t *testing.T) {
	now := time.Now()
	workspaceID := uuid.New()

	mock := &mockSellerCabinetService{
		listFn: func(_ context.Context, _ string, _ string, _ uuid.UUID, filter service.SellerCabinetListFilter, _, _ int32) ([]domain.SellerCabinet, error) {
			assert.Equal(t, "", filter.Status)
			return []domain.SellerCabinet{
				{
					ID:             uuid.New(),
					WorkspaceID:    workspaceID,
					Name:           "Secret Cabinet",
					EncryptedToken: "super-secret-encrypted-token-value",
					Status:         "active",
					CreatedAt:      now,
					UpdatedAt:      now,
				},
			}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Critical security assertion: the raw JSON must NOT contain "encrypted_token" anywhere
	rawJSON := rec.Body.String()
	assert.False(t, strings.Contains(rawJSON, "encrypted_token"),
		"response JSON must not contain encrypted_token field (security requirement 4.4, 19.5)")
	assert.False(t, strings.Contains(rawJSON, "super-secret-encrypted-token-value"),
		"response JSON must not contain the actual encrypted token value")
}

// --- Get tests ---

func TestGetSellerCabinet_Success(t *testing.T) {
	now := time.Now()
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	mock := &mockSellerCabinetService{
		getFn: func(_ context.Context, _ string, _ string, wsID uuid.UUID, cabinetRef string) (*domain.SellerCabinet, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, cabinetID.String(), cabinetRef)
			return &domain.SellerCabinet{
				ID:             cabinetID,
				WorkspaceID:    workspaceID,
				Name:           "My Cabinet",
				EncryptedToken: "encrypted-value",
				Status:         "active",
				CreatedAt:      now,
				UpdatedAt:      now,
			}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/"+cabinetID.String(), nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	sc := decodeSellerCabinet(t, resp.Data)
	assert.Equal(t, cabinetID.String(), sc.ID)
	assert.Equal(t, "My Cabinet", sc.Name)
	assert.NotContains(t, rec.Body.String(), "encrypted_token")
}

func TestGetSellerCabinet_NotFound(t *testing.T) {
	mock := &mockSellerCabinetService{
		getFn: func(_ context.Context, _ string, _ string, _ uuid.UUID, _ string) (*domain.SellerCabinet, error) {
			return nil, apperror.New(apperror.ErrNotFound, "cabinet not found")
		},
	}
	h := NewSellerCabinetHandler(mock)
	cabinetID := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/"+cabinetID.String(), nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "NOT_FOUND", resp.Errors[0].Code)
}

func TestGetSellerCabinet_ExternalIntegrationID(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{
		getFn: func(_ context.Context, _ string, _ string, _ uuid.UUID, cabinetRef string) (*domain.SellerCabinet, error) {
			assert.Equal(t, "wb-integration-1", cabinetRef)
			return nil, apperror.New(apperror.ErrNotFound, "cabinet not found")
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/wb-integration-1", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "wb-integration-1")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListSellerCabinetCampaigns_Success(t *testing.T) {
	now := time.Now()
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	mock := &mockSellerCabinetService{
		listCampaignsFn: func(_ context.Context, _ string, _ string, wsID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Campaign, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, cabinetID.String(), cabinetRef)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			return []domain.Campaign{{
				ID:              uuid.New(),
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBCampaignID:    12345,
				Name:            "Brand Search",
				Status:          "active",
				CampaignType:    1,
				BidType:         "auto",
				PaymentType:     "cpm",
				CreatedAt:       now,
				UpdatedAt:       now,
			}}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/"+cabinetID.String()+"/campaigns", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.ListCampaigns(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	campaign := decodeSellerCabinetCampaign(t, items[0])
	assert.Equal(t, "Brand Search", campaign.Name)
	assert.Equal(t, cabinetID, campaign.SellerCabinetID)
}

func TestListSellerCabinetCampaigns_ExternalIntegrationID(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{
		listCampaignsFn: func(_ context.Context, _ string, _ string, _ uuid.UUID, cabinetRef string, _, _ int32) ([]domain.Campaign, error) {
			assert.Equal(t, "wb-integration-1", cabinetRef)
			return nil, apperror.New(apperror.ErrNotFound, "cabinet not found")
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/wb-integration-1/campaigns", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "wb-integration-1")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.ListCampaigns(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListSellerCabinetProducts_Success(t *testing.T) {
	now := time.Now()
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	mock := &mockSellerCabinetService{
		listProductsFn: func(_ context.Context, _ string, _ string, wsID uuid.UUID, cabinetRef string, limit, offset int32) ([]domain.Product, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, cabinetID.String(), cabinetRef)
			assert.Equal(t, int32(20), limit)
			assert.Equal(t, int32(0), offset)
			price := int64(1999)
			return []domain.Product{{
				ID:              uuid.New(),
				WorkspaceID:     workspaceID,
				SellerCabinetID: cabinetID,
				WBProductID:     777,
				Title:           "Phone Case",
				Price:           &price,
				CreatedAt:       now,
				UpdatedAt:       now,
			}}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/"+cabinetID.String()+"/products", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.ListProducts(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	items, ok := resp.Data.([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	product := decodeSellerCabinetProduct(t, items[0])
	assert.Equal(t, "Phone Case", product.Title)
	assert.Equal(t, cabinetID, product.SellerCabinetID)
}

func TestListSellerCabinetProducts_ExternalIntegrationID(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{
		listProductsFn: func(_ context.Context, _ string, _ string, _ uuid.UUID, cabinetRef string, _, _ int32) ([]domain.Product, error) {
			assert.Equal(t, "wb-integration-1", cabinetRef)
			return nil, apperror.New(apperror.ErrNotFound, "cabinet not found")
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/wb-integration-1/products", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "wb-integration-1")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.ListProducts(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Delete tests ---

func TestDeleteSellerCabinet_Success(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()
	cabinetID := uuid.New()

	mock := &mockSellerCabinetService{
		deleteFn: func(_ context.Context, aID uuid.UUID, _ string, _ string, wsID uuid.UUID, cabinetRef string) error {
			assert.Equal(t, userID, aID)
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, cabinetID.String(), cabinetRef)
			return nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodDelete, "/seller-cabinets/"+cabinetID.String(), nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
}

func TestDeleteSellerCabinet_NotFound(t *testing.T) {
	mock := &mockSellerCabinetService{
		deleteFn: func(_ context.Context, _ uuid.UUID, _ string, _ string, _ uuid.UUID, _ string) error {
			return apperror.New(apperror.ErrNotFound, "cabinet not found")
		},
	}
	h := NewSellerCabinetHandler(mock)
	cabinetID := uuid.New()

	req := httptest.NewRequest(http.MethodDelete, "/seller-cabinets/"+cabinetID.String(), nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	ctx = context.WithValue(ctx, middleware.UserIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "NOT_FOUND", resp.Errors[0].Code)
}

func TestDeleteSellerCabinet_NoAuth(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{})
	cabinetID := uuid.New()

	req := httptest.NewRequest(http.MethodDelete, "/seller-cabinets/"+cabinetID.String(), nil)
	// workspace_id present but no user_id
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "UNAUTHORIZED", resp.Errors[0].Code)
}

func TestTriggerSellerCabinetSync_Success(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()
	cabinetID := uuid.New()

	mock := &mockSellerCabinetService{
		triggerSyncFn: func(_ context.Context, actualActorID uuid.UUID, _ string, _ string, actualWorkspaceID uuid.UUID, cabinetRef string) (*service.SyncTriggerResult, error) {
			assert.Equal(t, userID, actualActorID)
			assert.Equal(t, workspaceID, actualWorkspaceID)
			assert.Equal(t, cabinetID.String(), cabinetRef)
			return &service.SyncTriggerResult{
				TaskType:    "wb:sync_workspace",
				Status:      "enqueued",
				WorkspaceID: workspaceID,
				CabinetID:   cabinetID,
			}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodPost, "/seller-cabinets/"+cabinetID.String()+"/sync", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.TriggerSync(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	assert.Contains(t, rec.Body.String(), "wb:sync_workspace")
	assert.Contains(t, rec.Body.String(), "enqueued")
}

func TestTriggerSellerCabinetSync_ExternalIntegrationID(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{
		triggerSyncFn: func(_ context.Context, _ uuid.UUID, _ string, _ string, _ uuid.UUID, cabinetRef string) (*service.SyncTriggerResult, error) {
			assert.Equal(t, "wb-integration-1", cabinetRef)
			return nil, apperror.New(apperror.ErrNotFound, "cabinet not found")
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/seller-cabinets/wb-integration-1/sync", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	ctx = context.WithValue(ctx, middleware.UserIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "wb-integration-1")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.TriggerSync(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTriggerSellerCabinetSync_NoAuth(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{})
	cabinetID := uuid.New()

	req := httptest.NewRequest(http.MethodPost, "/seller-cabinets/"+cabinetID.String()+"/sync", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", cabinetID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.TriggerSync(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "UNAUTHORIZED", resp.Errors[0].Code)
}
