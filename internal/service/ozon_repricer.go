package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	// ozonRepricerPageSize pages the cabinet price mirror during a run.
	ozonRepricerPageSize = 1000
	// maxOzonRepricerPriceAge blocks decisions on stale mirrors (the ozon
	// sync sweep runs hourly; anything older means sync is broken).
	maxOzonRepricerPriceAge = 6 * time.Hour
	// Ozon rejects more than 10 price changes per product per hour. The
	// strategy sweep stays far below (2/hour) so manual writes and rollbacks
	// always have headroom; manual writes are capped at the hard limit.
	ozonStrategyHourlyWriteCap = 2
	ozonHardHourlyWriteCap     = 10
	// ozonManualPriceItemsLimit caps one manual bulk request (mirrors the
	// import/prices per-request limit).
	ozonManualPriceItemsLimit = 1000
	// minOzonPriceDeltaRub: sub-ruble moves are noise, never shipped.
	minOzonPriceDeltaRub = 1.0
)

// OzonManualPriceInput is one manual price write coming from the API layer.
type OzonManualPriceInput struct {
	SKU         int64    `json:"sku"`
	PriceRub    float64  `json:"price_rub"`
	OldPriceRub *float64 `json:"old_price_rub,omitempty"`
	MinPriceRub *float64 `json:"min_price_rub,omitempty"`
}

// OzonRepricerService runs the Ozon price strategies (ozon_price_margin_floor,
// ozon_price_competitor_follow) on the economics Ozon itself provides
// (net_price, commissions, acquiring, competitor price indexes), audits every
// decision in ozon_price_changes and applies live writes through
// POST /v1/product/import/prices. Deterministic guardrails mirror the WB
// repricer: floor clamp, step cap, cooldown, daily cap, and Ozon's hard
// 10-changes-per-hour budget.
type OzonRepricerService struct {
	queries       *sqlcgen.Queries
	sellerClient  *ozon.SellerClient
	encryptionKey []byte
	logger        zerolog.Logger
}

// NewOzonRepricerService wires the Ozon repricer.
func NewOzonRepricerService(queries *sqlcgen.Queries, sellerClient *ozon.SellerClient, encryptionKey []byte, logger zerolog.Logger) *OzonRepricerService {
	return &OzonRepricerService{
		queries:       queries,
		sellerClient:  sellerClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "ozon_repricer").Logger(),
	}
}

// --- pure floor + decision engine (unit-tested) ---

// ozonFloorInputs is everything computeOzonFloor needs for one product.
type ozonFloorInputs struct {
	NetPriceRub         float64 // себестоимость pushed to Ozon (0 = unknown)
	CommissionFBOPct    float64
	CommissionFBSPct    float64
	AcquiringRub        float64 // Ozon reports the max acquiring fee in rubles
	TargetMarginPercent float64 // % of the sale price
	// Fallback when NetPriceRub is absent: relative floor off the current price.
	CurrentPriceRub    float64
	MaxDiscountPercent float64 // default 30 when zero
}

// computeOzonFloor returns the lowest price (rubles, 2dp) at which the product
// still covers cost + Ozon fees + the target margin:
//
//	floor = (net_price + acquiring_rub) / (1 − (commission% + margin%)/100)
//
// commission is the conservative max(FBO, FBS). Without net_price it falls
// back to a relative floor — current × (1 − MaxDiscountPercent/100) — like
// the WB engine. Returns (0, reason) when nothing can be computed.
func computeOzonFloor(in ozonFloorInputs) (float64, string) {
	if in.NetPriceRub > 0 {
		commission := math.Max(in.CommissionFBOPct, in.CommissionFBSPct)
		if commission <= 0 {
			return 0, "missing_commission"
		}
		if in.AcquiringRub < 0 {
			return 0, "negative_acquiring"
		}
		denom := 1 - (commission+in.TargetMarginPercent)/100
		if denom < 0.05 {
			// commission+margin ≥ 95% — no achievable price; refuse rather
			// than return an absurd floor.
			return 0, "economics_percentages_invalid"
		}
		return roundRub((in.NetPriceRub + in.AcquiringRub) / denom), ""
	}
	if in.CurrentPriceRub > 0 {
		maxDiscount := in.MaxDiscountPercent
		if maxDiscount <= 0 {
			maxDiscount = domain.DefaultPriceParams().MaxDiscountPercent
		}
		return roundRub(in.CurrentPriceRub * (1 - maxDiscount/100)), ""
	}
	return 0, "missing_net_price"
}

// ozonEffectiveNetPrice picks the cost that feeds the floor formula: Ozon's own
// net_price when it is pushed to the marketplace, otherwise the Sellico
// unit-economics fallback (cost_price + other_costs). logistics_cost_rub is
// deliberately NOT added: on Ozon logistics is already inside the commission
// percentages the floor formula applies, so adding it would double-count.
// NOTE: econ.MaxAllowedDrr is ignored here for now — TODO: feed it into the AI
// autopilot context as a per-product ad-spend ceiling.
func ozonEffectiveNetPrice(netPriceRub float64, econ *sqlcgen.OzonProductEconomic) float64 {
	if netPriceRub > 0 {
		return netPriceRub
	}
	if econ == nil {
		return 0
	}
	cost := pgNumericToFloat(econ.CostPriceRub)
	if cost <= 0 {
		return 0
	}
	if other := pgNumericToFloat(econ.OtherCostsRub); other > 0 {
		cost += other
	}
	return cost
}

// ozonPriceDecision is the engine verdict for one product.
type ozonPriceDecision struct {
	ShouldChange  bool
	NewPriceRub   float64
	FloorRub      float64
	CompetitorRub float64
	Direction     string
	Reason        string
	SkipReason    string
}

func ozonSkip(reason string) ozonPriceDecision {
	return ozonPriceDecision{Direction: "none", SkipReason: reason}
}

// decideOzonMarginFloor raises a product priced below its floor back up to it.
// The corrective raise bypasses the step cap (selling below cost is worse than
// a large raise) but refuses floors ≥3× the live price — that shape means
// corrupted economics, not a price worth shipping.
func decideOzonMarginFloor(currentRub, floorRub float64) ozonPriceDecision {
	if currentRub <= 0 {
		return ozonSkip("invalid_current_price")
	}
	if floorRub <= 0 {
		return ozonSkip("no_floor")
	}
	if currentRub >= floorRub {
		return ozonPriceDecision{Direction: "none", Reason: "above_margin_floor", FloorRub: floorRub}
	}
	if floorRub >= currentRub*3 {
		return ozonSkip("floor_suspicious")
	}
	return ozonPriceDecision{
		ShouldChange: true,
		NewPriceRub:  floorRub,
		FloorRub:     floorRub,
		Direction:    "up",
		Reason:       fmt.Sprintf("below margin floor %.2f₽, raising to floor", floorRub),
	}
}

// decideOzonCompetitorFollow tracks the cheapest competitor price:
// target = competitorMin × (1 − undercut%), clamped to the floor and capped
// to ±step% per run. competitorMinRub ≤ 0 (no competitor data) → skip.
func decideOzonCompetitorFollow(currentRub, floorRub, competitorMinRub, undercutPct, stepPct float64) ozonPriceDecision {
	if currentRub <= 0 {
		return ozonSkip("invalid_current_price")
	}
	if competitorMinRub <= 0 {
		return ozonSkip("no_competitor_price")
	}
	if floorRub <= 0 {
		return ozonSkip("no_floor")
	}

	target := roundRub(competitorMinRub * (1 - undercutPct/100))
	if target < floorRub {
		target = floorRub
	}
	// Per-run move capped to step%.
	step := stepPct / 100
	if target > currentRub {
		if capped := roundRub(currentRub * (1 + step)); target > capped {
			target = capped
		}
	} else if target < currentRub {
		if capped := roundRub(currentRub * (1 - step)); target < capped {
			target = capped
		}
		if target < floorRub {
			target = floorRub
		}
	}

	// Dead-band: ignore sub-1₽ / sub-1% nudges.
	if diff := math.Abs(target - currentRub); diff < math.Max(minOzonPriceDeltaRub, currentRub*0.01) {
		return ozonPriceDecision{Direction: "none", Reason: "near_competitor", FloorRub: floorRub, CompetitorRub: competitorMinRub}
	}
	direction := "up"
	if target < currentRub {
		direction = "down"
	}
	return ozonPriceDecision{
		ShouldChange:  true,
		NewPriceRub:   target,
		FloorRub:      floorRub,
		CompetitorRub: competitorMinRub,
		Direction:     direction,
		Reason:        fmt.Sprintf("competitor min %.2f₽, %s to %.2f₽ (−%.0f%%)", competitorMinRub, direction, target, undercutPct),
	}
}

// --- strategy sweep ---

// ozonPriceDecisionContext is persisted into ozon_price_changes.decision_context.
type ozonPriceDecisionContext struct {
	ActorType     string  `json:"actor_type"`
	StrategyID    string  `json:"strategy_id,omitempty"`
	StrategyType  string  `json:"strategy_type,omitempty"`
	Direction     string  `json:"direction,omitempty"`
	Reason        string  `json:"reason,omitempty"`
	FloorRub      float64 `json:"floor_rub,omitempty"`
	CompetitorRub float64 `json:"competitor_min_rub,omitempty"`
	UserID        string  `json:"user_id,omitempty"`
	RollbackOf    string  `json:"rollback_of,omitempty"`
	ScheduleID    string  `json:"schedule_id,omitempty"`
}

// ozonPriceIntent is one pending live write of the auto path.
type ozonPriceIntent struct {
	ChangeID    pgtype.UUID
	SKU         int64
	NewPriceRub float64
	OldPriceRub *float64
	MinPriceRub *float64
}

// RunForWorkspace evaluates every active ozon_price_* strategy of the
// workspace and records decisions in ozon_price_changes: dry_run strategies
// write shadow rows, auto strategies apply through import/prices. Returns the
// number of decisions recorded.
func (s *OzonRepricerService) RunForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	rows, err := s.queries.ListActiveStrategiesByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return 0, fmt.Errorf("list active strategies: %w", err)
	}
	byCabinet := map[uuid.UUID][]domain.Strategy{}
	for _, row := range rows {
		strategy := strategyFromSqlc(row)
		if !domain.IsOzonPriceStrategy(strategy.Type) {
			continue
		}
		byCabinet[strategy.SellerCabinetID] = append(byCabinet[strategy.SellerCabinetID], strategy)
	}
	if len(byCabinet) == 0 {
		return 0, nil
	}

	written := 0
	var runErrors []error
	for cabinetID, strategies := range byCabinet {
		count, cabErr := s.runCabinet(ctx, workspaceID, cabinetID, strategies)
		written += count
		if cabErr != nil {
			s.logger.Error().Err(cabErr).Str("cabinet_id", cabinetID.String()).Msg("ozon repricer cabinet run failed")
			runErrors = append(runErrors, fmt.Errorf("cabinet %s: %w", cabinetID, cabErr))
		}
	}
	return written, errors.Join(runErrors...)
}

func (s *OzonRepricerService) runCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, strategies []domain.Strategy) (int, error) {
	cabinet, creds, err := s.resolveCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return 0, err
	}
	// Freeze switch: a paused cabinet skips the whole strategy sweep (manual
	// writes stay available — the pause governs automation only).
	if cabinet.RepricerPausedUntil != nil && cabinet.RepricerPausedUntil.After(time.Now()) {
		s.logger.Info().Str("cabinet_id", cabinet.ID.String()).Time("paused_until", *cabinet.RepricerPausedUntil).Msg("ozon repricer paused, skipping cabinet")
		return 0, nil
	}

	prices, err := s.loadCabinetPrices(ctx, cabinet.ID)
	if err != nil {
		return 0, err
	}
	if len(prices) == 0 {
		return 0, nil
	}
	economics, err := s.loadCabinetEconomics(ctx, cabinet.ID)
	if err != nil {
		return 0, err
	}
	aux, err := s.loadStrategyAux(ctx, cabinet.ID, strategies)
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	written := 0
	decided := map[int64]struct{}{} // first strategy to decide a SKU wins
	var intents []ozonPriceIntent

	for _, strategy := range strategies {
		params := strategy.Params.MergedPriceParams()
		auto := params.PriceApplyMode == domain.PriceApplyModeAuto

		for _, row := range prices {
			if _, done := decided[row.Sku]; done {
				continue
			}
			decision, ok := s.evaluateProduct(ctx, cabinet.ID, strategy, params, row, economics, aux, now)
			if !ok {
				continue
			}
			decided[row.Sku] = struct{}{}

			status := domain.OzonPriceStatusShadow
			if auto {
				status = domain.OzonPriceStatusPending
			}
			contextJSON, _ := json.Marshal(ozonPriceDecisionContext{
				ActorType:     "strategy",
				StrategyID:    strategy.ID.String(),
				StrategyType:  strategy.Type,
				Direction:     decision.Direction,
				Reason:        decision.Reason,
				FloorRub:      decision.FloorRub,
				CompetitorRub: decision.CompetitorRub,
			})
			strategyID := strategy.ID
			change, insertErr := s.queries.InsertOzonPriceChange(ctx, sqlcgen.InsertOzonPriceChangeParams{
				SellerCabinetID: uuidToPgtype(cabinet.ID),
				Sku:             row.Sku,
				OfferID:         row.OfferID,
				OldPriceRub:     row.PriceRub,
				NewPriceRub:     floatToPgNumeric(decision.NewPriceRub),
				FloorRub:        floatToPgNumeric(decision.FloorRub),
				Reason:          textToPgtype(decision.Reason),
				Source:          domain.PriceSourceStrategy,
				StrategyID:      uuidToPgtypePtr(&strategyID),
				Status:          status,
				DecisionContext: contextJSON,
			})
			if insertErr != nil {
				return written, fmt.Errorf("record price change for sku %d: %w", row.Sku, insertErr)
			}
			written++
			if auto {
				intents = append(intents, ozonPriceIntent{
					ChangeID:    change.ID,
					SKU:         row.Sku,
					NewPriceRub: decision.NewPriceRub,
				})
			}
		}
	}

	if len(intents) > 0 {
		if err := s.applyIntents(ctx, cabinet.ID, creds, intents); err != nil {
			return written, err
		}
	}
	return written, nil
}

// evaluateProduct runs guardrails and the strategy engine for one price row.
// Returns (decision, true) only when a change should be recorded.
func (s *OzonRepricerService) evaluateProduct(ctx context.Context, cabinetID uuid.UUID, strategy domain.Strategy, params domain.StrategyParams, row sqlcgen.OzonProductPrice, economics map[string]sqlcgen.OzonProductEconomic, aux ozonStrategyAux, now time.Time) (ozonPriceDecision, bool) {
	current := pgNumericToFloat(row.PriceRub)
	if current <= 0 {
		return ozonPriceDecision{}, false
	}
	if !row.SyncedAt.Valid || now.Sub(row.SyncedAt.Time) > maxOzonRepricerPriceAge {
		return ozonPriceDecision{}, false
	}

	// Cost source: Ozon's own net_price, else the Sellico unit-economics
	// mirror keyed by offer_id (себестоимость из юнит-экономики Sellico).
	var econ *sqlcgen.OzonProductEconomic
	if row.OfferID.Valid {
		if e, ok := economics[row.OfferID.String]; ok {
			econ = &e
		}
	}
	floor, floorReason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:         ozonEffectiveNetPrice(pgNumericToFloat(row.NetPriceRub), econ),
		CommissionFBOPct:    pgNumericToFloat(row.CommissionFboPct),
		CommissionFBSPct:    pgNumericToFloat(row.CommissionFbsPct),
		AcquiringRub:        pgNumericToFloat(row.AcquiringPct),
		TargetMarginPercent: params.TargetMarginPercent,
		CurrentPriceRub:     current,
		MaxDiscountPercent:  params.MaxDiscountPercent,
	})
	if floorReason != "" {
		return ozonPriceDecision{}, false
	}

	var decision ozonPriceDecision
	switch strategy.Type {
	case domain.StrategyTypeOzonPriceMarginFloor:
		decision = decideOzonMarginFloor(current, floor)
	case domain.StrategyTypeOzonPriceCompetitorFollow:
		decision = decideOzonCompetitorFollow(current, floor, ozonCompetitorMin(row), params.UndercutPercent, params.StepPercent)
	case domain.StrategyTypeOzonPriceInventoryDemand:
		stock, stockKnown := aux.presentByProductID[row.Sku]
		decision = decideOzonInventoryDemand(current, floor, stock, stockKnown, aux.velocityForProduct(row.Sku), strategy.Params)
	case domain.StrategyTypeOzonPricePeakHours:
		intensity, hasDemandData := aux.peakIntensityForProduct(row.Sku)
		decision = decideOzonPeakHours(current, floor, intensity, hasDemandData, strategy.Params)
	case domain.StrategyTypeOzonPriceAdLinked:
		salesSKU := aux.salesSKUByProductID[row.Sku]
		var maxDRRFallback *float64
		if econ != nil && econ.MaxAllowedDrr.Valid {
			maxDRRFallback = pgNumericToFloatPtr(econ.MaxAllowedDrr)
		}
		var drrPtr *float64
		if drr, ok := aux.drrBySalesSKU[salesSKU]; ok {
			drrValue := drr
			drrPtr = &drrValue
		}
		decision = decideOzonAdLinked(current, floor, drrPtr, aux.spendBySalesSKU[salesSKU], maxDRRFallback, strategy.Params)
	default:
		return ozonPriceDecision{}, false
	}
	if !decision.ShouldChange {
		return ozonPriceDecision{}, false
	}
	// Belt-and-braces magnitude clamp on top of the engine caps.
	if math.Abs(decision.NewPriceRub-current) < minOzonPriceDeltaRub {
		return ozonPriceDecision{}, false
	}

	// Guardrails backed by ozon_price_changes history.
	if params.PriceCooldownHours > 0 {
		since := now.Add(-time.Duration(params.PriceCooldownHours) * time.Hour)
		if n, err := s.queries.CountOzonStrategyDecisionsBySkuSince(ctx, sqlcgen.CountOzonStrategyDecisionsBySkuSinceParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Sku:             row.Sku,
			Since:           pgtype.Timestamptz{Time: since, Valid: true},
		}); err != nil || n > 0 {
			return ozonPriceDecision{}, false
		}
	}
	dayStart := now.Truncate(24 * time.Hour)
	if n, err := s.queries.CountOzonStrategyDecisionsBySkuSince(ctx, sqlcgen.CountOzonStrategyDecisionsBySkuSinceParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Sku:             row.Sku,
		Since:           pgtype.Timestamptz{Time: dayStart, Valid: true},
	}); err != nil || n >= int64(params.MaxPriceChangesPerDay) {
		return ozonPriceDecision{}, false
	}
	if reason := s.hourlyWriteGuard(ctx, cabinetID, row.Sku, ozonStrategyHourlyWriteCap, now); reason != "" {
		return ozonPriceDecision{}, false
	}
	return decision, true
}

// ozonVelocityLookbackDays is the shared sales-velocity / ad-ДРР window for
// the inventory-demand and ad-linked strategies. ponytail: mirrors the WB
// repricer's shared window (priceVelocityLookbackDays), not each strategy's
// own LookbackDays; per-strategy windows can be added if needed.
const ozonVelocityLookbackDays = 14

// ozonStrategyAux is the extra per-cabinet data the inventory-demand and
// ad-linked strategies need on top of the price mirror. Maps stay empty when
// no strategy of the cabinet needs them.
type ozonStrategyAux struct {
	// presentByProductID: available stock summed over schemes, keyed by
	// product_id. A missing key means "stock unknown" → the engine skips.
	presentByProductID map[int64]int64
	// salesSKUByProductID bridges the two Ozon key spaces: prices/stocks are
	// keyed by product_id, analytics/campaigns by the sales SKU.
	salesSKUByProductID map[int64]int64
	// velocityBySalesSKU: avg ordered units/day over the lookback window.
	velocityBySalesSKU map[int64]float64
	// drrBySalesSKU: ad ДРР % (spend/revenue×100) over the lookback window;
	// only present when the SKU had both spend and revenue.
	drrBySalesSKU map[int64]float64
	// spendBySalesSKU: ad spend rubles over the window (ad-linked only acts
	// on SKUs with spend > 0).
	spendBySalesSKU map[int64]float64
	// peakIntensityBySalesSKU: current MSK-slot demand intensity (0..1) and
	// total window orders per sales SKU (ozon_price_peak_hours).
	peakIntensityBySalesSKU map[int64]sqlcgen.OzonSlotIntensity
	// peakCabinetIntensity: cabinet-aggregated slot intensity, the fallback
	// for SKUs with fewer than ozonPeakHoursMinSKUOrders orders in the window.
	peakCabinetIntensity float64
	peakCabinetHasData   bool
}

// ozonPeakHoursMinSKUOrders: below this many orders in the 28-day window a
// SKU's own heatmap is noise — the cabinet-aggregated matrix decides instead.
const ozonPeakHoursMinSKUOrders = 20

// peakIntensityForProduct resolves the demand intensity for a price row via
// the sales-SKU mapping: the SKU's own slot intensity when it has enough
// orders, the cabinet aggregate otherwise. ok=false → no demand data at all.
func (a ozonStrategyAux) peakIntensityForProduct(productID int64) (float64, bool) {
	if slot, ok := a.peakIntensityBySalesSKU[a.salesSKUByProductID[productID]]; ok && slot.TotalOrders >= ozonPeakHoursMinSKUOrders {
		return slot.Intensity, true
	}
	if a.peakCabinetHasData {
		return a.peakCabinetIntensity, true
	}
	return 0, false
}

// velocityForProduct resolves units/day for a price row via the sales-SKU
// mapping (0 = no sales in the window).
func (a ozonStrategyAux) velocityForProduct(productID int64) float64 {
	return a.velocityBySalesSKU[a.salesSKUByProductID[productID]]
}

// loadStrategyAux loads stocks/velocity/ДРР only when a strategy of the
// cabinet actually needs them.
func (s *OzonRepricerService) loadStrategyAux(ctx context.Context, cabinetID uuid.UUID, strategies []domain.Strategy) (ozonStrategyAux, error) {
	aux := ozonStrategyAux{
		presentByProductID:      map[int64]int64{},
		salesSKUByProductID:     map[int64]int64{},
		velocityBySalesSKU:      map[int64]float64{},
		drrBySalesSKU:           map[int64]float64{},
		spendBySalesSKU:         map[int64]float64{},
		peakIntensityBySalesSKU: map[int64]sqlcgen.OzonSlotIntensity{},
	}
	needsInventory, needsAd, needsPeak := false, false, false
	for _, strategy := range strategies {
		switch strategy.Type {
		case domain.StrategyTypeOzonPriceInventoryDemand:
			needsInventory = true
		case domain.StrategyTypeOzonPriceAdLinked:
			needsAd = true
		case domain.StrategyTypeOzonPricePeakHours:
			needsPeak = true
		}
	}
	if !needsInventory && !needsAd && !needsPeak {
		return aux, nil
	}

	// product_id → sales SKU bridge (both strategies key their signals by
	// the sales SKU except stocks, which arrive by product_id).
	productRows, err := s.queries.ListOzonProductsByCabinet(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return aux, fmt.Errorf("list product mapping: %w", err)
	}
	for _, row := range productRows {
		if row.Sku.Valid && row.Sku.Int64 != 0 {
			aux.salesSKUByProductID[row.ProductID] = row.Sku.Int64
		}
	}

	since := time.Now().UTC().AddDate(0, 0, -ozonVelocityLookbackDays)
	if needsInventory {
		stockRows, err := s.queries.ListOzonProductStocksByCabinet(ctx, uuidToPgtype(cabinetID))
		if err != nil {
			return aux, fmt.Errorf("list stocks: %w", err)
		}
		for _, row := range stockRows {
			aux.presentByProductID[row.Sku] = int64(row.Present)
		}
		salesRows, err := s.queries.OzonSalesVelocityByCabinet(ctx, sqlcgen.OzonSalesVelocityByCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Date:            pgtype.Date{Time: since, Valid: true},
		})
		if err != nil {
			return aux, fmt.Errorf("aggregate sales velocity: %w", err)
		}
		for _, row := range salesRows {
			aux.velocityBySalesSKU[row.Sku] = float64(row.Units) / float64(ozonVelocityLookbackDays)
		}
	}
	if needsPeak {
		// Current MSK time slot, the same convention as the heatmap sync
		// (dow 0=Пн..6=Вс) and the WB peak-hours precedent.
		dow, hour := ozonMSKSlot(time.Now())
		slotIntensities, err := s.queries.OzonSlotIntensities(ctx, uuidToPgtype(cabinetID), dow, hour)
		if err != nil {
			return aux, fmt.Errorf("load slot intensities: %w", err)
		}
		aux.peakIntensityBySalesSKU = slotIntensities
		cabinetIntensity, hasData, err := s.queries.OzonCabinetSlotIntensity(ctx, uuidToPgtype(cabinetID), dow, hour)
		if err != nil {
			return aux, fmt.Errorf("load cabinet slot intensity: %w", err)
		}
		aux.peakCabinetIntensity, aux.peakCabinetHasData = cabinetIntensity, hasData
	}
	if needsAd {
		adRows, err := s.queries.OzonSkuAdSpendByCabinet(ctx, sqlcgen.OzonSkuAdSpendByCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Date:            pgtype.Date{Time: since, Valid: true},
		})
		if err != nil {
			return aux, fmt.Errorf("aggregate ad spend: %w", err)
		}
		for _, row := range adRows {
			spend := pgNumericToFloat(row.SpendRub)
			if spend <= 0 {
				continue
			}
			aux.spendBySalesSKU[row.Sku] = spend
			if revenue := pgNumericToFloat(row.RevenueRub); revenue > 0 {
				aux.drrBySalesSKU[row.Sku] = spend / revenue * 100
			}
		}
	}
	return aux, nil
}

// --- inventory-demand + ad-linked wrappers over the shared pure engine ---

// ozonEngineParams adapts strategy params for the WB pure engine: the
// precomputed Ozon floor is injected as MinPriceRub so resolveFloor lands on
// it (Ozon economics never route through ComputeMinEffectivePrice).
func ozonEngineParams(params domain.StrategyParams, floorRub float64) domain.StrategyParams {
	if floorRub > 0 {
		floorInt := int64(math.Ceil(floorRub))
		if params.MinPriceRub == nil || *params.MinPriceRub < floorInt {
			params.MinPriceRub = &floorInt
		}
	}
	return params
}

// ozonDecisionFromEngine converts a WB engine verdict back to the Ozon shape.
// The engine works in whole rubles (discount 0 → base == effective), so
// TargetEffectiveRub is the Ozon target price directly.
func ozonDecisionFromEngine(d PriceDecision, floorRub float64) ozonPriceDecision {
	if !d.ShouldChange {
		return ozonPriceDecision{
			Direction:  "none",
			Reason:     d.Reason,
			SkipReason: d.SkipReason,
			FloorRub:   floorRub,
		}
	}
	target := float64(d.TargetEffectiveRub)
	if floorRub > 0 && target < floorRub {
		target = floorRub // belt-and-braces: ceil rounding already guarantees this
	}
	return ozonPriceDecision{
		ShouldChange: true,
		NewPriceRub:  roundRub(target),
		FloorRub:     floorRub,
		Direction:    d.Direction,
		Reason:       d.Reason,
	}
}

// decideOzonInventoryDemand runs «Разгрузка склада» for one Ozon product:
// overstock + slow sales → step down (clamped to the floor), low stock +
// active sales → step up (requires max_price_rub). Reuses the pure WB
// DecideInventoryDemand engine.
func decideOzonInventoryDemand(currentRub, floorRub float64, stock int64, stockKnown bool, salesPerDay float64, params domain.StrategyParams) ozonPriceDecision {
	if currentRub <= 0 {
		return ozonSkip("invalid_current_price")
	}
	in := PriceEngineInputs{
		Current:          domain.ProductPrice{PriceRub: int64(math.Round(currentRub))},
		Stock:            stock,
		StockKnown:       stockKnown,
		SalesUnitsPerDay: salesPerDay,
	}
	return ozonDecisionFromEngine(DecideInventoryDemand(in, ozonEngineParams(params, floorRub)), floorRub)
}

// decideOzonAdLinked runs «Реклама → цена» for one Ozon product: when the ad
// ДРР exceeds the allowed ceiling (strategy param first, Sellico economics
// max_allowed_drr as fallback), price steps down toward the floor. Only SKUs
// with ad spend > 0 in the window are considered. Reuses the pure WB
// DecideAdLinked engine.
func decideOzonAdLinked(currentRub, floorRub float64, drr *float64, spendRub float64, maxDRRFallback *float64, params domain.StrategyParams) ozonPriceDecision {
	if currentRub <= 0 {
		return ozonSkip("invalid_current_price")
	}
	if spendRub <= 0 {
		return ozonSkip("no_ad_spend")
	}
	in := PriceEngineInputs{
		Current:            domain.ProductPrice{PriceRub: int64(math.Round(currentRub))},
		DRR:                drr,
		HasActiveCampaigns: true,
	}
	in.Economics.MaxAllowedDRR = maxDRRFallback
	return ozonDecisionFromEngine(DecideAdLinked(in, ozonEngineParams(params, floorRub)), floorRub)
}

// decideOzonPeakHours runs «Цена по спросу» for one Ozon product: the target
// sits between current×(1−dead%) at a dead hour and current×(1+peak%) at a
// demand peak, interpolated by the current MSK slot's order intensity from
// ozon_orders_hourly. Reuses the pure WB DecidePeakHours engine (floor clamp,
// ±step% per-run cap, 1% dead-band, max_price required for auto raises).
func decideOzonPeakHours(currentRub, floorRub, intensity float64, hasDemandData bool, params domain.StrategyParams) ozonPriceDecision {
	if currentRub <= 0 {
		return ozonSkip("invalid_current_price")
	}
	if !hasDemandData {
		return ozonSkip("no_demand_data")
	}
	in := PriceEngineInputs{
		Current: domain.ProductPrice{PriceRub: int64(math.Round(currentRub))},
	}
	return ozonDecisionFromEngine(DecidePeakHours(in, ozonEngineParams(params, floorRub), intensity), floorRub)
}

// ozonCompetitorMin picks the lowest present competitor price: other sellers
// on Ozon and external marketplaces (the seller's own other listings are
// informational, not competition).
func ozonCompetitorMin(row sqlcgen.OzonProductPrice) float64 {
	min := 0.0
	for _, candidate := range []float64{
		pgNumericToFloat(row.OzonIndexMinPriceRub),
		pgNumericToFloat(row.ExternalIndexMinPriceRub),
	} {
		if candidate > 0 && (min == 0 || candidate < min) {
			min = candidate
		}
	}
	return min
}

// hourlyWriteGuard enforces the per-product hourly write budget (Ozon's hard
// limit is 10/hour). Returns a non-empty reason when blocked.
func (s *OzonRepricerService) hourlyWriteGuard(ctx context.Context, cabinetID uuid.UUID, sku int64, budget int64, now time.Time) string {
	n, err := s.queries.CountOzonPriceWritesBySkuSince(ctx, sqlcgen.CountOzonPriceWritesBySkuSinceParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Sku:             sku,
		Since:           pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
	})
	if err != nil {
		return "hourly write count unavailable"
	}
	return ozonHourlyGuardReason(n, budget)
}

// ozonHourlyGuardReason is the pure hourly-budget check: blocks when the
// product already used its per-hour write budget (Ozon hard limit: 10/hour).
func ozonHourlyGuardReason(writesThisHour, budget int64) string {
	if writesThisHour >= budget {
		return fmt.Sprintf("hourly price change limit reached (%d/%d)", writesThisHour, budget)
	}
	return ""
}

// applyIntents ships pending intents through import/prices (chunked inside
// the client), finalizes the audit rows per item and mirrors successes.
func (s *OzonRepricerService) applyIntents(ctx context.Context, cabinetID uuid.UUID, creds ozon.Credentials, intents []ozonPriceIntent) error {
	updates := make([]ozon.PriceUpdate, 0, len(intents))
	for _, intent := range intents {
		updates = append(updates, ozon.PriceUpdate{
			ProductID:   intent.SKU,
			PriceRub:    intent.NewPriceRub,
			OldPriceRub: intent.OldPriceRub,
			MinPriceRub: intent.MinPriceRub,
		})
	}
	results, writeErr := s.sellerClient.UpdatePrices(ctx, creds, updates)

	outcomeBySKU := map[int64]ozon.PriceUpdateResult{}
	for _, result := range results {
		outcomeBySKU[result.ProductID] = result
	}

	var failed int
	for _, intent := range intents {
		status := domain.OzonPriceStatusApplied
		var errText pgtype.Text
		if outcome, ok := outcomeBySKU[intent.SKU]; ok {
			if !outcome.Updated {
				status = domain.OzonPriceStatusFailed
				message := "ozon rejected the price update"
				if len(outcome.Errors) > 0 {
					message = truncateError(fmt.Sprintf("%v", outcome.Errors))
				}
				errText = textToPgtype(message)
			}
		} else {
			// No per-item outcome — the request carrying it failed in transit.
			status = domain.OzonPriceStatusFailed
			message := "no result returned by ozon"
			if writeErr != nil {
				message = truncateError(writeErr.Error())
			}
			errText = textToPgtype(message)
		}

		if markErr := s.queries.MarkOzonPriceChangeResult(ctx, sqlcgen.MarkOzonPriceChangeResultParams{
			ID: intent.ChangeID, Status: status, Error: errText,
		}); markErr != nil {
			s.logger.Warn().Err(markErr).Int64("sku", intent.SKU).Msg("failed to finalize ozon price change")
		}
		if status == domain.OzonPriceStatusApplied {
			if mirrorErr := s.queries.UpdateOzonProductPriceMirror(ctx, sqlcgen.UpdateOzonProductPriceMirrorParams{
				SellerCabinetID: uuidToPgtype(cabinetID),
				Sku:             intent.SKU,
				PriceRub:        floatToPgNumeric(intent.NewPriceRub),
				OldPriceRub:     floatPtrToPgNumeric(intent.OldPriceRub),
				MinPriceRub:     floatPtrToPgNumeric(intent.MinPriceRub),
			}); mirrorErr != nil {
				s.logger.Warn().Err(mirrorErr).Int64("sku", intent.SKU).Msg("failed to mirror applied ozon price")
			}
		} else {
			failed++
		}
	}

	s.logger.Info().
		Str("cabinet_id", cabinetID.String()).
		Int("applied", len(intents)-failed).
		Int("failed", failed).
		Msg("ozon repricer prices applied")
	if writeErr != nil {
		return fmt.Errorf("ozon import/prices: %w", writeErr)
	}
	return nil
}

// --- manual bulk ---

// ApplyManual writes prices supplied by a user. It bypasses the strategy
// cooldown (an explicit human decision overrides pacing) but never the hard
// hourly Ozon budget. Every item is audited with source='manual'.
func (s *OzonRepricerService) ApplyManual(ctx context.Context, workspaceID, cabinetID uuid.UUID, items []OzonManualPriceInput, userID uuid.UUID) ([]domain.OzonPriceChange, error) {
	if len(items) == 0 {
		return nil, apperror.New(apperror.ErrValidation, "items are required")
	}
	if len(items) > ozonManualPriceItemsLimit {
		return nil, apperror.New(apperror.ErrValidation, fmt.Sprintf("at most %d items per request", ozonManualPriceItemsLimit))
	}
	for _, item := range items {
		if item.SKU <= 0 || item.PriceRub <= 0 {
			return nil, apperror.New(apperror.ErrValidation, "every item needs a positive sku and price_rub")
		}
		if item.OldPriceRub != nil && *item.OldPriceRub < 0 {
			return nil, apperror.New(apperror.ErrValidation, "old_price_rub must be non-negative")
		}
		if item.MinPriceRub != nil && *item.MinPriceRub < 0 {
			return nil, apperror.New(apperror.ErrValidation, "min_price_rub must be non-negative")
		}
	}
	cabinet, creds, err := s.resolveCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	contextJSON, _ := json.Marshal(ozonPriceDecisionContext{ActorType: "manual", UserID: userID.String()})

	result := make([]domain.OzonPriceChange, 0, len(items))
	var intents []ozonPriceIntent
	for _, item := range items {
		var oldPrice, oldOldPrice pgtype.Numeric
		var offerID pgtype.Text
		if mirror, mirrorErr := s.queries.GetOzonProductPriceBySku(ctx, sqlcgen.GetOzonProductPriceBySkuParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID), Sku: item.SKU,
		}); mirrorErr == nil {
			oldPrice = mirror.PriceRub
			oldOldPrice = mirror.OldPriceRub
			offerID = mirror.OfferID
		}

		blocked := s.hourlyWriteGuard(ctx, cabinet.ID, item.SKU, ozonHardHourlyWriteCap, now)
		status := domain.OzonPriceStatusPending
		if blocked != "" {
			status = domain.OzonPriceStatusFailed
		}
		change, insertErr := s.queries.InsertOzonPriceChange(ctx, sqlcgen.InsertOzonPriceChangeParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			Sku:             item.SKU,
			OfferID:         offerID,
			OldPriceRub:     oldPrice,
			NewPriceRub:     floatToPgNumeric(item.PriceRub),
			OldOldPriceRub:  oldOldPrice,
			NewOldPriceRub:  floatPtrToPgNumeric(item.OldPriceRub),
			MinPriceRub:     floatPtrToPgNumeric(item.MinPriceRub),
			Reason:          textToPgtype("manual price update"),
			Source:          domain.PriceSourceManual,
			Status:          status,
			DecisionContext: contextJSON,
		})
		if insertErr != nil {
			return nil, fmt.Errorf("record manual price change for sku %d: %w", item.SKU, insertErr)
		}
		if blocked != "" {
			if markErr := s.queries.MarkOzonPriceChangeResult(ctx, sqlcgen.MarkOzonPriceChangeResultParams{
				ID: change.ID, Status: domain.OzonPriceStatusFailed, Error: textToPgtype(blocked),
			}); markErr != nil {
				s.logger.Warn().Err(markErr).Int64("sku", item.SKU).Msg("failed to finalize blocked manual price change")
			}
			domainChange := ozonPriceChangeFromSqlc(change)
			domainChange.Status = domain.OzonPriceStatusFailed
			domainChange.Error = blocked
			result = append(result, domainChange)
			continue
		}
		result = append(result, ozonPriceChangeFromSqlc(change))
		intents = append(intents, ozonPriceIntent{
			ChangeID:    change.ID,
			SKU:         item.SKU,
			NewPriceRub: item.PriceRub,
			OldPriceRub: item.OldPriceRub,
			MinPriceRub: item.MinPriceRub,
		})
	}

	var applyErr error
	if len(intents) > 0 {
		applyErr = s.applyIntents(ctx, cabinet.ID, creds, intents)
	}
	// Refresh statuses on the returned rows (applyIntents finalized them).
	for i := range result {
		if result[i].Status != domain.OzonPriceStatusPending {
			continue
		}
		if fresh, freshErr := s.queries.GetOzonPriceChangeByID(ctx, uuidToPgtype(result[i].ID)); freshErr == nil {
			result[i] = ozonPriceChangeFromSqlc(fresh)
		}
	}
	return result, applyErr
}

// --- rollback ---

// Rollback restores the pre-change price of one applied change. It verifies
// the current mirror price still equals the change's new price (a later write
// means the rollback target is gone), writes the old price back through the
// audited manual path, and marks the original row rolled_back.
func (s *OzonRepricerService) Rollback(ctx context.Context, workspaceID uuid.UUID, changeID uuid.UUID) (*domain.OzonPriceChange, error) {
	row, err := s.queries.GetOzonPriceChangeByID(ctx, uuidToPgtype(changeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "price change not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load price change: %w", err)
	}
	cabinet, creds, err := s.resolveCabinet(ctx, workspaceID, uuidFromPgtype(row.SellerCabinetID))
	if err != nil {
		if apperror.Is(err, apperror.ErrNotFound) {
			return nil, apperror.New(apperror.ErrNotFound, "price change not found")
		}
		return nil, err
	}
	if row.Status != domain.OzonPriceStatusApplied {
		return nil, apperror.New(apperror.ErrValidation, "only applied price changes can be rolled back")
	}
	oldPrice := pgNumericToFloat(row.OldPriceRub)
	if oldPrice <= 0 {
		return nil, apperror.New(apperror.ErrValidation, "price change has no previous price to restore")
	}
	newPrice := pgNumericToFloat(row.NewPriceRub)

	// Fresh-state check against the local mirror: a later write (sync keeps
	// the mirror current hourly) means this change is no longer the live price.
	mirror, err := s.queries.GetOzonProductPriceBySku(ctx, sqlcgen.GetOzonProductPriceBySkuParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID), Sku: row.Sku,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "product price mirror is missing; sync the cabinet first")
	}
	if math.Abs(pgNumericToFloat(mirror.PriceRub)-newPrice) > 0.01 {
		return nil, apperror.New(apperror.ErrValidation, "current price no longer matches this change; rollback aborted")
	}

	now := time.Now().UTC()
	if blocked := s.hourlyWriteGuard(ctx, cabinet.ID, row.Sku, ozonHardHourlyWriteCap, now); blocked != "" {
		return nil, apperror.New(apperror.ErrRateLimited, blocked)
	}

	contextJSON, _ := json.Marshal(ozonPriceDecisionContext{
		ActorType:  "manual",
		Reason:     "rollback",
		RollbackOf: uuidFromPgtype(row.ID).String(),
	})
	var oldOldPtr *float64
	if row.OldOldPriceRub.Valid {
		value := pgNumericToFloat(row.OldOldPriceRub)
		oldOldPtr = &value
	}
	rollbackRow, err := s.queries.InsertOzonPriceChange(ctx, sqlcgen.InsertOzonPriceChangeParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID),
		Sku:             row.Sku,
		OfferID:         row.OfferID,
		OldPriceRub:     row.NewPriceRub,
		NewPriceRub:     row.OldPriceRub,
		OldOldPriceRub:  row.NewOldPriceRub,
		NewOldPriceRub:  row.OldOldPriceRub,
		Reason:          textToPgtype(fmt.Sprintf("rollback of %s", uuidFromPgtype(row.ID))),
		Source:          domain.PriceSourceManual,
		Status:          domain.OzonPriceStatusPending,
		DecisionContext: contextJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("record rollback change: %w", err)
	}

	if applyErr := s.applyIntents(ctx, cabinet.ID, creds, []ozonPriceIntent{{
		ChangeID:    rollbackRow.ID,
		SKU:         row.Sku,
		NewPriceRub: oldPrice,
		OldPriceRub: oldOldPtr,
	}}); applyErr != nil {
		return nil, applyErr
	}
	fresh, err := s.queries.GetOzonPriceChangeByID(ctx, rollbackRow.ID)
	if err != nil {
		return nil, fmt.Errorf("reload rollback change: %w", err)
	}
	if fresh.Status != domain.OzonPriceStatusApplied {
		change := ozonPriceChangeFromSqlc(fresh)
		return &change, apperror.New(apperror.ErrValidation, fmt.Sprintf("rollback write failed: %s", pgTextValue(fresh.Error)))
	}
	if markErr := s.queries.MarkOzonPriceChangeResult(ctx, sqlcgen.MarkOzonPriceChangeResultParams{
		ID: row.ID, Status: domain.OzonPriceStatusRolledBack,
	}); markErr != nil {
		s.logger.Warn().Err(markErr).Str("change_id", changeID.String()).Msg("failed to mark price change rolled back")
	}
	change := ozonPriceChangeFromSqlc(fresh)
	return &change, nil
}

// --- listing ---

// ListPriceChanges returns the ozon_price_changes audit page for a cabinet.
func (s *OzonRepricerService) ListPriceChanges(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, limit, offset int32) ([]domain.OzonPriceChange, int64, error) {
	if _, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}
	var skuFilter pgtype.Int8
	if sku != nil {
		skuFilter = pgtype.Int8{Int64: *sku, Valid: true}
	}
	total, err := s.queries.CountOzonPriceChangesByCabinet(ctx, sqlcgen.CountOzonPriceChangesByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Sku:             skuFilter,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count ozon price changes")
	}
	rows, err := s.queries.ListOzonPriceChangesByCabinet(ctx, sqlcgen.ListOzonPriceChangesByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Sku:             skuFilter,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list ozon price changes")
	}
	result := make([]domain.OzonPriceChange, 0, len(rows))
	for _, row := range rows {
		result = append(result, ozonPriceChangeFromSqlc(row))
	}
	return result, total, nil
}

// ResolveWorkspaceCabinet re-exposes the tenancy gate for the HTTP layer
// (the manual repricer-run endpoint validates the cabinet before enqueueing).
func (s *OzonRepricerService) ResolveWorkspaceCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) error {
	_, _, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID)
	return err
}

// --- helpers ---

// resolveCabinet loads + gates the cabinet and returns Seller API credentials
// (required for every write path; the sweep also needs them to run at all —
// prices-only cabinets always have them).
func (s *OzonRepricerService) resolveCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, ozon.Credentials, error) {
	cabinet, creds, err := s.resolveCabinetReadOnly(ctx, workspaceID, cabinetID)
	if err != nil {
		return nil, ozon.Credentials{}, err
	}
	if !creds.HasSellerAPI() {
		return nil, ozon.Credentials{}, apperror.New(apperror.ErrValidation, "seller api credentials are missing for this cabinet")
	}
	return cabinet, ozonClientCreds(creds), nil
}

func (s *OzonRepricerService) resolveCabinetReadOnly(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, domain.OzonCredentials, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.OzonCredentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return nil, domain.OzonCredentials{}, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.Marketplace != domain.MarketplaceOzon || cabinet.WorkspaceID != workspaceID {
		return nil, domain.OzonCredentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return nil, domain.OzonCredentials{}, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	return &cabinet, creds, nil
}

// loadCabinetPrices pages the whole price mirror of one cabinet.
func (s *OzonRepricerService) loadCabinetPrices(ctx context.Context, cabinetID uuid.UUID) ([]sqlcgen.OzonProductPrice, error) {
	var out []sqlcgen.OzonProductPrice
	for offset := int32(0); ; offset += ozonRepricerPageSize {
		rows, err := s.queries.ListOzonProductPrices(ctx, sqlcgen.ListOzonProductPricesParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Limit:           ozonRepricerPageSize,
			Offset:          offset,
		})
		if err != nil {
			return nil, fmt.Errorf("list cabinet prices: %w", err)
		}
		out = append(out, rows...)
		if len(rows) < ozonRepricerPageSize {
			return out, nil
		}
	}
}

// loadCabinetEconomics mirrors the Sellico unit-economics rows of one cabinet
// into a map keyed by offer_id (артикул) for the cost fallback.
func (s *OzonRepricerService) loadCabinetEconomics(ctx context.Context, cabinetID uuid.UUID) (map[string]sqlcgen.OzonProductEconomic, error) {
	rows, err := s.queries.ListOzonProductEconomicsByCabinet(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return nil, fmt.Errorf("list cabinet economics: %w", err)
	}
	out := make(map[string]sqlcgen.OzonProductEconomic, len(rows))
	for _, row := range rows {
		out[row.OfferID] = row
	}
	return out, nil
}

func floatPtrToPgNumeric(value *float64) pgtype.Numeric {
	if value == nil {
		return pgtype.Numeric{}
	}
	return floatToPgNumeric(*value)
}

func ozonPriceChangeFromSqlc(row sqlcgen.OzonPriceChange) domain.OzonPriceChange {
	change := domain.OzonPriceChange{
		ID:              uuidFromPgtype(row.ID),
		SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		SKU:             row.Sku,
		OfferID:         pgTextValue(row.OfferID),
		NewPriceRub:     pgNumericToFloat(row.NewPriceRub),
		OldPriceRub:     pgNumericToFloatPtr(row.OldPriceRub),
		OldOldPriceRub:  pgNumericToFloatPtr(row.OldOldPriceRub),
		NewOldPriceRub:  pgNumericToFloatPtr(row.NewOldPriceRub),
		MinPriceRub:     pgNumericToFloatPtr(row.MinPriceRub),
		FloorRub:        pgNumericToFloatPtr(row.FloorRub),
		Reason:          pgTextValue(row.Reason),
		Source:          row.Source,
		Status:          row.Status,
		Error:           pgTextValue(row.Error),
		CreatedAt:       row.CreatedAt.Time,
	}
	if row.StrategyID.Valid {
		id := uuidFromPgtype(row.StrategyID)
		change.StrategyID = &id
	}
	if row.AppliedAt.Valid {
		appliedAt := row.AppliedAt.Time
		change.AppliedAt = &appliedAt
	}
	if len(row.DecisionContext) > 0 {
		var decisionContext any
		if err := json.Unmarshal(row.DecisionContext, &decisionContext); err == nil {
			change.DecisionContext = decisionContext
		}
	}
	return change
}
