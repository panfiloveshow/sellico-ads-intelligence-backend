package domain

import (
	"github.com/google/uuid"
	"time"
)

// NativeAdProductBid retains exact money and the native strategy. Missing
// limits never become zero. An amount whose native unit is ambiguous is kept
// as raw evidence and cannot be labelled as a verified RUB minimum.
type NativeAdProductBid struct {
	ProductID            string            `json:"product_id"`
	Placement            string            `json:"placement"`
	CurrentBidRub        string            `json:"current_bid_rub"`
	MinimumBidRub        *string           `json:"minimum_bid_rub"`
	MaximumBidRub        *string           `json:"maximum_bid_rub"`
	MinimumSource        string            `json:"minimum_source"`
	UnverifiedMinimumRaw *string           `json:"unverified_minimum_raw,omitempty"`
	Strategy             string            `json:"strategy"`
	PaymentType          string            `json:"payment_type"`
	PeerBidsRub          map[string]string `json:"peer_bids_rub"`
	Warnings             []string          `json:"warnings"`
}

type NativeAdBidContext struct {
	Campaign NativeAdCampaign   `json:"campaign"`
	Bid      NativeAdProductBid `json:"bid"`
}

// Personal constraints apply only to this immutable bid decision. They are
// explicit user inputs, not evidence of profitability or complete vendor limits.
type ManagerAdBidConstraints struct {
	MaxBidRub        string `json:"max_bid_rub"`
	MaxChangePercent string `json:"max_change_percent"`
}
type ManagerAdBidPlan struct {
	Before       NativeAdProductBid      `json:"before"`
	TargetBidRub string                  `json:"target_bid_rub"`
	Constraints  ManagerAdBidConstraints `json:"constraints"`
}

type ManagerAdProductBidContext struct {
	NativeAdBidContext
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
}
