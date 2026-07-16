package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	RolloutShadowValidating = "shadow_validating"
	RolloutLive             = "live"
	RolloutBlocked          = "blocked"
	RolloutManualHold       = "manual_hold"
)

const (
	EvaluationEvaluating     = "evaluating"
	EvaluationNoDecision     = "no_decision"
	EvaluationBlocked        = "blocked"
	EvaluationShadowDecision = "shadow_decision"
	EvaluationClaimed        = "claimed"
	EvaluationApplied        = "applied"
	EvaluationFailed         = "failed"
	EvaluationUnknown        = "unknown"
)

const (
	FactBlocked       = "blocked"
	FactReadyNoChange = "ready_no_change"
	FactWouldApply    = "would_apply"
	FactClaimed       = "claimed"
	FactApplied       = "applied"
	FactFailed        = "failed"
	FactUnknown       = "unknown"
)

type StrategyBindingRollout struct {
	BindingID           uuid.UUID  `json:"binding_id"`
	WorkspaceID         uuid.UUID  `json:"workspace_id"`
	StrategyID          uuid.UUID  `json:"strategy_id"`
	State               string     `json:"state"`
	DesiredMode         string     `json:"desired_mode"`
	ManualHold          bool       `json:"manual_hold"`
	HoldReason          string     `json:"hold_reason"`
	LastBlockCode       string     `json:"last_block_code,omitempty"`
	LastBlockDetail     string     `json:"last_block_detail,omitempty"`
	ValidationStartedAt time.Time  `json:"validation_started_at"`
	LiveEnabledAt       *time.Time `json:"live_enabled_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type StrategyEvaluationFact struct {
	ID         uuid.UUID       `json:"id"`
	RunID      uuid.UUID       `json:"run_id"`
	Code       string          `json:"code"`
	Status     string          `json:"status"`
	Outcome    string          `json:"outcome"`
	Source     string          `json:"source"`
	Value      json.RawMessage `json:"value"`
	ObservedAt time.Time       `json:"observed_at"`
}

type StrategyEvaluationRun struct {
	ID                uuid.UUID                `json:"id"`
	WorkspaceID       uuid.UUID                `json:"workspace_id"`
	SellerCabinetID   uuid.UUID                `json:"seller_cabinet_id"`
	StrategyID        uuid.UUID                `json:"strategy_id"`
	StrategyBindingID uuid.UUID                `json:"strategy_binding_id"`
	CampaignID        *uuid.UUID               `json:"campaign_id,omitempty"`
	ProductID         *uuid.UUID               `json:"product_id,omitempty"`
	AutomationLevel   int                      `json:"automation_level"`
	RolloutState      string                   `json:"rollout_state"`
	ApplyRequested    bool                     `json:"apply_requested"`
	Outcome           string                   `json:"outcome"`
	ReasonCode        string                   `json:"reason_code"`
	ReasonDetail      string                   `json:"reason_detail,omitempty"`
	ProposedActions   int                      `json:"proposed_actions"`
	AppliedActions    int                      `json:"applied_actions"`
	StartedAt         time.Time                `json:"started_at"`
	FinishedAt        *time.Time               `json:"finished_at,omitempty"`
	Facts             []StrategyEvaluationFact `json:"facts,omitempty"`
}

type StrategyActivityCampaign struct {
	BindingID  uuid.UUID              `json:"binding_id"`
	CampaignID *uuid.UUID             `json:"campaign_id,omitempty"`
	ProductID  *uuid.UUID             `json:"product_id,omitempty"`
	Rollout    StrategyBindingRollout `json:"rollout"`
}

type StrategyActivityBlocker struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type StrategyDataFreshness struct {
	State      string     `json:"state"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type StrategyActivity struct {
	StrategyID    uuid.UUID                  `json:"strategy_id"`
	Status        string                     `json:"status"`
	NextCheckAt   *time.Time                 `json:"next_check_at,omitempty"`
	Campaigns     []StrategyActivityCampaign `json:"campaigns"`
	LatestRun     *StrategyEvaluationRun     `json:"latest_run,omitempty"`
	Facts         []StrategyEvaluationFact   `json:"facts"`
	Blockers      []StrategyActivityBlocker  `json:"blockers"`
	DataFreshness StrategyDataFreshness      `json:"data_freshness"`
}
