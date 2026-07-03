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
	return p
}
