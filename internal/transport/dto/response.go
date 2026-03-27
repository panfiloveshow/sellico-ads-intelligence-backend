package dto

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
)

// --- Helper functions ---

// WriteJSON writes a JSON response with the given status code and envelope data.
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	resp := envelope.OK(data, nil)
	writeResponse(w, status, resp)
}

// WriteJSONWithMeta writes a JSON response with pagination metadata.
func WriteJSONWithMeta(w http.ResponseWriter, status int, data interface{}, meta *envelope.Meta) {
	resp := envelope.OK(data, meta)
	writeResponse(w, status, resp)
}

// WriteError writes an error response with the given status code, error code and message.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	resp := envelope.Err(envelope.Error{
		Code:    code,
		Message: message,
	})
	writeResponse(w, status, resp)
}

// WriteValidationError writes a 400 response with field-level validation errors.
func WriteValidationError(w http.ResponseWriter, fieldErrors map[string]string) {
	resp := envelope.ValidationErr(fieldErrors)
	writeResponse(w, http.StatusBadRequest, resp)
}

func writeResponse(w http.ResponseWriter, status int, resp envelope.Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// --- Auth responses ---

// AuthTokensResponse is the response for login/register/refresh.
type AuthTokensResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// UserResponse is the public representation of a user (no password_hash).
type UserResponse struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserFromDomain maps domain.User to UserResponse.
func UserFromDomain(u domain.User) UserResponse {
	return UserResponse{
		ID:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// --- Workspace responses ---

// WorkspaceResponse is the public representation of a workspace.
type WorkspaceResponse struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// WorkspaceFromDomain maps domain.Workspace to WorkspaceResponse.
func WorkspaceFromDomain(w domain.Workspace) WorkspaceResponse {
	return WorkspaceResponse{
		ID:        w.ID,
		Name:      w.Name,
		Slug:      w.Slug,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
		DeletedAt: w.DeletedAt,
	}
}

// WorkspaceMemberResponse is the public representation of a workspace member.
type WorkspaceMemberResponse struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	UserID      uuid.UUID `json:"user_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WorkspaceMemberFromDomain maps domain.WorkspaceMember to WorkspaceMemberResponse.
func WorkspaceMemberFromDomain(m domain.WorkspaceMember) WorkspaceMemberResponse {
	return WorkspaceMemberResponse{
		ID:          m.ID,
		WorkspaceID: m.WorkspaceID,
		UserID:      m.UserID,
		Role:        m.Role,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// --- Seller Cabinet responses ---

// SellerCabinetResponse is the public representation (no encrypted_token).
type SellerCabinetResponse struct {
	ID           uuid.UUID  `json:"id"`
	WorkspaceID  uuid.UUID  `json:"workspace_id"`
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// SellerCabinetFromDomain maps domain.SellerCabinet to SellerCabinetResponse.
func SellerCabinetFromDomain(sc domain.SellerCabinet) SellerCabinetResponse {
	return SellerCabinetResponse{
		ID:           sc.ID,
		WorkspaceID:  sc.WorkspaceID,
		Name:         sc.Name,
		Status:       sc.Status,
		LastSyncedAt: sc.LastSyncedAt,
		CreatedAt:    sc.CreatedAt,
		UpdatedAt:    sc.UpdatedAt,
	}
}

// --- Campaign responses ---

// CampaignResponse is the public representation of a campaign.
type CampaignResponse struct {
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

// CampaignFromDomain maps domain.Campaign to CampaignResponse.
func CampaignFromDomain(c domain.Campaign) CampaignResponse {
	return CampaignResponse{
		ID:              c.ID,
		WorkspaceID:     c.WorkspaceID,
		SellerCabinetID: c.SellerCabinetID,
		WBCampaignID:    c.WBCampaignID,
		Name:            c.Name,
		Status:          c.Status,
		CampaignType:    c.CampaignType,
		BidType:         c.BidType,
		PaymentType:     c.PaymentType,
		DailyBudget:     c.DailyBudget,
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
	}
}

// CampaignStatResponse is the public representation of campaign statistics.
type CampaignStatResponse struct {
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

// CampaignStatFromDomain maps domain.CampaignStat to CampaignStatResponse.
func CampaignStatFromDomain(s domain.CampaignStat) CampaignStatResponse {
	return CampaignStatResponse{
		ID:          s.ID,
		CampaignID:  s.CampaignID,
		Date:        s.Date,
		Impressions: s.Impressions,
		Clicks:      s.Clicks,
		Spend:       s.Spend,
		Orders:      s.Orders,
		Revenue:     s.Revenue,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
}

// --- Phrase responses ---

// PhraseResponse is the public representation of a phrase.
type PhraseResponse struct {
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

// PhraseFromDomain maps domain.Phrase to PhraseResponse.
func PhraseFromDomain(p domain.Phrase) PhraseResponse {
	return PhraseResponse{
		ID:          p.ID,
		CampaignID:  p.CampaignID,
		WorkspaceID: p.WorkspaceID,
		WBClusterID: p.WBClusterID,
		Keyword:     p.Keyword,
		Count:       p.Count,
		CurrentBid:  p.CurrentBid,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// PhraseStatResponse is the public representation of phrase statistics.
type PhraseStatResponse struct {
	ID          uuid.UUID `json:"id"`
	PhraseID    uuid.UUID `json:"phrase_id"`
	Date        time.Time `json:"date"`
	Impressions int64     `json:"impressions"`
	Clicks      int64     `json:"clicks"`
	Spend       int64     `json:"spend"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PhraseStatFromDomain maps domain.PhraseStat to PhraseStatResponse.
func PhraseStatFromDomain(s domain.PhraseStat) PhraseStatResponse {
	return PhraseStatResponse{
		ID:          s.ID,
		PhraseID:    s.PhraseID,
		Date:        s.Date,
		Impressions: s.Impressions,
		Clicks:      s.Clicks,
		Spend:       s.Spend,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
}

// --- Product responses ---

// ProductResponse is the public representation of a product.
type ProductResponse struct {
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

// ProductFromDomain maps domain.Product to ProductResponse.
func ProductFromDomain(p domain.Product) ProductResponse {
	return ProductResponse{
		ID:              p.ID,
		WorkspaceID:     p.WorkspaceID,
		SellerCabinetID: p.SellerCabinetID,
		WBProductID:     p.WBProductID,
		Title:           p.Title,
		Brand:           p.Brand,
		Category:        p.Category,
		ImageURL:        p.ImageURL,
		Price:           p.Price,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
}

// --- Position responses ---

// PositionResponse is the public representation of a position.
type PositionResponse struct {
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

// PositionFromDomain maps domain.Position to PositionResponse.
func PositionFromDomain(p domain.Position) PositionResponse {
	return PositionResponse{
		ID:          p.ID,
		WorkspaceID: p.WorkspaceID,
		ProductID:   p.ProductID,
		Query:       p.Query,
		Region:      p.Region,
		Position:    p.Position,
		Page:        p.Page,
		Source:      p.Source,
		CheckedAt:   p.CheckedAt,
		CreatedAt:   p.CreatedAt,
	}
}

// PositionAggregateResponse is the public representation of an aggregated position metric.
type PositionAggregateResponse struct {
	ProductID   uuid.UUID `json:"product_id"`
	Query       string    `json:"query"`
	Region      string    `json:"region"`
	Average     float64   `json:"average"`
	DateFrom    time.Time `json:"date_from"`
	DateTo      time.Time `json:"date_to"`
	SampleCount int       `json:"sample_count"`
}

// PositionAggregateFromDomain maps domain.PositionAggregate to PositionAggregateResponse.
func PositionAggregateFromDomain(p domain.PositionAggregate) PositionAggregateResponse {
	return PositionAggregateResponse{
		ProductID:   p.ProductID,
		Query:       p.Query,
		Region:      p.Region,
		Average:     p.Average,
		DateFrom:    p.DateFrom,
		DateTo:      p.DateTo,
		SampleCount: p.SampleCount,
	}
}

// --- SERP responses ---

// SERPSnapshotResponse is the public representation of a SERP snapshot.
type SERPSnapshotResponse struct {
	ID           uuid.UUID `json:"id"`
	WorkspaceID  uuid.UUID `json:"workspace_id"`
	Query        string    `json:"query"`
	Region       string    `json:"region"`
	TotalResults int       `json:"total_results"`
	ScannedAt    time.Time `json:"scanned_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// SERPSnapshotFromDomain maps domain.SERPSnapshot to SERPSnapshotResponse.
func SERPSnapshotFromDomain(s domain.SERPSnapshot) SERPSnapshotResponse {
	return SERPSnapshotResponse{
		ID:           s.ID,
		WorkspaceID:  s.WorkspaceID,
		Query:        s.Query,
		Region:       s.Region,
		TotalResults: s.TotalResults,
		ScannedAt:    s.ScannedAt,
		CreatedAt:    s.CreatedAt,
	}
}

// SERPResultItemResponse is the public representation of a SERP result item.
type SERPResultItemResponse struct {
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

// SERPResultItemFromDomain maps domain.SERPResultItem to SERPResultItemResponse.
func SERPResultItemFromDomain(i domain.SERPResultItem) SERPResultItemResponse {
	return SERPResultItemResponse{
		ID:           i.ID,
		SnapshotID:   i.SnapshotID,
		Position:     i.Position,
		WBProductID:  i.WBProductID,
		Title:        i.Title,
		Price:        i.Price,
		Rating:       i.Rating,
		ReviewsCount: i.ReviewsCount,
		CreatedAt:    i.CreatedAt,
	}
}

// SERPSnapshotDetailResponse is the public representation of a snapshot with its items.
type SERPSnapshotDetailResponse struct {
	ID           uuid.UUID                `json:"id"`
	WorkspaceID  uuid.UUID                `json:"workspace_id"`
	Query        string                   `json:"query"`
	Region       string                   `json:"region"`
	TotalResults int                      `json:"total_results"`
	ScannedAt    time.Time                `json:"scanned_at"`
	CreatedAt    time.Time                `json:"created_at"`
	Items        []SERPResultItemResponse `json:"items"`
}

// SERPSnapshotDetailFromDomain maps snapshot and items into a detail response.
func SERPSnapshotDetailFromDomain(snapshot domain.SERPSnapshot, items []domain.SERPResultItem) SERPSnapshotDetailResponse {
	respItems := make([]SERPResultItemResponse, len(items))
	for i, item := range items {
		respItems[i] = SERPResultItemFromDomain(item)
	}

	return SERPSnapshotDetailResponse{
		ID:           snapshot.ID,
		WorkspaceID:  snapshot.WorkspaceID,
		Query:        snapshot.Query,
		Region:       snapshot.Region,
		TotalResults: snapshot.TotalResults,
		ScannedAt:    snapshot.ScannedAt,
		CreatedAt:    snapshot.CreatedAt,
		Items:        respItems,
	}
}

// --- Bid responses ---

// BidSnapshotResponse is the public representation of a bid snapshot.
type BidSnapshotResponse struct {
	ID             uuid.UUID `json:"id"`
	PhraseID       uuid.UUID `json:"phrase_id"`
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	CompetitiveBid int64     `json:"competitive_bid"`
	LeadershipBid  int64     `json:"leadership_bid"`
	CPMMin         int64     `json:"cpm_min"`
	CapturedAt     time.Time `json:"captured_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// BidSnapshotFromDomain maps domain.BidSnapshot to BidSnapshotResponse.
func BidSnapshotFromDomain(b domain.BidSnapshot) BidSnapshotResponse {
	return BidSnapshotResponse{
		ID:             b.ID,
		PhraseID:       b.PhraseID,
		WorkspaceID:    b.WorkspaceID,
		CompetitiveBid: b.CompetitiveBid,
		LeadershipBid:  b.LeadershipBid,
		CPMMin:         b.CPMMin,
		CapturedAt:     b.CapturedAt,
		CreatedAt:      b.CreatedAt,
	}
}

// --- Recommendation responses ---

// RecommendationResponse is the public representation of a recommendation.
type RecommendationResponse struct {
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

// RecommendationFromDomain maps domain.Recommendation to RecommendationResponse.
func RecommendationFromDomain(r domain.Recommendation) RecommendationResponse {
	return RecommendationResponse{
		ID:            r.ID,
		WorkspaceID:   r.WorkspaceID,
		CampaignID:    r.CampaignID,
		PhraseID:      r.PhraseID,
		ProductID:     r.ProductID,
		Title:         r.Title,
		Description:   r.Description,
		Type:          r.Type,
		Severity:      r.Severity,
		Confidence:    r.Confidence,
		SourceMetrics: r.SourceMetrics,
		NextAction:    r.NextAction,
		Status:        r.Status,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

// --- Export responses ---

// ExportResponse is the public representation of an export.
type ExportResponse struct {
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

// ExportFromDomain maps domain.Export to ExportResponse.
func ExportFromDomain(e domain.Export) ExportResponse {
	return ExportResponse{
		ID:           e.ID,
		WorkspaceID:  e.WorkspaceID,
		UserID:       e.UserID,
		EntityType:   e.EntityType,
		Format:       e.Format,
		Status:       e.Status,
		FilePath:     e.FilePath,
		ErrorMessage: e.ErrorMessage,
		Filters:      e.Filters,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
	}
}

// --- Extension responses ---

// ExtensionSessionResponse is the public representation of an extension session.
type ExtensionSessionResponse struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"user_id"`
	WorkspaceID      uuid.UUID `json:"workspace_id"`
	ExtensionVersion string    `json:"extension_version"`
	LastActiveAt     time.Time `json:"last_active_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// ExtensionSessionFromDomain maps domain.ExtensionSession to ExtensionSessionResponse.
func ExtensionSessionFromDomain(s domain.ExtensionSession) ExtensionSessionResponse {
	return ExtensionSessionResponse{
		ID:               s.ID,
		UserID:           s.UserID,
		WorkspaceID:      s.WorkspaceID,
		ExtensionVersion: s.ExtensionVersion,
		LastActiveAt:     s.LastActiveAt,
		CreatedAt:        s.CreatedAt,
	}
}

// ExtensionVersionResponse is the public representation of the current extension/backend version.
type ExtensionVersionResponse struct {
	Version string `json:"version"`
}

// --- Audit Log responses ---

// AuditLogResponse is the public representation of an audit log entry.
type AuditLogResponse struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	UserID      *uuid.UUID      `json:"user_id,omitempty"`
	Action      string          `json:"action"`
	EntityType  string          `json:"entity_type"`
	EntityID    *uuid.UUID      `json:"entity_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AuditLogFromDomain maps domain.AuditLog to AuditLogResponse.
func AuditLogFromDomain(a domain.AuditLog) AuditLogResponse {
	return AuditLogResponse{
		ID:          a.ID,
		WorkspaceID: a.WorkspaceID,
		UserID:      a.UserID,
		Action:      a.Action,
		EntityType:  a.EntityType,
		EntityID:    a.EntityID,
		Metadata:    a.Metadata,
		CreatedAt:   a.CreatedAt,
	}
}

// --- Job Run responses ---

// JobRunResponse is the public representation of a job run.
type JobRunResponse struct {
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

// JobRunFromDomain maps domain.JobRun to JobRunResponse.
func JobRunFromDomain(j domain.JobRun) JobRunResponse {
	return JobRunResponse{
		ID:           j.ID,
		WorkspaceID:  j.WorkspaceID,
		TaskType:     j.TaskType,
		Status:       j.Status,
		StartedAt:    j.StartedAt,
		FinishedAt:   j.FinishedAt,
		ErrorMessage: j.ErrorMessage,
		Metadata:     j.Metadata,
		CreatedAt:    j.CreatedAt,
	}
}
