package service

import (
	"context"
	"math"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// ApplyManualBulk changes prices for an explicit item list or for a whole scope
// (all products / one cabinet) via an adjustment. Every new effective price is
// clamped to the product's margin floor unless force is set. Uploads to WB in
// ≤1000-item chunks and returns accepted/skipped counts + upload task IDs.
func (s *RepricerService) ApplyManualBulk(ctx context.Context, actorID, workspaceID uuid.UUID, req domain.ManualPriceBulkRequest) (*domain.PriceBulkResult, error) {
	if len(req.Items) == 0 && req.Scope == nil {
		return nil, apperror.New(apperror.ErrValidation, "provide items or a scope")
	}
	if req.Scope != nil && req.Adjustment == nil {
		return nil, apperror.New(apperror.ErrValidation, "scope requires an adjustment")
	}
	if req.Adjustment != nil {
		switch req.Adjustment.Type {
		case domain.PriceAdjustPercent, domain.PriceAdjustAbsolute, domain.PriceAdjustTargetRub:
		default:
			return nil, apperror.New(apperror.ErrValidation, "invalid adjustment type")
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
			effective := effectiveOf(newBase, newDiscount)
			if effective < floor {
				newBase = basePriceForTarget(floor, newDiscount)
			}
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
				newDiscount = clampDiscount(*item.DiscountPercent)
			}
			appendIntent(cur, newBase, newDiscount, "manual bulk item")
		}
	} else {
		for nm, cur := range priceByNm {
			if req.Scope.SellerCabinetID != nil && cur.SellerCabinetID != *req.Scope.SellerCabinetID {
				continue
			}
			newBase := applyAdjustment(cur.PriceRub, *req.Adjustment)
			appendIntent(cur, newBase, cur.DiscountPercent, "manual bulk adjustment")
			_ = nm
		}
	}

	taskIDs, err := s.applyIntents(ctx, workspaceID, intents)
	if err != nil {
		return nil, err
	}
	return &domain.PriceBulkResult{Accepted: len(intents), Skipped: skipped, TaskIDs: taskIDs}, nil
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

func applyAdjustment(base int64, adj domain.ManualPriceAdjustment) int64 {
	switch adj.Type {
	case domain.PriceAdjustPercent:
		return int64(math.Round(float64(base) * (1 + adj.Value/100)))
	case domain.PriceAdjustAbsolute:
		return base + int64(math.Round(adj.Value))
	case domain.PriceAdjustTargetRub:
		return int64(math.Round(adj.Value))
	default:
		return base
	}
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
