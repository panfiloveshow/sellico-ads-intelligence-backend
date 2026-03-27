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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock ---

type mockSellerCabinetService struct {
	createFn func(ctx context.Context, workspaceID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error)
	listFn   func(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.SellerCabinet, error)
	getFn    func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error)
	deleteFn func(ctx context.Context, actorID, workspaceID, cabinetID uuid.UUID) error
}

func (m *mockSellerCabinetService) Create(ctx context.Context, workspaceID uuid.UUID, name, apiToken string) (*domain.SellerCabinet, error) {
	return m.createFn(ctx, workspaceID, name, apiToken)
}
func (m *mockSellerCabinetService) List(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.SellerCabinet, error) {
	return m.listFn(ctx, workspaceID, limit, offset)
}
func (m *mockSellerCabinetService) Get(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error) {
	return m.getFn(ctx, workspaceID, cabinetID)
}
func (m *mockSellerCabinetService) Delete(ctx context.Context, actorID, workspaceID, cabinetID uuid.UUID) error {
	return m.deleteFn(ctx, actorID, workspaceID, cabinetID)
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
	assert.Equal(t, cabinetID, sc.ID)
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
		listFn: func(_ context.Context, wsID uuid.UUID, limit, offset int32) ([]domain.SellerCabinet, error) {
			assert.Equal(t, workspaceID, wsID)
			return []domain.SellerCabinet{sc1, sc2}, nil
		},
	}
	h := NewSellerCabinetHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets", nil)
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
		listFn: func(_ context.Context, _ uuid.UUID, _, _ int32) ([]domain.SellerCabinet, error) {
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
		listFn: func(_ context.Context, _ uuid.UUID, _, _ int32) ([]domain.SellerCabinet, error) {
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
		getFn: func(_ context.Context, wsID, cID uuid.UUID) (*domain.SellerCabinet, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, cabinetID, cID)
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
	assert.Equal(t, cabinetID, sc.ID)
	assert.Equal(t, "My Cabinet", sc.Name)
	assert.NotContains(t, rec.Body.String(), "encrypted_token")
}

func TestGetSellerCabinet_NotFound(t *testing.T) {
	mock := &mockSellerCabinetService{
		getFn: func(_ context.Context, _, _ uuid.UUID) (*domain.SellerCabinet, error) {
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

func TestGetSellerCabinet_InvalidID(t *testing.T) {
	h := NewSellerCabinetHandler(&mockSellerCabinetService{})

	req := httptest.NewRequest(http.MethodGet, "/seller-cabinets/bad-uuid", nil)
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "bad-uuid")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "VALIDATION_ERROR", resp.Errors[0].Code)
}

// --- Delete tests ---

func TestDeleteSellerCabinet_Success(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()
	cabinetID := uuid.New()

	mock := &mockSellerCabinetService{
		deleteFn: func(_ context.Context, aID, wsID, cID uuid.UUID) error {
			assert.Equal(t, userID, aID)
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, cabinetID, cID)
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
		deleteFn: func(_ context.Context, _, _, _ uuid.UUID) error {
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
