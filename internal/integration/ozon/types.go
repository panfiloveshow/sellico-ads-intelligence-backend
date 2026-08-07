// Package ozon implements HTTP clients for the two Ozon APIs used by the
// marketplace module:
//
//   - Seller API  (api-seller.ozon.ru)      — Client-Id + Api-Key headers
//   - Performance API (api-performance.ozon.ru) — OAuth client_credentials
//
// Phase 1 is read-only: product/price listings, campaign listings and daily
// statistics. The only write is the Performance token request.
//
// Money convention: the Performance API expresses budgets and bids in
// MICRO-rubles (1 ₽ = 1_000_000). Conversion to rubles happens here, at the
// client boundary — the rest of the system only ever sees rubles.
package ozon

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// microPerRuble is the Performance API money scale: 1 ruble = 1e6 micro-rubles.
const microPerRuble = 1_000_000

// MicroRubToRub converts a Performance API micro-ruble string (e.g. "530000000")
// to whole rubles, rounding half away from zero. Empty and "0" map to 0.
func MicroRubToRub(raw string) (int64, error) {
	f, err := MicroRubToRubFloat(raw)
	if err != nil {
		return 0, err
	}
	if f < 0 {
		return int64(f - 0.5), nil
	}
	return int64(f + 0.5), nil
}

// MicroRubToRubFloat converts a micro-ruble string to rubles keeping kopecks
// (used for per-SKU bids where sub-ruble precision matters).
func MicroRubToRubFloat(raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	micro, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("ozon: parse micro-ruble value %q: %w", raw, err)
	}
	return micro / microPerRuble, nil
}

// RubToMicroRub converts whole rubles to the Performance API micro-ruble
// string form (inverse of MicroRubToRub).
func RubToMicroRub(rub int64) string {
	return strconv.FormatInt(rub*microPerRuble, 10)
}

// RubFloatToMicroRub converts rubles (with kopecks) to the micro-ruble string
// form, rounding to the nearest micro-ruble (inverse of MicroRubToRubFloat).
func RubFloatToMicroRub(rub float64) string {
	return strconv.FormatInt(int64(math.Round(rub*microPerRuble)), 10)
}

// ParsePriceString parses the Seller API's decimal-string money values
// (e.g. "1990.0000"). Empty maps to 0.
func ParsePriceString(raw string) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, fmt.Errorf("ozon: parse price value %q: %w", raw, err)
	}
	return v, nil
}

// Credentials is the transport-level credential pair set. It mirrors
// domain.OzonCredentials without importing the domain package.
type Credentials struct {
	ClientID         string
	APIKey           string
	PerfClientID     string
	PerfClientSecret string
}

// ProductRef is one row of POST /v3/product/list.
type ProductRef struct {
	ProductID int64  `json:"product_id"`
	OfferID   string `json:"offer_id"`
}

// ProductInfo is one parsed item of POST /v3/product/info/list: the bridge
// between the Seller API key space (ProductID) and the Performance API key
// space (SKU, the sales SKU used by campaigns/CPO), plus display fields.
type ProductInfo struct {
	ProductID    int64
	SKU          int64
	OfferID      string
	Name         string
	PrimaryImage string
}

// ProductPrice is one parsed row of POST /v5/product/info/prices.
type ProductPrice struct {
	ProductID               int64
	OfferID                 string
	PriceRub                float64
	OldPriceRub             float64
	MinPriceRub             float64
	NetPriceRub             float64
	MarketingSellerPriceRub float64
	VATPct                  float64
	ColorIndex              string
	CommissionFBOPct        float64
	CommissionFBSPct        float64
	AcquiringPct            float64
	// Competitor minimum prices from price_indexes (0 = no data):
	// other Ozon sellers, external marketplaces, and the seller's own
	// listings on other marketplaces.
	OzonIndexMinPriceRub     float64
	ExternalIndexMinPriceRub float64
	SelfIndexMinPriceRub     float64
}

// PriceUpdate is one item of POST /v1/product/import/prices. Prices are
// rubles; zero optional fields are omitted on the wire (Ozon treats "0" in
// old_price as "remove the crossed-out price", so it is never sent
// implicitly).
type PriceUpdate struct {
	ProductID   int64
	OfferID     string
	PriceRub    float64
	OldPriceRub *float64
	MinPriceRub *float64
}

// PriceUpdateResult is the per-item outcome of POST /v1/product/import/prices.
type PriceUpdateResult struct {
	ProductID int64
	OfferID   string
	Updated   bool
	Errors    []string
}

// ProductStock is one parsed row of POST /v4/product/info/stocks with
// present/reserved summed across the fulfillment schemes (FBO+FBS).
type ProductStock struct {
	ProductID int64
	OfferID   string
	Present   int64
	Reserved  int64
}

// SalesDaily is one parsed row of POST /v1/analytics/data with
// dimension [sku, day]: ordered units and revenue of one sales SKU on one
// day. SKU here is the SALES sku (analytics/campaign key space), not the
// Seller API product_id.
type SalesDaily struct {
	SKU          int64
	Date         time.Time
	OrderedUnits int64
	RevenueRub   float64
}

// Campaign is one campaign from GET /api/client/campaign, with budgets
// already converted from micro-rubles to whole rubles.
type Campaign struct {
	ID                int64
	Title             string
	State             string
	AdvObjectType     string
	Placement         string
	AutopilotStrategy string
	DailyBudgetRub    *int64
	WeeklyBudgetRub   *int64
	FromDate          *time.Time
	ToDate            *time.Time
}

// CampaignProduct is one SKU row from GET /api/client/campaign/{id}/v2/products,
// bid converted from micro-rubles to rubles.
type CampaignProduct struct {
	SKU    int64
	BidRub float64
}

// CampaignPatch carries the mutable campaign fields for
// PATCH /api/client/campaign/{campaignId}. Nil fields are omitted. Budgets
// are whole rubles and converted to micro-ruble strings at the wire boundary.
// Note: dailyBudget is deprecated by Ozon (2026-05-22) in favor of
// weeklyBudget but is still accepted.
type CampaignPatch struct {
	DailyBudgetRub  *int64
	WeeklyBudgetRub *int64
	FromDate        *time.Time
	ToDate          *time.Time
}

// ProductBid is one per-SKU bid for PUT /api/client/campaign/{id}/products.
// BidRub is rubles (kopecks allowed) and converted to micro-rubles on write.
type ProductBid struct {
	SKU       int64
	BidRub    float64
	TargetCIR *float64
}

// CompetitiveBid is one row of GET .../products/bids/competitive, bid already
// converted from micro-rubles to rubles.
type CompetitiveBid struct {
	SKU    int64   `json:"sku"`
	BidRub float64 `json:"bid_rub"`
}

// MinSKUBid is one row of POST /api/client/min/sku, bid converted from
// micro-rubles to rubles.
type MinSKUBid struct {
	SKU    int64   `json:"sku"`
	BidRub float64 `json:"bid_rub"`
}

// CPOProduct is one row of POST /api/client/campaign/search_promo/v2/products.
// CPO (search promo) bids are plain rubles on the wire, not micro-rubles.
type CPOProduct struct {
	SKU     int64   `json:"sku"`
	BidRub  float64 `json:"bid_rub"`
	Enabled bool    `json:"enabled"`
}

// CPOBid is one per-SKU fixed bid for search_promo/v2/bids/set (rubles).
type CPOBid struct {
	SKU    int64
	BidRub float64
}

// DailyStatRow is one row of GET /api/client/statistics/daily.
type DailyStatRow struct {
	CampaignID int64
	Date       time.Time
	Views      int64
	Clicks     int64
	SpendRub   float64
	Orders     int64
	RevenueRub float64
}
