package service

import (
	"context"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	maxPriceAdjustmentPercent = 95.0
	maxPriceRub               = 1_000_000_000.0
)

// ApplyManualBulk changes prices for an explicit item list or for a whole scope
// (all products / one cabinet) via an adjustment. A change is kept from crossing
// the product's margin floor unless force is set (see clampToMarginFloor).
// Uploads to WB in
// ≤1000-item chunks and returns accepted/skipped counts + upload task IDs.
func (s *RepricerService) ApplyManualBulk(ctx context.Context, actorID, workspaceID uuid.UUID, req domain.ManualPriceBulkRequest) (*domain.PriceBulkResult, error) {
	if err := validateManualPriceBulkRequest(req); err != nil {
		return nil, err
	}
	if req.Scope != nil && req.Scope.SellerCabinetID != nil {
		if err := s.requireCabinetInWorkspace(ctx, workspaceID, *req.Scope.SellerCabinetID); err != nil {
			return nil, err
		}
	}

	prices, err := s.queries.ListProductPricesByWorkspace(ctx, uuidToPgtype(workspaceID), 100000, 0)
	if err != nil {
		return nil, err
	}
	priceByNm := make(map[int64]domain.ProductPrice, len(prices))
	for _, row := range prices {
		priceByNm[row.WbProductID] = productPriceFromSqlc(row)
	}
	latest, err := s.queries.ListLatestPriceIntents(ctx, uuidToPgtype(workspaceID), pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	overlayLatestPriceIntents(priceByNm, latest)
	floorByNm := s.marginFloors(ctx, workspaceID)

	var intents []priceChangeIntent
	skipped := 0

	appendIntent := func(cur domain.ProductPrice, newBase int64, newDiscount int, reason string) {
		if newBase <= 0 {
			skipped++
			return
		}
		floor := floorByNm[cur.WBProductID]
		if !req.Force && floor > 0 {
			clamped, ok := clampToMarginFloor(cur, newBase, newDiscount, floor)
			if !ok {
				skipped++
				return
			}
			newBase = clamped
		}
		if newBase == cur.PriceRub && newDiscount == cur.DiscountPercent {
			skipped++
			return
		}
		intents = append(intents, priceChangeIntent{
			CabinetID:   cur.SellerCabinetID,
			NmID:        cur.WBProductID,
			OldPriceRub: cur.PriceRub,
			NewPriceRub: newBase,
			OldDiscount: cur.DiscountPercent,
			NewDiscount: newDiscount,
			MinPriceRub: floor,
			Reason:      reason,
			Source:      domain.PriceSourceManual,
			CreatedBy:   &actorID,
		})
	}

	if len(req.Items) > 0 {
		for _, item := range req.Items {
			cur, ok := priceByNm[item.WBProductID]
			if !ok {
				skipped++
				continue
			}
			newBase := cur.PriceRub
			if item.TargetPriceRub != nil {
				newBase = *item.TargetPriceRub
			}
			newDiscount := cur.DiscountPercent
			if item.DiscountPercent != nil {
				newDiscount = *item.DiscountPercent
			}
			appendIntent(cur, newBase, newDiscount, "manual bulk item")
		}
	} else if len(req.Scope.ProductIDs) > 0 {
		requestedCabinetID := *req.Scope.SellerCabinetID
		for _, nm := range req.Scope.ProductIDs {
			cur, ok := priceByNm[nm]
			if !ok || !priceBelongsToCabinet(cur, requestedCabinetID) {
				skipped++
				continue
			}
			newBase := applyAdjustment(cur.PriceRub, *req.Adjustment)
			appendIntent(cur, newBase, cur.DiscountPercent, "manual selected-products adjustment")
		}
	} else {
		for _, cur := range priceByNm {
			if req.Scope.SellerCabinetID != nil && cur.SellerCabinetID != *req.Scope.SellerCabinetID {
				continue
			}
			newBase := applyAdjustment(cur.PriceRub, *req.Adjustment)
			appendIntent(cur, newBase, cur.DiscountPercent, "manual bulk adjustment")
		}
	}

	applyResult, err := s.enqueueAndApplyIntents(ctx, workspaceID, intents)
	if err != nil && applyResult.Accepted == 0 {
		return nil, err
	}
	if err != nil {
		s.logger.Warn().Err(err).Int("accepted", applyResult.Accepted).Int("queued", applyResult.Queued).Int("failed", applyResult.Failed).Msg("manual price changes were saved with partial delivery errors")
	}
	skipped += len(intents) - applyResult.Accepted
	return &domain.PriceBulkResult{
		Accepted: applyResult.Accepted,
		Queued:   applyResult.Queued,
		Failed:   applyResult.Failed,
		Skipped:  skipped,
		TaskIDs:  applyResult.TaskIDs,
	}, nil
}

func validateManualPriceBulkRequest(req domain.ManualPriceBulkRequest) error {
	itemsMode := len(req.Items) > 0
	scopeMode := req.Scope != nil
	if itemsMode == scopeMode {
		return apperror.New(apperror.ErrValidation, "provide exactly one of items or scope")
	}

	if itemsMode {
		if req.Adjustment != nil {
			return apperror.New(apperror.ErrValidation, "items mode does not accept an adjustment")
		}
		seen := make(map[int64]struct{}, len(req.Items))
		for _, item := range req.Items {
			if item.WBProductID <= 0 {
				return apperror.New(apperror.ErrValidation, "wb_product_id must be positive")
			}
			if _, duplicate := seen[item.WBProductID]; duplicate {
				return apperror.New(apperror.ErrValidation, "duplicate wb_product_id")
			}
			seen[item.WBProductID] = struct{}{}
			if item.TargetPriceRub == nil && item.DiscountPercent == nil {
				return apperror.New(apperror.ErrValidation, "each item requires target_price_rub or discount_percent")
			}
			if item.TargetPriceRub != nil && (*item.TargetPriceRub <= 0 || float64(*item.TargetPriceRub) > maxPriceRub) {
				return apperror.New(apperror.ErrValidation, "target_price_rub must be between 1 and 1000000000")
			}
			if item.DiscountPercent != nil && (*item.DiscountPercent < 0 || *item.DiscountPercent > 95) {
				return apperror.New(apperror.ErrValidation, "discount_percent must be between 0 and 95")
			}
		}
		return nil
	}

	if req.Adjustment == nil {
		return apperror.New(apperror.ErrValidation, "scope requires an adjustment")
	}
	allScope := req.Scope.All && req.Scope.SellerCabinetID == nil && len(req.Scope.ProductIDs) == 0
	cabinetScope := !req.Scope.All && req.Scope.SellerCabinetID != nil && len(req.Scope.ProductIDs) == 0
	productScope := !req.Scope.All && req.Scope.SellerCabinetID != nil && len(req.Scope.ProductIDs) > 0
	if !allScope && !cabinetScope && !productScope {
		return apperror.New(apperror.ErrValidation, "scope must select all products, one seller cabinet, or product_ids")
	}
	if productScope {
		seen := make(map[int64]struct{}, len(req.Scope.ProductIDs))
		for _, nmID := range req.Scope.ProductIDs {
			if nmID <= 0 {
				return apperror.New(apperror.ErrValidation, "scope product_ids must be positive")
			}
			if _, exists := seen[nmID]; exists {
				return apperror.New(apperror.ErrValidation, "scope product_ids must not contain duplicates")
			}
			seen[nmID] = struct{}{}
		}
	}
	return validatePriceAdjustment(*req.Adjustment, false)
}

func priceBelongsToCabinet(price domain.ProductPrice, cabinetID uuid.UUID) bool {
	return price.SellerCabinetID == cabinetID
}

// overlayLatestPriceIntents keeps relative changes chained to the latest real
// persisted intent while product_prices is waiting for the next WB sync.
func overlayLatestPriceIntents(prices map[int64]domain.ProductPrice, latest []sqlcgen.PriceChange) {
	for _, row := range latest {
		cur, hasSyncedPrice := prices[row.WbProductID]
		active := row.WbStatus == domain.PriceStatusPending || row.WbStatus == domain.PriceStatusSubmitting || row.WbStatus == domain.PriceStatusSubmitUnknown || row.WbStatus == domain.PriceStatusUploaded
		if hasSyncedPrice && !active && !row.CreatedAt.Time.After(cur.SyncedAt) {
			continue
		}
		prices[row.WbProductID] = domain.ProductPrice{
			WorkspaceID:     uuidFromPgtype(row.WorkspaceID),
			SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
			WBProductID:     row.WbProductID,
			PriceRub:        row.NewPriceRub,
			DiscountPercent: int(row.NewDiscountPercent),
			SyncedAt:        row.UpdatedAt.Time,
			UpdatedAt:       row.UpdatedAt.Time,
		}
	}
}

func validatePriceAdjustment(adj domain.ManualPriceAdjustment, allowDelta bool) error {
	if math.IsNaN(adj.Value) || math.IsInf(adj.Value, 0) {
		return apperror.New(apperror.ErrValidation, "adjustment value must be finite")
	}
	switch adj.Type {
	case domain.PriceAdjustPercent:
		if adj.Value == 0 || math.Abs(adj.Value) > maxPriceAdjustmentPercent {
			return apperror.New(apperror.ErrValidation, "percent adjustment must be non-zero and between -95 and 95")
		}
	case domain.PriceAdjustDeltaPercent:
		if !allowDelta {
			return apperror.New(apperror.ErrValidation, "invalid adjustment type")
		}
		if adj.Value == 0 || math.Abs(adj.Value) > maxPriceAdjustmentPercent {
			return apperror.New(apperror.ErrValidation, "delta_percent adjustment must be non-zero and between -95 and 95")
		}
	case domain.PriceAdjustAbsolute:
		if adj.Value == 0 || math.Abs(adj.Value) > maxPriceRub {
			return apperror.New(apperror.ErrValidation, "absolute adjustment must be non-zero and between -1000000000 and 1000000000")
		}
	case domain.PriceAdjustTargetRub:
		if adj.Value < 1 || adj.Value > maxPriceRub {
			return apperror.New(apperror.ErrValidation, "target_rub must be between 1 and 1000000000")
		}
	default:
		return apperror.New(apperror.ErrValidation, "invalid adjustment type")
	}
	return nil
}

// marginFloors computes the margin floor per nmID from product economics.
func (s *RepricerService) marginFloors(ctx context.Context, workspaceID uuid.UUID) map[int64]int64 {
	floors := map[int64]int64{}
	for offset := int32(0); ; offset += economicsPageSize {
		rows, err := s.queries.ListProductEconomicsByWorkspace(ctx, uuidToPgtype(workspaceID), economicsPageSize, offset)
		if err != nil {
			return floors
		}
		for _, row := range rows {
			econ := productEconomicsFromSqlc(row)
			if floor, skip := ComputeMinEffectivePrice(econ, nil); skip == "" {
				floors[econ.WBProductID] = floor
			}
		}
		if len(rows) < economicsPageSize {
			break
		}
	}
	return floors
}

// clampToMarginFloor keeps a manual price change from crossing the product's
// margin floor. It returns the base price to use and false when the change must
// be skipped. A product already priced below its floor is never lifted up to the
// floor: with a large discount that turns a +5% request into a several-fold jump,
// so upward moves pass through unchanged and downward moves are refused.
func clampToMarginFloor(cur domain.ProductPrice, newBase int64, newDiscount int, floor int64) (int64, bool) {
	newEffective := effectiveOf(newBase, newDiscount)
	if newEffective >= floor {
		return newBase, true
	}
	if effectiveOf(cur.PriceRub, cur.DiscountPercent) >= floor {
		return basePriceForTarget(floor, newDiscount), true
	}
	if newEffective > effectiveOf(cur.PriceRub, cur.DiscountPercent) {
		return newBase, true
	}
	return 0, false
}

func applyAdjustment(base int64, adj domain.ManualPriceAdjustment) int64 {
	var result float64
	switch adj.Type {
	case domain.PriceAdjustPercent, domain.PriceAdjustDeltaPercent:
		result = float64(base) * (1 + adj.Value/100)
	case domain.PriceAdjustAbsolute:
		result = float64(base) + adj.Value
	case domain.PriceAdjustTargetRub:
		result = adj.Value
	default:
		return base
	}
	if math.IsNaN(result) || math.IsInf(result, 0) || result < 1 || result > maxPriceRub {
		return 0
	}
	return int64(math.Round(result))
}

func effectiveOf(base int64, discount int) int64 {
	d := clampDiscount(discount)
	return int64(math.Round(float64(base) * float64(100-d) / 100))
}

func clampDiscount(d int) int {
	if d < 0 {
		return 0
	}
	if d > 95 {
		return 95
	}
	return d
}
