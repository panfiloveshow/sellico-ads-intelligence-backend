package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/collector"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/seo"
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
	// aiPackTopQueriesPerSKU caps the search-query detail per SKU.
	aiPackTopQueriesPerSKU = 15
	// aiPackSearchQueryWindowDays is the search-query lookback window (the
	// phrases sync requests 7 days at a time; the table accumulates more).
	aiPackSearchQueryWindowDays = 30
	// aiPackRecentDecisions caps the feedback-loop section (recent applied
	// decisions with their measured outcome).
	aiPackRecentDecisions = 15
)

type aiPackTotals struct {
	Views      int64   `json:"views"`
	Clicks     int64   `json:"clicks"`
	SpendRub   float64 `json:"spend_rub"`
	Orders     int64   `json:"orders"`
	RevenueRub float64 `json:"revenue_rub"`
	DRR        float64 `json:"drr_pct"`
	// TotalDRR is this campaign's spend over its attributed share of the
	// cabinet's whole turnover. A campaign whose drr_pct looks fine while
	// total_drr_pct is high is buying orders the shop was getting anyway.
	// Omitted when the turnover is unknown — never reported as zero.
	TotalDRR *float64 `json:"total_drr_pct,omitempty"`
}

// aiPackDay is one compact daily row: [date, views, clicks, spend, orders, revenue].
type aiPackDay [6]any

type aiPackProduct struct {
	SKU    int64   `json:"sku"`
	BidRub float64 `json:"bid_rub"`
	// Spend30dRub is the SKU's ad spend from the phrases-report mirror over
	// the search-query window — the only per-SKU spend surface there is.
	// Omitted when the sync has no data for the SKU.
	Spend30dRub *float64 `json:"spend_30d_rub,omitempty"`
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
	// MarginPct is the computed approximate margin (see ozonSKUMarginPct);
	// nil when price or cost is unknown. CostSource says where the cost came
	// from: "ozon" (net_price) or "sellico" (unit-economics fallback).
	MarginPct  *float64 `json:"margin_pct,omitempty"`
	CostSource string   `json:"cost_source,omitempty"`
	ColorIndex string   `json:"color_index,omitempty"`
	// Stock is the warehouse quantity (ozon_product_stocks.present); omitted
	// when the stocks sync has no row for the SKU (unknown ≠ zero).
	Stock *int64 `json:"stock,omitempty"`
	// DaysOfCover = stock / average units per day over the last 28 days.
	// Omitted when stock is unknown or the SKU had no sales in the window —
	// a missing value must read as «не измерено», never as «запаса нет».
	DaysOfCover *float64 `json:"days_of_cover,omitempty"`

	// Card funnel over the last 14 days from the SEO-service mirror of
	// /v1/analytics/data: search impressions → card views → cart → orders.
	// Distinguishes «чинить ставки» from «чинить карточку»: high ДРР with a
	// healthy card conversion is a bidding problem, with a poor one — a card
	// problem ads cannot fix. All omitted when the SEO bridge has no data.
	CardImpressions *int64 `json:"card_impressions_14d,omitempty"`
	CardViews       *int64 `json:"card_views_14d,omitempty"`
	CardCartAdds    *int64 `json:"card_cart_adds_14d,omitempty"`
	// ConvToCartPct = корзина / просмотры карточки × 100 (просмотры > 0).
	ConvToCartPct *float64 `json:"conv_to_cart_pct,omitempty"`
	// ConvToOrderPct = заказы карточки / просмотры карточки × 100.
	ConvToOrderPct *float64 `json:"conv_to_order_pct,omitempty"`

	// Rating/ReviewsCount — средняя оценка покупателей и число оценок из
	// коллектора отзывов, сматченные по названию карточки (жёсткого ключа к
	// SKU у отзывов нет). Отсутствие полей — «не измерено».
	Rating       *float64 `json:"rating,omitempty"`
	ReviewsCount *int64   `json:"reviews_count,omitempty"`
}

// aiPackDecisionOutcome is one past applied decision + its measured result, fed
// to the model so it learns per-cabinet what worked («что сработало, а что нет»).
type aiPackDecisionOutcome struct {
	Action       string   `json:"action"`
	Campaign     string   `json:"campaign,omitempty"`
	SKU          int64    `json:"sku,omitempty"`
	ProposedVal  *float64 `json:"proposed_value,omitempty"`
	Status       string   `json:"status"`
	Outcome      string   `json:"outcome,omitempty"`
	DRRBefore    *float64 `json:"drr_before,omitempty"`
	DRRAfter     *float64 `json:"drr_after,omitempty"`
	SpendDelta   *float64 `json:"spend_delta_rub,omitempty"`
	RevenueDelta *float64 `json:"revenue_delta_rub,omitempty"`
	// Cabinet-wide ДРР around the same decision. A decision that improved the
	// campaign ДРР while raising this one bought traffic that was already
	// converting — without these two fields the model cannot tell the cases
	// apart and keeps repeating the second kind.
	TotalDRRBefore *float64 `json:"total_drr_before,omitempty"`
	TotalDRRAfter  *float64 `json:"total_drr_after,omitempty"`
}

type aiPackRules struct {
	TargetDRRPct     float64 `json:"target_drr_pct"`
	AutomationLevel  int     `json:"automation_level"`
	MaxChangePercent float64 `json:"max_change_percent"`
	MinBidRub        int     `json:"min_bid_rub"`
	MaxBidRub        int     `json:"max_bid_rub"`
	CooldownMinutes  int     `json:"cooldown_minutes"`
	MaxChangesPerDay int     `json:"max_changes_per_day"`
	// MinStockForIncrease — порог склада: повышение ставки/включение CPO для
	// SKU с известным остатком ниже порога отклоняется автоматикой.
	MinStockForIncrease int `json:"min_stock_for_increase"`

	// «ДРР от общего оборота» — the cabinet-wide ratio and its ceiling. Status
	// is ok|no_data|stale; on anything but ok the value is meaningless and the
	// ceiling is not enforced.
	TotalDRRPct       float64  `json:"total_drr_pct"`
	TotalDRRStatus    string   `json:"total_drr_status"`
	MaxTotalDRRPct    *float64 `json:"max_total_drr_pct,omitempty"`
	MaxTotalDRRSource string   `json:"max_total_drr_source,omitempty"`
	TotalTurnoverRub  float64  `json:"total_turnover_rub"`
	TotalDRRWindowDay int      `json:"total_drr_window_days"`

	// Incremental compares the last two windows: did extra spend bring extra
	// turnover, or buy orders the shop already had. Advisory — no guardrail
	// enforces it, but it is the strongest evidence in the pack.
	Incremental incrementalDRR `json:"incremental_drr"`

	// ShopRating/ShopReviewsCount — средняя оценка магазина по всем отзывам
	// коллектора. Отсутствие — «не измерено».
	ShopRating       *float64 `json:"shop_rating,omitempty"`
	ShopReviewsCount *int64   `json:"shop_reviews_count,omitempty"`
}

// aiPackSearchQuery is one aggregated search query row of the context pack
// (top queries by views per SKU over the last aiPackSearchQueryWindowDays).
type aiPackSearchQuery struct {
	SKU         int64    `json:"sku,omitempty"`
	Query       string   `json:"query"`
	Views       int64    `json:"views"`
	Clicks      int64    `json:"clicks"`
	Orders      int64    `json:"orders"`
	SpendRub    float64  `json:"spend_rub"`
	AvgPosition *float64 `json:"avg_position,omitempty"`
}

type aiContextPack struct {
	Rules aiPackRules `json:"rules"`
	// RecentDecisions is the feedback-loop section — placed high (right after
	// the rules) because it is high-value: it must survive the degradation
	// ladder before lower-value sections are cut.
	RecentDecisions []aiPackDecisionOutcome `json:"recent_decisions,omitempty"`
	Campaigns       []aiPackCampaign        `json:"campaigns"`
	RestCampaigns   int                     `json:"rest_campaigns_count,omitempty"`
	RestTotals      *aiPackTotals           `json:"rest_campaigns_totals_14d,omitempty"`
	CPO             []aiPackCPO             `json:"cpo_products,omitempty"`
	Economics       []aiPackEconomics       `json:"economics,omitempty"`
	SearchQueries   []aiPackSearchQuery     `json:"search_queries_30d,omitempty"`
	BidLimits       json.RawMessage         `json:"ozon_bid_limits,omitempty"`
	BoundCampaigns  bool                    `json:"bound_campaigns_only,omitempty"`
}

// aiCabinetData keeps the raw rows the guardrail/apply phases need after the
// pack is built (local UUIDs, current bids, states).
type aiCabinetData struct {
	campaignsByOzonID map[int64]sqlcgen.OzonCampaign
	bidsByCampaignSKU map[int64]map[int64]float64 // ozon campaign id → sku → current bid
	cpoBySKU          map[int64]domain.OzonCPOProduct
	// spend14ByOzonID is each campaign's spend over the stats window — the
	// anchor for budget proposals on campaigns that have no configured budget.
	spend14ByOzonID map[int64]float64
	// stockBySKU is the warehouse quantity per SKU (nil-able through lookup:
	// a missing key means the stocks sync has no data — unknown, not zero).
	stockBySKU map[int64]int64
	// totalDRR is the cabinet-wide «ДРР от общего оборота» measured once per
	// run; the guardrail phase reads it to gate every spend increase.
	totalDRR totalDRR
	// totalDRRCeiling is the ceiling the guardrail phase enforces; Source says
	// whether it was stated outright or derived from unit economics.
	totalDRRCeiling       *float64
	totalDRRCeilingSource string
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
	// Attributed turnover per campaign — the denominator of the per-campaign
	// «ДРР от общего оборота». One round trip for the whole cabinet.
	attributed := loadCampaignAttributedTurnover(ctx, s.queries, s.logger, cabinetID, since)
	for id, t := range totalsByCampaign {
		if t.RevenueRub > 0 {
			t.DRR = roundRub(t.SpendRub / t.RevenueRub * 100)
		}
		if row, ok := attributed[id]; ok {
			if v := drrPct(t.SpendRub, pgNumericToFloat(row.RevenueRub)); v != nil {
				t.TotalDRR = v
			}
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
		spend14ByOzonID:   map[int64]float64{},
		// stockBySKU заполняет мост идентификаторов ниже (ключ — рекламный SKU).
		stockBySKU: map[int64]int64{},
	}

	// Воронка карточек из СЕО-сервиса (best-effort, ключи — sku и offer_id).
	funnelBySKU, funnelByOffer := s.loadCardFunnel(ctx, cabinetID)

	// Отзывы из коллектора: рейтинг магазина + рейтинги товаров по названию.
	shopRating, ratingByName := s.loadReviewRatings(ctx, cabinetID)

	// Скорость продаж за 28 дней — знаменатель для days_of_cover.
	unitsPerDay := map[int64]float64{}
	if velocityRows, velErr := s.queries.OzonSalesVelocityByCabinet(ctx, sqlcgen.OzonSalesVelocityByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            pgtype.Date{Time: time.Now().UTC().AddDate(0, 0, -28), Valid: true},
	}); velErr != nil {
		s.logger.Warn().Err(velErr).Msg("failed to load sales velocity for ai context")
	} else {
		for _, row := range velocityRows {
			if row.Units > 0 {
				unitsPerDay[row.Sku] = float64(row.Units) / 28
			}
		}
	}

	// Per-SKU spend from the phrases mirror: ranks the product detail cut by
	// money actually spent instead of bid size, so the SKUs burning budget are
	// the ones the model sees. Best-effort — empty map degrades to bid order.
	skuSpend := map[int64]float64{}
	if spendRows, spendErr := s.queries.AggregateOzonSearchSpendBySku(ctx, sqlcgen.AggregateOzonSearchSpendBySkuParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		DateFrom:        pgtype.Date{Time: time.Now().UTC().AddDate(0, 0, -aiPackSearchQueryWindowDays), Valid: true},
	}); spendErr != nil {
		s.logger.Warn().Err(spendErr).Msg("failed to load per-sku spend for ai context")
	} else {
		for _, row := range spendRows {
			skuSpend[row.Sku] = pgNumericToFloat(row.SpendRub)
		}
	}
	for _, campaign := range campaigns {
		data.campaignsByOzonID[campaign.OzonCampaignID] = campaign
		if t := totalsByCampaign[uuidFromPgtype(campaign.ID)]; t != nil {
			data.spend14ByOzonID[campaign.OzonCampaignID] = t.SpendRub
		}
	}

	// The cabinet-wide ДРР over the same window the pack reports. The model
	// must see both the current value and the ceiling, otherwise it keeps
	// proposing increases that the guardrail then rejects.
	data.totalDRR = loadCabinetTotalDRR(ctx, s.queries, s.logger, cabinetID, since, time.Now().UTC(), params.MaxDataAgeHours)
	// Same ceiling resolution as the deterministic strategy: explicit if the
	// strategy states one, otherwise derived from the cabinet's own margin.
	margin := loadCabinetMargin(ctx, s.queries, s.logger, cabinetID, since)
	data.totalDRRCeiling, data.totalDRRCeilingSource = resolveTotalDRRCeiling(
		params.MaxTotalDRRPercent, margin, params.TargetMarginPercent, params.ExpectedBuyoutPercent)

	pack := &aiContextPack{
		Rules: aiPackRules{
			TargetDRRPct:        params.TargetACoS,
			AutomationLevel:     params.AutomationLevel,
			MaxChangePercent:    params.MaxChangePercent,
			MinBidRub:           params.MinBid,
			MaxBidRub:           params.MaxBid,
			CooldownMinutes:     params.CooldownMinutes,
			MaxChangesPerDay:    params.MaxChangesPerDay,
			MinStockForIncrease: params.MinStockForIncrease,

			ShopRating:       shopRatingValue(shopRating),
			ShopReviewsCount: shopRatingCount(shopRating),

			TotalDRRPct:       data.totalDRR.Value,
			TotalDRRStatus:    data.totalDRR.Status,
			MaxTotalDRRPct:    data.totalDRRCeiling,
			MaxTotalDRRSource: data.totalDRRCeilingSource,
			TotalTurnoverRub:  data.totalDRR.RevenueRub,
			TotalDRRWindowDay: aiPackStatsWindowDays,
			Incremental: loadIncrementalDRR(ctx, s.queries, s.logger, cabinetID,
				time.Now().UTC(), aiPackStatsWindowDays),
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
			product := aiPackProduct{SKU: row.Sku, BidRub: bid}
			if spend, ok := skuSpend[row.Sku]; ok && spend > 0 {
				v := roundRub(spend)
				product.Spend30dRub = &v
			}
			active = append(active, product)
		}
		data.bidsByCampaignSKU[campaign.OzonCampaignID] = bids
		entry.ProductsTotal = len(active)
		// The detail cut keeps the SKUs that actually spend money (phrases
		// mirror); bid size breaks ties and covers SKUs the sync has no data for.
		sort.SliceStable(active, func(i, j int) bool {
			spendI, spendJ := skuSpend[active[i].SKU], skuSpend[active[j].SKU]
			if spendI != spendJ {
				return spendI > spendJ
			}
			return active[i].BidRub > active[j].BidRub
		})
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
		// Мост идентификаторов: у Ozon рекламный SKU кампаний ≠ продажному SKU
		// зеркал цен/стоков/продаж; общий ключ — артикул. Без моста экономика
		// и склад для таких кабинетов были пустыми.
		bridge := s.buildOzonSKUBridge(ctx, cabinetID, skus)
		data.stockBySKU = bridge.stockBySKU
		// Cost fallback: when Ozon does not report net_price, take the cost
		// from the Sellico unit-economics mirror (cost_price + other_costs;
		// logistics is already inside Ozon commissions, never added). Failures
		// are non-fatal — the pack simply keeps net_price empty.
		// TODO: feed economics.max_allowed_drr into the AI context as a
		// per-product ad-spend ceiling.
		economics := map[string]sqlcgen.OzonProductEconomic{}
		if econRows, econErr := s.queries.ListOzonProductEconomicsByCabinet(ctx, uuidToPgtype(cabinetID)); econErr == nil {
			for _, econ := range econRows {
				economics[econ.OfferID] = econ
			}
		}
		for _, sku := range skus {
			row, hasPrice := bridge.priceBySKU[sku]
			stock, hasStock := bridge.stockBySKU[sku]
			// SKU без единого сигнала не тратит токены пака.
			if !hasPrice && !hasStock {
				continue
			}
			entry := aiPackEconomics{SKU: sku}
			if offer := bridge.offerBySKU[sku]; offer != "" {
				entry.OfferID = offer
			}
			if hasPrice {
				if entry.OfferID == "" {
					entry.OfferID = pgTextValue(row.OfferID)
				}
				entry.PriceRub = pgNumericToFloatPtr(row.PriceRub)
				entry.MinPriceRub = pgNumericToFloatPtr(row.MinPriceRub)
				entry.NetPriceRub = pgNumericToFloatPtr(row.NetPriceRub)
				entry.CommissionFBOPct = pgNumericToFloatPtr(row.CommissionFboPct)
				entry.CommissionFBSPct = pgNumericToFloatPtr(row.CommissionFbsPct)
				entry.ColorIndex = pgTextValue(row.ColorIndex)
			}
			costSource := ""
			if entry.NetPriceRub != nil && *entry.NetPriceRub > 0 {
				costSource = "ozon"
			}
			if (entry.NetPriceRub == nil || *entry.NetPriceRub <= 0) && entry.OfferID != "" {
				if econ, ok := economics[entry.OfferID]; ok {
					if cost := ozonEffectiveNetPrice(0, &econ); cost > 0 {
						entry.NetPriceRub = &cost
						costSource = "sellico"
					}
				}
			}
			entry.CostSource = costSource
			// Остаток и покрытие: без них модель масштабирует товары, которых
			// нет на складе. Отсутствие данных не пишем как ноль.
			if hasStock {
				v := stock
				entry.Stock = &v
				// Продажи (analytics/data) идут под РЕКЛАМНЫМ SKU — прямой
				// ключ первым, продажный из моста — запасным.
				perDay := unitsPerDay[sku]
				if perDay == 0 {
					if salesSKU, ok := bridge.salesSKUBySKU[sku]; ok {
						perDay = unitsPerDay[salesSKU]
					}
				}
				if perDay > 0 {
					cover := roundRub(float64(stock) / perDay)
					entry.DaysOfCover = &cover
				}
			}
			// Воронка карточки: рекламный SKU → артикул → продажный SKU —
			// в СЕО-базе поле sku может держать любой из идентификаторов.
			funnel, hasFunnel := funnelBySKU[strconv.FormatInt(sku, 10)]
			if !hasFunnel && entry.OfferID != "" {
				funnel, hasFunnel = funnelByOffer[entry.OfferID]
			}
			if !hasFunnel {
				if salesSKU, ok := bridge.salesSKUBySKU[sku]; ok {
					funnel, hasFunnel = funnelBySKU[strconv.FormatInt(salesSKU, 10)]
				}
			}
			if hasFunnel {
				applyCardFunnel(&entry, funnel)
			}
			// Рейтинг товара: ключ — название карточки. Своё имя из каталога
			// надёжнее, имя из СЕО-моста — запасной вариант.
			ratingName := bridge.nameBySKU[sku]
			if ratingName == "" && hasFunnel {
				ratingName = funnel.Name
			}
			if ratingName != "" {
				if rating, ok := ratingByName[ratingName]; ok {
					r, cnt := rating.Rating, rating.ReviewsCount
					entry.Rating = &r
					entry.ReviewsCount = &cnt
				}
			}
			// Per-SKU margin: the model reasons on BOTH margin and ДРР (system
			// prompt). Uses the conservative max(FBO, FBS) commission and the
			// same cost resolution as the repricer's ozonEffectiveNetPrice.
			if hasPrice && entry.PriceRub != nil && entry.NetPriceRub != nil {
				commission := 0.0
				if entry.CommissionFBOPct != nil {
					commission = *entry.CommissionFBOPct
				}
				if entry.CommissionFBSPct != nil && *entry.CommissionFBSPct > commission {
					commission = *entry.CommissionFBSPct
				}
				entry.MarginPct = ozonSKUMarginPct(*entry.PriceRub, *entry.NetPriceRub, commission, pgNumericToFloat(row.AcquiringPct))
			}
			pack.Economics = append(pack.Economics, entry)
		}
	}

	// Feedback loop: the cabinet's recent applied decisions with their measured
	// outcome — «что из твоих прошлых решений сработало». Best-effort.
	pack.RecentDecisions = s.recentDecisionOutcomes(ctx, cabinetID, data)

	// Top search queries per SKU from the phrases-report mirror. Non-fatal:
	// an empty table (sync not run yet) simply leaves the section out.
	pack.SearchQueries = s.topSearchQueriesForSKUs(ctx, cabinetID, skus, aiPackTopQueriesPerSKU)

	// Bid limits reference (hour-cached passthrough); included only when small
	// enough to be worth the tokens. Failures are non-fatal by design.
	if s.actions != nil {
		if limits, limitsErr := s.actions.GetBidLimits(ctx, workspaceID, cabinetID); limitsErr == nil && len(limits) > 0 && len(limits) <= 8*1024 {
			pack.BidLimits = limits
		}
	}

	return pack, data, nil
}

// loadCardFunnel pulls the 14-day card funnel from the SEO service, keyed by
// its sku string and (as a fallback key) nothing else — the same value serves
// both maps because СЕО хранит в products.sku либо числовой Ozon SKU, либо
// артикул. Best-effort: any failure returns empty maps and the pack simply
// has no funnel section.
func (s *OzonAIManagerService) loadCardFunnel(ctx context.Context, cabinetID uuid.UUID) (map[string]seo.CardMetric, map[string]seo.CardMetric) {
	empty := map[string]seo.CardMetric{}
	if s.seoFunnel == nil || !s.seoFunnel.Enabled() {
		return empty, empty
	}
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return empty, empty
	}
	creds, err := decryptOzonCredentialsBlob(sellerCabinetFromSqlc(row).EncryptedCredentials, s.encryptionKey)
	if err != nil || creds.ClientID == "" {
		return empty, empty
	}
	metrics, err := s.seoFunnel.GetOzonCardMetrics(ctx, creds.ClientID, aiPackStatsWindowDays)
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load card funnel from seo service")
		return empty, empty
	}
	bySKU := make(map[string]seo.CardMetric, len(metrics))
	for _, m := range metrics {
		if m.SKU != "" {
			bySKU[m.SKU] = m
		}
	}
	return bySKU, bySKU
}

// applyCardFunnel fills the funnel fields of one economics entry. Conversions
// are only computed over non-zero card views — деление на ноль тут читалось
// бы моделью как «нулевая конверсия», что неправда.
func applyCardFunnel(entry *aiPackEconomics, funnel seo.CardMetric) {
	impressions, views, cart := funnel.Impressions, funnel.CardViews, funnel.CartAdds
	entry.CardImpressions = &impressions
	entry.CardViews = &views
	entry.CardCartAdds = &cart
	if views > 0 {
		toCart := roundRub(float64(cart) / float64(views) * 100)
		entry.ConvToCartPct = &toCart
		toOrder := roundRub(float64(funnel.Orders) / float64(views) * 100)
		entry.ConvToOrderPct = &toOrder
	}
}

// loadReviewRatings pulls the collector's review aggregate for the cabinet's
// CRM integration. Best-effort: any failure returns (nil, empty map).
func (s *OzonAIManagerService) loadReviewRatings(ctx context.Context, cabinetID uuid.UUID) (*collector.ShopRating, map[string]collector.ProductRating) {
	empty := map[string]collector.ProductRating{}
	if s.reviews == nil || !s.reviews.Enabled() {
		return nil, empty
	}
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil || !row.ExternalIntegrationID.Valid || row.ExternalIntegrationID.String == "" {
		return nil, empty
	}
	summary, err := s.reviews.GetReviewSummary(ctx, row.ExternalIntegrationID.String)
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load review ratings from collector")
		return nil, empty
	}
	byName := make(map[string]collector.ProductRating, len(summary.Products))
	for _, product := range summary.Products {
		if product.Name != "" {
			byName[product.Name] = product
		}
	}
	return summary.Shop, byName
}

func shopRatingValue(shop *collector.ShopRating) *float64 {
	if shop == nil {
		return nil
	}
	v := shop.Rating
	return &v
}

func shopRatingCount(shop *collector.ShopRating) *int64 {
	if shop == nil {
		return nil
	}
	v := shop.ReviewsCount
	return &v
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
		func(p *aiContextPack) { // 2.5: cap search queries at 100 rows total
			if len(p.SearchQueries) > 100 {
				p.SearchQueries = p.SearchQueries[:100]
			}
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
			p.SearchQueries = nil
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

// topSearchQueriesForSKUs loads the per-SKU top queries by views over the
// last aiPackSearchQueryWindowDays days. SKU 0 (rows the report did not
// attribute to a product) is always included. Best-effort: any failure
// returns nil and the pack simply has no search-query section.
func (s *OzonAIManagerService) topSearchQueriesForSKUs(ctx context.Context, cabinetID uuid.UUID, skus []int64, perSKU int) []aiPackSearchQuery {
	lookup := append(append(make([]int64, 0, len(skus)+1), skus...), 0)
	since := time.Now().UTC().AddDate(0, 0, -aiPackSearchQueryWindowDays)
	rows, err := s.queries.ListOzonSearchQueriesBySkus(ctx, sqlcgen.ListOzonSearchQueriesBySkusParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Skus:            lookup,
		DateFrom:        pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load search queries for ai context")
		return nil
	}
	// Rows arrive ordered by (sku, views DESC); the per-SKU top-N cut happens
	// here (sqlc cannot express the ROW_NUMBER filter).
	perSKUCount := map[int64]int{}
	out := make([]aiPackSearchQuery, 0, len(rows))
	for _, row := range rows {
		if perSKUCount[row.Sku] >= perSKU {
			continue
		}
		perSKUCount[row.Sku]++
		out = append(out, aiPackSearchQuery{
			SKU:         row.Sku,
			Query:       row.Query,
			Views:       row.Views,
			Clicks:      row.Clicks,
			Orders:      row.Orders,
			SpendRub:    roundRub(pgNumericToFloat(row.SpendRub)),
			AvgPosition: pgNumericToFloatPtr(row.AvgPosition),
		})
	}
	return out
}

// ozonSKUMarginPct computes the approximate per-SKU margin percentage fed to
// the AI context:
//
//	маржа% ≈ (price − cost − price × commission%/100 − acquiring) / price × 100
//
// cost is the resolved net_price/себестоимость (Ozon or the Sellico fallback),
// commission the conservative max(FBO, FBS). Returns nil when price or cost is
// unknown (the model must not scale unknown-margin SKUs); a negative margin is
// a real value and is returned so the model can steer clear of it.
func ozonSKUMarginPct(price, cost, commissionPct, acquiringRub float64) *float64 {
	if price <= 0 || cost <= 0 {
		return nil
	}
	margin := (price - cost - price*commissionPct/100 - acquiringRub) / price * 100
	v := roundRub(margin)
	return &v
}

// recentDecisionOutcomes builds the feedback-loop section: the cabinet's newest
// applied/auto_applied decisions with the impact numbers the sweep measured.
// Best-effort — any failure returns nil and the section is simply absent.
func (s *OzonAIManagerService) recentDecisionOutcomes(ctx context.Context, cabinetID uuid.UUID, data *aiCabinetData) []aiPackDecisionOutcome {
	rows, err := s.queries.ListRecentAppliedAIDecisions(ctx, sqlcgen.ListRecentAppliedAIDecisionsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Lim:             aiPackRecentDecisions,
	})
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load recent ai decisions for context")
		return nil
	}
	out := make([]aiPackDecisionOutcome, 0, len(rows))
	for _, row := range rows {
		var target domain.AIDecisionTarget
		if len(row.Target) > 0 {
			_ = json.Unmarshal(row.Target, &target)
		}
		var payload struct {
			NewValue *float64 `json:"new_value"`
		}
		if len(row.Proposal) > 0 {
			_ = json.Unmarshal(row.Proposal, &payload)
		}
		item := aiPackDecisionOutcome{
			Action:      row.ActionType,
			SKU:         target.SKU,
			ProposedVal: payload.NewValue,
			Status:      row.Status,
			Outcome:     pgTextValue(row.OutcomeStatus),
			DRRBefore:   pgNumericToFloatPtr(row.DrrBefore),
			DRRAfter:    pgNumericToFloatPtr(row.DrrAfter),

			TotalDRRBefore: pgNumericToFloatPtr(row.TotalDrrBefore),
			TotalDRRAfter:  pgNumericToFloatPtr(row.TotalDrrAfter),
		}
		if target.OzonCampaignID > 0 {
			if c, ok := data.campaignsByOzonID[target.OzonCampaignID]; ok && pgTextValue(c.Title) != "" {
				item.Campaign = pgTextValue(c.Title)
			} else {
				item.Campaign = fmt.Sprintf("%d", target.OzonCampaignID)
			}
		}
		if row.SpendBeforeRub.Valid && row.SpendAfterRub.Valid {
			d := roundRub(pgNumericToFloat(row.SpendAfterRub) - pgNumericToFloat(row.SpendBeforeRub))
			item.SpendDelta = &d
		}
		if row.RevenueBeforeRub.Valid && row.RevenueAfterRub.Valid {
			d := roundRub(pgNumericToFloat(row.RevenueAfterRub) - pgNumericToFloat(row.RevenueBeforeRub))
			item.RevenueDelta = &d
		}
		out = append(out, item)
	}
	return out
}

func pgInt8ToPtr(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}
