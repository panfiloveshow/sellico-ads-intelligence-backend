package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-key-for-jwt-middleware"

// dummyHandler is a handler that records whether it was called and the user_id from context.
func dummyHandler() (http.Handler, *bool, *uuid.UUID) {
	called := false
	var gotUserID uuid.UUID
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		uid, ok := UserIDFromContext(r.Context())
		if ok {
			gotUserID = uid
		}
		w.WriteHeader(http.StatusOK)
	})
	return h, &called, &gotUserID
}

func TestAuth_ValidAccessToken(t *testing.T) {
	userID := uuid.New()
	token, err := jwt.GenerateAccessToken(userID, testSecret, 15*time.Minute)
	require.NoError(t, err)

	handler, called, gotUserID := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *called)
	assert.Equal(t, userID, *gotUserID)
}

func TestAuth_MissingAuthorizationHeader(t *testing.T) {
	handler, called, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
	assertErrorResponse(t, rec, "UNAUTHORIZED", "missing authorization header")
}

func TestAuth_InvalidHeaderFormat(t *testing.T) {
	handler, called, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Basic abc123")
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
	assertErrorResponse(t, rec, "UNAUTHORIZED", "invalid authorization header format")
}

func TestAuth_EmptyBearerToken(t *testing.T) {
	handler, called, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
}

func TestAuth_InvalidToken(t *testing.T) {
	handler, called, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
	assertErrorResponse(t, rec, "UNAUTHORIZED", "invalid or expired token")
}

func TestAuth_ExpiredToken(t *testing.T) {
	userID := uuid.New()
	// Generate a token that expires immediately.
	token, err := jwt.GenerateAccessToken(userID, testSecret, -1*time.Second)
	require.NoError(t, err)

	handler, called, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
}

func TestAuth_WrongSecret(t *testing.T) {
	userID := uuid.New()
	token, err := jwt.GenerateAccessToken(userID, "other-secret", 15*time.Minute)
	require.NoError(t, err)

	handler, called, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
}

func TestAuth_RefreshTokenRejected(t *testing.T) {
	userID := uuid.New()
	token, _, err := jwt.GenerateRefreshToken(userID, testSecret, 168*time.Hour)
	require.NoError(t, err)

	handler, called, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
	assertErrorResponse(t, rec, "UNAUTHORIZED", "invalid token type")
}

func TestAuth_BearerCaseInsensitive(t *testing.T) {
	userID := uuid.New()
	token, err := jwt.GenerateAccessToken(userID, testSecret, 15*time.Minute)
	require.NoError(t, err)

	handler, called, gotUserID := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "bearer "+token)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *called)
	assert.Equal(t, userID, *gotUserID)
}

func TestUserIDFromContext_NotPresent(t *testing.T) {
	ctx := context.Background()
	uid, ok := UserIDFromContext(ctx)
	assert.False(t, ok)
	assert.Equal(t, uuid.Nil, uid)
}

func TestAuth_ResponseContentType(t *testing.T) {
	handler, _, _ := dummyHandler()
	mw := Auth(testSecret)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

// assertErrorResponse checks that the response body is a valid Response_Envelope with the expected error.
func assertErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	var resp envelope.Response
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Errors)
	assert.Equal(t, code, resp.Errors[0].Code)
	assert.Equal(t, message, resp.Errors[0].Message)
}
