package domain

import (
	"time"

	"github.com/google/uuid"
)

// LegacyPriceJournalItem describes a recorded admission, never a Native result.
type LegacyPriceJournalItem struct {
	CommandID                uuid.UUID  `json:"command_id"`
	Marketplace              string     `json:"marketplace"`
	OperationKind            string     `json:"operation_kind"`
	IssuedAt                 time.Time  `json:"issued_at"`
	AdmissionExpiresAt       time.Time  `json:"admission_expires_at"`
	Status                   string     `json:"status"`
	HoldActive               bool       `json:"hold_active"`
	NativeOutcomeVerified    bool       `json:"native_outcome_verified"`
	ResolutionID             *uuid.UUID `json:"resolution_id"`
	ReviewContextFingerprint string     `json:"review_context_fingerprint"`
	PersonalReviewAvailable  bool       `json:"personal_review_available"`
}

type LegacyPriceJournalContext struct {
	Version           string                     `json:"version"`
	WorkspaceID       string                     `json:"workspace_id"`
	IntegrationID     string                     `json:"integration_id"`
	IntegrationName   string                     `json:"integration_name"`
	Marketplace       string                     `json:"marketplace"`
	Scope             string                     `json:"scope"`
	AccountProof      PriceExecutionAccountProof `json:"account_proof"`
	Control           PriceExecutionControlState `json:"control"`
	Items             []LegacyPriceJournalItem   `json:"items"`
	NextCursor        *string                    `json:"next_cursor"`
	HeldCount         int64                      `json:"held_count"`
	ApprovalAvailable bool                       `json:"approval_available"`
}

type LegacyPriceReviewOperation struct {
	ID                    uuid.UUID                   `json:"id"`
	WorkspaceID           uuid.UUID                   `json:"-"`
	ActorID               uuid.UUID                   `json:"-"`
	CabinetID             uuid.UUID                   `json:"-"`
	ExternalUserID        string                      `json:"-"`
	ExternalWorkspaceID   string                      `json:"workspace_id"`
	IntegrationID         string                      `json:"integration_id"`
	IntegrationName       string                      `json:"integration_name"`
	Marketplace           string                      `json:"marketplace"`
	AccountKey            string                      `json:"-"`
	CredentialDigest      string                      `json:"-"`
	GrantFingerprint      string                      `json:"-"`
	Scope                 string                      `json:"scope"`
	Entry                 LegacyPriceJournalItem      `json:"entry"`
	Before                PriceExecutionControlState  `json:"before"`
	Proof                 PriceExecutionAccountProof  `json:"account_proof"`
	Reason                string                      `json:"reason"`
	SupportReference      string                      `json:"support_reference"`
	Fingerprint           string                      `json:"fingerprint"`
	Status                string                      `json:"status"`
	Confirmations         map[string]bool             `json:"confirmations"`
	CreatedAt             time.Time                   `json:"created_at"`
	ExpiresAt             time.Time                   `json:"expires_at"`
	ResolvedAt            *time.Time                  `json:"resolved_at"`
	CurrentControl        *PriceExecutionControlState `json:"current_control"`
	CurrentHoldActive     *bool                       `json:"current_hold_active"`
	CurrentHeldCount      *int64                      `json:"current_held_count"`
	CurrentCheckedAt      *time.Time                  `json:"current_checked_at"`
	ApprovalAvailable     bool                        `json:"approval_available"`
	NativeOutcomeVerified bool                        `json:"native_outcome_verified"`
	NativePricesChanged   bool                        `json:"native_prices_changed"`
}
