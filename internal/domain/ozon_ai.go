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
	// AIDecisionStatusExpired — a copilot proposal nobody confirmed within the
	// TTL; the underlying data snapshot is stale, so the card leaves the queue.
	AIDecisionStatusExpired = "expired"
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

// OzonProductInsight is the «глазами ИИ» enrichment of one campaign product:
// the same stock/funnel/rating signals the AI context carries, exposed to the
// manager UI. Nil fields mean «не измерено», never zero.
type OzonProductInsight struct {
	SKU            int64    `json:"sku"`
	Stock          *int64   `json:"stock,omitempty"`
	DaysOfCover    *float64 `json:"days_of_cover,omitempty"`
	Rating         *float64 `json:"rating,omitempty"`
	ReviewsCount   *int64   `json:"reviews_count,omitempty"`
	CardViews14d   *int64   `json:"card_views_14d,omitempty"`
	ConvToCartPct  *float64 `json:"conv_to_cart_pct,omitempty"`
	ConvToOrderPct *float64 `json:"conv_to_order_pct,omitempty"`
	MarginPct      *float64 `json:"margin_pct,omitempty"`
	// Продажи товара за 14 дней — ВСЕ заказы SKU (ozon_sales_daily), не
	// только пришедшие с конкретной кампании.
	Orders14d     *int64   `json:"orders_14d,omitempty"`
	Revenue14dRub *float64 `json:"revenue_14d_rub,omitempty"`
	// Заказы/выручка, атрибутированные ИМЕННО этой кампании (отчёт
	// Performance API → ozon_campaign_sku_stats), окно 14 дней.
	CampaignOrders14d     *int64   `json:"campaign_orders_14d,omitempty"`
	CampaignRevenue14dRub *float64 `json:"campaign_revenue_14d_rub,omitempty"`
}

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
	// LowData: fewer evaluated decisions than the display threshold — the
	// aggregate is statistical noise and the UI must say «данных пока мало»
	// instead of rendering the numbers as a verdict.
	LowData bool `json:"low_data"`
}

// OzonAIWeeklyReport is a manager-facing plain-Russian recap of a cabinet's
// last week (no actions — summary only). One per cabinet per ISO week.
type OzonAIWeeklyReport struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	DRRStart    *float64  `json:"drr_start,omitempty"`
	DRREnd      *float64  `json:"drr_end,omitempty"`
	Text        string    `json:"text"`
	GeneratedAt time.Time `json:"generated_at"`
}

// AIReadiness is the shadow → next-level readiness stat for a cabinet's active
// AI autopilot strategy: how long it has run in shadow, how many decisions it
// made, how many stayed within the guardrails, the measured ДРР effect so far,
// and whether it is safe to promote to the next automation level.
type AIReadiness struct {
	CurrentLevel        int      `json:"current_level"`
	ShadowDays          int      `json:"shadow_days"`
	DecisionsTotal      int64    `json:"decisions_total"`
	WithinGuardrailsPct float64  `json:"within_guardrails_pct"`
	ProjectedDRRDelta   *float64 `json:"projected_drr_delta,omitempty"`
	RecommendNextLevel  bool     `json:"recommend_next_level"`
	Reason              string   `json:"reason"`
}

// AIDecisionBatchResult is one entry of a bulk approve/reject response.
type AIDecisionBatchResult struct {
	ID    uuid.UUID `json:"id"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
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
