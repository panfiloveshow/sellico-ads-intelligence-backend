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
}
