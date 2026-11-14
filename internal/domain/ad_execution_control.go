package domain

import (
	"github.com/google/uuid"
	"time"
)

// A service write restriction is independent of native campaign activity.
type AdExecutionControlState struct {
	Mode      string     `json:"mode"`
	Version   int64      `json:"version"`
	ChangedAt *time.Time `json:"changed_at"`
}
type AdExecutionAccountProof struct {
	CampaignID   uuid.UUID `json:"campaign_id"`
	ExternalID   string    `json:"external_id"`
	CampaignName string    `json:"campaign_name"`
	Source       string    `json:"source"`
	ObservedAt   time.Time `json:"observed_at"`
}
type AdExecutionControlOperation struct {
	ID                     uuid.UUID                `json:"id"`
	WorkspaceID            uuid.UUID                `json:"-"`
	ActorID                uuid.UUID                `json:"-"`
	ExternalUserID         string                   `json:"-"`
	ExternalWorkspaceID    string                   `json:"workspace_id"`
	IntegrationID          string                   `json:"integration_id"`
	IntegrationName        string                   `json:"integration_name"`
	CabinetID              uuid.UUID                `json:"-"`
	Marketplace            string                   `json:"marketplace"`
	AccountKey             string                   `json:"-"`
	CredentialDigest       string                   `json:"-"`
	Scope                  string                   `json:"scope"`
	TargetMode             string                   `json:"target_mode"`
	Before                 AdExecutionControlState  `json:"before"`
	Proof                  AdExecutionAccountProof  `json:"account_proof"`
	Reason                 string                   `json:"reason"`
	Fingerprint            string                   `json:"fingerprint"`
	Status                 string                   `json:"status"`
	CreatedAt              time.Time                `json:"created_at"`
	ExpiresAt              time.Time                `json:"expires_at"`
	ResultControl          *AdExecutionControlState `json:"result_control"`
	CurrentControl         *AdExecutionControlState `json:"current_control"`
	CurrentCheckedAt       *time.Time               `json:"current_checked_at"`
	ApprovalAvailable      bool                     `json:"approval_available"`
	NativeCampaignsChanged bool                     `json:"native_campaigns_changed"`
}

type AdExecutionControlContext struct {
	WorkspaceID        string                  `json:"workspace_id"`
	IntegrationID      string                  `json:"integration_id"`
	IntegrationName    string                  `json:"integration_name"`
	Marketplace        string                  `json:"marketplace"`
	Scope              string                  `json:"scope"`
	AccountProof       AdExecutionAccountProof `json:"account_proof"`
	Control            AdExecutionControlState `json:"control"`
	ContextFingerprint string                  `json:"context_fingerprint"`
	ApprovalAvailable  bool                    `json:"approval_available"`
}
