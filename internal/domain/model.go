package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// --- Role constants ---

const (
	RoleOwner   = "owner"
	RoleManager = "manager"
	RoleAnalyst = "analyst"
	RoleViewer  = "viewer"
)

// --- Seller Cabinet status constants ---

const (
	StatusActive   = "active"
	StatusInactive = "inactive"
	StatusError    = "error"
)

// --- Campaign bid type constants ---

const (
	BidTypeManual  = "manual"
	BidTypeUnified = "unified"
)

// --- Campaign payment type constants ---

const (
	PaymentTypeCPM = "cpm"
	PaymentTypeCPC = "cpc"
)

// --- Position source constants ---

const (
	SourceParser       = "parser"
	SourceAnalyticsCSV = "analytics_csv"
)

// --- Recommendation type constants ---

const (
	RecommendationTypeBidAdjustment      = "bid_adjustment"
	RecommendationTypePositionDrop       = "position_drop"
	RecommendationTypeLowCTR             = "low_ctr"
	RecommendationTypeHighSpendLowOrders = "high_spend_low_orders"
	RecommendationTypeNewCompetitor      = "new_competitor"
)

// --- Recommendation severity constants ---

const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// --- Recommendation status constants ---

const (
	RecommendationStatusActive    = "active"
	RecommendationStatusCompleted = "completed"
	RecommendationStatusDismissed = "dismissed"
)

// --- Export format constants ---

const (
	ExportFormatCSV  = "csv"
	ExportFormatXLSX = "xlsx"
)

// --- Export status constants ---

const (
	ExportStatusPending    = "pending"
	ExportStatusProcessing = "processing"
	ExportStatusCompleted  = "completed"
	ExportStatusFailed     = "failed"
)

// --- Job run status constants ---

const (
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
)

// --- Domain models ---

// User represents a registered user.
type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RefreshToken represents a stored refresh token for a user session.
type RefreshToken struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

// Workspace represents an isolated tenant workspace.
type Workspace struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// WorkspaceMember represents a user's membership in a workspace with an RBAC role.
type WorkspaceMember struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	UserID      uuid.UUID `json:"user_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SellerCabinet represents a connected Wildberries seller account.
type SellerCabinet struct {
	ID             uuid.UUID  `json:"id"`
	WorkspaceID    uuid.UUID  `json:"workspace_id"`
	Name           string     `json:"name"`
	EncryptedToken string     `json:"-"`
	Status         string     `json:"status"`
	LastSyncedAt   *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

// Campaign represents a Wildberries advertising campaign.
type Campaign struct {
	ID              uuid.UUID `json:"id"`
	WorkspaceID     uuid.UUID `json:"workspace_id"`
	SellerCabinetID uuid.UUID `json:"seller_cabinet_id"`
	WBCampaignID    int64     `json:"wb_campaign_id"`
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	CampaignType    int       `json:"campaign_type"`
	BidType         string    `json:"bid_type"`
	PaymentType     string    `json:"payment_type"`
	DailyBudget     *int64    `json:"daily_budget,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CampaignStat represents daily statistics for a campaign.
type CampaignStat struct {
	ID          uuid.UUID `json:"id"`
	CampaignID  uuid.UUID `json:"campaign_id"`
	Date        time.Time `json:"date"`
	Impressions int64     `json:"impressions"`
	Clicks      int64     `json:"clicks"`
	Spend       int64     `json:"spend"`
	Orders      *int64    `json:"orders,omitempty"`
	Revenue     *int64    `json:"revenue,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Phrase represents a search cluster (keyword phrase) linked to a campaign.
type Phrase struct {
	ID          uuid.UUID `json:"id"`
	CampaignID  uuid.UUID `json:"campaign_id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	WBClusterID int64     `json:"wb_cluster_id"`
	Keyword     string    `json:"keyword"`
	Count       *int      `json:"count,omitempty"`
	CurrentBid  *int64    `json:"current_bid,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PhraseStat represents daily statistics for a phrase.
type PhraseStat struct {
	ID          uuid.UUID `json:"id"`
	PhraseID    uuid.UUID `json:"phrase_id"`
	Date        time.Time `json:"date"`
	Impressions int64     `json:"impressions"`
	Clicks      int64     `json:"clicks"`
	Spend       int64     `json:"spend"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Product represents a Wildberries product linked to a seller cabinet.
type Product struct {
	ID              uuid.UUID `json:"id"`
	WorkspaceID     uuid.UUID `json:"workspace_id"`
	SellerCabinetID uuid.UUID `json:"seller_cabinet_id"`
	WBProductID     int64     `json:"wb_product_id"`
	Title           string    `json:"title"`
	Brand           *string   `json:"brand,omitempty"`
	Category        *string   `json:"category,omitempty"`
	ImageURL        *string   `json:"image_url,omitempty"`
	Price           *int64    `json:"price,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Position represents a product's position in Wildberries search results.
type Position struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	ProductID   uuid.UUID `json:"product_id"`
	Query       string    `json:"query"`
	Region      string    `json:"region"`
	Position    int       `json:"position"`
	Page        int       `json:"page"`
	Source      string    `json:"source"`
	CheckedAt   time.Time `json:"checked_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// PositionAggregate represents an aggregated position metric over a time range.
type PositionAggregate struct {
	ProductID   uuid.UUID `json:"product_id"`
	Query       string    `json:"query"`
	Region      string    `json:"region"`
	Average     float64   `json:"average"`
	DateFrom    time.Time `json:"date_from"`
	DateTo      time.Time `json:"date_to"`
	SampleCount int       `json:"sample_count"`
}

// SERPSnapshot represents a snapshot of Wildberries search results.
type SERPSnapshot struct {
	ID           uuid.UUID `json:"id"`
	WorkspaceID  uuid.UUID `json:"workspace_id"`
	Query        string    `json:"query"`
	Region       string    `json:"region"`
	TotalResults int       `json:"total_results"`
	ScannedAt    time.Time `json:"scanned_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// SERPResultItem represents a single product entry within a SERP snapshot.
type SERPResultItem struct {
	ID           uuid.UUID `json:"id"`
	SnapshotID   uuid.UUID `json:"snapshot_id"`
	Position     int       `json:"position"`
	WBProductID  int64     `json:"wb_product_id"`
	Title        string    `json:"title"`
	Price        *int64    `json:"price,omitempty"`
	Rating       *float64  `json:"rating,omitempty"`
	ReviewsCount *int      `json:"reviews_count,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// BidSnapshot represents a snapshot of recommended bids for a phrase.
type BidSnapshot struct {
	ID             uuid.UUID `json:"id"`
	PhraseID       uuid.UUID `json:"phrase_id"`
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	CompetitiveBid int64     `json:"competitive_bid"`
	LeadershipBid  int64     `json:"leadership_bid"`
	CPMMin         int64     `json:"cpm_min"`
	CapturedAt     time.Time `json:"captured_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// Recommendation represents a generated optimization recommendation.
type Recommendation struct {
	ID            uuid.UUID       `json:"id"`
	WorkspaceID   uuid.UUID       `json:"workspace_id"`
	CampaignID    *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID      *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID     *uuid.UUID      `json:"product_id,omitempty"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Type          string          `json:"type"`
	Severity      string          `json:"severity"`
	Confidence    float64         `json:"confidence"`
	SourceMetrics json.RawMessage `json:"source_metrics"`
	NextAction    *string         `json:"next_action,omitempty"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Export represents a data export task.
type Export struct {
	ID           uuid.UUID       `json:"id"`
	WorkspaceID  uuid.UUID       `json:"workspace_id"`
	UserID       uuid.UUID       `json:"user_id"`
	EntityType   string          `json:"entity_type"`
	Format       string          `json:"format"`
	Status       string          `json:"status"`
	FilePath     *string         `json:"file_path,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	Filters      json.RawMessage `json:"filters,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// ExtensionSession represents a Chrome extension session.
type ExtensionSession struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"user_id"`
	WorkspaceID      uuid.UUID `json:"workspace_id"`
	ExtensionVersion string    `json:"extension_version"`
	LastActiveAt     time.Time `json:"last_active_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// ExtensionContextEvent represents an event captured from the browser extension.
type ExtensionContextEvent struct {
	ID          uuid.UUID       `json:"id"`
	SessionID   uuid.UUID       `json:"session_id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	UserID      uuid.UUID       `json:"user_id"`
	URL         string          `json:"url"`
	PageType    string          `json:"page_type"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AuditLog represents an audit trail entry for user or system actions.
type AuditLog struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	UserID      *uuid.UUID      `json:"user_id,omitempty"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    *uuid.UUID      `json:"entity_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// JobRun represents a record of a background task execution.
type JobRun struct {
	ID           uuid.UUID       `json:"id"`
	WorkspaceID  *uuid.UUID      `json:"workspace_id,omitempty"`
	TaskType     string          `json:"task_type"`
	Status       string          `json:"status"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}
