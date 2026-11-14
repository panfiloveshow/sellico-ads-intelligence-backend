package domain

import (
	"time"

	"github.com/google/uuid"
)

type ManagerAdStrategyBreakdown struct {
	StrategyCode         string  `json:"strategy_code"`
	CampaignsCount       int64   `json:"campaigns_count"`
	SpendRub             string  `json:"spend_rub"`
	AttributedOrders     int64   `json:"attributed_orders"`
	ObservedCPORub       *string `json:"observed_cpo_rub"`
	DirectWriteSupported bool    `json:"direct_write_supported"`
}

type ManagerAdAttributionReport struct {
	WorkspaceID          string                       `json:"workspace_id"`
	IntegrationID        string                       `json:"integration_id"`
	IntegrationName      string                       `json:"integration_name"`
	Marketplace          string                       `json:"marketplace"`
	CampaignID           *uuid.UUID                   `json:"campaign_id,omitempty"`
	Period               ManagerAdsPeriod             `json:"period"`
	Currency             string                       `json:"currency"`
	RetrievedAt          time.Time                    `json:"retrieved_at"`
	TotalSpendRub        string                       `json:"total_spend_rub"`
	AttributedOrders     int64                        `json:"attributed_orders"`
	AttributedRevenueRub string                       `json:"attributed_revenue_rub"`
	UnattributedSpendRub string                       `json:"unattributed_spend_rub"`
	ObservedCPORub       *string                      `json:"observed_cpo_rub"`
	AttributionVerified  bool                         `json:"attribution_verified"`
	ObservedDays         int64                        `json:"observed_days"`
	MissingDaysCount     int64                        `json:"missing_days_count"`
	Strategies           []ManagerAdStrategyBreakdown `json:"strategies"`
}

type ManagerAdManualResolution struct {
	ID                     uuid.UUID       `json:"id"`
	Fingerprint            string          `json:"fingerprint"`
	WorkspaceID            uuid.UUID       `json:"-"`
	ActorID                uuid.UUID       `json:"-"`
	CabinetID              uuid.UUID       `json:"-"`
	ExternalUserID         string          `json:"-"`
	ExternalWorkspaceID    string          `json:"workspace_id"`
	Status                 string          `json:"status"`
	OperationID            uuid.UUID       `json:"operation_id"`
	OperationType          string          `json:"operation_type"`
	IntegrationID          string          `json:"integration_id"`
	IntegrationName        string          `json:"integration_name"`
	Marketplace            string          `json:"marketplace"`
	CampaignID             uuid.UUID       `json:"campaign_id"`
	ExternalID             string          `json:"external_id"`
	Reason                 string          `json:"reason"`
	SupportReference       string          `json:"support_reference"`
	Confirmations          map[string]bool `json:"confirmations,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	ExpiresAt              time.Time       `json:"expires_at"`
	ResolvedAt             *time.Time      `json:"resolved_at"`
	ControlHoldActive      bool            `json:"control_hold_active"`
	NativeOutcomeVerified  bool            `json:"native_outcome_verified"`
	NativeCampaignsChanged bool            `json:"native_campaigns_changed"`
	ApprovalAvailable      bool            `json:"approval_available"`
}
