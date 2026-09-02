//go:build integration

package sqlcgen_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
)

func TestStableSyncResultsAreStoredOnceAndLatestStockRemainsQueryable(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	workspace, err := db.Queries.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{
		Name: "storage-dedup",
		Slug: "storage-dedup",
	})
	require.NoError(t, err)
	cabinet, err := db.Queries.CreateSellerCabinet(ctx, sqlcgen.CreateSellerCabinetParams{
		WorkspaceID:    workspace.ID,
		Name:           "storage-dedup",
		EncryptedToken: "test-only-token",
	})
	require.NoError(t, err)
	product, err := db.Queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
		WorkspaceID:     workspace.ID,
		SellerCabinetID: cabinet.ID,
		WbProductID:     900001,
		Title:           "Test product",
	})
	require.NoError(t, err)
	keyword, err := db.Queries.UpsertKeyword(ctx, sqlcgen.UpsertKeywordParams{
		WorkspaceID:     workspace.ID,
		SellerCabinetID: cabinet.ID,
		Query:           "test query",
		Normalized:      "test query",
		Frequency:       42,
		Source:          "integration_test",
	})
	require.NoError(t, err)

	require.NoError(t, db.Queries.CreateFrequencyHistory(ctx, keyword.ID, 42))
	require.NoError(t, db.Queries.CreateFrequencyHistory(ctx, keyword.ID, 42))
	var frequencyRows int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM keyword_frequency_history WHERE keyword_id = $1`,
		keyword.ID,
	).Scan(&frequencyRows))
	require.Equal(t, 1, frequencyRows)

	analysis := sqlcgen.CreateSEOAnalysisParams{
		WorkspaceID:       workspace.ID,
		ProductID:         product.ID,
		TitleScore:        80,
		DescriptionScore:  70,
		KeywordsScore:     60,
		OverallScore:      70,
		TitleIssues:       []byte(`[]`),
		DescriptionIssues: []byte(`[]`),
		KeywordCoverage:   []byte(`{"covered":true}`),
		Recommendations:   []byte(`[]`),
	}
	firstAnalysis, err := db.Queries.CreateSEOAnalysis(ctx, analysis)
	require.NoError(t, err)
	secondAnalysis, err := db.Queries.CreateSEOAnalysis(ctx, analysis)
	require.NoError(t, err)
	require.Equal(t, firstAnalysis.ID, secondAnalysis.ID)
	var analysisRows int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM seo_analyses WHERE product_id = $1`,
		product.ID,
	).Scan(&analysisRows))
	require.Equal(t, 1, analysisRows)

	require.NoError(t, db.Queries.SetProductStock(ctx, workspace.ID, cabinet.ID, 900001, 10))
	firstSnapshot, err := db.Queries.GetLatestProductSnapshot(ctx, product.ID)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, db.Queries.SetProductStock(ctx, workspace.ID, cabinet.ID, 900001, 10))
	refreshedSnapshot, err := db.Queries.GetLatestProductSnapshot(ctx, product.ID)
	require.NoError(t, err)
	require.True(t, refreshedSnapshot.CapturedAt.Time.After(firstSnapshot.CapturedAt.Time))
	var stockRows int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM product_snapshots WHERE product_id = $1`,
		product.ID,
	).Scan(&stockRows))
	require.Equal(t, 1, stockRows)

	require.NoError(t, db.Queries.SetProductStock(ctx, workspace.ID, cabinet.ID, 900001, 7))
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM product_snapshots WHERE product_id = $1`,
		product.ID,
	).Scan(&stockRows))
	require.Equal(t, 2, stockRows)

	evidence, err := db.Queries.ListLatestProductStockEvidenceByWorkspace(ctx, workspace.ID)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	require.Equal(t, int32(7), evidence[0].StockTotal)
	require.Equal(t, "product_snapshot", evidence[0].Source)
	require.Equal(t, product.ID, evidence[0].ProductID)
}
