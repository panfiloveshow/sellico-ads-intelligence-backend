//go:build integration

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testJWTSecret = "test-secret-min-32-chars-long!!!"

func TestAuthService_RegisterAndLogin(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	authSvc := service.NewAuthService(db.Queries, testJWTSecret, 15*time.Minute, 168*time.Hour)
	ctx := context.Background()

	// Register
	tokens, err := authSvc.Register(ctx, "alice@example.com", "StrongP@ss1", "Alice")
	require.NoError(t, err)
	assert.NotEmpty(t, tokens.AccessToken)
	assert.NotEmpty(t, tokens.RefreshToken)

	// Login with correct password
	loginTokens, err := authSvc.Login(ctx, "alice@example.com", "StrongP@ss1")
	require.NoError(t, err)
	assert.NotEmpty(t, loginTokens.AccessToken)
	assert.NotEmpty(t, loginTokens.RefreshToken)

	// Login with wrong password
	_, err = authSvc.Login(ctx, "alice@example.com", "wrongpassword")
	require.Error(t, err)

	// Login with non-existent email
	_, err = authSvc.Login(ctx, "nobody@example.com", "any")
	require.Error(t, err)
}

func TestAuthService_RegisterDuplicate(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	authSvc := service.NewAuthService(db.Queries, testJWTSecret, 15*time.Minute, 168*time.Hour)
	ctx := context.Background()

	_, err := authSvc.Register(ctx, "bob@example.com", "StrongP@ss1", "Bob")
	require.NoError(t, err)

	_, err = authSvc.Register(ctx, "bob@example.com", "StrongP@ss1", "Bob Again")
	require.Error(t, err)
}

func TestAuthService_RefreshToken(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	authSvc := service.NewAuthService(db.Queries, testJWTSecret, 15*time.Minute, 168*time.Hour)
	ctx := context.Background()

	tokens, err := authSvc.Register(ctx, "carol@example.com", "StrongP@ss1", "Carol")
	require.NoError(t, err)

	// Refresh
	newTokens, err := authSvc.Refresh(ctx, tokens.RefreshToken)
	require.NoError(t, err)
	assert.NotEmpty(t, newTokens.AccessToken)
	assert.NotEmpty(t, newTokens.RefreshToken)

	// Old refresh token should be revoked
	_, err = authSvc.Refresh(ctx, tokens.RefreshToken)
	require.Error(t, err)
}

func TestAuthService_Logout(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	authSvc := service.NewAuthService(db.Queries, testJWTSecret, 15*time.Minute, 168*time.Hour)
	ctx := context.Background()

	tokens, err := authSvc.Register(ctx, "dave@example.com", "StrongP@ss1", "Dave")
	require.NoError(t, err)

	err = authSvc.Logout(ctx, tokens.RefreshToken)
	require.NoError(t, err)

	// Refresh after logout should fail
	_, err = authSvc.Refresh(ctx, tokens.RefreshToken)
	require.Error(t, err)
}

// userIDFromToken extracts user ID from an access token (test helper).
func userIDFromToken(t *testing.T, accessToken string) *jwt.TokenClaims {
	t.Helper()
	claims, err := jwt.ValidateToken(accessToken, testJWTSecret)
	require.NoError(t, err)
	return claims
}
