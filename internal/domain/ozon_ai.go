package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AI run lifecycle (ai_runs.status / trigger).
const (
	AIRunStatusRunning   = "running"
	AIRunStatusCompleted = "completed"
	AIRunStatusFailed    = "failed"
	AIRunStatusSkipped   = "skipped"

	AIRunTriggerCron   = "cron"
	AIRunTriggerManual = "manual"
)

// AI decision action types (ai_decisions.action_type).
const (
	AIActionBidChange        = "bid_change"
	AIActionBudgetChange     = "budget_change"
	AIActionCampaignPause    = "campaign_pause"
	AIActionCampaignActivate = "campaign_activate"
	AIActionCPOBid           = "cpo_bid"
	AIActionCPOEnable        = "cpo_enable"
	AIActionCPODisable       = "cpo_disable"
)

// AI decision statuses (ai_decisions.status).
const (
	AIDecisionStatusShadow              = "shadow"
	AIDecisionStatusProposed            = "proposed"
	AIDecisionStatusApproved            = "approved"
	AIDecisionStatusAutoApplied         = "auto_applied"
	AIDecisionStatusApplied             = "applied"
	AIDecisionStatusFailed              = "failed"
	AIDecisionStatusRejectedByUser      = "rejected_by_user"
	AIDecisionStatusRejectedByGuardrail = "rejected_by_guardrail"
)

// AI decision impact outcome states (ai_decisions.outcome_status).
const (
	// AIOutcomePendingEval — seen by the impact sweep, after-window still has
	// fewer than 3 days of stats; will be re-evaluated.
	AIOutcomePendingEval = "pending_eval"
	// AIOutcomeEvaluated — before/after numbers are written.
	AIOutcomeEvaluated = "evaluated"
	// AIOutcomeNotEvaluable — no campaign-level stats for the target (cpo_*)
	// or the decision aged out (>14 days) without enough after-window data.
	AIOutcomeNotEvaluable = "not_evaluable"
)

// AIRun is one AI manager execution over a cabinet.
type AIRun struct {
	ID               uuid.UUID  `json:"id"`
	WorkspaceID      uuid.UUID  `json:"workspace_id"`
	SellerCabinetID  uuid.UUID  `json:"seller_cabinet_id"`
	StrategyID       *uuid.UUID `json:"strategy_id,omitempty"`
	Status           string     `json:"status"`
	Trigger          string     `json:"trigger"`
	Summary          string     `json:"summary,omitempty"`
	Error            string     `json:"error,omitempty"`
	PromptTokens     int        `json:"prompt_tokens"`
	CompletionTokens int        `json:"completion_tokens"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
}

// AIDecisionTarget identifies what a proposal acts on: an Ozon campaign (by
// its Ozon-side numeric ID, the identifier the model sees) and/or a SKU.
type AIDecisionTarget struct {
	OzonCampaignID int64 `json:"ozon_campaign_id,omitempty"`
	SKU            int64 `json:"sku,omitempty"`
}

// AIDecision is one audited proposal the model made, with the guardrail
// verdict and application outcome.
type AIDecision struct {
	ID               uuid.UUID       `json:"id"`
	RunID            uuid.UUID       `json:"run_id"`
	WorkspaceID      uuid.UUID       `json:"workspace_id"`
	SellerCabinetID  uuid.UUID       `json:"seller_cabinet_id"`
	ActionType       string          `json:"action_type"`
	Target           json.RawMessage `json:"target"`
	Proposal         json.RawMessage `json:"proposal"`
	Rationale        string          `json:"rationale,omitempty"`
	ExpectedEffect   string          `json:"expected_effect,omitempty"`
	GuardrailVerdict string          `json:"guardrail_verdict"`
	Status           string          `json:"status"`
	Error            string          `json:"error,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	AppliedAt        *time.Time      `json:"applied_at,omitempty"`
	AppliedBy        *uuid.UUID      `json:"applied_by,omitempty"`
	// Impact evaluation (7d-before / 7d-after windows around applied_at;
	// written by the ozon:ai_impact_sweep job).
	OutcomeStatus    string     `json:"outcome_status,omitempty"`
	DRRBefore        *float64   `json:"drr_before,omitempty"`
	DRRAfter         *float64   `json:"drr_after,omitempty"`
	SpendBeforeRub   *float64   `json:"spend_before_rub,omitempty"`
	SpendAfterRub    *float64   `json:"spend_after_rub,omitempty"`
	RevenueBeforeRub *float64   `json:"revenue_before_rub,omitempty"`
	RevenueAfterRub  *float64   `json:"revenue_after_rub,omitempty"`
	EvaluatedAt      *time.Time `json:"evaluated_at,omitempty"`
}

// AIImpactSummary is the 30-day «ИИ заработал/сэкономил» aggregate for a
// cabinet. Attribution is deliberately rough: each applied decision is
// compared over a 7-day window before vs a 7-day window after its apply
// moment on its target campaign — no holdout, no cross-decision isolation.
type AIImpactSummary struct {
	WindowDays         int      `json:"window_days"`
	DecisionsApplied   int64    `json:"decisions_applied"`
	DecisionsEvaluated int64    `json:"decisions_evaluated"`
	AvgDRRBefore       *float64 `json:"avg_drr_before,omitempty"`
	AvgDRRAfter        *float64 `json:"avg_drr_after,omitempty"`
	SpendDeltaRub      float64  `json:"spend_delta_rub"`
	RevenueDeltaRub    float64  `json:"revenue_delta_rub"`
	SavedRub           float64  `json:"saved_rub"`
	ExtraRevenueRub    float64  `json:"extra_revenue_rub"`
}

// OzonSearchQueryStat is one aggregated search query row for
// GET /ozon/search-queries (grouped by query over the requested window).
type OzonSearchQueryStat struct {
	Query       string   `json:"query"`
	SKU         int64    `json:"sku,omitempty"`
	ProductName string   `json:"product_name,omitempty"`
	Views       int64    `json:"views"`
	Clicks      int64    `json:"clicks"`
	Orders      int64    `json:"orders"`
	SpendRub    float64  `json:"spend_rub"`
	AvgPosition *float64 `json:"avg_position,omitempty"`
	CTR         float64  `json:"ctr_pct"`
}
