package domain

import (
	"time"

	"github.com/google/uuid"
)

// NativeAdCampaign contains only fields returned by a current native read.
// Missing amounts stay nil. Status/type codes retain the marketplace's vocabulary.
type NativeAdCampaign struct {
	ExternalID         string   `json:"external_id"`
	Name               string   `json:"name"`
	Status             string   `json:"status"`
	CampaignType       *string  `json:"campaign_type"`
	BidType            *string  `json:"bid_type"`
	PaymentType        *string  `json:"payment_type"`
	DailyBudgetRub     *string  `json:"daily_budget_rub"`
	WeeklyBudgetRub    *string  `json:"weekly_budget_rub"`
	RemainingBudgetRub *string  `json:"remaining_budget_rub,omitempty"`
	Warnings           []string `json:"warnings"`
}

type ManagerAdContext struct {
	NativeAdCampaign
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
	AvailableActions []string   `json:"available_actions"`
}
