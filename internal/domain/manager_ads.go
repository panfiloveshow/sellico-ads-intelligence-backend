package domain

import (
	"github.com/google/uuid"
	"time"
)

// ManagerIntegration deliberately contains no seller/performance credentials.
type ManagerIntegration struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Marketplace string `json:"marketplace"`
	Name        string `json:"name"`
}

type ManagerAdsCabinet struct {
	ID            uuid.UUID  `json:"id"`
	WorkspaceID   uuid.UUID  `json:"-"`
	IntegrationID string     `json:"integration_id"`
	Marketplace   string     `json:"marketplace"`
	Status        string     `json:"status"`
	LastSyncedAt  *time.Time `json:"last_synced_at"`
}

// Amounts/ratios are decimal strings. Missing rows and absent attribution stay null.
type ManagerAdsMetrics struct {
	Impressions int64   `json:"impressions"`
	Clicks      int64   `json:"clicks"`
	SpendRub    string  `json:"spend_rub"`
	Orders      *int64  `json:"orders"`
	RevenueRub  *string `json:"revenue_rub"`
	CTRPercent  *string `json:"ctr_percent"`
	CPCRub      *string `json:"cpc_rub"`
	DRRPercent  *string `json:"drr_percent"`
	ROAS        *string `json:"roas"`
}

type ManagerAdsCampaign struct {
	ID                   uuid.UUID          `json:"id"`
	ExternalID           string             `json:"external_id"`
	Name                 string             `json:"name"`
	Status               string             `json:"status"`
	ObservedDays         int64              `json:"observed_days"`
	DataUpdatedAt        *time.Time         `json:"data_updated_at"`
	Metrics              *ManagerAdsMetrics `json:"metrics"`
	PreviousMetrics      *ManagerAdsMetrics `json:"previous_metrics,omitempty"`
	PreviousObservedDays *int64             `json:"previous_observed_days,omitempty"`
}

type ManagerAdsPeriod struct {
	DateFrom string `json:"date_from"`
	DateTo   string `json:"date_to"`
	Timezone string `json:"timezone"`
}

type ManagerAdsReport struct {
	WorkspaceID       string               `json:"workspace_id"`
	IntegrationID     string               `json:"integration_id"`
	IntegrationName   string               `json:"integration_name"`
	Marketplace       string               `json:"marketplace"`
	Cabinet           ManagerAdsCabinet    `json:"cabinet"`
	Period            ManagerAdsPeriod     `json:"period"`
	Comparison        *ManagerAdsPeriod    `json:"comparison,omitempty"`
	Items             []ManagerAdsCampaign `json:"items"`
	Page              int                  `json:"page"`
	Limit             int                  `json:"limit"`
	Total             int64                `json:"total"`
	HasMore           bool                 `json:"has_more"`
	Currency          string               `json:"currency"`
	Source            string               `json:"source"`
	MonetaryPrecision string               `json:"monetary_precision"`
	RetrievedAt       time.Time            `json:"retrieved_at"`
	Partial           bool                 `json:"partial"`
	Warnings          []string             `json:"warnings"`
}
