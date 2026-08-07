package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// Context pack: everything the model sees about a cabinet, assembled from OUR
// tables only (no Ozon API calls except the hour-cached bid limits) — the
// free-tier LLM budget is ~40 RPM globally, so data gathering must not spend
// LLM round-trips or Ozon quota.
const (
	// aiContextPackMaxBytes caps the serialized pack (~60KB per the design).
	aiContextPackMaxBytes = 60 * 1024
	// aiPackTopCampaigns is the detailed-campaign cap (rest is aggregated).
	aiPackTopCampaigns = 20
	// aiPackTopProductsPerCampaign caps per-campaign SKU detail.
	aiPackTopProductsPerCampaign = 30
	// aiPackCPOLimit caps the CPO product list.
	aiPackCPOLimit = 200
	// aiPackEconomicsLimit caps the per-SKU economics list.
	aiPackEconomicsLimit = 300
	// aiPackStatsWindowDays is the stats lookback window.
	aiPackStatsWindowDays = 14
)

type aiPackTotals struct {
	Views      int64   `json:"views"`
	Clicks     int64   `json:"clicks"`
	SpendRub   float64 `json:"spend_rub"`
	Orders     int64   `json:"orders"`
	RevenueRub float64 `json:"revenue_rub"`
	DRR        float64 `json:"drr_pct"`
}

// aiPackDay is one compact daily row: [date, views, clicks, spend, orders, revenue].
type aiPackDay [6]any

type aiPackProduct struct {
	SKU    int64   `json:"sku"`
	BidRub float64 `json:"bid_rub"`
}

type aiPackCampaign struct {
	OzonCampaignID  int64           `json:"ozon_campaign_id"`
	Title           string          `json:"title,omitempty"`
	State           string          `json:"state,omitempty"`
	AdvObjectType   string          `json:"adv_object_type,omitempty"`
	Placement       string          `json:"placement,omitempty"`
	DailyBudgetRub  *int64          `json:"daily_budget_rub,omitempty"`
	WeeklyBudgetRub *int64          `json:"weekly_budget_rub,omitempty"`
	Totals          aiPackTotals    `json:"totals_14d"`
	Daily           []aiPackDay     `json:"daily,omitempty"`
	Products        []aiPackProduct `json:"products,omitempty"`
	ProductsTotal   int             `json:"products_total,omitempty"`
}

type aiPackCPO struct {
	SKU     int64    `json:"sku"`
	Enabled bool     `json:"enabled"`
	BidRub  *float64 `json:"bid_rub,omitempty"`
}

type aiPackEconomics struct {
	SKU              int64    `json:"sku"`
	OfferID          string   `json:"offer_id,omitempty"`
	PriceRub         *float64 `json:"price_rub,omitempty"`
	MinPriceRub      *float64 `json:"min_price_rub,omitempty"`
	NetPriceRub      *float64 `json:"net_price_rub,omitempty"`
	CommissionFBOPct *float64 `json:"commission_fbo_pct,omitempty"`
	CommissionFBSPct *float64 `json:"commission_fbs_pct,omitempty"`
	ColorIndex       string   `json:"color_index,omitempty"`
}

type aiPackRules struct {
	TargetDRRPct     float64 `json:"target_drr_pct"`
	AutomationLevel  int     `json:"automation_level"`
	MaxChangePercent float64 `json:"max_change_percent"`
	MinBidRub        int     `json:"min_bid_rub"`
	MaxBidRub        int     `json:"max_bid_rub"`
	CooldownMinutes  int     `json:"cooldown_minutes"`
	MaxChangesPerDay int     `json:"max_changes_per_day"`
}

type aiContextPack struct {
	Rules          aiPackRules       `json:"rules"`
	Campaigns      []aiPackCampaign  `json:"campaigns"`
	RestCampaigns  int               `json:"rest_campaigns_count,omitempty"`
	RestTotals     *aiPackTotals     `json:"rest_campaigns_totals_14d,omitempty"`
	CPO            []aiPackCPO       `json:"cpo_products,omitempty"`
	Economics      []aiPackEconomics `json:"economics,omitempty"`
	BidLimits      json.RawMessage   `json:"ozon_bid_limits,omitempty"`
	BoundCampaigns bool              `json:"bound_campaigns_only,omitempty"`
}

// aiCabinetData keeps the raw rows the guardrail/apply phases need after the
// pack is built (local UUIDs, current bids, states).
type aiCabinetData struct {
	campaignsByOzonID map[int64]sqlcgen.OzonCampaign
	bidsByCampaignSKU map[int64]map[int64]float64 // ozon campaign id → sku → current bid
	cpoBySKU          map[int64]domain.OzonCPOProduct
}

// buildAIContext loads everything from local tables and assembles both the
// serializable pack and the lookup data for guardrails.
func (s *OzonAIManagerService) buildAIContext(ctx context.Context, workspaceID, cabinetID uuid.UUID, strategy domain.Strategy, params domain.StrategyParams) (*aiContextPack, *aiCabinetData, error) {
	// Campaign scope: explicit bindings restrict the run; none = whole cabinet.
	var campaigns []sqlcgen.OzonCampaign
	bound := false
	bindings, err := s.queries.ListOzonCampaignBindingsByStrategy(ctx, uuidToPgtype(strategy.ID))
	if err != nil {
		return nil, nil, fmt.Errorf("list strategy bindings: %w", err)
	}
	if len(bindings) > 0 {
		bound = true
		for _, b := range bindings {
			campaigns = append(campaigns, sqlcgen.OzonCampaign{
				ID: b.ID, SellerCabinetID: b.SellerCabinetID, OzonCampaignID: b.OzonCampaignID,
				Title: b.Title, AdvObjectType: b.AdvObjectType, State: b.State,
				Placement: b.Placement, DailyBudgetRub: b.DailyBudgetRub, WeeklyBudgetRub: b.WeeklyBudgetRub,
			})
		}
	} else {
		rows, listErr := s.queries.ListOzonCampaignsByCabinet(ctx, sqlcgen.ListOzonCampaignsByCabinetParams{
			SellerCabinetID: uuidToPgtype(cabinetID), Limit: 500, Offset: 0,
		})
		if listErr != nil {
			return nil, nil, fmt.Errorf("list campaigns: %w", listErr)
		}
		campaigns = rows
	}

	since := time.Now().UTC().AddDate(0, 0, -aiPackStatsWindowDays)
	statRows, err := s.queries.ListOzonCampaignDailyStatsSince(ctx, sqlcgen.ListOzonCampaignDailyStatsSinceParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list daily stats: %w", err)
	}
	dailyByCampaign := map[uuid.UUID][]aiPackDay{}
	totalsByCampaign := map[uuid.UUID]*aiPackTotals{}
	for _, row := range statRows {
		id := uuidFromPgtype(row.CampaignID)
		spend := pgNumericToFloat(row.SpendRub)
		revenue := pgNumericToFloat(row.RevenueRub)
		dailyByCampaign[id] = append(dailyByCampaign[id], aiPackDay{
			row.Date.Time.Format("2006-01-02"), row.Views, row.Clicks, roundRub(spend), row.Orders, roundRub(revenue),
		})
		t := totalsByCampaign[id]
		if t == nil {
			t = &aiPackTotals{}
			totalsByCampaign[id] = t
		}
		t.Views += row.Views
		t.Clicks += row.Clicks
		t.SpendRub += spend
		t.Orders += row.Orders
		t.RevenueRub += revenue
	}
	for _, t := range totalsByCampaign {
		if t.RevenueRub > 0 {
			t.DRR = roundRub(t.SpendRub / t.RevenueRub * 100)
		}
		t.SpendRub = roundRub(t.SpendRub)
		t.RevenueRub = roundRub(t.RevenueRub)
	}

	// Rank by 14d spend; the top gets full detail, the rest is aggregated.
	sort.SliceStable(campaigns, func(i, j int) bool {
		var spendI, spendJ float64
		if t := totalsByCampaign[uuidFromPgtype(campaigns[i].ID)]; t != nil {
			spendI = t.SpendRub
		}
		if t := totalsByCampaign[uuidFromPgtype(campaigns[j].ID)]; t != nil {
			spendJ = t.SpendRub
		}
		return spendI > spendJ
	})

	data := &aiCabinetData{
		campaignsByOzonID: map[int64]sqlcgen.OzonCampaign{},
		bidsByCampaignSKU: map[int64]map[int64]float64{},
		cpoBySKU:          map[int64]domain.OzonCPOProduct{},
	}
	for _, campaign := range campaigns {
		data.campaignsByOzonID[campaign.OzonCampaignID] = campaign
	}

	pack := &aiContextPack{
		Rules: aiPackRules{
			TargetDRRPct:     params.TargetACoS,
			AutomationLevel:  params.AutomationLevel,
			MaxChangePercent: params.MaxChangePercent,
			MinBidRub:        params.MinBid,
			MaxBidRub:        params.MaxBid,
			CooldownMinutes:  params.CooldownMinutes,
			MaxChangesPerDay: params.MaxChangesPerDay,
		},
		BoundCampaigns: bound,
	}

	skuSet := map[int64]struct{}{}
	detailed := campaigns
	if len(detailed) > aiPackTopCampaigns {
		detailed = detailed[:aiPackTopCampaigns]
		rest := campaigns[aiPackTopCampaigns:]
		restTotals := aiPackTotals{}
		for _, campaign := range rest {
			if t := totalsByCampaign[uuidFromPgtype(campaign.ID)]; t != nil {
				restTotals.Views += t.Views
				restTotals.Clicks += t.Clicks
				restTotals.SpendRub += t.SpendRub
				restTotals.Orders += t.Orders
				restTotals.RevenueRub += t.RevenueRub
			}
		}
		if restTotals.RevenueRub > 0 {
			restTotals.DRR = roundRub(restTotals.SpendRub / restTotals.RevenueRub * 100)
		}
		pack.RestCampaigns = len(rest)
		pack.RestTotals = &restTotals
	}

	for _, campaign := range detailed {
		localID := uuidFromPgtype(campaign.ID)
		entry := aiPackCampaign{
			OzonCampaignID:  campaign.OzonCampaignID,
			Title:           pgTextValue(campaign.Title),
			State:           pgTextValue(campaign.State),
			AdvObjectType:   pgTextValue(campaign.AdvObjectType),
			Placement:       pgTextValue(campaign.Placement),
			DailyBudgetRub:  pgInt8ToPtr(campaign.DailyBudgetRub),
			WeeklyBudgetRub: pgInt8ToPtr(campaign.WeeklyBudgetRub),
			Daily:           dailyByCampaign[localID],
		}
		if t := totalsByCampaign[localID]; t != nil {
			entry.Totals = *t
		}

		productRows, productsErr := s.queries.ListOzonCampaignProducts(ctx, campaign.ID)
		if productsErr != nil {
			return nil, nil, fmt.Errorf("list products of campaign %d: %w", campaign.OzonCampaignID, productsErr)
		}
		bids := map[int64]float64{}
		active := make([]aiPackProduct, 0, len(productRows))
		for _, row := range productRows {
			bid := pgNumericToFloat(row.BidRub)
			if !row.IsActive {
				continue
			}
			bids[row.Sku] = bid
			active = append(active, aiPackProduct{SKU: row.Sku, BidRub: bid})
		}
		data.bidsByCampaignSKU[campaign.OzonCampaignID] = bids
		entry.ProductsTotal = len(active)
		// Per-SKU spend does not exist on this surface (stats are per
		// campaign/day), so the detail cut keeps the highest-bid SKUs.
		sort.SliceStable(active, func(i, j int) bool { return active[i].BidRub > active[j].BidRub })
		if len(active) > aiPackTopProductsPerCampaign {
			active = active[:aiPackTopProductsPerCampaign]
		}
		entry.Products = active
		for _, product := range active {
			skuSet[product.SKU] = struct{}{}
		}
		pack.Campaigns = append(pack.Campaigns, entry)
	}

	// CPO (search promo) state: riskless spend, the model may scale it freely.
	cpoRows, err := s.queries.ListOzonCpoProducts(ctx, sqlcgen.ListOzonCpoProductsParams{
		SellerCabinetID: uuidToPgtype(cabinetID), Limit: aiPackCPOLimit, Offset: 0,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list cpo products: %w", err)
	}
	for _, row := range cpoRows {
		bid := pgNumericToFloatPtr(row.Bid)
		pack.CPO = append(pack.CPO, aiPackCPO{SKU: row.Sku, Enabled: row.Enabled, BidRub: bid})
		data.cpoBySKU[row.Sku] = domain.OzonCPOProduct{SKU: row.Sku, Enabled: row.Enabled, Bid: bid}
		skuSet[row.Sku] = struct{}{}
	}

	// Unit economics straight from ozon_product_prices (margin = price −
	// net_price − commission; the model computes it per the system prompt).
	skus := make([]int64, 0, len(skuSet))
	for sku := range skuSet {
		skus = append(skus, sku)
	}
	sort.Slice(skus, func(i, j int) bool { return skus[i] < skus[j] })
	if len(skus) > aiPackEconomicsLimit {
		skus = skus[:aiPackEconomicsLimit]
	}
	if len(skus) > 0 {
		priceRows, pricesErr := s.queries.ListOzonProductPricesBySkus(ctx, sqlcgen.ListOzonProductPricesBySkusParams{
			SellerCabinetID: uuidToPgtype(cabinetID), Skus: skus,
		})
		if pricesErr != nil {
			return nil, nil, fmt.Errorf("list product prices: %w", pricesErr)
		}
		for _, row := range priceRows {
			pack.Economics = append(pack.Economics, aiPackEconomics{
				SKU:              row.Sku,
				OfferID:          pgTextValue(row.OfferID),
				PriceRub:         pgNumericToFloatPtr(row.PriceRub),
				MinPriceRub:      pgNumericToFloatPtr(row.MinPriceRub),
				NetPriceRub:      pgNumericToFloatPtr(row.NetPriceRub),
				CommissionFBOPct: pgNumericToFloatPtr(row.CommissionFboPct),
				CommissionFBSPct: pgNumericToFloatPtr(row.CommissionFbsPct),
				ColorIndex:       pgTextValue(row.ColorIndex),
			})
		}
	}

	// Bid limits reference (hour-cached passthrough); included only when small
	// enough to be worth the tokens. Failures are non-fatal by design.
	if s.actions != nil {
		if limits, limitsErr := s.actions.GetBidLimits(ctx, workspaceID, cabinetID); limitsErr == nil && len(limits) > 0 && len(limits) <= 8*1024 {
			pack.BidLimits = limits
		}
	}

	return pack, data, nil
}

// marshalAIContextPack serializes the pack under maxBytes, degrading detail
// step by step: daily rows go first, then campaign and product depth, then
// economics breadth. The pack survives every cut — the totals stay.
func marshalAIContextPack(pack *aiContextPack, maxBytes int) ([]byte, error) {
	steps := []func(*aiContextPack){
		func(p *aiContextPack) { // 1: drop daily detail, keep 14d totals
			for i := range p.Campaigns {
				p.Campaigns[i].Daily = nil
			}
		},
		func(p *aiContextPack) { // 2: drop the bid-limits reference
			p.BidLimits = nil
		},
		func(p *aiContextPack) { // 3: halve product detail
			for i := range p.Campaigns {
				if len(p.Campaigns[i].Products) > aiPackTopProductsPerCampaign/2 {
					p.Campaigns[i].Products = p.Campaigns[i].Products[:aiPackTopProductsPerCampaign/2]
				}
			}
		},
		func(p *aiContextPack) { // 4: halve campaign detail
			if len(p.Campaigns) > aiPackTopCampaigns/2 {
				p.RestCampaigns += len(p.Campaigns) - aiPackTopCampaigns/2
				p.Campaigns = p.Campaigns[:aiPackTopCampaigns/2]
			}
		},
		func(p *aiContextPack) { // 5: cut economics to 100 rows
			if len(p.Economics) > 100 {
				p.Economics = p.Economics[:100]
			}
		},
		func(p *aiContextPack) { // 6: last resort — 10 products per campaign
			for i := range p.Campaigns {
				if len(p.Campaigns[i].Products) > 10 {
					p.Campaigns[i].Products = p.Campaigns[i].Products[:10]
				}
			}
			if len(p.Economics) > 30 {
				p.Economics = p.Economics[:30]
			}
			if len(p.CPO) > 50 {
				p.CPO = p.CPO[:50]
			}
		},
	}
	payload, err := json.Marshal(pack)
	if err != nil {
		return nil, fmt.Errorf("marshal context pack: %w", err)
	}
	for _, step := range steps {
		if len(payload) <= maxBytes {
			return payload, nil
		}
		step(pack)
		payload, err = json.Marshal(pack)
		if err != nil {
			return nil, fmt.Errorf("marshal context pack: %w", err)
		}
	}
	if len(payload) > maxBytes {
		return nil, fmt.Errorf("context pack still %d bytes after all reductions (cap %d)", len(payload), maxBytes)
	}
	return payload, nil
}

func pgInt8ToPtr(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}
