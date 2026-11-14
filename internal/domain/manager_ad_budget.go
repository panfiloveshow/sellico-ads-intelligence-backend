package domain

import (
	"github.com/google/uuid"
	"time"
)

// NativeAdBudget keeps explicit vendor values separate from their absence.
// Enabled=false means the period limit is off, not a zero spending allowance.
type NativeAdBudget struct {
	Period                string   `json:"period"`
	PeriodConfirmed       bool     `json:"period_confirmed"`
	Enabled               bool     `json:"enabled"`
	AmountRub             *string  `json:"amount_rub"`
	CarryOverEnabled      *bool    `json:"carry_over_enabled"`
	SpentTodayRub         *string  `json:"spent_today_rub"`
	MinimumAmountRub      *string  `json:"minimum_amount_rub"`
	RecommendedMinimumRub *string  `json:"recommended_minimum_rub"`
	MaximumAmountRub      *string  `json:"maximum_amount_rub"`
	VendorLimitValid      *bool    `json:"vendor_limit_valid"`
	Currency              string   `json:"currency"`
	Warnings              []string `json:"warnings"`
}

type NativeAdBudgetContext struct {
	Campaign NativeAdCampaign `json:"campaign"`
	Budget   NativeAdBudget   `json:"budget"`
}

type ManagerAdBudgetTarget struct {
	Enabled          bool    `json:"enabled"`
	AmountRub        *string `json:"amount_rub"`
	CarryOverEnabled *bool   `json:"carry_over_enabled"`
}
type ManagerAdBudgetConstraints struct {
	MaxBudgetRub         string `json:"max_budget_rub"`
	MaxAbsoluteChangeRub string `json:"max_absolute_change_rub"`
}

// Period evidence is historical native evidence inside the uninterrupted
// managed-control chain. It is not an invented current vendor flag.
type ManagerAdBudgetPeriodEvidence struct {
	OperationID uuid.UUID `json:"operation_id"`
	Fingerprint string    `json:"fingerprint"`
	Period      string    `json:"period"`
}
type ManagerAdBudgetPlan struct {
	Before         NativeAdBudget                 `json:"before"`
	Target         ManagerAdBudgetTarget          `json:"target"`
	Constraints    *ManagerAdBudgetConstraints    `json:"constraints"`
	PeriodEvidence *ManagerAdBudgetPeriodEvidence `json:"period_evidence"`
}

type ManagerAdCampaignBudgetContext struct {
	NativeAdBudgetContext
	WorkspaceID      string     `json:"workspace_id"`
	IntegrationID    string     `json:"integration_id"`
	IntegrationName  string     `json:"integration_name"`
	Marketplace      string     `json:"marketplace"`
	CampaignID       uuid.UUID  `json:"campaign_id"`
	Currency         string     `json:"currency"`
	Source           string     `json:"source"`
	RetrievedAt      time.Time  `json:"retrieved_at"`
	DataUpdatedAt    *time.Time `json:"data_updated_at"`
	Partial          bool       `json:"partial"`
	ExecutionPolicy  string     `json:"execution_policy"`
	ExecutionAllowed bool       `json:"execution_allowed"`
	AvailableActions []string   `json:"available_actions"`
}
