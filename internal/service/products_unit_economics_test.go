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
	response   *sellico.UnitEconomicsReadinessResponse
	responses  []*sellico.UnitEconomicsReadinessResponse
	exportRows []sellico.WBUnitEconomics
	exportSet  bool
	request    sellico.UnitEconomicsReadinessRequest
	requests   []sellico.UnitEconomicsReadinessRequest
}

func (c *readinessProductsClient) CheckUnitEconomicsReadiness(_ context.Context, _, _ string, request sellico.UnitEconomicsReadinessRequest) (*sellico.UnitEconomicsReadinessResponse, error) {
	c.request = request
	c.requests = append(c.requests, request)
	if len(c.responses) > 0 {
		return c.responses[len(c.requests)-1], nil
	}
	return c.response, nil
}

func (c *readinessProductsClient) ListWBUnitEconomics(_ context.Context, _, _, _ string) ([]sellico.WBUnitEconomics, error) {
	if c.exportSet {
		return c.exportRows, nil
	}
	response := c.response
	if len(c.responses) > 0 && len(c.requests) > 0 {
		response = c.responses[len(c.requests)-1]
	}
	if response == nil {
		return nil, nil
	}
	rows := make([]sellico.WBUnitEconomics, 0, len(response.CheckedProductIDs))
	for _, nmID := range response.CheckedProductIDs {
		customerPrice := 1000.0
		marginBeforeAds := 200.0
		calculatedAt := response.CheckedAt
		rows = append(rows, sellico.WBUnitEconomics{
			NmID: nmID, CostPrice: 500, CustomerPrice: &customerPrice,
			MarginBeforeAds: &marginBeforeAds, CalculatedAt: &calculatedAt,
			Source: response.Source, Ready: true,
		})
	}
	return rows, nil
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
	}}, client, "token", "/products/unit-economics/readiness", "/products/unit-economics/export", 72*time.Hour)
	provider.now = func() time.Time { return now }

	readiness, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{102, 101},
	})
	require.NoError(t, err)
	require.True(t, readiness.AllowsBidIncrease())
	require.Equal(t, 18.0, readiness.MaxAllowedDRRPercent)
	require.Equal(t, 200.0, readiness.MaxAllowedCPO)
	require.Equal(t, "17", client.request.IntegrationID)
	require.Equal(t, []int64{101, 102}, client.request.WBProductIDs)
}

func TestProductsUnitEconomicsReadinessProviderFailsClosedWithoutExactCPOExport(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	client := &readinessProductsClient{
		response: &sellico.UnitEconomicsReadinessResponse{
			IntegrationID: "17", Source: "products", CheckedAt: now, Complete: true,
			CheckedProductIDs: []int64{101}, MaxAllowedDRRByProduct: map[int64]float64{101: 20},
		},
		exportSet: true,
		exportRows: []sellico.WBUnitEconomics{{
			NmID: 101, CostPrice: 500, Ready: true, CalculatedAt: &now,
		}},
	}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID: uuidToPgtype(cabinetID), WorkspaceID: uuidToPgtype(workspaceID), ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/readiness", "/export", 72*time.Hour)
	provider.now = func() time.Time { return now }

	readiness, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{101},
	})
	require.Nil(t, readiness)
	require.ErrorContains(t, err, "CPO ceiling is unavailable")
}

func TestProductsUnitEconomicsReadinessProviderUsesConservativeVariantCeilings(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	marginHigh, marginLow := 240.0, 110.0
	priceHigh, priceLow := 1200.0, 800.0
	client := &readinessProductsClient{
		response: &sellico.UnitEconomicsReadinessResponse{
			IntegrationID: "17", Source: "products", CheckedAt: now, Complete: true,
			CheckedProductIDs: []int64{101}, MaxAllowedDRRByProduct: map[int64]float64{101: 20},
		},
		exportSet: true,
		exportRows: []sellico.WBUnitEconomics{
			{NmID: 101, CostPrice: 500, Ready: true, CalculatedAt: &now, MarginBeforeAds: &marginHigh, CustomerPrice: &priceHigh},
			{NmID: 101, CostPrice: 700, Ready: true, CalculatedAt: &now, MarginBeforeAds: &marginLow, CustomerPrice: &priceLow},
		},
	}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID: uuidToPgtype(cabinetID), WorkspaceID: uuidToPgtype(workspaceID), ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/readiness", "/export", 72*time.Hour)
	provider.now = func() time.Time { return now }

	readiness, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{101},
	})
	require.NoError(t, err)
	require.Equal(t, marginLow, readiness.MaxAllowedCPO)
	require.Equal(t, priceLow, readiness.BuyerPrice)
}

func TestProductsUnitEconomicsReadinessProviderBlocksProfitExportOlderThan24Hours(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	calculatedAt := now.Add(-25 * time.Hour)
	margin, price := 200.0, 1000.0
	client := &readinessProductsClient{
		response: &sellico.UnitEconomicsReadinessResponse{
			IntegrationID: "17", Source: "products", CheckedAt: now, Complete: true,
			CheckedProductIDs: []int64{101}, MaxAllowedDRRByProduct: map[int64]float64{101: 20},
		},
		exportSet: true,
		exportRows: []sellico.WBUnitEconomics{{
			NmID: 101, CostPrice: 500, Ready: true, CalculatedAt: &calculatedAt,
			MarginBeforeAds: &margin, CustomerPrice: &price,
		}},
	}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID: uuidToPgtype(cabinetID), WorkspaceID: uuidToPgtype(workspaceID), ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/readiness", "/export", 72*time.Hour)
	provider.now = func() time.Time { return now }

	readiness, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{101},
	})
	require.Nil(t, readiness)
	require.ErrorContains(t, err, "export is stale")
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
	}}, client, "token", "/readiness", "/export", 72*time.Hour)
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
	}}, client, "token", "/readiness", "/export", 72*time.Hour)
	provider.now = func() time.Time { return now }

	_, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: []int64{101},
	})
	require.ErrorContains(t, err, "stale or invalid")
}

func TestProductsUnitEconomicsReadinessProviderChunksAndCombinesDeterministically(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	requested := make([]int64, 205)
	for index := range requested {
		requested[index] = int64(205 - index)
	}

	responses := make([]*sellico.UnitEconomicsReadinessResponse, 0, 3)
	for chunk := 0; chunk < 3; chunk++ {
		start := chunk * productsReadinessMaxWBProductIDs
		end := start + productsReadinessMaxWBProductIDs
		if end > len(requested) {
			end = len(requested)
		}
		checked := make([]int64, end-start)
		ceilings := make(map[int64]float64, end-start)
		for index := range checked {
			checked[index] = int64(start + index + 1)
			ceilings[checked[index]] = 30
		}
		responses = append(responses, &sellico.UnitEconomicsReadinessResponse{
			IntegrationID: "17", Source: "products", CheckedAt: now.Add(-time.Duration(chunk+1) * time.Minute),
			Complete: true, CheckedProductIDs: checked, MaxAllowedDRRByProduct: ceilings,
		})
	}
	responses[0].MissingEconomicsProductIDs = []int64{5, 5}
	responses[1].UnprofitableProductIDs = []int64{150}
	responses[2].StaleProductIDs = []int64{205}
	client := &readinessProductsClient{responses: responses}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID: uuidToPgtype(cabinetID), WorkspaceID: uuidToPgtype(workspaceID), ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/readiness", "/export", 72*time.Hour)
	provider.now = func() time.Time { return now }

	readiness, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: requested,
	})
	require.NoError(t, err)
	require.Len(t, client.requests, 3)
	require.Len(t, client.requests[0].WBProductIDs, 100)
	require.Len(t, client.requests[1].WBProductIDs, 100)
	require.Len(t, client.requests[2].WBProductIDs, 5)
	require.Equal(t, int64(1), client.requests[0].WBProductIDs[0])
	require.Equal(t, int64(205), client.requests[2].WBProductIDs[4])
	require.Equal(t, requestedAscending(205), readiness.CheckedProductIDs)
	require.Equal(t, []int64{5}, readiness.MissingEconomicsProductIDs)
	require.Equal(t, []int64{150}, readiness.UnprofitableProductIDs)
	require.Equal(t, []int64{205}, readiness.StaleProductIDs)
	require.Equal(t, now.Add(-3*time.Minute), readiness.CheckedAt)
	require.Zero(t, readiness.MaxAllowedDRRPercent)
	require.False(t, readiness.AllowsBidIncrease())
}

func TestProductsUnitEconomicsReadinessProviderFailsClosedWhenAnyChunkIsIncomplete(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	now := time.Now().UTC()
	first := requestedAscending(100)
	second := []int64{101}
	client := &readinessProductsClient{responses: []*sellico.UnitEconomicsReadinessResponse{
		{IntegrationID: "17", Source: "products", CheckedAt: now, Complete: true, CheckedProductIDs: first, MaxAllowedDRRByProduct: readinessCeilings(first, 20)},
		{IntegrationID: "17", Source: "products", CheckedAt: now, Complete: false, CheckedProductIDs: second, MaxAllowedDRRByProduct: readinessCeilings(second, 20)},
	}}
	provider := NewProductsUnitEconomicsReadinessProvider(readinessCabinetReader{cabinet: sqlcgen.SellerCabinet{
		ID: uuidToPgtype(cabinetID), WorkspaceID: uuidToPgtype(workspaceID), ExternalIntegrationID: pgtype.Text{String: "17", Valid: true},
	}}, client, "token", "/readiness", "/export", 72*time.Hour)
	provider.now = func() time.Time { return now }

	readiness, err := provider.CheckBidIncreaseReadiness(context.Background(), UnitEconomicsReadinessInput{
		WorkspaceID: workspaceID, SellerCabinetID: cabinetID, WBProductIDs: requestedAscending(101),
	})
	require.Nil(t, readiness)
	require.ErrorContains(t, err, "incomplete for chunk 2")
	require.Len(t, client.requests, 2)
}

func requestedAscending(count int) []int64 {
	result := make([]int64, count)
	for index := range result {
		result[index] = int64(index + 1)
	}
	return result
}

func readinessCeilings(ids []int64, ceiling float64) map[int64]float64 {
	result := make(map[int64]float64, len(ids))
	for _, id := range ids {
		result[id] = ceiling
	}
	return result
}
