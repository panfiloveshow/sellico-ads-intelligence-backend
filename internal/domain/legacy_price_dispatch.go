package domain

import "time"

// LegacyPriceDispatchRequest is an authenticated service attestation. It is not
// a personal price approval and must never authorize a manual-only account.
type LegacyPriceDispatchRequest struct {
	Version       string `json:"version"`
	Origin        string `json:"origin"`
	CommandID     string `json:"command_id"`
	SentAt        int64  `json:"sent_at"`
	Marketplace   string `json:"marketplace"`
	Method        string `json:"method"`
	AccountKey    string `json:"account_key"`
	Endpoint      string `json:"endpoint"`
	PayloadSHA256 string `json:"payload_sha256"`
}

// A grant records a dispatch barrier, not a native receipt or a price change.
type LegacyPriceDispatchGrant struct {
	Version        string    `json:"version"`
	Origin         string    `json:"origin"`
	CommandID      string    `json:"command_id"`
	Marketplace    string    `json:"marketplace"`
	Method         string    `json:"method"`
	Endpoint       string    `json:"endpoint"`
	PayloadSHA256  string    `json:"payload_sha256"`
	Authorized     bool      `json:"authorized"`
	Scope          string    `json:"scope"`
	ControlVersion int64     `json:"control_version"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	ValidForMS     int64     `json:"valid_for_ms"`
}
