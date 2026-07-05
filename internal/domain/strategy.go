package domain

import (
	"time"

	"github.com/google/uuid"
)

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
	StrategyTypePriceMarginFloor     = "price_margin_floor"
	StrategyTypePriceInventoryDemand = "price_inventory_demand"
	StrategyTypePriceAdLinked        = "price_ad_linked"
	StrategyTypePricePeakHours       = "price_peak_hours"
)

// IsPriceStrategy reports whether a strategy type is a repricer strategy.
func IsPriceStrategy(strategyType string) bool {
	switch strategyType {
	case StrategyTypePriceMarginFloor, StrategyTypePriceInventoryDemand, StrategyTypePriceAdLinked, StrategyTypePricePeakHours:
		return true
	}
	return false
}

// Price apply modes.
const (
	PriceApplyModeDryRun = "dry_run"
	PriceApplyModeAuto   = "auto"
)

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

	// Search playbook strategy (position-targeting, frequency-tiered search).
	// FrequencyTier tags the campaign's keyword group: high|mid|low. When
	// TargetPosition is 0 it is derived from the tier (high→4, mid→3, low→1).
	FrequencyTier            string  `json:"frequency_tier,omitempty"`
	TargetPosition           float64 `json:"target_position,omitempty"`             // desired avg position (1 = top)
	SacrificialSpendPricePct float64 `json:"sacrificial_spend_price_pct,omitempty"` // cut when spend ≥ this % of buyer price with 0 orders; default 100
	FlatImpressionsPct       float64 `json:"flat_impressions_pct,omitempty"`        // impressions within ±this % of prior window = "flat"; default 20
	RollbackStepPercent      float64 `json:"rollback_step_percent,omitempty"`       // pullback % once at target with flat impressions; default 9

	// Common limits
	MinBid                    int     `json:"min_bid,omitempty"`                      // default: 50
	MaxBid                    int     `json:"max_bid,omitempty"`                      // default: 5000
	MaxCPC                    float64 `json:"max_cpc,omitempty"`                      // optional bid-increase guardrail
	MaxCPO                    float64 `json:"max_cpo,omitempty"`                      // optional bid-increase guardrail
	AutomationLevel           int     `json:"automation_level,omitempty"`             // default: 3
	MaxChangePercent          float64 `json:"max_change_percent,omitempty"`           // default: 15
	MinClicks                 int     `json:"min_clicks,omitempty"`                   // default: 10
	LookbackDays              int     `json:"lookback_days,omitempty"`                // default: 7
	MinStockForIncrease       int     `json:"min_stock_for_increase,omitempty"`       // default: 1
	CooldownMinutes           int     `json:"cooldown_minutes,omitempty"`             // default: 120
	MaxChangesPerDay          int     `json:"max_changes_per_day,omitempty"`          // default: 3
	MaxDataAgeHours           int     `json:"max_data_age_hours,omitempty"`           // default: 36
	AllowIncreaseWithoutStock bool    `json:"allow_increase_without_stock,omitempty"` // default: false

	// Repricer (price_* strategies). All *Rub values are integer rubles.
	MinPriceRub           *int64  `json:"min_price_rub,omitempty"`             // hard floor override (on top of margin floor)
	MaxPriceRub           *int64  `json:"max_price_rub,omitempty"`             // required for upward moves
	StepPercent           float64 `json:"step_percent,omitempty"`              // default: 3, cap 10
	OverstockDays         int     `json:"overstock_days,omitempty"`            // default: 60
	LowStockDays          int     `json:"low_stock_days,omitempty"`            // default: 14
	SlowVelocityPerDay    float64 `json:"slow_velocity_per_day,omitempty"`     // units/day below which "slow"
	PriceCooldownHours    int     `json:"price_cooldown_hours,omitempty"`      // default: 24
	MaxPriceChangesPerDay int     `json:"max_price_changes_per_day,omitempty"` // default: 2
	PriceApplyMode        string  `json:"price_apply_mode,omitempty"`          // dry_run|auto, default dry_run
	AdLookbackDays         int      `json:"ad_lookback_days,omitempty"`          // default: 7 (price_ad_linked)
	MaxAllowedDRRPercent   *float64 `json:"max_allowed_drr_percent,omitempty"`   // price_ad_linked: DRR ceiling; falls back to product economics
	RevertWhenAdsPaused    bool     `json:"revert_when_ads_paused,omitempty"`    // price_ad_linked, opt-in
	DisableBidCoordination bool     `json:"disable_bid_coordination,omitempty"`  // price_ad_linked; zero = coordinate with bids (safe default)

	// price_peak_hours: percentage band around each product's own price.
	PeakUpliftPercent   float64 `json:"peak_uplift_percent,omitempty"`   // % above current at a demand peak; default 8
	DeadDiscountPercent float64 `json:"dead_discount_percent,omitempty"` // % below current at a dead hour; default 12
	// Relative safety floor when product economics is absent: never sell below
	// current × (1 − this%). Applies to all price strategies. Default 30.
	MaxDiscountPercent float64 `json:"max_discount_percent,omitempty"`
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
	return p
}

// StrategyBinding links a strategy to a campaign or product.
type StrategyBinding struct {
	ID         uuid.UUID  `json:"id"`
	StrategyID uuid.UUID  `json:"strategy_id"`
	CampaignID *uuid.UUID `json:"campaign_id,omitempty"`
	ProductID  *uuid.UUID `json:"product_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
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
		AutomationLevel:     3,
		MaxChangePercent:    15,
		MinClicks:           10,
		LookbackDays:        7,
		BaseMultiplier:      1.0,
		MinStockForIncrease: 1,
		CooldownMinutes:     120,
		MaxChangesPerDay:    3,
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
	if p.MinStockForIncrease == 0 {
		p.MinStockForIncrease = defaults.MinStockForIncrease
	}
	if p.CooldownMinutes == 0 {
		p.CooldownMinutes = defaults.CooldownMinutes
	}
	if p.MaxChangesPerDay == 0 {
		p.MaxChangesPerDay = defaults.MaxChangesPerDay
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
