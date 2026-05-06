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
	SourceAPI          = "api"
	SourceDerived      = "derived"
	SourceExtension    = "extension"
)

// --- Recommendation type constants ---

const (
	RecommendationTypeBidAdjustment      = "bid_adjustment"
	RecommendationTypeRaiseBid           = "raise_bid"
	RecommendationTypeLowerBid           = "lower_bid"
	RecommendationTypePositionDrop       = "position_drop"
	RecommendationTypeLowCTR             = "low_ctr"
	RecommendationTypeHighSpendLowOrders = "high_spend_low_orders"
	RecommendationTypeNewCompetitor      = "new_competitor"
	RecommendationTypeDisablePhrase      = "disable_phrase"
	RecommendationTypeAddMinusPhrase     = "add_minus_phrase"
	RecommendationTypeImproveTitle       = "improve_title"
	RecommendationTypeImproveDescription = "improve_description"
	RecommendationTypeOptimizeSEO        = "optimize_seo"
	RecommendationTypePriceOptimization  = "price_optimization"
	RecommendationTypePhotoImprovement   = "photo_improvement"
	RecommendationTypeDeliveryIssue      = "delivery_issue"
	RecommendationTypeStockAlert         = "stock_alert"
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

type SellerCabinetAutoSyncSummary struct {
	JobRunID       uuid.UUID  `json:"job_run_id"`
	Status         string     `json:"status"`
	ResultState    string     `json:"result_state"`
	FreshnessState string     `json:"freshness_state"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Cabinets       int        `json:"cabinets"`
	Campaigns      int        `json:"campaigns"`
	CampaignStats  int        `json:"campaign_stats"`
	Phrases        int        `json:"phrases"`
	PhraseStats    int        `json:"phrase_stats"`
	Products       int        `json:"products"`
	SyncIssues     int        `json:"sync_issues"`
}

// SellerCabinet represents a connected Wildberries seller account.
type SellerCabinet struct {
	ID                    uuid.UUID                     `json:"id"`
	WorkspaceID           uuid.UUID                     `json:"workspace_id"`
	Name                  string                        `json:"name"`
	EncryptedToken        string                        `json:"-"`
	Status                string                        `json:"status"`
	ExternalIntegrationID *string                       `json:"external_integration_id,omitempty"`
	Source                string                        `json:"source"`
	IntegrationType       *string                       `json:"integration_type,omitempty"`
	LastSyncedAt          *time.Time                    `json:"last_synced_at,omitempty"`
	LastSellicoSyncAt     *time.Time                    `json:"last_sellico_sync_at,omitempty"`
	LastAutoSync          *SellerCabinetAutoSyncSummary `json:"last_auto_sync,omitempty"`
	CreatedAt             time.Time                     `json:"created_at"`
	UpdatedAt             time.Time                     `json:"updated_at"`
	DeletedAt             *time.Time                    `json:"deleted_at,omitempty"`
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
	Atbs        *int64    `json:"atbs,omitempty"`     // Добавления в корзину
	Canceled    *int64    `json:"canceled,omitempty"` // Технические отмены
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

// ProductStat represents daily advertising statistics for a product inside a campaign.
type ProductStat struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	CampaignID  uuid.UUID `json:"campaign_id"`
	Date        time.Time `json:"date"`
	Impressions int64     `json:"impressions"`
	Clicks      int64     `json:"clicks"`
	Spend       int64     `json:"spend"`
	Orders      *int64    `json:"orders,omitempty"`
	Revenue     *int64    `json:"revenue,omitempty"`
	Atbs        *int64    `json:"atbs,omitempty"`
	Canceled    *int64    `json:"canceled,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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

type PositionTrackingTarget struct {
	ID                uuid.UUID  `json:"id"`
	WorkspaceID       uuid.UUID  `json:"workspace_id"`
	ProductID         uuid.UUID  `json:"product_id"`
	ProductTitle      string     `json:"product_title"`
	Query             string     `json:"query"`
	Region            string     `json:"region"`
	IsActive          bool       `json:"is_active"`
	BaselinePosition  *int       `json:"baseline_position,omitempty"`
	BaselineCheckedAt *time.Time `json:"baseline_checked_at,omitempty"`
	LatestPosition    *int       `json:"latest_position,omitempty"`
	LatestPage        *int       `json:"latest_page,omitempty"`
	LatestCheckedAt   *time.Time `json:"latest_checked_at,omitempty"`
	Delta             *int       `json:"delta,omitempty"`
	SampleCount       int        `json:"sample_count"`
	AlertCandidate    bool       `json:"alert_candidate"`
	AlertSeverity     string     `json:"alert_severity,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
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

// SERPCompareItem represents one product position delta between two SERP snapshots.
type SERPCompareItem struct {
	WBProductID      int64  `json:"wb_product_id"`
	Title            string `json:"title"`
	IsOwnProduct     bool   `json:"is_own_product"`
	CurrentPosition  *int   `json:"current_position,omitempty"`
	PreviousPosition *int   `json:"previous_position,omitempty"`
	Delta            *int   `json:"delta,omitempty"`
}

// SERPComparison represents a before/after diff between two snapshots of the same query and region.
type SERPComparison struct {
	PreviousSnapshotID *uuid.UUID        `json:"previous_snapshot_id,omitempty"`
	PreviousScannedAt  *time.Time        `json:"previous_scanned_at,omitempty"`
	CurrentOwnCount    int               `json:"current_own_count"`
	PreviousOwnCount   int               `json:"previous_own_count"`
	NewEntrantsCount   int               `json:"new_entrants_count"`
	DroppedCount       int               `json:"dropped_count"`
	OwnProductsGained  int               `json:"own_products_gained"`
	OwnProductsLost    int               `json:"own_products_lost"`
	NewEntrants        []SERPCompareItem `json:"new_entrants"`
	DroppedItems       []SERPCompareItem `json:"dropped_items"`
	BiggestMovers      []SERPCompareItem `json:"biggest_movers"`
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
	ID              uuid.UUID       `json:"id"`
	WorkspaceID     uuid.UUID       `json:"workspace_id"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Type            string          `json:"type"`
	Severity        string          `json:"severity"`
	Confidence      float64         `json:"confidence"`
	SourceMetrics   json.RawMessage `json:"source_metrics"`
	NextAction      *string         `json:"next_action,omitempty"`
	Status          string          `json:"status"`
	Evidence        *SourceEvidence `json:"evidence,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type SourceEvidence struct {
	Source             string     `json:"source"`
	CapturedAt         *time.Time `json:"captured_at,omitempty"`
	FreshnessState     string     `json:"freshness_state"`
	Confidence         float64    `json:"confidence"`
	Coverage           string     `json:"coverage"`
	ConfirmedInCabinet bool       `json:"confirmed_in_cabinet"`
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

type ExtensionPageContext struct {
	ID              uuid.UUID       `json:"id"`
	SessionID       uuid.UUID       `json:"session_id"`
	WorkspaceID     uuid.UUID       `json:"workspace_id"`
	UserID          uuid.UUID       `json:"user_id"`
	URL             string          `json:"url"`
	PageType        string          `json:"page_type"`
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	Query           *string         `json:"query,omitempty"`
	Region          *string         `json:"region,omitempty"`
	ActiveFilters   json.RawMessage `json:"active_filters,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CapturedAt      time.Time       `json:"captured_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ExtensionBidSnapshot struct {
	ID              uuid.UUID       `json:"id"`
	SessionID       uuid.UUID       `json:"session_id"`
	WorkspaceID     uuid.UUID       `json:"workspace_id"`
	UserID          uuid.UUID       `json:"user_id"`
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	Query           *string         `json:"query,omitempty"`
	Region          *string         `json:"region,omitempty"`
	VisibleBid      *int64          `json:"visible_bid,omitempty"`
	RecommendedBid  *int64          `json:"recommended_bid,omitempty"`
	CompetitiveBid  *int64          `json:"competitive_bid,omitempty"`
	LeadershipBid   *int64          `json:"leadership_bid,omitempty"`
	CPMMin          *int64          `json:"cpm_min,omitempty"`
	Source          string          `json:"source"`
	Confidence      float64         `json:"confidence"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CapturedAt      time.Time       `json:"captured_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ExtensionPositionSnapshot struct {
	ID              uuid.UUID       `json:"id"`
	SessionID       uuid.UUID       `json:"session_id"`
	WorkspaceID     uuid.UUID       `json:"workspace_id"`
	UserID          uuid.UUID       `json:"user_id"`
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	Query           string          `json:"query"`
	Region          string          `json:"region"`
	VisiblePosition int             `json:"visible_position"`
	VisiblePage     *int            `json:"visible_page,omitempty"`
	PageSubtype     *string         `json:"page_subtype,omitempty"`
	Source          string          `json:"source"`
	Confidence      float64         `json:"confidence"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CapturedAt      time.Time       `json:"captured_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ExtensionUISignal struct {
	ID              uuid.UUID       `json:"id"`
	SessionID       uuid.UUID       `json:"session_id"`
	WorkspaceID     uuid.UUID       `json:"workspace_id"`
	UserID          uuid.UUID       `json:"user_id"`
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	Query           *string         `json:"query,omitempty"`
	Region          *string         `json:"region,omitempty"`
	SignalType      string          `json:"signal_type"`
	Severity        string          `json:"severity"`
	Title           string          `json:"title"`
	Message         *string         `json:"message,omitempty"`
	Confidence      float64         `json:"confidence"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CapturedAt      time.Time       `json:"captured_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

type ExtensionNetworkCapture struct {
	ID              uuid.UUID       `json:"id"`
	SessionID       uuid.UUID       `json:"session_id"`
	WorkspaceID     uuid.UUID       `json:"workspace_id"`
	UserID          uuid.UUID       `json:"user_id"`
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	PageType        string          `json:"page_type"`
	EndpointKey     string          `json:"endpoint_key"`
	Query           *string         `json:"query,omitempty"`
	Region          *string         `json:"region,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	CapturedAt      time.Time       `json:"captured_at"`
	CreatedAt       time.Time       `json:"created_at"`
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
	Evidence     *SourceEvidence `json:"evidence,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

type AdsMetricsSummary struct {
	Impressions    int64   `json:"impressions"`
	Clicks         int64   `json:"clicks"`
	Spend          int64   `json:"spend"`
	Orders         int64   `json:"orders"`
	Revenue        int64   `json:"revenue"`
	Atbs           int64   `json:"atbs"`     // Добавления в корзину
	Canceled       int64   `json:"canceled"` // Технические отмены
	CTR            float64 `json:"ctr"`
	CPC            float64 `json:"cpc"`
	CPO            float64 `json:"cpo"`
	ROAS           float64 `json:"roas"`
	DRR            float64 `json:"drr"` // ДРР = Spend/Revenue × 100%
	ConversionRate float64 `json:"conversion_rate"`
	CartRate       float64 `json:"cart_rate"` // Корзина = Atbs/Clicks
	DataMode       string  `json:"data_mode"`
}

type AdsMetricsDelta struct {
	Impressions    int64   `json:"impressions"`
	Clicks         int64   `json:"clicks"`
	Spend          int64   `json:"spend"`
	Orders         int64   `json:"orders"`
	Revenue        int64   `json:"revenue"`
	CTR            float64 `json:"ctr"`
	CPC            float64 `json:"cpc"`
	CPO            float64 `json:"cpo"`
	ROAS           float64 `json:"roas"`
	ConversionRate float64 `json:"conversion_rate"`
}

type AdsPeriodCompare struct {
	Current  AdsMetricsSummary `json:"current"`
	Previous AdsMetricsSummary `json:"previous"`
	Delta    AdsMetricsDelta   `json:"delta"`
	Trend    string            `json:"trend"`
}

type AdsEntityRef struct {
	ID     uuid.UUID `json:"id"`
	Label  string    `json:"label"`
	WBID   *int64    `json:"wb_id,omitempty"`
	Count  *int      `json:"count,omitempty"`
	Source string    `json:"source,omitempty"`
}

type AttentionItem struct {
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Severity    string  `json:"severity"`
	ActionLabel string  `json:"action_label"`
	ActionPath  string  `json:"action_path"`
	SourceType  string  `json:"source_type"`
	SourceID    *string `json:"source_id,omitempty"`
}

type CabinetSummary struct {
	ID                   string                        `json:"id"`
	CabinetID            uuid.UUID                     `json:"cabinet_id"`
	IntegrationID        *string                       `json:"integration_id,omitempty"`
	IntegrationName      string                        `json:"integration_name"`
	CabinetName          string                        `json:"cabinet_name"`
	Status               string                        `json:"status"`
	FreshnessState       string                        `json:"freshness_state"`
	LastSync             *time.Time                    `json:"last_sync,omitempty"`
	LastAutoSync         *SellerCabinetAutoSyncSummary `json:"last_auto_sync,omitempty"`
	CampaignsCount       int                           `json:"campaigns_count"`
	ProductsCount        int                           `json:"products_count"`
	QueriesCount         int                           `json:"queries_count"`
	ActiveCampaignsCount int                           `json:"active_campaigns_count"`
}

type ProductAdsSummary struct {
	ID               uuid.UUID         `json:"id"`
	WorkspaceID      uuid.UUID         `json:"workspace_id"`
	SellerCabinetID  uuid.UUID         `json:"seller_cabinet_id"`
	IntegrationID    *string           `json:"integration_id,omitempty"`
	IntegrationName  string            `json:"integration_name"`
	CabinetName      string            `json:"cabinet_name"`
	WBProductID      int64             `json:"wb_product_id"`
	Title            string            `json:"title"`
	Brand            *string           `json:"brand,omitempty"`
	Category         *string           `json:"category,omitempty"`
	ImageURL         *string           `json:"image_url,omitempty"`
	Price            *int64            `json:"price,omitempty"`
	CampaignsCount   int               `json:"campaigns_count"`
	QueriesCount     int               `json:"queries_count"`
	HealthStatus     string            `json:"health_status"`
	HealthReason     *string           `json:"health_reason,omitempty"`
	PrimaryAction    *string           `json:"primary_action,omitempty"`
	FreshnessState   string            `json:"freshness_state"`
	Performance      AdsMetricsSummary `json:"performance"`
	PeriodCompare    *AdsPeriodCompare `json:"period_compare,omitempty"`
	RelatedCampaigns []AdsEntityRef    `json:"related_campaigns,omitempty"`
	TopQueries       []AdsEntityRef    `json:"top_queries,omitempty"`
	WasteQueries     []AdsEntityRef    `json:"waste_queries,omitempty"`
	WinningQueries   []AdsEntityRef    `json:"winning_queries,omitempty"`
	Evidence         *SourceEvidence   `json:"evidence,omitempty"`
	DataCoverageNote *string           `json:"data_coverage_note,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type CampaignPerformanceSummary struct {
	ID              uuid.UUID         `json:"id"`
	WorkspaceID     uuid.UUID         `json:"workspace_id"`
	SellerCabinetID uuid.UUID         `json:"seller_cabinet_id"`
	IntegrationID   *string           `json:"integration_id,omitempty"`
	IntegrationName string            `json:"integration_name"`
	CabinetName     string            `json:"cabinet_name"`
	WBCampaignID    int64             `json:"wb_campaign_id"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	CampaignType    int               `json:"campaign_type"`
	BidType         string            `json:"bid_type"`
	PaymentType     string            `json:"payment_type"`
	DailyBudget     *int64            `json:"daily_budget,omitempty"`
	LastSync        *time.Time        `json:"last_sync,omitempty"`
	HealthStatus    string            `json:"health_status"`
	HealthReason    *string           `json:"health_reason,omitempty"`
	PrimaryAction   *string           `json:"primary_action,omitempty"`
	FreshnessState  string            `json:"freshness_state"`
	Performance     AdsMetricsSummary `json:"performance"`
	PeriodCompare   *AdsPeriodCompare `json:"period_compare,omitempty"`
	RelatedProducts []AdsEntityRef    `json:"related_products,omitempty"`
	TopQueries      []AdsEntityRef    `json:"top_queries,omitempty"`
	WasteQueries    []AdsEntityRef    `json:"waste_queries,omitempty"`
	WinningQueries  []AdsEntityRef    `json:"winning_queries,omitempty"`
	Evidence        *SourceEvidence   `json:"evidence,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type QueryPerformanceSummary struct {
	ID              uuid.UUID         `json:"id"`
	WorkspaceID     uuid.UUID         `json:"workspace_id"`
	CampaignID      uuid.UUID         `json:"campaign_id"`
	SellerCabinetID uuid.UUID         `json:"seller_cabinet_id"`
	IntegrationID   *string           `json:"integration_id,omitempty"`
	IntegrationName string            `json:"integration_name"`
	CabinetName     string            `json:"cabinet_name"`
	CampaignName    string            `json:"campaign_name"`
	WBCampaignID    int64             `json:"wb_campaign_id"`
	WBClusterID     int64             `json:"wb_cluster_id"`
	Keyword         string            `json:"keyword"`
	CurrentBid      *int64            `json:"current_bid,omitempty"`
	ClusterSize     *int              `json:"cluster_size,omitempty"`
	Source          string            `json:"source"`
	SignalCategory  string            `json:"signal_category"`
	HealthStatus    string            `json:"health_status"`
	HealthReason    *string           `json:"health_reason,omitempty"`
	PrimaryAction   *string           `json:"primary_action,omitempty"`
	FreshnessState  string            `json:"freshness_state"`
	Performance     AdsMetricsSummary `json:"performance"`
	PeriodCompare   *AdsPeriodCompare `json:"period_compare,omitempty"`
	PriorityScore   int               `json:"priority_score"`
	RelatedProducts []AdsEntityRef    `json:"related_products,omitempty"`
	Evidence        *SourceEvidence   `json:"evidence,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type AdsOverview struct {
	LastAutoSync       *SellerCabinetAutoSyncSummary `json:"last_auto_sync,omitempty"`
	PerformanceCompare *AdsPeriodCompare             `json:"performance_compare,omitempty"`
	Evidence           *SourceEvidence               `json:"evidence,omitempty"`
	Cabinets           []CabinetSummary              `json:"cabinets"`
	Attention          []AttentionItem               `json:"attention"`
	TopProducts        []ProductAdsSummary           `json:"top_products"`
	TopCampaigns       []CampaignPerformanceSummary  `json:"top_campaigns"`
	TopQueries         []QueryPerformanceSummary     `json:"top_queries"`
	Totals             AdsOverviewTotals             `json:"totals"`
}

type AdsOverviewTotals struct {
	Cabinets        int `json:"cabinets"`
	Products        int `json:"products"`
	Campaigns       int `json:"campaigns"`
	Queries         int `json:"queries"`
	ActiveCampaigns int `json:"active_campaigns"`
	AttentionItems  int `json:"attention_items"`
}
