package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type readinessCabinetReader struct {
	cabinet sqlcgen.SellerCabinet
}

func (r readinessCabinetReader) GetSellerCabinetByID(context.Context, pgtype.UUID) (sqlcgen.SellerCabinet, error) {
	return r.cabinet, nil
}

type readinessProductsClient struct {
	response *sellico.UnitEconomicsReadinessResponse
	request  sellico.UnitEconomicsReadinessRequest
}

func (c *readinessProductsClient) CheckUnitEconomicsReadiness(_ context.Context, _, _ string, request sellico.UnitEconomicsReadinessRequest) (*sellico.UnitEconomicsReadinessResponse, error) {
	c.request = request
	return c.response, nil
}

func TestProductsUnitEconomicsReadinessProviderRequiresExactFreshCoverage(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	client := &readinessProductsClient{response: &sellico.UnitEconomicsReadinessResponse{
		IntegrationID:          "17",
		Source:                 "sellico-products-unit-economics",
		CheckedAt:              now.Add(-time.Minute),
		Complete:               true,
		CheckedProductIDs:      []int64{101, 102},
		MaxAllowedDRRByProduct: map[int64]float64{101: 18, 102: 22},
	}}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID:                    uuidToPgtype(cabinetID),
		WorkspaceID:           uuidToPgtype(workspaceID),
		ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/products/unit-economics/readiness", 72*time.Hour)
	provider.now = func() time.Time { return now }

	readiness, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{102, 101},
	})
	require.NoError(t, err)
	require.True(t, readiness.AllowsBidIncrease())
	require.Equal(t, 18.0, readiness.MaxAllowedDRRPercent)
	require.Equal(t, "17", client.request.IntegrationID)
	require.Equal(t, []int64{101, 102}, client.request.WBProductIDs)
}

func TestProductsUnitEconomicsReadinessProviderBlocksCoverageMismatch(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Now().UTC()
	client := &readinessProductsClient{response: &sellico.UnitEconomicsReadinessResponse{
		IntegrationID: "17", Source: "products", CheckedAt: now, Complete: true, CheckedProductIDs: []int64{101}, MaxAllowedDRRByProduct: map[int64]float64{101: 20},
	}}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID: uuidToPgtype(cabinetID), WorkspaceID: uuidToPgtype(workspaceID), ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/readiness", 72*time.Hour)
	provider.now = func() time.Time { return now }

	_, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{101, 102},
	})
	require.ErrorContains(t, err, "coverage mismatch")
}

func TestProductsUnitEconomicsReadinessProviderBlocksStaleTimestamp(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Now().UTC()
	client := &readinessProductsClient{response: &sellico.UnitEconomicsReadinessResponse{
		IntegrationID: "17", Source: "products", CheckedAt: now.Add(-73 * time.Hour), Complete: true, CheckedProductIDs: []int64{101}, MaxAllowedDRRByProduct: map[int64]float64{101: 20},
	}}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID: uuidToPgtype(cabinetID), WorkspaceID: uuidToPgtype(workspaceID), ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/readiness", 72*time.Hour)
	provider.now = func() time.Time { return now }

	_, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{101},
	})
	require.ErrorContains(t, err, "stale or invalid")
}
