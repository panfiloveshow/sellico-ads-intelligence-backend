package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// UnitEconomicsReadinessProvider checks product profitability from a real
// upstream unit-economics source. Ads Intelligence must not invent margin
// data or infer profitability from ROAS alone.
type UnitEconomicsReadinessProvider interface {
	CheckBidIncreaseReadiness(ctx context.Context, input UnitEconomicsReadinessInput) (*UnitEconomicsReadiness, error)
}

type UnitEconomicsReadinessInput struct {
	WorkspaceID     uuid.UUID
	SellerCabinetID uuid.UUID
	ProductIDs      []uuid.UUID
	WBProductIDs    []int64
}

type UnitEconomicsReadiness struct {
	Source                     string
	CheckedAt                  time.Time
	MissingEconomicsProductIDs []int64
	UnprofitableProductIDs     []int64
	StaleProductIDs            []int64
}

func (r *UnitEconomicsReadiness) AllowsBidIncrease() bool {
	if r == nil {
		return false
	}
	return r.Source != "" &&
		len(r.MissingEconomicsProductIDs) == 0 &&
		len(r.UnprofitableProductIDs) == 0 &&
		len(r.StaleProductIDs) == 0
}

func (r *UnitEconomicsReadiness) BlockReason() string {
	if r == nil {
		return "unit economics readiness is unavailable"
	}
	if r.Source == "" {
		return "unit economics source is unavailable"
	}
	if len(r.MissingEconomicsProductIDs) > 0 {
		return fmt.Sprintf("unit economics is missing for %d product(s): wb_product_ids=%s", len(r.MissingEconomicsProductIDs), unitEconomicsIDSample(r.MissingEconomicsProductIDs))
	}
	if len(r.UnprofitableProductIDs) > 0 {
		return fmt.Sprintf("unit economics marks %d product(s) as unprofitable: wb_product_ids=%s", len(r.UnprofitableProductIDs), unitEconomicsIDSample(r.UnprofitableProductIDs))
	}
	if len(r.StaleProductIDs) > 0 {
		return fmt.Sprintf("unit economics is stale for %d product(s): wb_product_ids=%s", len(r.StaleProductIDs), unitEconomicsIDSample(r.StaleProductIDs))
	}
	return ""
}

func unitEconomicsIDSample(ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	limit := len(ids)
	if limit > 5 {
		limit = 5
	}
	parts := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		parts = append(parts, strconv.FormatInt(ids[i], 10))
	}
	if len(ids) > limit {
		parts = append(parts, fmt.Sprintf("+%d more", len(ids)-limit))
	}
	return strings.Join(parts, ",")
}
