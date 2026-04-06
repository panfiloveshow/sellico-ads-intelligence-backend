//go:build integration

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceService_CreateAndList(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	authSvc := service.NewAuthService(db.Queries, testJWTSecret, 15*time.Minute, 168*time.Hour)
	wsSvc := service.NewWorkspaceService(db.Queries)
	ctx := context.Background()

	tokens, err := authSvc.Register(ctx, "owner@example.com", "StrongP@ss1", "Owner")
	require.NoError(t, err)

	claims := userIDFromToken(t, tokens.AccessToken)

	// Create workspace
	ws, err := wsSvc.Create(ctx, claims.UserID, "Test Workspace", "test-ws")
	require.NoError(t, err)
	assert.Equal(t, "Test Workspace", ws.Name)
	assert.Equal(t, "test-ws", ws.Slug)

	// List workspaces
	list, err := wsSvc.List(ctx, claims.UserID, 10, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, ws.ID, list[0].ID)
}

func TestWorkspaceService_MemberManagement(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	authSvc := service.NewAuthService(db.Queries, testJWTSecret, 15*time.Minute, 168*time.Hour)
	wsSvc := service.NewWorkspaceService(db.Queries)
	ctx := context.Background()

	// Create owner
	ownerTokens, err := authSvc.Register(ctx, "owner@ws.com", "StrongP@ss1", "Owner")
	require.NoError(t, err)
	ownerClaims := userIDFromToken(t, ownerTokens.AccessToken)

	// Create analyst
	_, err = authSvc.Register(ctx, "analyst@ws.com", "StrongP@ss1", "Analyst")
	require.NoError(t, err)

	// Create workspace
	ws, err := wsSvc.Create(ctx, ownerClaims.UserID, "Team WS", "team-ws")
	require.NoError(t, err)

	// Owner should be auto-added
	members, err := wsSvc.ListMembers(ctx, ws.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, string(domain.RoleOwner), members[0].Role)

	// Invite analyst by email
	member, err := wsSvc.InviteMember(ctx, ws.ID, "analyst@ws.com", string(domain.RoleAnalyst))
	require.NoError(t, err)
	assert.Equal(t, string(domain.RoleAnalyst), member.Role)

	// Update role to manager
	updated, err := wsSvc.UpdateMemberRole(ctx, ownerClaims.UserID, ws.ID, member.ID, string(domain.RoleManager))
	require.NoError(t, err)
	assert.Equal(t, string(domain.RoleManager), updated.Role)

	// Remove member
	err = wsSvc.RemoveMember(ctx, ws.ID, member.ID)
	require.NoError(t, err)

	// Should be back to 1 member
	members, err = wsSvc.ListMembers(ctx, ws.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, members, 1)
}

func TestWorkspaceService_DuplicateSlug(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()

	authSvc := service.NewAuthService(db.Queries, testJWTSecret, 15*time.Minute, 168*time.Hour)
	wsSvc := service.NewWorkspaceService(db.Queries)
	ctx := context.Background()

	tokens, err := authSvc.Register(ctx, "slugtest@ws.com", "StrongP@ss1", "User")
	require.NoError(t, err)
	claims := userIDFromToken(t, tokens.AccessToken)

	_, err = wsSvc.Create(ctx, claims.UserID, "WS One", "unique-slug")
	require.NoError(t, err)

	_, err = wsSvc.Create(ctx, claims.UserID, "WS Two", "unique-slug")
	require.Error(t, err)
}
