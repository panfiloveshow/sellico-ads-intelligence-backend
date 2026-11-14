package service

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestExplicitZeroEconomicsHTTPStorageAndCalculators(t *testing.T) {
	db, cleanup := newOzonTestDB(t)
	defer cleanup()
	ctx := context.Background()
	ws := newOzonWorkspace(t, db, "zero-cost-bridge")
	wbCab := seedWBCabinet(t, db, ws)
	ozCab := seedOzonCabinet(t, db, ws, pricesOnlyOzonCreds())
	_, err := db.Pool.Exec(ctx, `UPDATE seller_cabinets SET external_integration_id = CASE WHEN marketplace='ozon' THEN '18' ELSE '17' END WHERE workspace_id=$1`, uuidToPgtype(ws))
	require.NoError(t, err)
	product, err := db.Queries.CreateProduct(ctx, sqlcgen.CreateProductParams{WorkspaceID: uuidToPgtype(ws), SellerCabinetID: uuidToPgtype(wbCab),
		WbProductID: 101, Title: "Test zero cost", Price: pgtype.Int8{Int64: 1000, Valid: true}})
	require.NoError(t, err)
	foreignWS := newOzonWorkspace(t, db, "zero-cost-foreign")
	foreignCab := seedWBCabinet(t, db, foreignWS)
	_, err = db.Queries.CreateProduct(ctx, sqlcgen.CreateProductParams{WorkspaceID: uuidToPgtype(foreignWS), SellerCabinetID: uuidToPgtype(foreignCab), WbProductID: 201, Title: "Foreign product"})
	require.NoError(t, err)

	var phase atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/export", r.URL.Path)
		require.Equal(t, "Bearer test-service", r.Header.Get("Authorization"))
		integration := r.URL.Query().Get("integration_id")
		require.Contains(t, []string{"17", "18"}, integration)
		row := map[string]any{"cost_price": 0, "explicit_zero_cost_price": true, "ready": true,
			"commission_percent": 10, "tax_percent": 6, "logistics_cost": 50, "other_costs": 25, "max_allowed_drr": 20,
			"calculated_at": time.Now().UTC().Format(time.RFC3339)}
		if integration == "17" {
			row["nm_id"] = 101
		} else {
			row["offer_id"] = "ZERO-OFFER"
			row["sku"] = 301
		}
		rows := []map[string]any{row}
		switch phase.Load() {
		case 1:
			row["cost_price"] = 77
			row["ready"] = false // a failed/stale recalculation cannot replace the mirror
		case 2:
			if integration == "17" {
				rows = append(rows, map[string]any{"nm_id": 201, "cost_price": 0, "explicit_zero_cost_price": true, "ready": true})
			} else {
				row["ready"] = false
			}
		}
		integrationNumber := 17
		if integration == "18" {
			integrationNumber = 18
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"schema_version": 2, "source": "sellico-products-unit-economics",
			"integration_id": integrationNumber, "complete": true, "items": rows}))
	}))
	defer server.Close()

	economics := NewProductEconomicsService(db.Queries)
	bridge := NewSellicoEconomicsSyncService(db.Queries, sellico.NewClient(server.URL, time.Second), "test-service", economics, "/export", ozonTestLogger())
	n, err := bridge.SyncWorkspace(ctx, ws)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	wbRows, err := economics.List(ctx, ws, 100, 0)
	require.NoError(t, err)
	require.Len(t, wbRows, 1)
	require.NotNil(t, wbRows[0].CostPrice)
	require.Equal(t, int64(0), *wbRows[0].CostPrice)
	// (0 purchase + 50 logistics + 25 other) / (1 - 10% - 6%) = 89.2857, rounded up.
	floor, reason := ComputeMinEffectivePrice(wbRows[0], nil)
	require.Empty(t, reason)
	require.Equal(t, int64(90), floor)
	price := int64(1000)
	business := applyProductEconomics(domain.ProductBusinessSummary{}, domain.Product{Price: &price, ID: uuidFromPgtype(product.ID)}, wbRows[0])
	require.NotNil(t, business.MarginBeforeAds)
	require.Equal(t, int64(765), *business.MarginBeforeAds)
	require.Nil(t, business.ProfitAfterAds) // no invented sales in the empty period
	foreignRows, err := economics.List(ctx, foreignWS, 100, 0)
	require.NoError(t, err)
	require.Empty(t, foreignRows)

	ozRows, err := db.Queries.ListOzonProductEconomicsByCabinet(ctx, uuidToPgtype(ozCab))
	require.NoError(t, err)
	require.Len(t, ozRows, 1)
	require.True(t, ozRows[0].CostPriceRub.Valid)
	require.Equal(t, 0.0, pgNumericToFloat(ozRows[0].CostPriceRub))
	cost, known := ozonEffectiveCostBasis(0, &ozRows[0])
	require.True(t, known)
	require.Equal(t, 25.0, cost) // zero purchase still carries the other costs
	ozFloor, reason := computeOzonFloor(ozonFloorInputs{NetPriceRub: cost, NetPriceKnown: known, CommissionFBOPct: 10, AcquiringRub: 5})
	require.Empty(t, reason)
	require.Equal(t, 33.33, ozFloor)

	phase.Store(1)
	n, err = bridge.SyncWorkspace(ctx, ws)
	require.NoError(t, err)
	require.Zero(t, n)
	wbAfter, err := economics.List(ctx, ws, 100, 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), *wbAfter[0].CostPrice)
	ozAfter, err := db.Queries.ListOzonProductEconomicsByCabinet(ctx, uuidToPgtype(ozCab))
	require.NoError(t, err)
	require.Equal(t, ozRows[0].SyncedAt, ozAfter[0].SyncedAt)
	require.Equal(t, 0.0, pgNumericToFloat(ozAfter[0].CostPriceRub))

	// A matching zero flag does not bypass tenant/product verification.
	manualCost := int64(100)
	result, err := economics.Import(ctx, uuid.Nil, ws, []domain.ProductEconomicsInput{{WBProductID: 101, CostPrice: &manualCost}})
	require.NoError(t, err)
	require.Equal(t, 1, result.Imported)
	phase.Store(2)
	n, err = bridge.SyncWorkspace(ctx, ws)
	require.NoError(t, err)
	require.Zero(t, n)
	wbAfter, err = economics.List(ctx, ws, 100, 0)
	require.NoError(t, err)
	require.Equal(t, int64(100), *wbAfter[0].CostPrice)
}

func TestExplicitZeroEconomicsWBMappingRejectsUnknownAndNonFiniteCosts(t *testing.T) {
	rows := []sellico.WBUnitEconomics{
		{NmID: 101, CostPrice: 0, ExplicitZeroCostPrice: true, Ready: true, Source: "sellico-products-unit-economics"},
		{NmID: 102, CostPrice: 0, Ready: true},
		{NmID: 103, CostPrice: 0, ExplicitZeroCostPrice: true, Ready: false, Source: "sellico-products-unit-economics"},
		{NmID: 104, CostPrice: 0, ExplicitZeroCostPrice: true, Ready: true, Source: "unknown-publisher"},
		{NmID: 105, CostPrice: math.NaN(), Ready: true},
		{NmID: 106, CostPrice: math.Inf(1), Ready: true},
		{NmID: 107, CostPrice: -1, Ready: true},
	}
	inputs := buildEconomicsInputs(rows)
	require.Len(t, inputs, 1)
	require.Equal(t, int64(101), inputs[0].WBProductID)
	require.NotNil(t, inputs[0].CostPrice)
	require.Equal(t, int64(0), *inputs[0].CostPrice)
}

func TestExplicitZeroOzonBasisKeepsZeroDistinctFromMissing(t *testing.T) {
	econ := sqlcgen.OzonProductEconomic{CostPriceRub: floatToPgNumeric(0), OtherCostsRub: floatToPgNumeric(0)}
	cost, known := ozonEffectiveCostBasis(0, &econ)
	require.True(t, known)
	require.Equal(t, 0.0, cost)
	floor, reason := computeOzonFloor(ozonFloorInputs{NetPriceRub: cost, NetPriceKnown: known, CommissionFBOPct: 10, AcquiringRub: 5,
		CurrentPriceRub: 1000, MaxDiscountPercent: 20})
	require.Empty(t, reason)
	require.Equal(t, 5.56, floor) // known zero must not select the relative 800-ruble floor
	price := domain.OzonProductPrice{}
	applyOzonEconomicsFloorFallback(&price, sqlcgen.OzonProductPrice{OfferID: pgtype.Text{String: "ZERO", Valid: true},
		CommissionFboPct: floatToPgNumeric(10), AcquiringPct: floatToPgNumeric(5)}, map[string]sqlcgen.OzonProductEconomic{"ZERO": econ})
	require.NotNil(t, price.EffectiveCostRub)
	require.Equal(t, 0.0, *price.EffectiveCostRub)
	require.Equal(t, "sellico", price.CostSource)
	require.NotNil(t, price.FloorRub)
	require.Equal(t, 5.56, *price.FloorRub)
	_, known = ozonEffectiveCostBasis(0, &sqlcgen.OzonProductEconomic{OtherCostsRub: floatToPgNumeric(50)})
	require.False(t, known)
	_, known = ozonEffectiveCostBasis(0, &sqlcgen.OzonProductEconomic{CostPriceRub: pgtype.Numeric{NaN: true, Valid: true}})
	require.False(t, known)
}

func TestExplicitZeroOzonAIContextKeepsKnownZeroAndMissingCostSeparate(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "zero-cost-ai-context")
	defer cleanup()
	ctx := context.Background()
	manager := newAIManager(fx.db, &fakeLLM{})
	campaign := seedOzonCampaign(t, fx.db, fx.cabinetID, 9201, "CAMPAIGN_STATE_RUNNING", nil, nil)
	for _, sku := range []int64{601, 602} {
		seedOzonCampaignProduct(t, fx.db, campaign.ID, sku, 20)
		seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{SKU: sku, OfferID: "ZERO-AI-" + string(rune('A'+sku-601)),
			PriceRub: 1000, CommissionFBOPct: 10})
	}
	seedOzonEconomics(t, fx.db, fx.cabinetID, "ZERO-AI-A", 601, 0, 0)
	strategyID := seedAIStrategy(t, fx, 3)
	row, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)
	strategy := strategyFromSqlc(row)
	pack, _, err := manager.buildAIContext(ctx, fx.workspaceID, fx.cabinetID, strategy, strategy.Params.Merged())
	require.NoError(t, err)
	entries := map[int64]aiPackEconomics{}
	for _, entry := range pack.Economics {
		entries[entry.SKU] = entry
	}
	known, exists := entries[601]
	require.True(t, exists)
	require.Equal(t, "sellico", known.CostSource)
	require.NotNil(t, known.NetPriceRub)
	require.Equal(t, 0.0, *known.NetPriceRub)
	require.NotNil(t, known.MarginPct)
	require.Equal(t, 90.0, *known.MarginPct)
	unknown, exists := entries[602]
	require.True(t, exists)
	require.Empty(t, unknown.CostSource)
	require.Nil(t, unknown.MarginPct)
}
