package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// BidDecisionObservation is a shadow-mode decision calculated from real data.
// It describes a counterfactual action and never means that WB was mutated.
type BidDecisionObservation struct {
	ID                uuid.UUID       `json:"id"`
	WorkspaceID       uuid.UUID       `json:"workspace_id"`
	SellerCabinetID   uuid.UUID       `json:"seller_cabinet_id"`
	StrategyID        uuid.UUID       `json:"strategy_id"`
	StrategyBindingID uuid.UUID       `json:"strategy_binding_id"`
	CampaignID        uuid.UUID       `json:"campaign_id"`
	ProductID         *uuid.UUID      `json:"product_id,omitempty"`
	PhraseID          *uuid.UUID      `json:"phrase_id,omitempty"`
	WBCampaignID      int64           `json:"wb_campaign_id"`
	WBProductID       int64           `json:"wb_product_id"`
	NormQuery         string          `json:"norm_query,omitempty"`
	Placement         string          `json:"placement"`
	OldBid            int             `json:"old_bid"`
	ProposedBid       int             `json:"proposed_bid"`
	Reason            string          `json:"reason"`
	Metrics           json.RawMessage `json:"metrics"`
	AutomationLevel   int             `json:"automation_level"`
	BidObservedAt     time.Time       `json:"bid_observed_at"`
	FirstSeenAt       time.Time       `json:"first_seen_at"`
	LastSeenAt        time.Time       `json:"last_seen_at"`
}

// Strategy types for automated bid management.
const (
	StrategyTypeACoS           = "acos"
	StrategyTypeROAS           = "roas"
	StrategyTypeAntiSliv       = "anti_sliv"
	StrategyTypeDayparting     = "dayparting"
	StrategyTypeRecommendation = "recommendation"

	// StrategyTypeSearchPlaybook drives a search campaign to a target position
	// derived from its keyword frequency tier, governed by the sacrificial-spend,
	// DRR-ceiling, competitive-pressure and flat-impression-pullback rules.
	StrategyTypeSearchPlaybook = "search_playbook"

	// Repricer strategy types (price_* prefix — bid automation skips these).
	StrategyTypePriceMarginFloor      = "price_margin_floor"
	StrategyTypePriceInventoryDemand  = "price_inventory_demand"
	StrategyTypePriceAdLinked         = "price_ad_linked"
	StrategyTypePricePeakHours        = "price_peak_hours"
	StrategyTypePriceCompetitorFollow = "price_competitor_follow"

	// Ozon strategy types (ozon_* prefix — WB bid automation skips these).
	// ozon_cpc_target_drr drives Ozon CPC campaign bids toward a target ДРР
	// (ДРР == ACoS semantically; TargetACoS doubles as the target ДРР).
	StrategyTypeOzonCPCTargetDRR = "ozon_cpc_target_drr"

	// StrategyTypeOzonAIAutopilot hands the cabinet to the LLM-driven AI
	// manager (phase 3). Only OzonAIManagerService executes it: the
	// deterministic Ozon sweep and the WB automations all skip it. TargetACoS
	// doubles as the target ДРР; AutomationLevel 1..3 = shadow/copilot/auto.
	StrategyTypeOzonAIAutopilot = "ozon_ai_autopilot"

	// Ozon repricer strategy types (phase 4). Executed exclusively by
	// OzonRepricerService — the Ozon bid sweep and the WB repricer both skip
	// them. Economics come from Ozon itself (/v5/product/info/prices:
	// net_price, commissions, acquiring, competitor price indexes).
	StrategyTypeOzonPriceMarginFloor      = "ozon_price_margin_floor"
	StrategyTypeOzonPriceCompetitorFollow = "ozon_price_competitor_follow"

	// Phase 5 (WB parity): «Разгрузка склада» on ozon_product_stocks +
	// ozon_sales_daily velocity, and «Реклама → цена» on the per-SKU ДРР
	// derived from ozon_campaign_stats via ozon_campaign_products.
	StrategyTypeOzonPriceInventoryDemand = "ozon_price_inventory_demand"
	StrategyTypeOzonPriceAdLinked        = "ozon_price_ad_linked"

	// Phase 6: «Цена по спросу» on the ozon_orders_hourly 7×24 heatmap built
	// from FBO/FBS postings. Reuses the pure WB DecidePeakHours engine with
	// per-SKU slot intensity (cabinet-aggregated fallback for thin SKUs).
	StrategyTypeOzonPricePeakHours = "ozon_price_peak_hours"
)

// KnownStrategyTypes is every type the API accepts, in one place.
//
// It exists because the transport-layer validator carried its own hand-written
// whitelist of SIX types while the database CHECK constraint allowed fourteen.
// ozon_ai_autopilot was missing from it, so every attempt to save the AI
// strategy — including changing its automation level — came back 400 and the
// level silently stayed on «Тень». The repricer types were missing too.
//
// Anything added here must also be added to the strategies_type_check
// constraint in migrations; keeping the list next to the constants is what
// makes the two easy to compare.
var KnownStrategyTypes = []string{
	StrategyTypeACoS,
	StrategyTypeROAS,
	StrategyTypeAntiSliv,
	StrategyTypeDayparting,
	StrategyTypeRecommendation,
	StrategyTypeSearchPlaybook,
	StrategyTypePriceMarginFloor,
	StrategyTypePriceInventoryDemand,
	StrategyTypePriceAdLinked,
	StrategyTypePricePeakHours,
	StrategyTypePriceCompetitorFollow,
	StrategyTypeOzonCPCTargetDRR,
	StrategyTypeOzonAIAutopilot,
	StrategyTypeOzonPriceMarginFloor,
	StrategyTypeOzonPriceCompetitorFollow,
	StrategyTypeOzonPriceInventoryDemand,
	StrategyTypeOzonPriceAdLinked,
	StrategyTypeOzonPricePeakHours,
}

// IsKnownStrategyType reports whether the API may accept this type at all.
func IsKnownStrategyType(strategyType string) bool {
	for _, known := range KnownStrategyTypes {
		if known == strategyType {
			return true
		}
	}
	return false
}

// IsOzonStrategy reports whether a strategy type belongs to the Ozon module.
// WB bid automation and the WB repricer must skip these.
func IsOzonStrategy(strategyType string) bool {
	return strategyType == StrategyTypeOzonCPCTargetDRR ||
		strategyType == StrategyTypeOzonAIAutopilot ||
		IsOzonPriceStrategy(strategyType)
}

// IsOzonPriceStrategy reports whether a strategy type is an Ozon repricer
// strategy (executed by OzonRepricerService only).
func IsOzonPriceStrategy(strategyType string) bool {
	switch strategyType {
	case StrategyTypeOzonPriceMarginFloor,
		StrategyTypeOzonPriceCompetitorFollow,
		StrategyTypeOzonPriceInventoryDemand,
		StrategyTypeOzonPriceAdLinked,
		StrategyTypeOzonPricePeakHours:
		return true
	}
	return false
}

// IsPriceStrategy reports whether a strategy type is a WB repricer strategy.
// Ozon price strategies (ozon_price_*) deliberately do NOT match: the WB
// repricer must never pick them up.
func IsPriceStrategy(strategyType string) bool {
	switch strategyType {
	case StrategyTypePriceMarginFloor, StrategyTypePriceInventoryDemand, StrategyTypePriceAdLinked, StrategyTypePricePeakHours, StrategyTypePriceCompetitorFollow:
		return true
	}
	return false
}

// Price apply modes.
const (
	PriceApplyModeDryRun = "dry_run"
	PriceApplyModeAuto   = "auto"
)

// OzonMaxLookbackDays is the longest window an Ozon strategy may look back
// over. Both stats syncs (campaign stats and daily sales) mirror exactly this
// many days, so a longer lookback would quietly truncate the denominator of
// every ДРР and make advertising look cheaper than it is.
const OzonMaxLookbackDays = 14

// Strategy represents an automated bidding strategy.
type Strategy struct {
	ID              uuid.UUID         `json:"id"`
	WorkspaceID     uuid.UUID         `json:"workspace_id"`
	SellerCabinetID uuid.UUID         `json:"seller_cabinet_id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Params          StrategyParams    `json:"params"`
	IsActive        bool              `json:"is_active"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Bindings        []StrategyBinding `json:"bindings,omitempty"`
}

// StrategyParams holds configurable parameters for each strategy type.
type StrategyParams struct {
	// ACoS strategy
	TargetACoS float64 `json:"target_acos,omitempty"`

	// ROAS strategy
	TargetROAS float64 `json:"target_roas,omitempty"`

	// Anti-Sliv strategy
	MaxACoS float64 `json:"max_acos,omitempty"`

	// Dayparting strategy
	BaseMultiplier     float64            `json:"base_multiplier,omitempty"`
	HourlyMultipliers  map[string]float64 `json:"hourly_multipliers,omitempty"`
	WeekdayMultipliers map[string]float64 `json:"weekday_multipliers,omitempty"`
	Timezone           string             `json:"timezone,omitempty"` // IANA timezone, default: Europe/Moscow

	// Search playbook strategy (position-targeting, frequency-tiered search).
	// FrequencyTier tags the campaign's keyword group: high|mid|low. When
	// TargetPosition is 0 it is derived from the tier (high→4, mid→3, low→1).
	FrequencyTier            string  `json:"frequency_tier,omitempty"`
	TargetPosition           float64 `json:"target_position,omitempty"`             // desired avg position (1 = top)
	SacrificialSpendPricePct float64 `json:"sacrificial_spend_price_pct,omitempty"` // cut when spend ≥ this % of buyer price with 0 orders; default 100
	FlatImpressionsPct       float64 `json:"flat_impressions_pct,omitempty"`        // impressions within ±this % of prior window = "flat"; default 20
	RollbackStepPercent      float64 `json:"rollback_step_percent,omitempty"`       // pullback % once at target with flat impressions; default 9

	// MaxTotalDRRPercent is the ceiling on «ДРР от общего оборота» (ad spend
	// over the cabinet's WHOLE turnover, not the revenue attributed to a
	// campaign). Above it, bid and budget increases are blocked — decreases
	// are never affected. nil disables the guardrail entirely.
	MaxTotalDRRPercent *float64 `json:"max_total_drr_percent,omitempty"`

	// TargetTotalDRRPercent turns «ДРР от общего оборота» from a ceiling into
	// a second target: the bid is then the lower of what the campaign ДРР and
	// what the attributed total ДРР each ask for. Increases need both to
	// agree, a decrease from either wins. 0 disables the second target.
	TargetTotalDRRPercent float64 `json:"target_total_drr_percent,omitempty"`

	// ExpectedBuyoutPercent is the share of ORDERED turnover expected to be
	// delivered and kept. Ozon analytics reports orders, not deliveries, so a
	// ceiling derived from that turnover without this haircut is too
	// generous. 0 falls back to a conservative default.
	ExpectedBuyoutPercent float64 `json:"expected_buyout_percent,omitempty"`

	// Common limits
	MinBid                    int     `json:"min_bid,omitempty"`                      // default: 50
	MaxBid                    int     `json:"max_bid,omitempty"`                      // default: 5000
	MaxCPC                    float64 `json:"max_cpc,omitempty"`                      // optional bid-increase guardrail
	MaxCPO                    float64 `json:"max_cpo,omitempty"`                      // optional bid-increase guardrail
	AutomationLevel           int     `json:"automation_level,omitempty"`             // default: 1 (shadow only)
	MaxChangePercent          float64 `json:"max_change_percent,omitempty"`           // default: 15
	MinClicks                 int     `json:"min_clicks,omitempty"`                   // default: 10
	LookbackDays              int     `json:"lookback_days,omitempty"`                // default: 7
	MinStockForIncrease       int     `json:"min_stock_for_increase,omitempty"`       // default: 1
	CooldownMinutes           int     `json:"cooldown_minutes,omitempty"`             // default: 120
	MaxChangesPerDay          int     `json:"max_changes_per_day,omitempty"`          // default: 3
	MaxActionsPerDay          int     `json:"max_actions_per_day,omitempty"`          // default: 10 — cabinet-wide daily cap on APPLIED AI actions
	MaxDataAgeHours           int     `json:"max_data_age_hours,omitempty"`           // default: 36
	AllowIncreaseWithoutStock bool    `json:"allow_increase_without_stock,omitempty"` // default: false

	// Repricer (price_* strategies). All *Rub values are integer rubles.
	MinPriceRub           *int64   `json:"min_price_rub,omitempty"`             // hard floor override (on top of margin floor)
	MaxPriceRub           *int64   `json:"max_price_rub,omitempty"`             // required for upward moves
	StepPercent           float64  `json:"step_percent,omitempty"`              // default: 3, cap 10
	OverstockDays         int      `json:"overstock_days,omitempty"`            // default: 60
	LowStockDays          int      `json:"low_stock_days,omitempty"`            // default: 14
	SlowVelocityPerDay    float64  `json:"slow_velocity_per_day,omitempty"`     // units/day below which "slow"
	PriceCooldownHours    int      `json:"price_cooldown_hours,omitempty"`      // default: 24
	MaxPriceChangesPerDay int      `json:"max_price_changes_per_day,omitempty"` // default: 2
	PriceApplyMode        string   `json:"price_apply_mode,omitempty"`          // dry_run|auto, default dry_run
	AdLookbackDays        int      `json:"ad_lookback_days,omitempty"`          // default: 7 (price_ad_linked)
	MaxAllowedDRRPercent  *float64 `json:"max_allowed_drr_percent,omitempty"`   // price_ad_linked: DRR ceiling; falls back to product economics

	// price_peak_hours: percentage band around each product's own price.
	PeakUpliftPercent   float64 `json:"peak_uplift_percent,omitempty"`   // % above current at a demand peak; default 8
	DeadDiscountPercent float64 `json:"dead_discount_percent,omitempty"` // % below current at a dead hour; default 12
	// price_competitor_follow: target = competitor median × (1 − this%).
	UndercutPercent float64 `json:"undercut_percent,omitempty"` // % below competitor median; default 2
	// Relative safety floor when product economics is absent: never sell below
	// current × (1 − this%). Applies to all price strategies. Default 30.
	MaxDiscountPercent float64 `json:"max_discount_percent,omitempty"`
	// ozon_price_* strategies: target margin (% of the sale price) baked into
	// the floor on top of net_price + commission + acquiring. Default 0 —
	// the floor then only covers cost + Ozon fees.
	TargetMarginPercent float64 `json:"target_margin_percent,omitempty"`
}

// DefaultPriceParams returns sensible defaults for repricer strategy parameters.
func DefaultPriceParams() StrategyParams {
	return StrategyParams{
		StepPercent:           3,
		OverstockDays:         60,
		LowStockDays:          14,
		PriceCooldownHours:    24,
		MaxPriceChangesPerDay: 2,
		PriceApplyMode:        PriceApplyModeDryRun,
		AdLookbackDays:        7,
		PeakUpliftPercent:     8,
		DeadDiscountPercent:   12,
		MaxDiscountPercent:    30,
		UndercutPercent:       2,
	}
}

// MergedPriceParams applies repricer defaults for any zero values and caps step.
func (p StrategyParams) MergedPriceParams() StrategyParams {
	d := DefaultPriceParams()
	if p.StepPercent == 0 {
		p.StepPercent = d.StepPercent
	}
	if p.StepPercent > 10 {
		p.StepPercent = 10
	}
	if p.OverstockDays == 0 {
		p.OverstockDays = d.OverstockDays
	}
	if p.LowStockDays == 0 {
		p.LowStockDays = d.LowStockDays
	}
	if p.PriceCooldownHours == 0 {
		p.PriceCooldownHours = d.PriceCooldownHours
	}
	if p.MaxPriceChangesPerDay == 0 {
		p.MaxPriceChangesPerDay = d.MaxPriceChangesPerDay
	}
	if p.PriceApplyMode == "" {
		p.PriceApplyMode = d.PriceApplyMode
	}
	if p.AdLookbackDays == 0 {
		p.AdLookbackDays = d.AdLookbackDays
	}
	if p.PeakUpliftPercent == 0 {
		p.PeakUpliftPercent = d.PeakUpliftPercent
	}
	if p.DeadDiscountPercent == 0 {
		p.DeadDiscountPercent = d.DeadDiscountPercent
	}
	if p.MaxDiscountPercent == 0 {
		p.MaxDiscountPercent = d.MaxDiscountPercent
	}
	if p.UndercutPercent == 0 {
		p.UndercutPercent = d.UndercutPercent
	}
	return p
}

// StrategyBinding links a strategy to a campaign or product. WB strategies
// use CampaignID/ProductID; Ozon strategies use OzonCampaignID (a separate FK
// because campaign_id references the WB campaigns table).
type StrategyBinding struct {
	ID             uuid.UUID  `json:"id"`
	StrategyID     uuid.UUID  `json:"strategy_id"`
	CampaignID     *uuid.UUID `json:"campaign_id,omitempty"`
	ProductID      *uuid.UUID `json:"product_id,omitempty"`
	OzonCampaignID *uuid.UUID `json:"ozon_campaign_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// BidChange records a single bid modification (audit trail).
type BidChange struct {
	ID               uuid.UUID                 `json:"id"`
	WorkspaceID      uuid.UUID                 `json:"workspace_id"`
	SellerCabinetID  uuid.UUID                 `json:"seller_cabinet_id"`
	CampaignID       uuid.UUID                 `json:"campaign_id"`
	ProductID        *uuid.UUID                `json:"product_id,omitempty"`
	PhraseID         *uuid.UUID                `json:"phrase_id,omitempty"`
	StrategyID       *uuid.UUID                `json:"strategy_id,omitempty"`
	RecommendationID *uuid.UUID                `json:"recommendation_id,omitempty"`
	Placement        string                    `json:"placement"`
	OldBid           int                       `json:"old_bid"`
	NewBid           int                       `json:"new_bid"`
	Reason           string                    `json:"reason"`
	Source           string                    `json:"source"` // strategy, recommendation, manual
	ACoS             *float64                  `json:"acos,omitempty"`
	ROAS             *float64                  `json:"roas,omitempty"`
	WBStatus         string                    `json:"wb_status"` // pending, applied, failed
	CanRollback      bool                      `json:"can_rollback"`
	RollbackBid      *int                      `json:"rollback_bid,omitempty"`
	DecisionContext  *BidChangeDecisionContext `json:"decision_context,omitempty"`
	Outcome          *BidChangeOutcome         `json:"outcome,omitempty"`
	CreatedAt        time.Time                 `json:"created_at"`
}

type BidChangeDecisionContext struct {
	ActorType          string   `json:"actor_type"`
	PrimaryMetric      string   `json:"primary_metric,omitempty"`
	PrimaryMetricValue *float64 `json:"primary_metric_value,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	DataMode           string   `json:"data_mode"`
	MissingEvidence    []string `json:"missing_evidence,omitempty"`
}

type BidChangeOutcome struct {
	DataMode     string            `json:"data_mode"`
	Window       string            `json:"window"`
	BaselineDate string            `json:"baseline_date"`
	OutcomeDate  string            `json:"outcome_date"`
	Baseline     AdsMetricsSummary `json:"baseline"`
	Outcome      AdsMetricsSummary `json:"outcome"`
	Delta        AdsMetricsDelta   `json:"delta"`
	Trend        string            `json:"trend"`
}

// BidChangeSource constants.
const (
	BidSourceStrategy       = "strategy"
	BidSourceRecommendation = "recommendation"
	BidSourceManual         = "manual"
	// BidSourceAI marks writes applied by the Ozon AI manager (only
	// ozon_bid_changes accepts it; the WB bid_changes table does not).
	BidSourceAI = "ai"
)

// CampaignPhrase represents a plus or minus phrase for a campaign.
type CampaignPhrase struct {
	ID         uuid.UUID `json:"id"`
	CampaignID uuid.UUID `json:"campaign_id"`
	Phrase     string    `json:"phrase"`
	CreatedAt  time.Time `json:"created_at"`
}

// DefaultStrategyParams returns sensible defaults for strategy parameters.
func DefaultStrategyParams() StrategyParams {
	return StrategyParams{
		MinBid:              50,
		MaxBid:              5000,
		AutomationLevel:     1,
		MaxChangePercent:    15,
		MinClicks:           10,
		LookbackDays:        7,
		BaseMultiplier:      1.0,
		Timezone:            "Europe/Moscow",
		MinStockForIncrease: 1,
		CooldownMinutes:     120,
		MaxChangesPerDay:    3,
		MaxActionsPerDay:    10,
		MaxDataAgeHours:     36,
	}
}

// Merged returns params with defaults applied for any zero values.
func (p StrategyParams) Merged() StrategyParams {
	defaults := DefaultStrategyParams()
	if p.MinBid == 0 {
		p.MinBid = defaults.MinBid
	}
	if p.MaxBid == 0 {
		p.MaxBid = defaults.MaxBid
	}
	if p.MaxChangePercent == 0 {
		p.MaxChangePercent = defaults.MaxChangePercent
	}
	if p.AutomationLevel == 0 {
		p.AutomationLevel = defaults.AutomationLevel
	}
	if p.MinClicks == 0 {
		p.MinClicks = defaults.MinClicks
	}
	if p.LookbackDays == 0 {
		p.LookbackDays = defaults.LookbackDays
	}
	if p.BaseMultiplier == 0 {
		p.BaseMultiplier = defaults.BaseMultiplier
	}
	if p.Timezone == "" {
		p.Timezone = defaults.Timezone
	}
	if p.MinStockForIncrease == 0 {
		p.MinStockForIncrease = defaults.MinStockForIncrease
	}
	if p.CooldownMinutes == 0 {
		p.CooldownMinutes = defaults.CooldownMinutes
	}
	if p.MaxChangesPerDay == 0 {
		p.MaxChangesPerDay = defaults.MaxChangesPerDay
	}
	if p.MaxActionsPerDay == 0 {
		p.MaxActionsPerDay = defaults.MaxActionsPerDay
	}
	if p.MaxDataAgeHours == 0 {
		p.MaxDataAgeHours = defaults.MaxDataAgeHours
	}
	// Search-playbook defaults (only read by the search_playbook engine; harmless elsewhere).
	if p.SacrificialSpendPricePct == 0 {
		p.SacrificialSpendPricePct = 100
	}
	if p.FlatImpressionsPct == 0 {
		p.FlatImpressionsPct = 20
	}
	if p.RollbackStepPercent == 0 {
		p.RollbackStepPercent = 9
	}
	return p
}
