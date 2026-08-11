package domain

import (
	"time"

	"github.com/google/uuid"
)

// OzonCredentials is the credential set an Ozon cabinet needs: the Seller API
// pair (ClientID + APIKey) and the Performance API OAuth pair (PerfClientID +
// PerfClientSecret). The struct is serialized to JSON and AES-GCM encrypted
// into seller_cabinets.encrypted_credentials — never stored in plaintext.
type OzonCredentials struct {
	ClientID         string `json:"client_id"`
	APIKey           string `json:"api_key"`
	PerfClientID     string `json:"perf_client_id"`
	PerfClientSecret string `json:"perf_client_secret"`
}

// HasSellerAPI reports whether the Seller API pair is present (prices sync).
func (c OzonCredentials) HasSellerAPI() bool {
	return c.ClientID != "" && c.APIKey != ""
}

// HasPerformanceAPI reports whether the Performance API pair is present
// (campaigns + stats sync). Cabinets without it run in prices-only mode.
func (c OzonCredentials) HasPerformanceAPI() bool {
	return c.PerfClientID != "" && c.PerfClientSecret != ""
}

// OzonCampaign mirrors an Ozon Performance campaign. Budgets are integer
// rubles (converted from the API's micro-rubles at the client boundary).
type OzonCampaign struct {
	ID                uuid.UUID  `json:"id"`
	SellerCabinetID   uuid.UUID  `json:"seller_cabinet_id"`
	OzonCampaignID    int64      `json:"ozon_campaign_id"`
	Title             string     `json:"title"`
	AdvObjectType     string     `json:"adv_object_type"`
	State             string     `json:"state"`
	Placement         string     `json:"placement"`
	AutopilotStrategy string     `json:"autopilot_strategy,omitempty"`
	DailyBudgetRub    *int64     `json:"daily_budget_rub,omitempty"`
	WeeklyBudgetRub   *int64     `json:"weekly_budget_rub,omitempty"`
	FromDate          *time.Time `json:"from_date,omitempty"`
	ToDate            *time.Time `json:"to_date,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// OzonCampaignProduct is a SKU inside an Ozon campaign with its bid.
// Name/OfferID are read-time enrichment from the ozon_products mapping
// (empty until the products sync has seen the SKU).
type OzonCampaignProduct struct {
	ID          uuid.UUID `json:"id"`
	CampaignID  uuid.UUID `json:"campaign_id"`
	SKU         int64     `json:"sku"`
	Name        string    `json:"name,omitempty"`
	OfferID     string    `json:"offer_id,omitempty"`
	BidRub      *float64  `json:"bid_rub,omitempty"`
	TargetCIR   *float64  `json:"target_cir,omitempty"`
	TopPosition *int32    `json:"top_position,omitempty"`
	IsActive    bool      `json:"is_active"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// OzonCampaignStat is one day of campaign statistics.
type OzonCampaignStat struct {
	ID         uuid.UUID `json:"id"`
	CampaignID uuid.UUID `json:"campaign_id"`
	Date       time.Time `json:"date"`
	Views      int64     `json:"views"`
	Clicks     int64     `json:"clicks"`
	SpendRub   float64   `json:"spend_rub"`
	Orders     int64     `json:"orders"`
	RevenueRub float64   `json:"revenue_rub"`
}

// OzonProductPrice is a product price row with Ozon's built-in unit economics
// (commissions, acquiring, price index) from /v5/product/info/prices.
type OzonProductPrice struct {
	ID                      uuid.UUID `json:"id"`
	SellerCabinetID         uuid.UUID `json:"seller_cabinet_id"`
	SKU                     int64     `json:"sku"`
	OfferID                 string    `json:"offer_id"`
	Name                    string    `json:"name,omitempty"`
	PriceRub                *float64  `json:"price_rub,omitempty"`
	OldPriceRub             *float64  `json:"old_price_rub,omitempty"`
	MinPriceRub             *float64  `json:"min_price_rub,omitempty"`
	NetPriceRub             *float64  `json:"net_price_rub,omitempty"`
	MarketingSellerPriceRub *float64  `json:"marketing_seller_price_rub,omitempty"`
	// EffectiveCostRub is the cost the floor math actually uses: Ozon's own
	// net_price when present, else the Sellico unit-economics cost.
	EffectiveCostRub *float64 `json:"effective_cost_rub,omitempty"`
	// CostSource marks where EffectiveCostRub came from: "ozon" | "sellico".
	CostSource       string     `json:"cost_source,omitempty"`
	ColorIndex       string     `json:"color_index,omitempty"`
	CommissionFBOPct *float64   `json:"commission_fbo_pct,omitempty"`
	CommissionFBSPct *float64   `json:"commission_fbs_pct,omitempty"`
	AcquiringPct     *float64   `json:"acquiring_pct,omitempty"`
	SyncedAt         *time.Time `json:"synced_at,omitempty"`
	// Competitor minimum prices from price_indexes (nil = no data).
	OzonIndexMinPriceRub     *float64 `json:"ozon_index_min_price_rub,omitempty"`
	ExternalIndexMinPriceRub *float64 `json:"external_index_min_price_rub,omitempty"`
	SelfIndexMinPriceRub     *float64 `json:"self_index_min_price_rub,omitempty"`
	// FloorRub is INFORMATIONAL: the margin floor computed on read from the
	// row's own economics (net_price + max commission + acquiring, zero target
	// margin — active strategies may use a different margin). Nil when the
	// economics are insufficient to compute it.
	FloorRub *float64 `json:"floor_rub,omitempty"`
}

// Ozon bid-change audit constants (ozon_bid_changes.kind / source / status).
const (
	OzonBidKindCPC = "cpc"
	OzonBidKindCPO = "cpo"

	OzonBidStatusPending    = "pending"
	OzonBidStatusApplied    = "applied"
	OzonBidStatusFailed     = "failed"
	OzonBidStatusRolledBack = "rolled_back"
	OzonBidStatusShadow     = "shadow"
)

// OzonBidChange is one audited Ozon bid write (manual or strategy-driven).
// CPC rows carry CampaignID; CPO rows carry SellerCabinetID + kind='cpo'.
type OzonBidChange struct {
	ID              uuid.UUID  `json:"id"`
	CampaignID      *uuid.UUID `json:"campaign_id,omitempty"`
	SellerCabinetID *uuid.UUID `json:"seller_cabinet_id,omitempty"`
	Kind            string     `json:"kind"`
	SKU             *int64     `json:"sku,omitempty"`
	// Name is read-time enrichment from the ozon_products mapping (empty when
	// the SKU is unknown or the products sync has not seen it yet).
	Name            string     `json:"name,omitempty"`
	OldBidRub       *float64   `json:"old_bid_rub,omitempty"`
	NewBidRub       *float64   `json:"new_bid_rub,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	Source          string     `json:"source"`
	Status          string     `json:"status"`
	DecisionContext any        `json:"decision_context,omitempty"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
}

// Ozon price-change audit constants (ozon_price_changes.status; sources
// reuse PriceSourceStrategy/PriceSourceManual plus BidSourceAI).
const (
	OzonPriceStatusPending    = "pending"
	OzonPriceStatusApplied    = "applied"
	OzonPriceStatusFailed     = "failed"
	OzonPriceStatusRolledBack = "rolled_back"
	OzonPriceStatusShadow     = "shadow"
)

// OzonPriceChange is one audited Ozon price write (strategy, manual or AI).
// status='shadow' rows are dry-run decisions that never reached Ozon.
type OzonPriceChange struct {
	ID              uuid.UUID  `json:"id"`
	SellerCabinetID uuid.UUID  `json:"seller_cabinet_id"`
	SKU             int64      `json:"sku"`
	OfferID         string     `json:"offer_id,omitempty"`
	OldPriceRub     *float64   `json:"old_price_rub,omitempty"`
	NewPriceRub     float64    `json:"new_price_rub"`
	OldOldPriceRub  *float64   `json:"old_old_price_rub,omitempty"`
	NewOldPriceRub  *float64   `json:"new_old_price_rub,omitempty"`
	MinPriceRub     *float64   `json:"min_price_rub,omitempty"`
	FloorRub        *float64   `json:"floor_rub,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	Source          string     `json:"source"`
	StrategyID      *uuid.UUID `json:"strategy_id,omitempty"`
	Status          string     `json:"status"`
	DecisionContext any        `json:"decision_context,omitempty"`
	Error           string     `json:"error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
}

// Ozon price schedule statuses (ozon_price_schedule_entries.status).
const (
	OzonScheduleStatusPending   = "pending"
	OzonScheduleStatusApplied   = "applied"
	OzonScheduleStatusReverted  = "reverted"
	OzonScheduleStatusCancelled = "cancelled"
	OzonScheduleStatusFailed    = "failed"
)

// OzonPriceScheduleInput is the API input for one scheduled Ozon price change.
type OzonPriceScheduleInput struct {
	SKU               int64      `json:"sku"`
	ScheduledPriceRub float64    `json:"scheduled_price_rub"`
	RevertPriceRub    *float64   `json:"revert_price_rub,omitempty"`
	StartsAt          time.Time  `json:"starts_at"`
	EndsAt            *time.Time `json:"ends_at,omitempty"`
}

// OzonPriceScheduleEntry is one calendar entry: apply ScheduledPriceRub at
// StartsAt; when EndsAt passes, restore RevertPriceRub.
type OzonPriceScheduleEntry struct {
	ID                uuid.UUID  `json:"id"`
	SellerCabinetID   uuid.UUID  `json:"seller_cabinet_id"`
	SKU               int64      `json:"sku"`
	OfferID           string     `json:"offer_id,omitempty"`
	ScheduledPriceRub float64    `json:"scheduled_price_rub"`
	RevertPriceRub    *float64   `json:"revert_price_rub,omitempty"`
	StartsAt          time.Time  `json:"starts_at"`
	EndsAt            *time.Time `json:"ends_at,omitempty"`
	Status            string     `json:"status"`
	Error             string     `json:"error,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	AppliedAt         *time.Time `json:"applied_at,omitempty"`
	RevertedAt        *time.Time `json:"reverted_at,omitempty"`
	// Warning is a non-blocking creation note (e.g. price below the computed
	// margin floor). Never persisted.
	Warning string `json:"warning,omitempty"`
}

// OzonRepricerHealth is a one-glance Ozon repricer status summary.
type OzonRepricerHealth struct {
	PausedUntil        *time.Time             `json:"paused_until,omitempty"`
	LastSweepAt        *time.Time             `json:"last_sweep_at,omitempty"`
	PendingSchedules   int                    `json:"pending_schedules"`
	Changes24h         OzonRepricerChanges24h `json:"changes_24h"`
	ProductsBelowFloor int                    `json:"products_below_floor"`
}

// OzonRepricerChanges24h buckets the last 24h of ozon_price_changes.
type OzonRepricerChanges24h struct {
	Applied int `json:"applied"`
	Shadow  int `json:"shadow"`
	Failed  int `json:"failed"`
}

// OzonCPOProduct is the per-SKU CPO (search promo) state mirrored from
// search_promo/v2/products. Name/OfferID prefer the ozon_products mapping and
// fall back to the CPO API's own title/sourceSku. PriceRub/BidPriceRub/ImageURL/
// VisibilityIndex are display-only enrichment carried straight from the CPO
// response. Enabled is true for every product returned (presence = in promo).
type OzonCPOProduct struct {
	ID              uuid.UUID `json:"id"`
	SellerCabinetID uuid.UUID `json:"seller_cabinet_id"`
	SKU             int64     `json:"sku"`
	Name            string    `json:"name,omitempty"`
	OfferID         string    `json:"offer_id,omitempty"`
	Enabled         bool      `json:"enabled"`
	Bid             *float64  `json:"bid,omitempty"`
	BidKind         string    `json:"bid_kind,omitempty"`
	PriceRub        *float64  `json:"price_rub,omitempty"`
	BidPriceRub     *float64  `json:"bid_price_rub,omitempty"`
	ImageURL        string    `json:"image_url,omitempty"`
	VisibilityIndex string    `json:"visibility_index,omitempty"`
	// PrevBidPct mirrors previousBid.bid — the CPO percent bid before the last
	// change (null when Ozon reports none).
	PrevBidPct *float64 `json:"prev_bid_pct,omitempty"`
	// ViewsThisWeek/ViewsPrevWeek mirror the views counters from the CPO
	// products response.
	ViewsThisWeek *int64 `json:"views_this_week,omitempty"`
	ViewsPrevWeek *int64 `json:"views_prev_week,omitempty"`
	// MinBidRub is the minimum fixed CPO bid (rubles) from get_cpo_min_bids —
	// live enrichment, best-effort, never persisted.
	MinBidRub *float64  `json:"min_bid_rub,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OzonCPOOrder is one promoted order line mirrored from the async
// all_sku_promo orders report (ozon_cpo_orders).
type OzonCPOOrder struct {
	ID              uuid.UUID `json:"id"`
	SellerCabinetID uuid.UUID `json:"seller_cabinet_id"`
	Date            time.Time `json:"date"`
	OrderID         string    `json:"order_id"`
	OrderNumber     string    `json:"order_number,omitempty"`
	SKU             int64     `json:"sku"`
	AdvSKU          int64     `json:"adv_sku,omitempty"`
	VendorCode      string    `json:"vendor_code,omitempty"`
	Name            string    `json:"name,omitempty"`
	Quantity        int32     `json:"quantity"`
	PriceRub        *float64  `json:"price_rub,omitempty"`
	SalePriceRub    *float64  `json:"sale_price_rub,omitempty"`
	BidPct          *float64  `json:"bid_pct,omitempty"`
	BidRub          *float64  `json:"bid_rub,omitempty"`
	SpendRub        *float64  `json:"spend_rub,omitempty"`
}

// OzonCPOStats7d is the 7-day aggregate for the CPO overview: same
// views/clicks/spend/orders/revenue + DRR shape as OzonCampaignWithStats.
type OzonCPOStats7d struct {
	Views      int64   `json:"views"`
	Clicks     int64   `json:"clicks"`
	SpendRub   float64 `json:"spend_rub"`
	Orders     int64   `json:"orders"`
	RevenueRub float64 `json:"revenue_rub"`
	// DRR = spend / revenue * 100 (0 when there is no revenue).
	DRR float64 `json:"drr"`
}

// OzonCPOOverview summarises the cabinet's CPO («Оплата за заказ») promo: its
// backing SEARCH_PROMO/ALL_SKU_PROMO campaign, whether it is running, the
// mirrored product count and the 7-day stats aggregate.
type OzonCPOOverview struct {
	Enabled            bool   `json:"enabled"`
	PromoCampaignID    *int64 `json:"promo_campaign_id"`
	PromoCampaignTitle string `json:"promo_campaign_title"`
	ProductsCount      int64  `json:"products_count"`
	// RatePct is the global CPO percentage (5/7/9 %), read from the
	// all_sku_promo rate endpoint when the promo is running. It is null when the
	// promo is off or the rate could not be read (best-effort — the overview
	// never fails on the rate lookup).
	RatePct *float64       `json:"rate_pct"`
	Stats7d OzonCPOStats7d `json:"stats7d"`
	// The *_7d fields below aggregate the last 7 days of ozon_cpo_orders —
	// the FACTUAL promoted orders from the async all_sku_promo report.
	// Stats7d stays sourced from ozon_campaign_stats (the campaign statistics
	// counters: views/clicks/spend); the two describe the same promo through
	// different Ozon surfaces and intentionally coexist.
	//
	// OrdersCount7d is the number of distinct promoted orders.
	OrdersCount7d int64 `json:"orders_count_7d"`
	// SoldUnits7d is the summed quantity across promoted order lines.
	SoldUnits7d int64 `json:"sold_units_7d"`
	// PromoRevenue7d is SUM(sale_price_rub * quantity).
	PromoRevenue7d float64 `json:"promo_revenue_7d"`
	// PromoSpend7d is SUM(spend_rub) — what the promotion charged.
	PromoSpend7d float64 `json:"promo_spend_7d"`
	// AvgBidPct7d is AVG(bid_pct) over the window (null when no orders).
	AvgBidPct7d *float64 `json:"avg_bid_pct_7d"`
}

// OzonCampaignWithStats is a campaign plus its recent aggregate statistics
// (used by the campaigns list endpoint; window is the last 7 days).
type OzonCampaignWithStats struct {
	OzonCampaign
	StatsViews   int64   `json:"stats_views"`
	StatsClicks  int64   `json:"stats_clicks"`
	StatsSpend   float64 `json:"stats_spend_rub"`
	StatsOrders  int64   `json:"stats_orders"`
	StatsRevenue float64 `json:"stats_revenue_rub"`
	// DRR = spend / revenue * 100 (0 when there is no revenue).
	DRR float64 `json:"drr"`
	// TotalDRR is «ДРР от общего оборота»: the same spend over the campaign's
	// attributed share of the shop's whole turnover, not just the revenue Ozon
	// credited to the campaign. A campaign with a healthy DRR and a high
	// TotalDRR is buying orders the shop was getting anyway.
	//
	// Null when it could not be measured — never 0, which would read as
	// "excellent". TotalDRRStatus says why: ok | no_data | stale.
	TotalDRR       *float64 `json:"total_drr"`
	TotalDRRStatus string   `json:"total_drr_status"`
	// TotalRevenueShared marks campaigns whose SKUs are advertised by other
	// campaigns too, so the turnover behind TotalDRR had to be split.
	TotalRevenueShared bool `json:"total_revenue_shared"`
}
