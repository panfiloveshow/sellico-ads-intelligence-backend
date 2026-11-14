package domain

import (
	"github.com/google/uuid"
	"time"
)

// A service write restriction is independent of the native prices.
type PriceExecutionControlState struct {
	Mode      string     `json:"mode"`
	Version   int64      `json:"version"`
	ChangedAt *time.Time `json:"changed_at"`
}
type PriceExecutionAccountProof struct {
	ResourceID    string    `json:"resource_id"`
	ResourceLabel string    `json:"resource_label"`
	Source        string    `json:"source"`
	ObservedAt    time.Time `json:"observed_at"`
}
type PriceExecutionControlOperation struct {
	ID                  uuid.UUID                   `json:"id"`
	WorkspaceID         uuid.UUID                   `json:"-"`
	ActorID             uuid.UUID                   `json:"-"`
	ExternalUserID      string                      `json:"-"`
	ExternalWorkspaceID string                      `json:"workspace_id"`
	IntegrationID       string                      `json:"integration_id"`
	IntegrationName     string                      `json:"integration_name"`
	CabinetID           uuid.UUID                   `json:"-"`
	Marketplace         string                      `json:"marketplace"`
	AccountKey          string                      `json:"-"`
	CredentialDigest    string                      `json:"-"`
	Scope               string                      `json:"scope"`
	TargetMode          string                      `json:"target_mode"`
	Before              PriceExecutionControlState  `json:"before"`
	Proof               PriceExecutionAccountProof  `json:"account_proof"`
	Reason              string                      `json:"reason"`
	Fingerprint         string                      `json:"fingerprint"`
	Status              string                      `json:"status"`
	CreatedAt           time.Time                   `json:"created_at"`
	ExpiresAt           time.Time                   `json:"expires_at"`
	ResultControl       *PriceExecutionControlState `json:"result_control"`
	CurrentControl      *PriceExecutionControlState `json:"current_control"`
	CurrentCheckedAt    *time.Time                  `json:"current_checked_at"`
	ApprovalAvailable   bool                        `json:"approval_available"`
	NativePricesChanged bool                        `json:"native_prices_changed"`
}

type PriceExecutionControlContext struct {
	WorkspaceID        string                     `json:"workspace_id"`
	IntegrationID      string                     `json:"integration_id"`
	IntegrationName    string                     `json:"integration_name"`
	Marketplace        string                     `json:"marketplace"`
	Scope              string                     `json:"scope"`
	AccountProof       PriceExecutionAccountProof `json:"account_proof"`
	Control            PriceExecutionControlState `json:"control"`
	ContextFingerprint string                     `json:"context_fingerprint"`
	ApprovalAvailable  bool                       `json:"approval_available"`
	// LegacyHeldCount is the number of recorded legacy admissions of this
	// account without a committed personal resolution. Any positive value holds
	// every new Native price command of the account. It is reported by the
	// public context read only and is deliberately outside ContextFingerprint.
	LegacyHeldCount int64 `json:"legacy_held_count"`
}
