package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	priceVelocityLookbackDays = 14
	economicsPageSize         = 1000
)

// repricerData is the per-workspace snapshot the engine reads.
type repricerData struct {
	pricesByNm        map[int64]domain.ProductPrice
	economicsByNm     map[int64]domain.ProductEconomics
	stockByNm         map[int64]stockInfo
	velocityByNm      map[int64]float64 // units/day over the lookback window
	nmByProductID     map[uuid.UUID]int64
	activeQuarNm      map[int64]struct{}
	hasActiveCampaign map[int64]bool
}

type stockInfo struct {
	units int64
	known bool
}

// RunForWorkspace evaluates active price strategies for a workspace and records
// recommended price changes. dry_run strategies stop at recommendations; auto
// strategies are applied by the worker apply path (Phase 5). Returns the number
// of recommendations written.
func (s *RepricerService) RunForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	if s.strategies == nil || s.engine == nil {
		return 0, nil
	}
	active, err := s.strategies.ListActive(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	priceStrategies := make([]domain.Strategy, 0, len(active))
	for _, st := range active {
		if domain.IsPriceStrategy(st.Type) {
			priceStrategies = append(priceStrategies, st)
		}
	}
	if len(priceStrategies) == 0 {
		return 0, nil
	}

	// Freshest possible baseline before deciding.
	if _, syncErr := s.SyncPrices(ctx, workspaceID); syncErr != nil {
		s.logger.Warn().Err(syncErr).Str("workspace_id", workspaceID.String()).Msg("price sync before run failed")
	}

	data, err := s.loadRepricerData(ctx, workspaceID, priceStrategies)
	if err != nil {
		return 0, err
	}

	written := 0
	var autoIntents []priceChangeIntent
	for _, st := range priceStrategies {
		params := st.Params.MergedPriceParams()
		auto := params.PriceApplyMode == domain.PriceApplyModeAuto
		for _, nm := range s.targetNmIDs(st, data) {
			decision, ctxInfo, ok := s.evaluateProduct(ctx, workspaceID, st, params, nm, data)
			if !ok {
				continue
			}
			if auto {
				autoIntents = append(autoIntents, s.intentFromDecision(st, nm, decision, ctxInfo, data))
				written++
				continue
			}
			if err := s.recordRecommendation(ctx, workspaceID, st, nm, decision, ctxInfo, data); err != nil {
				s.logger.Warn().Err(err).Int64("wb_product_id", nm).Msg("failed to record price recommendation")
				continue
			}
			written++
		}
	}
	if len(autoIntents) > 0 {
		if _, err := s.applyIntents(ctx, workspaceID, autoIntents); err != nil {
			return written, err
		}
	}
	return written, nil
}

// intentFromDecision builds an apply intent for an auto-mode strategy decision.
func (s *RepricerService) intentFromDecision(st domain.Strategy, nm int64, decision PriceDecision, dctx *domain.PriceChangeDecisionContext, data *repricerData) priceChangeIntent {
	price := data.pricesByNm[nm]
	strategyID := st.ID
	return priceChangeIntent{
		CabinetID:       st.SellerCabinetID,
		NmID:            nm,
		OldPriceRub:     price.PriceRub,
		NewPriceRub:     decision.NewPriceRub,
		OldDiscount:     price.DiscountPercent,
		NewDiscount:     decision.NewDiscountPercent,
		MinPriceRub:     decision.MinPriceRub,
		Reason:          decision.Reason,
		Source:          domain.PriceSourceStrategy,
		StrategyID:      &strategyID,
		DecisionContext: dctx,
	}
}

// targetNmIDs resolves the nmIDs a strategy applies to: its product bindings, or
// every synced product in the strategy's cabinet when it has none.
func (s *RepricerService) targetNmIDs(st domain.Strategy, data *repricerData) []int64 {
	bound := make([]int64, 0)
	for _, b := range st.Bindings {
		if b.ProductID == nil {
			continue
		}
		if nm, ok := data.nmByProductID[*b.ProductID]; ok {
			bound = append(bound, nm)
		}
	}
	if len(bound) > 0 {
		return bound
	}
	all := make([]int64, 0, len(data.pricesByNm))
	for nm, p := range data.pricesByNm {
		if p.SellerCabinetID == st.SellerCabinetID {
			all = append(all, nm)
		}
	}
	return all
}

// evaluateProduct runs guardrails then the strategy's engine function.
// Returns (decision, decisionContext, true) only when a change should be recorded.
func (s *RepricerService) evaluateProduct(ctx context.Context, workspaceID uuid.UUID, st domain.Strategy, params domain.StrategyParams, nm int64, data *repricerData) (PriceDecision, *domain.PriceChangeDecisionContext, bool) {
	price, ok := data.pricesByNm[nm]
	if !ok {
		return PriceDecision{}, nil, false
	}
	// Guardrails that short-circuit before the engine.
	if price.EditableSizePrice {
		return PriceDecision{}, nil, false // size-priced categories out of scope in v1
	}
	if _, quarantined := data.activeQuarNm[nm]; quarantined {
		return PriceDecision{}, nil, false
	}
	if inflight, err := s.queries.HasInFlightPriceChange(ctx, uuidToPgtype(workspaceID), nm); err == nil && inflight {
		return PriceDecision{}, nil, false
	}
	// Per-product cooldown.
	cooldownSince := time.Now().UTC().Add(-time.Duration(params.PriceCooldownHours) * time.Hour)
	if n, err := s.queries.CountRecentPriceChangesByProduct(ctx, uuidToPgtype(workspaceID), nm, pgtype.Timestamptz{Time: cooldownSince, Valid: true}); err == nil && n > 0 {
		return PriceDecision{}, nil, false
	}
	// Daily cap.
	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	if n, err := s.queries.CountRecentPriceChangesByProduct(ctx, uuidToPgtype(workspaceID), nm, pgtype.Timestamptz{Time: dayStart, Valid: true}); err == nil && n >= int64(params.MaxPriceChangesPerDay) {
		return PriceDecision{}, nil, false
	}

	econ := data.economicsByNm[nm]
	stock := data.stockByNm[nm]
	in := PriceEngineInputs{
		Current:            price,
		Economics:          econ,
		Stock:              stock.units,
		StockKnown:         stock.known,
		SalesUnitsPerDay:   data.velocityByNm[nm],
		HasActiveCampaigns: data.hasActiveCampaign[nm],
	}

	var decision PriceDecision
	switch st.Type {
	case domain.StrategyTypePriceMarginFloor:
		decision = DecideMarginFloor(in, params)
	case domain.StrategyTypePriceInventoryDemand:
		decision = DecideInventoryDemand(in, params)
	case domain.StrategyTypePriceAdLinked:
		decision = DecideAdLinked(in, params)
	default:
		return PriceDecision{}, nil, false
	}

	if !decision.ShouldChange {
		return PriceDecision{}, nil, false
	}
	// Anti-quarantine belt-and-braces: never let the effective drop ≥3x.
	if decision.Direction == "down" && decision.TargetEffectiveRub*3 <= price.EffectivePriceRub() {
		return PriceDecision{}, nil, false
	}

	minPtr := decision.MinPriceRub
	dctx := &domain.PriceChangeDecisionContext{
		ActorType:    "strategy",
		StrategyType: st.Type,
		Direction:    decision.Direction,
		Reason:       decision.Reason,
		MinPriceRub:  &minPtr,
	}
	return decision, dctx, true
}

func (s *RepricerService) recordRecommendation(ctx context.Context, workspaceID uuid.UUID, st domain.Strategy, nm int64, decision PriceDecision, dctx *domain.PriceChangeDecisionContext, data *repricerData) error {
	price := data.pricesByNm[nm]
	ctxJSON, _ := json.Marshal(dctx)
	strategyID := st.ID
	_, err := s.queries.CreatePriceChange(ctx, sqlcgen.CreatePriceChangeParams{
		WorkspaceID:        uuidToPgtype(workspaceID),
		SellerCabinetID:    uuidToPgtype(st.SellerCabinetID),
		StrategyID:         uuidToPgtypePtr(&strategyID),
		WbProductID:        nm,
		OldPriceRub:        price.PriceRub,
		NewPriceRub:        decision.NewPriceRub,
		OldDiscountPercent: int32(price.DiscountPercent),
		NewDiscountPercent: int32(decision.NewDiscountPercent),
		MinPriceRub:        pgtype.Int8{Int64: decision.MinPriceRub, Valid: decision.MinPriceRub > 0},
		Reason:             decision.Reason,
		Source:             domain.PriceSourceStrategy,
		WbStatus:           domain.PriceStatusRecommended,
		CanRollback:        false, // recommendations aren't rollback-able until applied
		DecisionContext:    ctxJSON,
	})
	return err
}

func (s *RepricerService) loadRepricerData(ctx context.Context, workspaceID uuid.UUID, strategies []domain.Strategy) (*repricerData, error) {
	data := &repricerData{
		pricesByNm:        map[int64]domain.ProductPrice{},
		economicsByNm:     map[int64]domain.ProductEconomics{},
		stockByNm:         map[int64]stockInfo{},
		velocityByNm:      map[int64]float64{},
		nmByProductID:     map[uuid.UUID]int64{},
		activeQuarNm:      map[int64]struct{}{},
		hasActiveCampaign: map[int64]bool{},
	}

	// Cabinets referenced by the strategies.
	cabinets := map[uuid.UUID]struct{}{}
	for _, st := range strategies {
		cabinets[st.SellerCabinetID] = struct{}{}
	}

	// Prices + products (stock, nm↔productID) per cabinet.
	for cabinetID := range cabinets {
		prices, err := s.queries.ListProductPricesByCabinet(ctx, uuidToPgtype(cabinetID))
		if err != nil {
			return nil, err
		}
		for _, row := range prices {
			data.pricesByNm[row.WbProductID] = productPriceFromSqlc(row)
		}
		products, err := s.queries.ListProductsBySellerCabinet(ctx, sqlcgen.ListProductsBySellerCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinetID),
			Limit:           priceListPageSize,
			Offset:          0,
		})
		if err != nil {
			return nil, err
		}
		for _, p := range products {
			data.nmByProductID[uuidFromPgtype(p.ID)] = p.WbProductID
			if p.StockTotal.Valid {
				data.stockByNm[p.WbProductID] = stockInfo{units: int64(p.StockTotal.Int32), known: true}
			}
		}
	}

	// Economics (paginated).
	for offset := int32(0); ; offset += economicsPageSize {
		rows, err := s.queries.ListProductEconomicsByWorkspace(ctx, uuidToPgtype(workspaceID), economicsPageSize, offset)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			data.economicsByNm[row.WbProductID] = productEconomicsFromSqlc(row)
		}
		if len(rows) < economicsPageSize {
			break
		}
	}

	// Sales velocity over the lookback window.
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -priceVelocityLookbackDays)
	salesRows, err := s.queries.ListProductSalesDailyByWorkspaceDateRange(ctx, sqlcgen.ListProductSalesDailyByWorkspaceDateRangeParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		DateFrom:    pgtype.Date{Time: from, Valid: true},
		DateTo:      pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	unitsByNm := map[int64]int64{}
	for _, row := range salesRows {
		unitsByNm[row.WbProductID] += row.Sales
	}
	for nm, units := range unitsByNm {
		data.velocityByNm[nm] = float64(units) / float64(priceVelocityLookbackDays)
	}

	// ponytail: hasActiveCampaign / DRR ad-signals are not loaded yet, so
	// price_ad_linked yields no changes until the ad-signal load is wired
	// (campaign_products + product_stats + bid_changes over AdLookbackDays).
	// margin_floor and inventory_demand are fully functional without it.

	// Active quarantine.
	quar, err := s.queries.ListActiveQuarantineGoods(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	for _, g := range quar {
		data.activeQuarNm[g.WbProductID] = struct{}{}
	}

	return data, nil
}
