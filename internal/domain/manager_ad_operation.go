package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

type ManagerAdResult struct {
	LaunchVerified         bool                    `json:"launch_verified,omitempty"`
	LaunchSettings         *NativeAdLaunchSettings `json:"launch_settings,omitempty"`
	FieldsVerified         bool                    `json:"fields_verified"`
	SubmissionAcknowledged bool                    `json:"submission_acknowledged"`
	VendorState            *string                 `json:"vendor_state"`
	ControlHoldActive      bool                    `json:"control_hold_active"`
	ObservedAt             *time.Time              `json:"observed_at"`
	ControlTransferredTo   *uuid.UUID              `json:"control_transferred_to,omitempty"`
	ControlReleased        bool                    `json:"control_released,omitempty"`
	BidVerified            bool                    `json:"bid_verified,omitempty"`
	BidRub                 *string                 `json:"bid_rub,omitempty"`
	ProductID              *string                 `json:"product_id,omitempty"`
	Placement              *string                 `json:"placement,omitempty"`
	MirrorUpdated          *bool                   `json:"mirror_updated,omitempty"`
	BudgetVerified         bool                    `json:"budget_verified,omitempty"`
	Budget                 *NativeAdBudget         `json:"budget,omitempty"`
}

// Request and credential digest stay server-side. The public proposal contains
// the exact native observation and the one intended state transition.
type ManagerAdOperation struct {
	ID                  uuid.UUID            `json:"id"`
	WorkspaceID         uuid.UUID            `json:"-"`
	ActorID             uuid.UUID            `json:"-"`
	ExternalUserID      string               `json:"-"`
	ExternalWorkspaceID string               `json:"workspace_id"`
	IntegrationID       string               `json:"integration_id"`
	IntegrationName     string               `json:"integration_name"`
	CabinetID           uuid.UUID            `json:"-"`
	CampaignID          uuid.UUID            `json:"campaign_id"`
	ExternalID          string               `json:"external_id"`
	Marketplace         string               `json:"marketplace"`
	OperationType       string               `json:"operation_type"`
	Action              string               `json:"action"`
	Snapshot            NativeAdCampaign     `json:"snapshot"`
	TargetStatus        string               `json:"target_status"`
	Source              string               `json:"source"`
	Request             adguard.Request      `json:"-"`
	CredentialDigest    string               `json:"-"`
	PreviousOperationID *uuid.UUID           `json:"previous_operation_id,omitempty"`
	PreviousFingerprint string               `json:"-"`
	BidPlan             *ManagerAdBidPlan    `json:"bid_plan,omitempty"`
	BudgetPlan          *ManagerAdBudgetPlan `json:"budget_plan,omitempty"`
	LaunchPlan          *ManagerAdLaunchPlan `json:"launch_plan,omitempty"`
	Fingerprint         string               `json:"fingerprint"`
	Status              string               `json:"status"`
	MessageCode         *string              `json:"message_code"`
	Result              *ManagerAdResult     `json:"result"`
	CreatedAt           time.Time            `json:"created_at"`
	ExpiresAt           time.Time            `json:"expires_at"`
	ApprovalAvailable   bool                 `json:"approval_available"`
	ExecutionAllowed    bool                 `json:"execution_allowed"`
}
