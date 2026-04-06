package dto

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/validate"
)

// --- Auth ---

// RegisterRequest is the DTO for POST /auth/register.
type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// Validate returns field errors (empty map if valid).
func (r RegisterRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("email", r.Email)
	v.Email("email", r.Email)
	v.Required("password", r.Password)
	v.MinLength("password", r.Password, 8)
	v.MaxLength("password", r.Password, 128)
	v.Required("name", r.Name)
	v.MaxLength("name", r.Name, 255)
	return v.FieldErrors()
}

// LoginRequest is the DTO for POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Validate returns field errors (empty map if valid).
func (r LoginRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("email", r.Email)
	v.Email("email", r.Email)
	v.Required("password", r.Password)
	return v.FieldErrors()
}

// RefreshTokenRequest is the DTO for POST /auth/refresh.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Validate returns field errors (empty map if valid).
func (r RefreshTokenRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("refresh_token", r.RefreshToken)
	return v.FieldErrors()
}

// --- Workspace ---

// CreateWorkspaceRequest is the DTO for POST /workspaces.
type CreateWorkspaceRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Validate returns field errors (empty map if valid).
func (r CreateWorkspaceRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("name", r.Name)
	v.MaxLength("name", r.Name, 255)
	v.Required("slug", r.Slug)
	v.MaxLength("slug", r.Slug, 100)
	return v.FieldErrors()
}

// InviteMemberRequest is the DTO for POST /workspaces/{id}/members.
type InviteMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Validate returns field errors (empty map if valid).
func (r InviteMemberRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("email", r.Email)
	v.Email("email", r.Email)
	v.Required("role", r.Role)
	v.OneOf("role", r.Role, []string{"owner", "manager", "analyst", "viewer"})
	return v.FieldErrors()
}

// UpdateMemberRoleRequest is the DTO for PATCH /workspaces/{id}/members/{memberId}.
type UpdateMemberRoleRequest struct {
	Role string `json:"role"`
}

// Validate returns field errors (empty map if valid).
func (r UpdateMemberRoleRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("role", r.Role)
	v.OneOf("role", r.Role, []string{"owner", "manager", "analyst", "viewer"})
	return v.FieldErrors()
}

// --- Seller Cabinet ---

// CreateSellerCabinetRequest is the DTO for POST /seller-cabinets.
type CreateSellerCabinetRequest struct {
	Name     string `json:"name"`
	APIToken string `json:"api_token"`
}

// Validate returns field errors (empty map if valid).
func (r CreateSellerCabinetRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("name", r.Name)
	v.MaxLength("name", r.Name, 255)
	v.Required("api_token", r.APIToken)
	v.MinLength("api_token", r.APIToken, 10)
	v.MaxLength("api_token", r.APIToken, 2048)
	return v.FieldErrors()
}

// --- Position ---

type CreatePositionTrackingTargetRequest struct {
	ProductID uuid.UUID `json:"product_id"`
	Query     string    `json:"query"`
	Region    string    `json:"region"`
}

func (r CreatePositionTrackingTargetRequest) Validate() map[string]string {
	v := validate.New()
	if r.ProductID == uuid.Nil {
		v.FieldErrors()["product_id"] = "is required"
	}
	v.Required("query", r.Query)
	v.Required("region", r.Region)
	return v.FieldErrors()
}

// CreatePositionRequest is the DTO for POST /positions.
type CreatePositionRequest struct {
	ProductID uuid.UUID  `json:"product_id"`
	Query     string     `json:"query"`
	Region    string     `json:"region"`
	Position  int        `json:"position"`
	Page      int        `json:"page"`
	Source    string     `json:"source"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

// Validate returns field errors (empty map if valid).
func (r CreatePositionRequest) Validate() map[string]string {
	v := validate.New()
	if r.ProductID == uuid.Nil {
		v.FieldErrors()["product_id"] = "is required"
	}
	v.Required("query", r.Query)
	v.Required("region", r.Region)
	v.Required("source", r.Source)
	v.Positive("position", r.Position)
	v.Positive("page", r.Page)
	return v.FieldErrors()
}

// --- SERP ---

// CreateSERPResultItemRequest describes one item within a SERP snapshot payload.
type CreateSERPResultItemRequest struct {
	Position     int      `json:"position"`
	WBProductID  int64    `json:"wb_product_id"`
	Title        string   `json:"title"`
	Price        *int64   `json:"price,omitempty"`
	Rating       *float64 `json:"rating,omitempty"`
	ReviewsCount *int     `json:"reviews_count,omitempty"`
}

// CreateSERPSnapshotRequest is the DTO for POST /serp-snapshots.
type CreateSERPSnapshotRequest struct {
	Query        string                        `json:"query"`
	Region       string                        `json:"region"`
	TotalResults int                           `json:"total_results"`
	ScannedAt    *time.Time                    `json:"scanned_at,omitempty"`
	Items        []CreateSERPResultItemRequest `json:"items"`
}

// Validate returns field errors (empty map if valid).
func (r CreateSERPSnapshotRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("query", r.Query)
	v.Required("region", r.Region)
	v.Positive("total_results", r.TotalResults)
	if len(r.Items) == 0 {
		v.FieldErrors()["items"] = "must contain at least one item"
	}
	for i, item := range r.Items {
		prefix := "items." + validateIndex(i)
		if item.Position <= 0 {
			v.FieldErrors()[prefix+".position"] = "must be positive"
		}
		if item.WBProductID <= 0 {
			v.FieldErrors()[prefix+".wb_product_id"] = "must be positive"
		}
		if item.Title == "" {
			v.FieldErrors()[prefix+".title"] = "is required"
		}
		if item.ReviewsCount != nil && *item.ReviewsCount < 0 {
			v.FieldErrors()[prefix+".reviews_count"] = "must be zero or positive"
		}
	}
	return v.FieldErrors()
}

// --- Bid ---

// CreateBidSnapshotRequest is the DTO for POST /bids/history.
type CreateBidSnapshotRequest struct {
	PhraseID       uuid.UUID  `json:"phrase_id"`
	CompetitiveBid int64      `json:"competitive_bid"`
	LeadershipBid  int64      `json:"leadership_bid"`
	CPMMin         int64      `json:"cpm_min"`
	CapturedAt     *time.Time `json:"captured_at,omitempty"`
}

// Validate returns field errors (empty map if valid).
func (r CreateBidSnapshotRequest) Validate() map[string]string {
	v := validate.New()
	if r.PhraseID == uuid.Nil {
		v.FieldErrors()["phrase_id"] = "is required"
	}
	if r.CompetitiveBid <= 0 {
		v.FieldErrors()["competitive_bid"] = "must be positive"
	}
	if r.LeadershipBid <= 0 {
		v.FieldErrors()["leadership_bid"] = "must be positive"
	}
	if r.CPMMin <= 0 {
		v.FieldErrors()["cpm_min"] = "must be positive"
	}
	return v.FieldErrors()
}

func validateIndex(i int) string {
	return strconv.Itoa(i)
}

// --- Recommendation ---

// UpdateRecommendationStatusRequest is the DTO for PATCH /recommendations/{id}.
type UpdateRecommendationStatusRequest struct {
	Status string `json:"status"`
}

// Validate returns field errors (empty map if valid).
func (r UpdateRecommendationStatusRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("status", r.Status)
	v.OneOf("status", r.Status, []string{"active", "completed", "dismissed"})
	return v.FieldErrors()
}

// --- Export ---

// CreateExportRequest is the DTO for POST /exports.
type CreateExportRequest struct {
	EntityType string          `json:"entity_type"`
	Format     string          `json:"format"`
	Filters    json.RawMessage `json:"filters,omitempty"`
}

// Validate returns field errors (empty map if valid).
func (r CreateExportRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("entity_type", r.EntityType)
	v.OneOf("entity_type", r.EntityType, []string{
		"campaigns", "campaign_stats", "phrases", "phrase_stats",
		"products", "positions", "recommendations",
		"keywords", "competitors", "bid_changes",
	})
	v.Required("format", r.Format)
	v.OneOf("format", r.Format, []string{"csv", "xlsx"})
	return v.FieldErrors()
}

// --- Extension ---

// CreateExtensionSessionRequest is the DTO for POST /extension/sessions.
type CreateExtensionSessionRequest struct {
	ExtensionVersion string `json:"extension_version"`
}

// Validate returns field errors (empty map if valid).
func (r CreateExtensionSessionRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("extension_version", r.ExtensionVersion)
	return v.FieldErrors()
}

// ExtensionContextRequest is the DTO for POST /extension/context.
type ExtensionContextRequest struct {
	URL      string `json:"url"`
	PageType string `json:"page_type"`
}

// Validate returns field errors (empty map if valid).
func (r ExtensionContextRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("url", r.URL)
	v.Required("page_type", r.PageType)
	v.OneOf("page_type", r.PageType, []string{"search", "product", "campaign", "query", "auction", "cabinet"})
	return v.FieldErrors()
}

type CreateExtensionPageContextRequest struct {
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
	CapturedAt      *time.Time      `json:"captured_at,omitempty"`
}

func (r CreateExtensionPageContextRequest) Validate() map[string]string {
	v := validate.New()
	v.Required("url", r.URL)
	v.Required("page_type", r.PageType)
	v.OneOf("page_type", r.PageType, []string{"search", "product", "campaign", "query", "auction", "cabinet"})
	return v.FieldErrors()
}

type CreateExtensionBidSnapshotItemRequest struct {
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
	Confidence      *float64        `json:"confidence,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CapturedAt      *time.Time      `json:"captured_at,omitempty"`
}

type CreateExtensionBidSnapshotsRequest struct {
	Items []CreateExtensionBidSnapshotItemRequest `json:"items"`
}

func (r CreateExtensionBidSnapshotsRequest) Validate() map[string]string {
	v := validate.New()
	if len(r.Items) == 0 {
		v.FieldErrors()["items"] = "must contain at least one item"
	}
	for i, item := range r.Items {
		prefix := "items." + validateIndex(i)
		if item.PhraseID == nil && item.Query == nil && item.CampaignID == nil {
			v.FieldErrors()[prefix+".context"] = "requires phrase_id, query or campaign_id"
		}
	}
	return v.FieldErrors()
}

type CreateExtensionPositionSnapshotItemRequest struct {
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	Query           string          `json:"query"`
	Region          string          `json:"region"`
	VisiblePosition int             `json:"visible_position"`
	VisiblePage     *int            `json:"visible_page,omitempty"`
	PageSubtype     *string         `json:"page_subtype,omitempty"`
	Confidence      *float64        `json:"confidence,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CapturedAt      *time.Time      `json:"captured_at,omitempty"`
}

type CreateExtensionPositionSnapshotsRequest struct {
	Items []CreateExtensionPositionSnapshotItemRequest `json:"items"`
}

func (r CreateExtensionPositionSnapshotsRequest) Validate() map[string]string {
	v := validate.New()
	if len(r.Items) == 0 {
		v.FieldErrors()["items"] = "must contain at least one item"
	}
	for i, item := range r.Items {
		prefix := "items." + validateIndex(i)
		if item.ProductID == nil && !metadataHasPositiveInt64(item.Metadata, "wb_product_id", "wbProductID") {
			v.FieldErrors()[prefix+".product_id"] = "is required"
		}
		if item.Query == "" {
			v.FieldErrors()[prefix+".query"] = "is required"
		}
		if item.Region == "" {
			v.FieldErrors()[prefix+".region"] = "is required"
		}
		if item.VisiblePosition <= 0 {
			v.FieldErrors()[prefix+".visible_position"] = "must be positive"
		}
	}
	return v.FieldErrors()
}

func metadataHasPositiveInt64(raw json.RawMessage, keys ...string) bool {
	if len(raw) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			if typed > 0 {
				return true
			}
		case string:
			if typed == "" {
				continue
			}
			parsed, err := strconv.ParseInt(typed, 10, 64)
			if err == nil && parsed > 0 {
				return true
			}
		}
	}
	return false
}

type CreateExtensionUISignalItemRequest struct {
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
	Confidence      *float64        `json:"confidence,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CapturedAt      *time.Time      `json:"captured_at,omitempty"`
}

type CreateExtensionUISignalsRequest struct {
	Items []CreateExtensionUISignalItemRequest `json:"items"`
}

func (r CreateExtensionUISignalsRequest) Validate() map[string]string {
	v := validate.New()
	if len(r.Items) == 0 {
		v.FieldErrors()["items"] = "must contain at least one item"
	}
	for i, item := range r.Items {
		prefix := "items." + validateIndex(i)
		if item.SignalType == "" {
			v.FieldErrors()[prefix+".signal_type"] = "is required"
		}
		if item.Title == "" {
			v.FieldErrors()[prefix+".title"] = "is required"
		}
	}
	return v.FieldErrors()
}

type CreateExtensionNetworkCaptureItemRequest struct {
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	PageType        string          `json:"page_type"`
	EndpointKey     string          `json:"endpoint_key"`
	Query           *string         `json:"query,omitempty"`
	Region          *string         `json:"region,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	CapturedAt      *time.Time      `json:"captured_at,omitempty"`
}

type CreateExtensionNetworkCapturesRequest struct {
	Items []CreateExtensionNetworkCaptureItemRequest `json:"items"`
}

func (r CreateExtensionNetworkCapturesRequest) Validate() map[string]string {
	v := validate.New()
	if len(r.Items) == 0 {
		v.FieldErrors()["items"] = "must contain at least one item"
	}
	for i, item := range r.Items {
		prefix := "items." + validateIndex(i)
		if item.PageType == "" {
			v.FieldErrors()[prefix+".page_type"] = "is required"
		}
		if item.EndpointKey == "" {
			v.FieldErrors()[prefix+".endpoint_key"] = "is required"
		}
		if len(item.Payload) == 0 {
			v.FieldErrors()[prefix+".payload"] = "is required"
		}
	}
	return v.FieldErrors()
}
