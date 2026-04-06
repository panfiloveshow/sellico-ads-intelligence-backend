package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock ---

type mockAuthService struct {
	registerFn     func(ctx context.Context, email, password, name string) (*service.AuthTokens, error)
	loginFn        func(ctx context.Context, email, password string) (*service.AuthTokens, error)
	refreshTokenFn func(ctx context.Context, refreshToken string) (*service.AuthTokens, error)
	logoutFn       func(ctx context.Context, refreshToken string) error
	getMeFn        func(ctx context.Context, userID uuid.UUID) (*domain.User, error)
}

func (m *mockAuthService) Register(ctx context.Context, email, password, name string) (*service.AuthTokens, error) {
	return m.registerFn(ctx, email, password, name)
}
func (m *mockAuthService) Login(ctx context.Context, email, password string) (*service.AuthTokens, error) {
	return m.loginFn(ctx, email, password)
}
func (m *mockAuthService) RefreshToken(ctx context.Context, refreshToken string) (*service.AuthTokens, error) {
	return m.refreshTokenFn(ctx, refreshToken)
}
func (m *mockAuthService) Logout(ctx context.Context, refreshToken string) error {
	return m.logoutFn(ctx, refreshToken)
}
func (m *mockAuthService) GetMe(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	return m.getMeFn(ctx, userID)
}

// --- helpers ---

func jsonBody(t *testing.T, v interface{}) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(b)
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope.Response {
	t.Helper()
	var resp envelope.Response
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	return resp
}

func decodeTokens(t *testing.T, data interface{}) dto.AuthTokensResponse {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var tokens dto.AuthTokensResponse
	require.NoError(t, json.Unmarshal(b, &tokens))
	return tokens
}

func decodeUser(t *testing.T, data interface{}) dto.UserResponse {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var user dto.UserResponse
	require.NoError(t, json.Unmarshal(b, &user))
	return user
}

// --- Register tests ---

func TestRegister_Success(t *testing.T) {
	mock := &mockAuthService{
		registerFn: func(_ context.Context, email, password, name string) (*service.AuthTokens, error) {
			assert.Equal(t, "user@example.com", email)
			assert.Equal(t, "securepass1", password)
			assert.Equal(t, "Test User", name)
			return &service.AuthTokens{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
			}, nil
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.RegisterRequest{
		Email:    "user@example.com",
		Password: "securepass1",
		Name:     "Test User",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	tokens := decodeTokens(t, resp.Data)
	assert.Equal(t, "access-tok", tokens.AccessToken)
	assert.Equal(t, "refresh-tok", tokens.RefreshToken)
}

func TestRegister_DuplicateEmail(t *testing.T) {
	mock := &mockAuthService{
		registerFn: func(_ context.Context, _, _, _ string) (*service.AuthTokens, error) {
			return nil, apperror.New(apperror.ErrConflict, "email already registered")
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.RegisterRequest{
		Email:    "dup@example.com",
		Password: "securepass1",
		Name:     "Dup User",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "CONFLICT", resp.Errors[0].Code)
	assert.Contains(t, resp.Errors[0].Message, "email already registered")
}

func TestRegister_ValidationError_MissingFields(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	// Empty body — all fields missing.
	body := jsonBody(t, dto.RegisterRequest{})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.NotEmpty(t, resp.Errors)
	// Should have errors for email, password, name.
	fields := make(map[string]bool)
	for _, e := range resp.Errors {
		fields[e.Field] = true
	}
	assert.True(t, fields["email"], "expected validation error for email")
	assert.True(t, fields["password"], "expected validation error for password")
	assert.True(t, fields["name"], "expected validation error for name")
}

func TestRegister_ValidationError_ShortPassword(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	body := jsonBody(t, dto.RegisterRequest{
		Email:    "user@example.com",
		Password: "short",
		Name:     "User",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/register", body)
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	fields := make(map[string]bool)
	for _, e := range resp.Errors {
		fields[e.Field] = true
	}
	assert.True(t, fields["password"], "expected validation error for short password")
}

func TestRegister_InvalidJSON(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString("{bad json"))
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "VALIDATION_ERROR", resp.Errors[0].Code)
}

// --- Login tests ---

func TestLogin_Success(t *testing.T) {
	mock := &mockAuthService{
		loginFn: func(_ context.Context, email, password string) (*service.AuthTokens, error) {
			assert.Equal(t, "user@example.com", email)
			assert.Equal(t, "correctpass1", password)
			return &service.AuthTokens{
				AccessToken:  "login-access",
				RefreshToken: "login-refresh",
			}, nil
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.LoginRequest{
		Email:    "user@example.com",
		Password: "correctpass1",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	tokens := decodeTokens(t, resp.Data)
	assert.Equal(t, "login-access", tokens.AccessToken)
	assert.Equal(t, "login-refresh", tokens.RefreshToken)
}

func TestLogin_InvalidCredentials(t *testing.T) {
	mock := &mockAuthService{
		loginFn: func(_ context.Context, _, _ string) (*service.AuthTokens, error) {
			return nil, apperror.New(apperror.ErrUnauthorized, "invalid email or password")
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.LoginRequest{
		Email:    "user@example.com",
		Password: "wrongpass123",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "UNAUTHORIZED", resp.Errors[0].Code)
}

func TestLogin_ValidationError_MissingEmail(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	body := jsonBody(t, dto.LoginRequest{Password: "somepass"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.NotEmpty(t, resp.Errors)
}

func TestLogin_InvalidJSON(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString("not-json"))
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- Refresh tests ---

func TestRefresh_Success(t *testing.T) {
	mock := &mockAuthService{
		refreshTokenFn: func(_ context.Context, token string) (*service.AuthTokens, error) {
			assert.Equal(t, "old-refresh-token", token)
			return &service.AuthTokens{
				AccessToken:  "new-access",
				RefreshToken: "new-refresh",
			}, nil
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.RefreshTokenRequest{RefreshToken: "old-refresh-token"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", body)
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
	tokens := decodeTokens(t, resp.Data)
	assert.Equal(t, "new-access", tokens.AccessToken)
	assert.Equal(t, "new-refresh", tokens.RefreshToken)
}

func TestRefresh_InvalidToken(t *testing.T) {
	mock := &mockAuthService{
		refreshTokenFn: func(_ context.Context, _ string) (*service.AuthTokens, error) {
			return nil, apperror.New(apperror.ErrUnauthorized, "invalid or expired refresh token")
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.RefreshTokenRequest{RefreshToken: "bad-token"})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", body)
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "UNAUTHORIZED", resp.Errors[0].Code)
	assert.Contains(t, resp.Errors[0].Message, "invalid or expired refresh token")
}

func TestRefresh_ValidationError_MissingToken(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	body := jsonBody(t, dto.RefreshTokenRequest{})
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", body)
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.NotEmpty(t, resp.Errors)
}

// --- Logout tests ---

func TestLogout_Success(t *testing.T) {
	mock := &mockAuthService{
		logoutFn: func(_ context.Context, token string) error {
			assert.Equal(t, "refresh-to-revoke", token)
			return nil
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.RefreshTokenRequest{RefreshToken: "refresh-to-revoke"})
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", body)
	rec := httptest.NewRecorder()

	h.Logout(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
}

func TestLogout_ServiceError(t *testing.T) {
	mock := &mockAuthService{
		logoutFn: func(_ context.Context, _ string) error {
			return apperror.New(apperror.ErrInternal, "failed to revoke refresh token")
		},
	}
	h := NewAuthHandler(mock)

	body := jsonBody(t, dto.RefreshTokenRequest{RefreshToken: "some-token"})
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", body)
	rec := httptest.NewRecorder()

	h.Logout(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "INTERNAL_ERROR", resp.Errors[0].Code)
}

func TestLogout_ValidationError_MissingToken(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	body := jsonBody(t, dto.RefreshTokenRequest{})
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", body)
	rec := httptest.NewRecorder()

	h.Logout(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMe_Success(t *testing.T) {
	userID := uuid.New()
	now := time.Now().UTC()
	mock := &mockAuthService{
		getMeFn: func(_ context.Context, actualUserID uuid.UUID) (*domain.User, error) {
			assert.Equal(t, userID, actualUserID)
			return &domain.User{
				ID:        userID,
				Email:     "user@example.com",
				Name:      "Test User",
				CreatedAt: now,
				UpdatedAt: now,
			}, nil
		},
	}
	h := NewAuthHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rec := httptest.NewRecorder()

	h.Me(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	user := decodeUser(t, resp.Data)
	assert.Equal(t, userID, user.ID)
	assert.Equal(t, "user@example.com", user.Email)
	assert.Equal(t, "Test User", user.Name)
	assert.NotContains(t, rec.Body.String(), "password_hash")
}

func TestMe_NoAuth(t *testing.T) {
	h := NewAuthHandler(&mockAuthService{})

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	rec := httptest.NewRecorder()

	h.Me(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "UNAUTHORIZED", resp.Errors[0].Code)
}
