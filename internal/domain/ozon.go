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
type OzonCampaignProduct struct {
	ID          uuid.UUID `json:"id"`
	CampaignID  uuid.UUID `json:"campaign_id"`
	SKU         int64     `json:"sku"`
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
	ID                      uuid.UUID  `json:"id"`
	SellerCabinetID         uuid.UUID  `json:"seller_cabinet_id"`
	SKU                     int64      `json:"sku"`
	OfferID                 string     `json:"offer_id"`
	Name                    string     `json:"name,omitempty"`
	PriceRub                *float64   `json:"price_rub,omitempty"`
	OldPriceRub             *float64   `json:"old_price_rub,omitempty"`
	MinPriceRub             *float64   `json:"min_price_rub,omitempty"`
	NetPriceRub             *float64   `json:"net_price_rub,omitempty"`
	MarketingSellerPriceRub *float64   `json:"marketing_seller_price_rub,omitempty"`
	ColorIndex              string     `json:"color_index,omitempty"`
	CommissionFBOPct        *float64   `json:"commission_fbo_pct,omitempty"`
	CommissionFBSPct        *float64   `json:"commission_fbs_pct,omitempty"`
	AcquiringPct            *float64   `json:"acquiring_pct,omitempty"`
	SyncedAt                *time.Time `json:"synced_at,omitempty"`
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
}
