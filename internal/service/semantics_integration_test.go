//go:build integration

package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
)

// Regression test for two real bugs reported against the Settings → Ключевые
// слова screen: keywords blending across stores in the same workspace, and
// cluster keyword_count/total_frequency always reading 0.
func TestSemanticsService_KeywordsAreIsolatedPerCabinetAndClusterCountsAreLive(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	svc := service.NewSemanticsService(db.Queries, zerolog.Nop())

	ws, err := db.Queries.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{Name: "WS", Slug: "ws-semantics-test"})
	require.NoError(t, err)
	workspaceID := uuid.UUID(ws.ID.Bytes)

	cabinetA, err := db.Queries.CreateSellerCabinet(ctx, sqlcgen.CreateSellerCabinetParams{
		WorkspaceID: ws.ID, Name: "Store A (bags)", EncryptedToken: "token-a",
	})
	require.NoError(t, err)
	cabinetAID := uuid.UUID(cabinetA.ID.Bytes)

	cabinetB, err := db.Queries.CreateSellerCabinet(ctx, sqlcgen.CreateSellerCabinetParams{
		WorkspaceID: ws.ID, Name: "Store B (groceries)", EncryptedToken: "token-b",
	})
	require.NoError(t, err)
	cabinetBID := uuid.UUID(cabinetB.ID.Bytes)

	// Cabinet A collects two bag keywords sharing a 2-word cluster prefix
	// ("рюкзак кожаный"); cabinet B collects an unrelated grocery keyword that
	// happens to share exact query text with nothing here — this must not collide.
	_, err = db.Queries.UpsertKeyword(ctx, sqlcgen.UpsertKeywordParams{
		WorkspaceID: ws.ID, SellerCabinetID: cabinetA.ID,
		Query: "рюкзак кожаный мужской", Normalized: "рюкзак кожаный мужской", Frequency: 25, Source: "wb_phrases",
	})
	require.NoError(t, err)
	_, err = db.Queries.UpsertKeyword(ctx, sqlcgen.UpsertKeywordParams{
		WorkspaceID: ws.ID, SellerCabinetID: cabinetB.ID,
		Query: "рис басмати", Normalized: "рис басмати", Frequency: 500, Source: "wb_phrases",
	})
	require.NoError(t, err)

	// Bug 1: cabinet A's keyword list must not include cabinet B's keywords.
	keywordsA, err := svc.ListKeywords(ctx, cabinetAID, "", 100, 0)
	require.NoError(t, err)
	require.Len(t, keywordsA, 1)
	require.Equal(t, "рюкзак кожаный мужской", keywordsA[0].Normalized)

	keywordsB, err := svc.ListKeywords(ctx, cabinetBID, "", 100, 0)
	require.NoError(t, err)
	require.Len(t, keywordsB, 1)
	require.Equal(t, "рис басмати", keywordsB[0].Normalized)

	// Add a second keyword sharing the same 2-word prefix so AutoCluster has something to group.
	_, err = db.Queries.UpsertKeyword(ctx, sqlcgen.UpsertKeywordParams{
		WorkspaceID: ws.ID, SellerCabinetID: cabinetA.ID,
		Query: "рюкзак кожаный женский", Normalized: "рюкзак кожаный женский", Frequency: 15, Source: "wb_phrases",
	})
	require.NoError(t, err)

	created, err := svc.AutoCluster(ctx, workspaceID, cabinetAID)
	require.NoError(t, err)
	require.Equal(t, 1, created)

	clustersA, err := svc.ListClusters(ctx, cabinetAID, 10, 0)
	require.NoError(t, err)
	require.Len(t, clustersA, 1)

	// Bug 2: keyword_count/total_frequency must reflect the assigned keywords,
	// not the stored-at-creation-time zero values.
	require.Equal(t, 2, clustersA[0].KeywordCount)
	require.Equal(t, 40, clustersA[0].TotalFrequency) // 25 + 15

	// Cabinet B has no clusters of its own.
	clustersB, err := svc.ListClusters(ctx, cabinetBID, 10, 0)
	require.NoError(t, err)
	require.Len(t, clustersB, 0)
}
