package dto

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
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
	ID                    string                         `json:"id"`
	CabinetID             uuid.UUID                      `json:"cabinet_id"`
	WorkspaceID           uuid.UUID                      `json:"workspace_id"`
	Name                  string                         `json:"name"`
	Marketplace           string                         `json:"marketplace"`
	Status                string                         `json:"status"`
	Source                string                         `json:"source"`
	ExternalIntegrationID *string                        `json:"integration_id,omitempty"`
	LastSync              *time.Time                     `json:"last_sync,omitempty"`
	LastSellicoSyncAt     *time.Time                     `json:"last_sellico_sync_at,omitempty"`
	LastAutoSync          *SellerCabinetAutoSyncResponse `json:"last_auto_sync,omitempty"`
	CreatedAt             time.Time                      `json:"created_at"`
	UpdatedAt             time.Time                      `json:"updated_at"`
}

type SellerCabinetAutoSyncResponse struct {
	JobRunID          uuid.UUID                   `json:"job_run_id"`
	Status            string                      `json:"status"`
	ResultState       string                      `json:"result_state"`
	FreshnessState    string                      `json:"freshness_state"`
	SyncPhase         string                      `json:"sync_phase,omitempty"`
	FinishedAt        *time.Time                  `json:"finished_at,omitempty"`
	RateLimited       bool                        `json:"rate_limited"`
	RateLimitEndpoint string                      `json:"rate_limit_endpoint,omitempty"`
	RetryAfterSeconds int                         `json:"retry_after_seconds,omitempty"`
	NextAllowedAt     *time.Time                  `json:"next_allowed_at,omitempty"`
	PhaseRetries      []AdsSyncPhaseRetryResponse `json:"phase_retries_queued,omitempty"`
	Cabinets          int                         `json:"cabinets"`
	Campaigns         int                         `json:"campaigns"`
	CampaignStats     int                         `json:"campaign_stats"`
	ProductStats      int                         `json:"product_stats"`
	CampaignBudgets   int                         `json:"campaign_budgets"`
	BusinessOrders    int                         `json:"business_orders"`
	BusinessSales     int                         `json:"business_sales"`
	Phrases           int                         `json:"phrases"`
	PhraseStats       int                         `json:"phrase_stats"`
	Products          int                         `json:"products"`
	WBErrors          int                         `json:"wb_errors"`
	SyncIssues        int                         `json:"sync_issues"`
}

type AdsSyncPhaseRetryResponse struct {
	Phase  string     `json:"phase"`
	Status string     `json:"status"`
	RunAt  *time.Time `json:"run_at,omitempty"`
}

type SellerCabinetCommunicationReputationResponse struct {
	SellerCabinetID uuid.UUID                                  `json:"seller_cabinet_id"`
	WBProductID     int64                                      `json:"wb_product_id"`
	Source          string                                     `json:"source"`
	GeneratedAt     time.Time                                  `json:"generated_at"`
	IsAnswered      bool                                       `json:"is_answered"`
	NewItems        SellerCabinetCommunicationNewItemsResponse `json:"new_items"`
	Counts          SellerCabinetCommunicationCountsResponse   `json:"counts"`
	Questions       []SellerCabinetQuestionEvidenceResponse    `json:"questions"`
	Feedbacks       []SellerCabinetFeedbackEvidenceResponse    `json:"feedbacks"`
}

type SellerCabinetCommunicationNewItemsResponse struct {
	HasNewQuestions bool `json:"has_new_questions"`
	HasNewFeedbacks bool `json:"has_new_feedbacks"`
}

type SellerCabinetCommunicationCountsResponse struct {
	UnansweredQuestions      int `json:"unanswered_questions"`
	UnansweredQuestionsToday int `json:"unanswered_questions_today"`
	UnansweredFeedbacks      int `json:"unanswered_feedbacks"`
	UnansweredFeedbacksToday int `json:"unanswered_feedbacks_today"`
}

type SellerCabinetCommunicationProductDetailsResponse struct {
	IMTID           int64  `json:"imt_id,omitempty"`
	NMID            int64  `json:"nm_id"`
	ProductName     string `json:"product_name,omitempty"`
	SupplierArticle string `json:"supplier_article,omitempty"`
	SupplierName    string `json:"supplier_name,omitempty"`
	BrandName       string `json:"brand_name,omitempty"`
	Size            string `json:"size,omitempty"`
}

type SellerCabinetCommunicationAnswerResponse struct {
	Text       string `json:"text,omitempty"`
	State      string `json:"state,omitempty"`
	Editable   bool   `json:"editable"`
	CreateDate string `json:"create_date,omitempty"`
}

type SellerCabinetQuestionEvidenceResponse struct {
	ID             string                                           `json:"id"`
	Text           string                                           `json:"text,omitempty"`
	CreatedDate    string                                           `json:"created_date,omitempty"`
	State          string                                           `json:"state,omitempty"`
	Answer         *SellerCabinetCommunicationAnswerResponse        `json:"answer,omitempty"`
	ProductDetails SellerCabinetCommunicationProductDetailsResponse `json:"product_details"`
	WasViewed      bool                                             `json:"was_viewed"`
	IsWarned       bool                                             `json:"is_warned"`
}

type SellerCabinetFeedbackEvidenceResponse struct {
	ID               string                                           `json:"id"`
	Text             string                                           `json:"text,omitempty"`
	Pros             string                                           `json:"pros,omitempty"`
	Cons             string                                           `json:"cons,omitempty"`
	ProductValuation int                                              `json:"product_valuation,omitempty"`
	CreatedDate      string                                           `json:"created_date,omitempty"`
	Answer           *SellerCabinetCommunicationAnswerResponse        `json:"answer,omitempty"`
	State            string                                           `json:"state,omitempty"`
	ProductDetails   SellerCabinetCommunicationProductDetailsResponse `json:"product_details"`
	WasViewed        bool                                             `json:"was_viewed"`
	OrderStatus      string                                           `json:"order_status,omitempty"`
	SubjectID        int64                                            `json:"subject_id,omitempty"`
	SubjectName      string                                           `json:"subject_name,omitempty"`
}

// SellerCabinetFromDomain maps domain.SellerCabinet to SellerCabinetResponse.
func SellerCabinetFromDomain(sc domain.SellerCabinet) SellerCabinetResponse {
	publicID := sc.ID.String()
	if sc.ExternalIntegrationID != nil && *sc.ExternalIntegrationID != "" {
		publicID = *sc.ExternalIntegrationID
	}

	marketplace := ""
	if sc.IntegrationType != nil && *sc.IntegrationType != "" {
		marketplace = *sc.IntegrationType
	}

	return SellerCabinetResponse{
		ID:                    publicID,
		CabinetID:             sc.ID,
		WorkspaceID:           sc.WorkspaceID,
		Name:                  sc.Name,
		Marketplace:           marketplace,
		Status:                sc.Status,
		Source:                sc.Source,
		ExternalIntegrationID: sc.ExternalIntegrationID,
		LastSync:              sc.LastSyncedAt,
		LastSellicoSyncAt:     sc.LastSellicoSyncAt,
		LastAutoSync:          sellerCabinetAutoSyncFromDomain(sc.LastAutoSync),
		CreatedAt:             sc.CreatedAt,
		UpdatedAt:             sc.UpdatedAt,
	}
}

func SellerCabinetCommunicationReputationFromService(report service.SellerCabinetCommunicationReputation) SellerCabinetCommunicationReputationResponse {
	return SellerCabinetCommunicationReputationResponse{
		SellerCabinetID: report.SellerCabinetID,
		WBProductID:     report.WBProductID,
		Source:          report.Source,
		GeneratedAt:     report.GeneratedAt,
		IsAnswered:      report.IsAnswered,
		NewItems: SellerCabinetCommunicationNewItemsResponse{
			HasNewQuestions: report.NewItems.HasNewQuestions,
			HasNewFeedbacks: report.NewItems.HasNewFeedbacks,
		},
		Counts: SellerCabinetCommunicationCountsResponse{
			UnansweredQuestions:      report.Counts.UnansweredQuestions,
			UnansweredQuestionsToday: report.Counts.UnansweredQuestionsToday,
			UnansweredFeedbacks:      report.Counts.UnansweredFeedbacks,
			UnansweredFeedbacksToday: report.Counts.UnansweredFeedbacksToday,
		},
		Questions: sellerCabinetQuestionEvidenceFromService(report.Questions),
		Feedbacks: sellerCabinetFeedbackEvidenceFromService(report.Feedbacks),
	}
}

func sellerCabinetQuestionEvidenceFromService(items []service.SellerCabinetQuestionEvidence) []SellerCabinetQuestionEvidenceResponse {
	result := make([]SellerCabinetQuestionEvidenceResponse, 0, len(items))
	for _, item := range items {
		result = append(result, SellerCabinetQuestionEvidenceResponse{
			ID:             item.ID,
			Text:           item.Text,
			CreatedDate:    item.CreatedDate,
			State:          item.State,
			Answer:         sellerCabinetCommunicationAnswerFromService(item.Answer),
			ProductDetails: sellerCabinetCommunicationProductDetailsFromService(item.ProductDetails),
			WasViewed:      item.WasViewed,
			IsWarned:       item.IsWarned,
		})
	}
	return result
}

func sellerCabinetFeedbackEvidenceFromService(items []service.SellerCabinetFeedbackEvidence) []SellerCabinetFeedbackEvidenceResponse {
	result := make([]SellerCabinetFeedbackEvidenceResponse, 0, len(items))
	for _, item := range items {
		result = append(result, SellerCabinetFeedbackEvidenceResponse{
			ID:               item.ID,
			Text:             item.Text,
			Pros:             item.Pros,
			Cons:             item.Cons,
			ProductValuation: item.ProductValuation,
			CreatedDate:      item.CreatedDate,
			Answer:           sellerCabinetCommunicationAnswerFromService(item.Answer),
			State:            item.State,
			ProductDetails:   sellerCabinetCommunicationProductDetailsFromService(item.ProductDetails),
			WasViewed:        item.WasViewed,
			OrderStatus:      item.OrderStatus,
			SubjectID:        item.SubjectID,
			SubjectName:      item.SubjectName,
		})
	}
	return result
}

func sellerCabinetCommunicationProductDetailsFromService(item service.SellerCabinetCommunicationProductDetails) SellerCabinetCommunicationProductDetailsResponse {
	return SellerCabinetCommunicationProductDetailsResponse{
		IMTID:           item.IMTID,
		NMID:            item.NMID,
		ProductName:     item.ProductName,
		SupplierArticle: item.SupplierArticle,
		SupplierName:    item.SupplierName,
		BrandName:       item.BrandName,
		Size:            item.Size,
	}
}

func sellerCabinetCommunicationAnswerFromService(item *service.SellerCabinetCommunicationAnswer) *SellerCabinetCommunicationAnswerResponse {
	if item == nil {
		return nil
	}
	return &SellerCabinetCommunicationAnswerResponse{
		Text:       item.Text,
		State:      item.State,
		Editable:   item.Editable,
		CreateDate: item.CreateDate,
	}
}

func sellerCabinetAutoSyncFromDomain(sync *domain.SellerCabinetAutoSyncSummary) *SellerCabinetAutoSyncResponse {
	if sync == nil {
		return nil
	}
	return &SellerCabinetAutoSyncResponse{
		JobRunID:          sync.JobRunID,
		Status:            sync.Status,
		ResultState:       sync.ResultState,
		FreshnessState:    sync.FreshnessState,
		SyncPhase:         sync.SyncPhase,
		FinishedAt:        sync.FinishedAt,
		RateLimited:       sync.RateLimited,
		RateLimitEndpoint: sync.RateLimitEndpoint,
		RetryAfterSeconds: sync.RetryAfterSeconds,
		NextAllowedAt:     sync.NextAllowedAt,
		PhaseRetries:      adsSyncPhaseRetriesFromDomain(sync.PhaseRetries),
		Cabinets:          sync.Cabinets,
		Campaigns:         sync.Campaigns,
		CampaignStats:     sync.CampaignStats,
		ProductStats:      sync.ProductStats,
		CampaignBudgets:   sync.CampaignBudgets,
		BusinessOrders:    sync.BusinessOrders,
		BusinessSales:     sync.BusinessSales,
		Phrases:           sync.Phrases,
		PhraseStats:       sync.PhraseStats,
		Products:          sync.Products,
		WBErrors:          sync.WBErrors,
		SyncIssues:        sync.SyncIssues,
	}
}

func adsSyncPhaseRetriesFromDomain(retries []domain.AdsSyncPhaseRetry) []AdsSyncPhaseRetryResponse {
	if len(retries) == 0 {
		return nil
	}
	result := make([]AdsSyncPhaseRetryResponse, len(retries))
	for i, retry := range retries {
		result[i] = AdsSyncPhaseRetryResponse{
			Phase:  retry.Phase,
			Status: retry.Status,
			RunAt:  retry.RunAt,
		}
	}
	return result
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
	CanChangeNMs    *bool     `json:"can_change_nms"`
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
		CanChangeNMs:    c.CanChangeNMs,
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
	ID          uuid.UUID  `json:"id"`
	CampaignID  uuid.UUID  `json:"campaign_id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	ProductID   *uuid.UUID `json:"product_id,omitempty"`
	WBProductID *int64     `json:"wb_product_id,omitempty"`
	WBClusterID *int64     `json:"wb_cluster_id,omitempty"`
	WBNormQuery string     `json:"wb_norm_query"`
	Keyword     string     `json:"keyword"`
	Count       *int       `json:"count,omitempty"`
	CurrentBid  *int64     `json:"current_bid,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// PhraseFromDomain maps domain.Phrase to PhraseResponse.
func PhraseFromDomain(p domain.Phrase) PhraseResponse {
	return PhraseResponse{
		ID:          p.ID,
		CampaignID:  p.CampaignID,
		WorkspaceID: p.WorkspaceID,
		ProductID:   p.ProductID,
		WBProductID: p.WBProductID,
		WBClusterID: p.WBClusterID,
		WBNormQuery: p.WBNormQuery,
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
	Atbs        *int64    `json:"atbs,omitempty"`
	Orders      *int64    `json:"orders,omitempty"`
	CPC         *float64  `json:"cpc,omitempty"`
	CPM         *float64  `json:"cpm,omitempty"`
	AvgPos      *float64  `json:"avg_pos,omitempty"`
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
		Atbs:        s.Atbs,
		Orders:      s.Orders,
		CPC:         s.CPC,
		CPM:         s.CPM,
		AvgPos:      s.AvgPos,
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

type AdsMetricsSummaryResponse struct {
	Impressions    int64   `json:"impressions"`
	Clicks         int64   `json:"clicks"`
	Spend          int64   `json:"spend"`
	Orders         int64   `json:"orders"`
	Revenue        int64   `json:"revenue"`
	Atbs           int64   `json:"atbs"`
	Canceled       int64   `json:"canceled"`
	Shks           int64   `json:"shks"`
	CTR            float64 `json:"ctr"`
	CPC            float64 `json:"cpc"`
	CPM            float64 `json:"cpm"`
	CPO            float64 `json:"cpo"`
	ROAS           float64 `json:"roas"`
	DRR            float64 `json:"drr"`
	ConversionRate float64 `json:"conversion_rate"`
	CartRate       float64 `json:"cart_rate"`
	AvgPosition    float64 `json:"avg_position"`
	DataMode       string  `json:"data_mode"`
}

type ProductBusinessSummaryResponse struct {
	Orders                             int64      `json:"orders"`
	CanceledOrders                     int64      `json:"canceled_orders"`
	Sales                              int64      `json:"sales"`
	Returns                            int64      `json:"returns"`
	OrderedRevenue                     int64      `json:"ordered_revenue"`
	SoldRevenue                        int64      `json:"sold_revenue"`
	ReturnedRevenue                    int64      `json:"returned_revenue"`
	BuyoutRate                         float64    `json:"buyout_rate"`
	ReturnRate                         float64    `json:"return_rate"`
	AdSpend                            int64      `json:"ad_spend"`
	AdToSoldRevenue                    float64    `json:"ad_to_sold_revenue"`
	DataMode                           string     `json:"data_mode"`
	SalesFunnelOpenCount               int64      `json:"sales_funnel_open_count"`
	SalesFunnelCartCount               int64      `json:"sales_funnel_cart_count"`
	SalesFunnelOrderCount              int64      `json:"sales_funnel_order_count"`
	SalesFunnelOpenToCartConversion    *float64   `json:"sales_funnel_open_to_cart_conversion,omitempty"`
	SalesFunnelCartToOrderConversion   *float64   `json:"sales_funnel_cart_to_order_conversion,omitempty"`
	SalesFunnelSource                  string     `json:"sales_funnel_source,omitempty"`
	SalesFunnelDataMode                string     `json:"sales_funnel_data_mode,omitempty"`
	SalesFunnelCapturedAt              *time.Time `json:"sales_funnel_captured_at,omitempty"`
	CostPrice                          *int64     `json:"cost_price,omitempty"`
	LogisticsCost                      *int64     `json:"logistics_cost,omitempty"`
	OtherCosts                         *int64     `json:"other_costs,omitempty"`
	TaxRatePercent                     *float64   `json:"tax_rate_percent,omitempty"`
	CommissionPercent                  *float64   `json:"commission_percent,omitempty"`
	TargetMarginPercent                *float64   `json:"target_margin_percent,omitempty"`
	MaxAllowedDRR                      *float64   `json:"max_allowed_drr,omitempty"`
	MarginBeforeAds                    *int64     `json:"margin_before_ads,omitempty"`
	MarginBeforeAdsTotal               *int64     `json:"margin_before_ads_total,omitempty"`
	MarginBeforeAdsPercent             *float64   `json:"margin_before_ads_percent,omitempty"`
	ProfitAfterAds                     *int64     `json:"profit_after_ads,omitempty"`
	MarginalDRR                        *float64   `json:"marginal_drr,omitempty"`
	EconomicsSource                    string     `json:"economics_source,omitempty"`
	EconomicsDataMode                  string     `json:"economics_data_mode,omitempty"`
	WBCommissionSubjectName            string     `json:"wb_commission_subject_name,omitempty"`
	WBCommissionMarketplacePercent     *float64   `json:"wb_commission_marketplace_percent,omitempty"`
	WBCommissionSupplierPercent        *float64   `json:"wb_commission_supplier_percent,omitempty"`
	WBCommissionPickupPercent          *float64   `json:"wb_commission_pickup_percent,omitempty"`
	WBCommissionBookingPercent         *float64   `json:"wb_commission_booking_percent,omitempty"`
	WBCommissionSupplierExpressPercent *float64   `json:"wb_commission_supplier_express_percent,omitempty"`
	WBCommissionDataMode               string     `json:"wb_commission_data_mode,omitempty"`
}

type ProductStockEvidenceResponse struct {
	StockTotal int32     `json:"stock_total"`
	Source     string    `json:"source"`
	CapturedAt time.Time `json:"captured_at"`
}

type ProductStockRunoutForecastResponse struct {
	State             string    `json:"state"`
	StockTotal        int32     `json:"stock_total"`
	AverageDailySales float64   `json:"average_daily_sales"`
	DaysToEmpty       *float64  `json:"days_to_empty,omitempty"`
	PeriodDays        int       `json:"period_days"`
	Source            string    `json:"source"`
	CapturedAt        time.Time `json:"captured_at"`
	Reason            string    `json:"reason,omitempty"`
}

type DecisionScoreSummaryResponse struct {
	Value           *int     `json:"value,omitempty"`
	DataMode        string   `json:"data_mode"`
	Evidence        []string `json:"evidence,omitempty"`
	MissingEvidence []string `json:"missing_evidence,omitempty"`
}

type ProductDecisionScoresResponse struct {
	Advertising DecisionScoreSummaryResponse `json:"advertising"`
	Readiness   DecisionScoreSummaryResponse `json:"readiness"`
	Growth      DecisionScoreSummaryResponse `json:"growth"`
}

type ProductDecisionSummaryResponse struct {
	Decision        string   `json:"decision"`
	DataMode        string   `json:"data_mode"`
	Reason          string   `json:"reason"`
	MissingEvidence []string `json:"missing_evidence,omitempty"`
}

type AdsMetricsDeltaResponse struct {
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

type AdsPeriodCompareResponse struct {
	Current  AdsMetricsSummaryResponse `json:"current"`
	Previous AdsMetricsSummaryResponse `json:"previous"`
	Delta    AdsMetricsDeltaResponse   `json:"delta"`
	Trend    string                    `json:"trend"`
}

type AdsEntityRefResponse struct {
	ID     uuid.UUID `json:"id"`
	Label  string    `json:"label"`
	WBID   *int64    `json:"wb_id,omitempty"`
	Count  *int      `json:"count,omitempty"`
	Source string    `json:"source,omitempty"`
}

type AttentionItemResponse struct {
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Severity    string  `json:"severity"`
	ActionLabel string  `json:"action_label"`
	ActionPath  string  `json:"action_path"`
	SourceType  string  `json:"source_type"`
	SourceID    *string `json:"source_id,omitempty"`
}

type SourceEvidenceResponse struct {
	Source             string                        `json:"source"`
	SourceLabel        string                        `json:"source_label,omitempty"`
	SourcePriority     []string                      `json:"source_priority,omitempty"`
	CapturedAt         *time.Time                    `json:"captured_at,omitempty"`
	FreshnessState     string                        `json:"freshness_state"`
	Confidence         float64                       `json:"confidence"`
	Coverage           string                        `json:"coverage"`
	ConfirmedInCabinet bool                          `json:"confirmed_in_cabinet"`
	Issues             []SourceEvidenceIssueResponse `json:"issues,omitempty"`
}

type SourceEvidenceIssueResponse struct {
	Type           string `json:"type"`
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	APIValue       string `json:"api_value,omitempty"`
	ExtensionValue string `json:"extension_value,omitempty"`
}

func sourceEvidenceFromDomain(evidence *domain.SourceEvidence) *SourceEvidenceResponse {
	if evidence == nil {
		return nil
	}
	issues := make([]SourceEvidenceIssueResponse, len(evidence.Issues))
	for i, issue := range evidence.Issues {
		issues[i] = SourceEvidenceIssueResponse{
			Type:           issue.Type,
			Severity:       issue.Severity,
			Message:        issue.Message,
			APIValue:       issue.APIValue,
			ExtensionValue: issue.ExtensionValue,
		}
	}
	return &SourceEvidenceResponse{
		Source:             evidence.Source,
		SourceLabel:        evidence.SourceLabel,
		SourcePriority:     evidence.SourcePriority,
		CapturedAt:         evidence.CapturedAt,
		FreshnessState:     evidence.FreshnessState,
		Confidence:         evidence.Confidence,
		Coverage:           evidence.Coverage,
		ConfirmedInCabinet: evidence.ConfirmedInCabinet,
		Issues:             issues,
	}
}

type CabinetSummaryResponse struct {
	ID                   string                         `json:"id"`
	CabinetID            uuid.UUID                      `json:"cabinet_id"`
	IntegrationID        *string                        `json:"integration_id,omitempty"`
	IntegrationName      string                         `json:"integration_name"`
	CabinetName          string                         `json:"cabinet_name"`
	Status               string                         `json:"status"`
	FreshnessState       string                         `json:"freshness_state"`
	LastSync             *time.Time                     `json:"last_sync,omitempty"`
	LastAutoSync         *SellerCabinetAutoSyncResponse `json:"last_auto_sync,omitempty"`
	CampaignsCount       int                            `json:"campaigns_count"`
	ProductsCount        int                            `json:"products_count"`
	QueriesCount         int                            `json:"queries_count"`
	ActiveCampaignsCount int                            `json:"active_campaigns_count"`
}

type ProductAdsSummaryResponse struct {
	ID               uuid.UUID                           `json:"id"`
	WorkspaceID      uuid.UUID                           `json:"workspace_id"`
	SellerCabinetID  uuid.UUID                           `json:"seller_cabinet_id"`
	IntegrationID    *string                             `json:"integration_id,omitempty"`
	IntegrationName  string                              `json:"integration_name"`
	CabinetName      string                              `json:"cabinet_name"`
	CampaignID       *uuid.UUID                          `json:"campaign_id,omitempty"`
	CampaignName     *string                             `json:"campaign_name,omitempty"`
	WBCampaignID     *int64                              `json:"wb_campaign_id,omitempty"`
	RowKey           string                              `json:"row_key,omitempty"`
	WBProductID      int64                               `json:"wb_product_id"`
	Title            string                              `json:"title"`
	Brand            *string                             `json:"brand,omitempty"`
	Category         *string                             `json:"category,omitempty"`
	ImageURL         *string                             `json:"image_url,omitempty"`
	Price            *int64                              `json:"price,omitempty"`
	Rating           *float64                            `json:"rating,omitempty"`
	ReviewsCount     *int                                `json:"reviews_count,omitempty"`
	CampaignsCount   int                                 `json:"campaigns_count"`
	QueriesCount     int                                 `json:"queries_count"`
	HealthStatus     string                              `json:"health_status"`
	HealthReason     *string                             `json:"health_reason,omitempty"`
	PrimaryAction    *string                             `json:"primary_action,omitempty"`
	FreshnessState   string                              `json:"freshness_state"`
	Performance      AdsMetricsSummaryResponse           `json:"performance"`
	Business         ProductBusinessSummaryResponse      `json:"business"`
	Scores           ProductDecisionScoresResponse       `json:"scores"`
	Decision         ProductDecisionSummaryResponse      `json:"decision"`
	PeriodCompare    *AdsPeriodCompareResponse           `json:"period_compare,omitempty"`
	RelatedCampaigns []AdsEntityRefResponse              `json:"related_campaigns,omitempty"`
	TopQueries       []AdsEntityRefResponse              `json:"top_queries,omitempty"`
	WasteQueries     []AdsEntityRefResponse              `json:"waste_queries,omitempty"`
	WinningQueries   []AdsEntityRefResponse              `json:"winning_queries,omitempty"`
	StockEvidence    *ProductStockEvidenceResponse       `json:"stock_evidence,omitempty"`
	StockRunout      *ProductStockRunoutForecastResponse `json:"stock_runout,omitempty"`
	Evidence         *SourceEvidenceResponse             `json:"evidence,omitempty"`
	DataCoverageNote *string                             `json:"data_coverage_note,omitempty"`
	CreatedAt        time.Time                           `json:"created_at"`
	UpdatedAt        time.Time                           `json:"updated_at"`
}

type CampaignPerformanceSummaryResponse struct {
	ID                       uuid.UUID                                   `json:"id"`
	WorkspaceID              uuid.UUID                                   `json:"workspace_id"`
	SellerCabinetID          uuid.UUID                                   `json:"seller_cabinet_id"`
	IntegrationID            *string                                     `json:"integration_id,omitempty"`
	IntegrationName          string                                      `json:"integration_name"`
	CabinetName              string                                      `json:"cabinet_name"`
	WBCampaignID             int64                                       `json:"wb_campaign_id"`
	Name                     string                                      `json:"name"`
	Status                   string                                      `json:"status"`
	CampaignType             int                                         `json:"campaign_type"`
	BidType                  string                                      `json:"bid_type"`
	PaymentType              string                                      `json:"payment_type"`
	DailyBudget              *int64                                      `json:"daily_budget,omitempty"`
	PlacementSearch          *bool                                       `json:"placement_search,omitempty"`
	PlacementRecommendations *bool                                       `json:"placement_recommendations,omitempty"`
	WBCreatedAt              *time.Time                                  `json:"wb_created_at,omitempty"`
	WBStartedAt              *time.Time                                  `json:"wb_started_at,omitempty"`
	WBUpdatedAt              *time.Time                                  `json:"wb_updated_at,omitempty"`
	WBDeletedAt              *time.Time                                  `json:"wb_deleted_at,omitempty"`
	LatestBudget             *CampaignBudgetSummaryResponse              `json:"latest_budget,omitempty"`
	BudgetPace               *CampaignBudgetPaceSummaryResponse          `json:"budget_pace,omitempty"`
	BudgetRunout             *CampaignBudgetRunoutSummaryResponse        `json:"budget_runout,omitempty"`
	AdFinance                *CampaignFinanceSummaryResponse             `json:"ad_finance,omitempty"`
	LastSync                 *time.Time                                  `json:"last_sync,omitempty"`
	HealthStatus             string                                      `json:"health_status"`
	HealthReason             *string                                     `json:"health_reason,omitempty"`
	PrimaryAction            *string                                     `json:"primary_action,omitempty"`
	FreshnessState           string                                      `json:"freshness_state"`
	Performance              AdsMetricsSummaryResponse                   `json:"performance"`
	PeriodCompare            *AdsPeriodCompareResponse                   `json:"period_compare,omitempty"`
	RelatedProducts          []AdsEntityRefResponse                      `json:"related_products,omitempty"`
	Products                 []CampaignProductPerformanceSummaryResponse `json:"products,omitempty"`
	TopQueries               []AdsEntityRefResponse                      `json:"top_queries,omitempty"`
	WasteQueries             []AdsEntityRefResponse                      `json:"waste_queries,omitempty"`
	WinningQueries           []AdsEntityRefResponse                      `json:"winning_queries,omitempty"`
	RecentBidChanges         []CampaignBidChangeSummaryResponse          `json:"recent_bid_changes,omitempty"`
	ActiveRecommendations    []CampaignRecommendationSummaryResponse     `json:"active_recommendations,omitempty"`
	Evidence                 *SourceEvidenceResponse                     `json:"evidence,omitempty"`
	CreatedAt                time.Time                                   `json:"created_at"`
	UpdatedAt                time.Time                                   `json:"updated_at"`
}

type CampaignBudgetSummaryResponse struct {
	Cash       int64     `json:"cash"`
	Netting    int64     `json:"netting"`
	Total      int64     `json:"total"`
	CapturedAt time.Time `json:"captured_at"`
}

type CampaignBudgetPaceSummaryResponse struct {
	State                            string   `json:"state"`
	PeriodDays                       int      `json:"period_days"`
	DailyBudget                      int64    `json:"daily_budget"`
	WeeklyBudget                     int64    `json:"weekly_budget"`
	MonthlyBudget                    int64    `json:"monthly_budget"`
	PlannedSpend                     int64    `json:"planned_spend"`
	ActualSpend                      int64    `json:"actual_spend"`
	UtilizationPercent               float64  `json:"utilization_percent"`
	ProjectedTodaySpend              *int64   `json:"projected_today_spend,omitempty"`
	ProjectedTodayUtilizationPercent *float64 `json:"projected_today_utilization_percent,omitempty"`
	Reason                           string   `json:"reason,omitempty"`
}

type CampaignBudgetRunoutSummaryResponse struct {
	State           string    `json:"state"`
	RemainingBudget int64     `json:"remaining_budget"`
	SpendToday      int64     `json:"spend_today"`
	HoursElapsed    float64   `json:"hours_elapsed"`
	HoursToEmpty    float64   `json:"hours_to_empty"`
	CapturedAt      time.Time `json:"captured_at"`
	Reason          string    `json:"reason,omitempty"`
}

type CampaignFinanceSummaryResponse struct {
	DocumentsCount   int            `json:"documents_count"`
	Amount           int64          `json:"amount"`
	DocumentTypes    map[string]int `json:"document_types,omitempty"`
	LatestDocumentAt *time.Time     `json:"latest_document_at,omitempty"`
	DataMode         string         `json:"data_mode"`
}

type CampaignProductPerformanceSummaryResponse struct {
	ID                 uuid.UUID                 `json:"id"`
	ProductID          uuid.UUID                 `json:"product_id"`
	WBProductID        int64                     `json:"wb_product_id"`
	ProductName        string                    `json:"product_name"`
	SubjectName        *string                   `json:"subject_name,omitempty"`
	BidSearch          *int64                    `json:"bid_search,omitempty"`
	BidRecommendations *int64                    `json:"bid_recommendations,omitempty"`
	ProductTotalCarts  *int64                    `json:"product_total_carts,omitempty"`
	Performance        AdsMetricsSummaryResponse `json:"performance"`
}

type CampaignBidChangeSummaryResponse struct {
	ID               uuid.UUID  `json:"id"`
	ProductID        *uuid.UUID `json:"product_id,omitempty"`
	PhraseID         *uuid.UUID `json:"phrase_id,omitempty"`
	RecommendationID *uuid.UUID `json:"recommendation_id,omitempty"`
	Placement        string     `json:"placement"`
	OldBid           int        `json:"old_bid"`
	NewBid           int        `json:"new_bid"`
	Reason           string     `json:"reason"`
	Source           string     `json:"source"`
	WBStatus         string     `json:"wb_status"`
	CanRollback      bool       `json:"can_rollback"`
	RollbackBid      *int       `json:"rollback_bid,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type CampaignRecommendationSummaryResponse struct {
	ID            uuid.UUID  `json:"id"`
	PhraseID      *uuid.UUID `json:"phrase_id,omitempty"`
	ProductID     *uuid.UUID `json:"product_id,omitempty"`
	Scope         string     `json:"scope"`
	Title         string     `json:"title"`
	Type          string     `json:"type"`
	Severity      string     `json:"severity"`
	Confidence    float64    `json:"confidence"`
	NextAction    *string    `json:"next_action,omitempty"`
	Status        string     `json:"status"`
	TaskCategory  string     `json:"task_category,omitempty"`
	TaskOwnerRole string     `json:"task_owner_role,omitempty"`
	TaskSLAHours  int        `json:"task_sla_hours"`
	TaskDueAt     *time.Time `json:"task_due_at,omitempty"`
	TaskAgeHours  int        `json:"task_age_hours"`
	IsOverdue     bool       `json:"is_overdue"`
	CreatedAt     time.Time  `json:"created_at"`
}

type QueryPerformanceSummaryResponse struct {
	ID              uuid.UUID                 `json:"id"`
	WorkspaceID     uuid.UUID                 `json:"workspace_id"`
	CampaignID      uuid.UUID                 `json:"campaign_id"`
	ProductID       *uuid.UUID                `json:"product_id,omitempty"`
	SellerCabinetID uuid.UUID                 `json:"seller_cabinet_id"`
	IntegrationID   *string                   `json:"integration_id,omitempty"`
	IntegrationName string                    `json:"integration_name"`
	CabinetName     string                    `json:"cabinet_name"`
	CampaignName    string                    `json:"campaign_name"`
	WBCampaignID    int64                     `json:"wb_campaign_id"`
	ProductName     *string                   `json:"product_name,omitempty"`
	WBProductID     *int64                    `json:"wb_product_id,omitempty"`
	WBClusterID     *int64                    `json:"wb_cluster_id,omitempty"`
	WBNormQuery     string                    `json:"wb_norm_query"`
	Keyword         string                    `json:"keyword"`
	CurrentBid      *int64                    `json:"current_bid,omitempty"`
	ClusterSize     *int                      `json:"cluster_size,omitempty"`
	Source          string                    `json:"source"`
	SignalCategory  string                    `json:"signal_category"`
	HealthStatus    string                    `json:"health_status"`
	HealthReason    *string                   `json:"health_reason,omitempty"`
	PrimaryAction   *string                   `json:"primary_action,omitempty"`
	FreshnessState  string                    `json:"freshness_state"`
	Performance     AdsMetricsSummaryResponse `json:"performance"`
	PeriodCompare   *AdsPeriodCompareResponse `json:"period_compare,omitempty"`
	PriorityScore   int                       `json:"priority_score"`
	RelatedProducts []AdsEntityRefResponse    `json:"related_products,omitempty"`
	Evidence        *SourceEvidenceResponse   `json:"evidence,omitempty"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}

type AdsOverviewTotalsResponse struct {
	Cabinets               int            `json:"cabinets"`
	Products               int            `json:"products"`
	Campaigns              int            `json:"campaigns"`
	Queries                int            `json:"queries"`
	ActiveCampaigns        int            `json:"active_campaigns"`
	AttentionItems         int            `json:"attention_items"`
	ActiveRecommendations  int            `json:"active_recommendations"`
	OverdueRecommendations int            `json:"overdue_recommendations"`
	DecisionQueueBuckets   map[string]int `json:"decision_queue_buckets,omitempty"`
	TaskOwnerBuckets       map[string]int `json:"task_owner_buckets,omitempty"`
	ProductDecisions       map[string]int `json:"product_decisions,omitempty"`
}

type AdsOverviewResponse struct {
	LastAutoSync       *SellerCabinetAutoSyncResponse       `json:"last_auto_sync,omitempty"`
	PerformanceCompare *AdsPeriodCompareResponse            `json:"performance_compare,omitempty"`
	Evidence           *SourceEvidenceResponse              `json:"evidence,omitempty"`
	DataStatus         AdsDataStatusResponse                `json:"data_status"`
	Cabinets           []CabinetSummaryResponse             `json:"cabinets"`
	Attention          []AttentionItemResponse              `json:"attention"`
	TopProducts        []ProductAdsSummaryResponse          `json:"top_products"`
	TopCampaigns       []CampaignPerformanceSummaryResponse `json:"top_campaigns"`
	TopQueries         []QueryPerformanceSummaryResponse    `json:"top_queries"`
	Totals             AdsOverviewTotalsResponse            `json:"totals"`
}

type AdsClientAuditReportResponse struct {
	ReportType      string                `json:"report_type"`
	GeneratedAt     time.Time             `json:"generated_at"`
	DateFrom        string                `json:"date_from,omitempty"`
	DateTo          string                `json:"date_to,omitempty"`
	Report          string                `json:"report"`
	DataStatus      AdsDataStatusResponse `json:"data_status"`
	Recommendations int                   `json:"recommendations"`
	AttentionItems  int                   `json:"attention_items"`
	ActiveCampaigns int                   `json:"active_campaigns"`
	Campaigns       int                   `json:"campaigns"`
	Products        int                   `json:"products"`
	Queries         int                   `json:"queries"`
}

type AdsDataStatusResponse struct {
	State                    string                       `json:"state"`
	Reason                   string                       `json:"reason"`
	BackendVersion           string                       `json:"backend_version,omitempty"`
	DateFrom                 string                       `json:"date_from,omitempty"`
	DateTo                   string                       `json:"date_to,omitempty"`
	ActiveJobRunID           *uuid.UUID                   `json:"active_job_run_id,omitempty"`
	ActiveSyncPhase          string                       `json:"active_sync_phase,omitempty"`
	FreshnessState           string                       `json:"freshness_state"`
	LastSyncedAt             *time.Time                   `json:"last_synced_at,omitempty"`
	RateLimitEndpoint        string                       `json:"rate_limit_endpoint,omitempty"`
	RetryAfterSeconds        int                          `json:"retry_after_seconds,omitempty"`
	NextAllowedAt            *time.Time                   `json:"next_allowed_at,omitempty"`
	PhaseRetries             []AdsSyncPhaseRetryResponse  `json:"phase_retries_queued,omitempty"`
	HasConnectedCabinet      bool                         `json:"has_connected_cabinet"`
	HasCampaigns             bool                         `json:"has_campaigns"`
	HasCurrentStats          bool                         `json:"has_current_stats"`
	CampaignsWithStats       int                          `json:"campaigns_with_stats"`
	CampaignsTotal           int                          `json:"campaigns_total"`
	ProductsWithBusinessData int                          `json:"products_with_business_data"`
	ProductsTotal            int                          `json:"products_total"`
	QueriesWithStats         int                          `json:"queries_with_stats"`
	QueriesTotal             int                          `json:"queries_total"`
	UnitEconomicsState       string                       `json:"unit_economics_state"`
	UnitEconomicsReason      string                       `json:"unit_economics_reason,omitempty"`
	Issues                   []AdsDataStatusIssueResponse `json:"issues,omitempty"`
}

type AdsDataStatusIssueResponse struct {
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	ActionPath string `json:"action_path,omitempty"`
}

func AdsOverviewFromDomain(overview domain.AdsOverview) AdsOverviewResponse {
	return AdsOverviewResponse{
		LastAutoSync:       sellerCabinetAutoSyncFromDomain(overview.LastAutoSync),
		PerformanceCompare: adsPeriodCompareFromDomain(overview.PerformanceCompare),
		Evidence:           sourceEvidenceFromDomain(overview.Evidence),
		DataStatus:         adsDataStatusFromDomain(overview.DataStatus),
		Cabinets:           cabinetSummariesFromDomain(overview.Cabinets),
		Attention:          attentionItemsFromDomain(overview.Attention),
		TopProducts:        productSummariesFromDomain(overview.TopProducts),
		TopCampaigns:       campaignSummariesFromDomain(overview.TopCampaigns),
		TopQueries:         querySummariesFromDomain(overview.TopQueries),
		Totals: AdsOverviewTotalsResponse{
			Cabinets:               overview.Totals.Cabinets,
			Products:               overview.Totals.Products,
			Campaigns:              overview.Totals.Campaigns,
			Queries:                overview.Totals.Queries,
			ActiveCampaigns:        overview.Totals.ActiveCampaigns,
			AttentionItems:         overview.Totals.AttentionItems,
			ActiveRecommendations:  overview.Totals.ActiveRecommendations,
			OverdueRecommendations: overview.Totals.OverdueRecommendations,
			DecisionQueueBuckets:   overview.Totals.DecisionQueueBuckets,
			TaskOwnerBuckets:       overview.Totals.TaskOwnerBuckets,
			ProductDecisions:       overview.Totals.ProductDecisions,
		},
	}
}

func adsDataStatusFromDomain(status domain.AdsDataStatus) AdsDataStatusResponse {
	return AdsDataStatusResponse{
		State:                    status.State,
		Reason:                   status.Reason,
		BackendVersion:           status.BackendVersion,
		DateFrom:                 status.DateFrom,
		DateTo:                   status.DateTo,
		ActiveJobRunID:           status.ActiveJobRunID,
		ActiveSyncPhase:          status.ActiveSyncPhase,
		FreshnessState:           status.FreshnessState,
		LastSyncedAt:             status.LastSyncedAt,
		RateLimitEndpoint:        status.RateLimitEndpoint,
		RetryAfterSeconds:        status.RetryAfterSeconds,
		NextAllowedAt:            status.NextAllowedAt,
		PhaseRetries:             adsSyncPhaseRetriesFromDomain(status.PhaseRetries),
		HasConnectedCabinet:      status.HasConnectedCabinet,
		HasCampaigns:             status.HasCampaigns,
		HasCurrentStats:          status.HasCurrentStats,
		CampaignsWithStats:       status.CampaignsWithStats,
		CampaignsTotal:           status.CampaignsTotal,
		ProductsWithBusinessData: status.ProductsWithBusinessData,
		ProductsTotal:            status.ProductsTotal,
		QueriesWithStats:         status.QueriesWithStats,
		QueriesTotal:             status.QueriesTotal,
		UnitEconomicsState:       status.UnitEconomicsState,
		UnitEconomicsReason:      status.UnitEconomicsReason,
		Issues:                   adsDataStatusIssuesFromDomain(status.Issues),
	}
}

func AdsDataStatusFromDomain(status domain.AdsDataStatus) AdsDataStatusResponse {
	return adsDataStatusFromDomain(status)
}

func adsDataStatusIssuesFromDomain(issues []domain.AdsDataStatusIssue) []AdsDataStatusIssueResponse {
	if len(issues) == 0 {
		return nil
	}
	result := make([]AdsDataStatusIssueResponse, len(issues))
	for i, issue := range issues {
		result[i] = AdsDataStatusIssueResponse{
			Stage:      issue.Stage,
			Message:    issue.Message,
			ActionPath: issue.ActionPath,
		}
	}
	return result
}

func ProductAdsSummaryFromDomain(product domain.ProductAdsSummary) ProductAdsSummaryResponse {
	return ProductAdsSummaryResponse{
		ID:               product.ID,
		WorkspaceID:      product.WorkspaceID,
		SellerCabinetID:  product.SellerCabinetID,
		IntegrationID:    product.IntegrationID,
		IntegrationName:  product.IntegrationName,
		CabinetName:      product.CabinetName,
		CampaignID:       product.CampaignID,
		CampaignName:     product.CampaignName,
		WBCampaignID:     product.WBCampaignID,
		RowKey:           product.RowKey,
		WBProductID:      product.WBProductID,
		Title:            product.Title,
		Brand:            product.Brand,
		Category:         product.Category,
		ImageURL:         product.ImageURL,
		Price:            product.Price,
		Rating:           product.Rating,
		ReviewsCount:     product.ReviewsCount,
		CampaignsCount:   product.CampaignsCount,
		QueriesCount:     product.QueriesCount,
		HealthStatus:     product.HealthStatus,
		HealthReason:     product.HealthReason,
		PrimaryAction:    product.PrimaryAction,
		FreshnessState:   product.FreshnessState,
		Performance:      adsMetricsFromDomain(product.Performance),
		Business:         productBusinessFromDomain(product.Business),
		Scores:           productDecisionScoresFromDomain(product.Scores),
		Decision:         productDecisionFromDomain(product.Decision),
		PeriodCompare:    adsPeriodCompareFromDomain(product.PeriodCompare),
		RelatedCampaigns: entityRefsFromDomain(product.RelatedCampaigns),
		TopQueries:       entityRefsFromDomain(product.TopQueries),
		WasteQueries:     entityRefsFromDomain(product.WasteQueries),
		WinningQueries:   entityRefsFromDomain(product.WinningQueries),
		StockEvidence:    productStockEvidenceFromDomain(product.StockEvidence),
		StockRunout:      productStockRunoutFromDomain(product.StockRunout),
		Evidence:         sourceEvidenceFromDomain(product.Evidence),
		DataCoverageNote: product.DataCoverageNote,
		CreatedAt:        product.CreatedAt,
		UpdatedAt:        product.UpdatedAt,
	}
}

func productStockEvidenceFromDomain(evidence *domain.ProductStockEvidence) *ProductStockEvidenceResponse {
	if evidence == nil {
		return nil
	}
	return &ProductStockEvidenceResponse{
		StockTotal: evidence.StockTotal,
		Source:     evidence.Source,
		CapturedAt: evidence.CapturedAt,
	}
}

func productStockRunoutFromDomain(forecast *domain.ProductStockRunoutForecast) *ProductStockRunoutForecastResponse {
	if forecast == nil {
		return nil
	}
	return &ProductStockRunoutForecastResponse{
		State:             forecast.State,
		StockTotal:        forecast.StockTotal,
		AverageDailySales: forecast.AverageDailySales,
		DaysToEmpty:       forecast.DaysToEmpty,
		PeriodDays:        forecast.PeriodDays,
		Source:            forecast.Source,
		CapturedAt:        forecast.CapturedAt,
		Reason:            forecast.Reason,
	}
}

func productDecisionScoresFromDomain(scores domain.ProductDecisionScores) ProductDecisionScoresResponse {
	return ProductDecisionScoresResponse{
		Advertising: decisionScoreFromDomain(scores.Advertising),
		Readiness:   decisionScoreFromDomain(scores.Readiness),
		Growth:      decisionScoreFromDomain(scores.Growth),
	}
}

func decisionScoreFromDomain(score domain.DecisionScoreSummary) DecisionScoreSummaryResponse {
	return DecisionScoreSummaryResponse{
		Value:           score.Value,
		DataMode:        score.DataMode,
		Evidence:        score.Evidence,
		MissingEvidence: score.MissingEvidence,
	}
}

func productDecisionFromDomain(decision domain.ProductDecisionSummary) ProductDecisionSummaryResponse {
	return ProductDecisionSummaryResponse{
		Decision:        decision.Decision,
		DataMode:        decision.DataMode,
		Reason:          decision.Reason,
		MissingEvidence: decision.MissingEvidence,
	}
}

func CampaignPerformanceSummaryFromDomain(campaign domain.CampaignPerformanceSummary) CampaignPerformanceSummaryResponse {
	return CampaignPerformanceSummaryResponse{
		ID:                       campaign.ID,
		WorkspaceID:              campaign.WorkspaceID,
		SellerCabinetID:          campaign.SellerCabinetID,
		IntegrationID:            campaign.IntegrationID,
		IntegrationName:          campaign.IntegrationName,
		CabinetName:              campaign.CabinetName,
		WBCampaignID:             campaign.WBCampaignID,
		Name:                     campaign.Name,
		Status:                   campaign.Status,
		CampaignType:             campaign.CampaignType,
		BidType:                  campaign.BidType,
		PaymentType:              campaign.PaymentType,
		DailyBudget:              campaign.DailyBudget,
		PlacementSearch:          campaign.PlacementSearch,
		PlacementRecommendations: campaign.PlacementRecommendations,
		WBCreatedAt:              campaign.WBCreatedAt,
		WBStartedAt:              campaign.WBStartedAt,
		WBUpdatedAt:              campaign.WBUpdatedAt,
		WBDeletedAt:              campaign.WBDeletedAt,
		LatestBudget:             campaignBudgetFromDomain(campaign.LatestBudget),
		BudgetPace:               campaignBudgetPaceFromDomain(campaign.BudgetPace),
		BudgetRunout:             campaignBudgetRunoutFromDomain(campaign.BudgetRunout),
		AdFinance:                campaignFinanceFromDomain(campaign.AdFinance),
		LastSync:                 campaign.LastSync,
		HealthStatus:             campaign.HealthStatus,
		HealthReason:             campaign.HealthReason,
		PrimaryAction:            campaign.PrimaryAction,
		FreshnessState:           campaign.FreshnessState,
		Performance:              adsMetricsFromDomain(campaign.Performance),
		PeriodCompare:            adsPeriodCompareFromDomain(campaign.PeriodCompare),
		RelatedProducts:          entityRefsFromDomain(campaign.RelatedProducts),
		Products:                 campaignProductsFromDomain(campaign.Products),
		TopQueries:               entityRefsFromDomain(campaign.TopQueries),
		WasteQueries:             entityRefsFromDomain(campaign.WasteQueries),
		WinningQueries:           entityRefsFromDomain(campaign.WinningQueries),
		RecentBidChanges:         campaignBidChangesFromDomain(campaign.RecentBidChanges),
		ActiveRecommendations:    campaignRecommendationsFromDomain(campaign.ActiveRecommendations),
		Evidence:                 sourceEvidenceFromDomain(campaign.Evidence),
		CreatedAt:                campaign.CreatedAt,
		UpdatedAt:                campaign.UpdatedAt,
	}
}

func QueryPerformanceSummaryFromDomain(query domain.QueryPerformanceSummary) QueryPerformanceSummaryResponse {
	return QueryPerformanceSummaryResponse{
		ID:              query.ID,
		WorkspaceID:     query.WorkspaceID,
		CampaignID:      query.CampaignID,
		ProductID:       query.ProductID,
		SellerCabinetID: query.SellerCabinetID,
		IntegrationID:   query.IntegrationID,
		IntegrationName: query.IntegrationName,
		CabinetName:     query.CabinetName,
		CampaignName:    query.CampaignName,
		WBCampaignID:    query.WBCampaignID,
		ProductName:     query.ProductName,
		WBProductID:     query.WBProductID,
		WBClusterID:     query.WBClusterID,
		WBNormQuery:     query.WBNormQuery,
		Keyword:         query.Keyword,
		CurrentBid:      query.CurrentBid,
		ClusterSize:     query.ClusterSize,
		Source:          query.Source,
		SignalCategory:  query.SignalCategory,
		HealthStatus:    query.HealthStatus,
		HealthReason:    query.HealthReason,
		PrimaryAction:   query.PrimaryAction,
		FreshnessState:  query.FreshnessState,
		Performance:     adsMetricsFromDomain(query.Performance),
		PeriodCompare:   adsPeriodCompareFromDomain(query.PeriodCompare),
		PriorityScore:   query.PriorityScore,
		RelatedProducts: entityRefsFromDomain(query.RelatedProducts),
		Evidence:        sourceEvidenceFromDomain(query.Evidence),
		CreatedAt:       query.CreatedAt,
		UpdatedAt:       query.UpdatedAt,
	}
}

func campaignBudgetFromDomain(budget *domain.CampaignBudgetSummary) *CampaignBudgetSummaryResponse {
	if budget == nil {
		return nil
	}
	return &CampaignBudgetSummaryResponse{
		Cash:       budget.Cash,
		Netting:    budget.Netting,
		Total:      budget.Total,
		CapturedAt: budget.CapturedAt,
	}
}

func campaignBudgetPaceFromDomain(pace *domain.CampaignBudgetPaceSummary) *CampaignBudgetPaceSummaryResponse {
	if pace == nil {
		return nil
	}
	return &CampaignBudgetPaceSummaryResponse{
		State:                            pace.State,
		PeriodDays:                       pace.PeriodDays,
		DailyBudget:                      pace.DailyBudget,
		WeeklyBudget:                     pace.WeeklyBudget,
		MonthlyBudget:                    pace.MonthlyBudget,
		PlannedSpend:                     pace.PlannedSpend,
		ActualSpend:                      pace.ActualSpend,
		UtilizationPercent:               pace.UtilizationPercent,
		ProjectedTodaySpend:              pace.ProjectedTodaySpend,
		ProjectedTodayUtilizationPercent: pace.ProjectedTodayUtilizationPercent,
		Reason:                           pace.Reason,
	}
}

func campaignBudgetRunoutFromDomain(runout *domain.CampaignBudgetRunoutSummary) *CampaignBudgetRunoutSummaryResponse {
	if runout == nil {
		return nil
	}
	return &CampaignBudgetRunoutSummaryResponse{
		State:           runout.State,
		RemainingBudget: runout.RemainingBudget,
		SpendToday:      runout.SpendToday,
		HoursElapsed:    runout.HoursElapsed,
		HoursToEmpty:    runout.HoursToEmpty,
		CapturedAt:      runout.CapturedAt,
		Reason:          runout.Reason,
	}
}

func campaignFinanceFromDomain(summary *domain.CampaignFinanceSummary) *CampaignFinanceSummaryResponse {
	if summary == nil {
		return nil
	}
	return &CampaignFinanceSummaryResponse{
		DocumentsCount:   summary.DocumentsCount,
		Amount:           summary.Amount,
		DocumentTypes:    summary.DocumentTypes,
		LatestDocumentAt: summary.LatestDocumentAt,
		DataMode:         summary.DataMode,
	}
}

func campaignProductsFromDomain(items []domain.CampaignProductPerformanceSummary) []CampaignProductPerformanceSummaryResponse {
	if len(items) == 0 {
		return nil
	}
	result := make([]CampaignProductPerformanceSummaryResponse, len(items))
	for i, item := range items {
		result[i] = CampaignProductPerformanceSummaryResponse{
			ID:                 item.ID,
			ProductID:          item.ProductID,
			WBProductID:        item.WBProductID,
			ProductName:        item.ProductName,
			SubjectName:        item.SubjectName,
			BidSearch:          item.BidSearch,
			BidRecommendations: item.BidRecommendations,
			ProductTotalCarts:  item.ProductTotalCarts,
			Performance:        adsMetricsFromDomain(item.Performance),
		}
	}
	return result
}

func campaignBidChangesFromDomain(items []domain.CampaignBidChangeSummary) []CampaignBidChangeSummaryResponse {
	if len(items) == 0 {
		return nil
	}
	result := make([]CampaignBidChangeSummaryResponse, len(items))
	for i, item := range items {
		result[i] = CampaignBidChangeSummaryResponse{
			ID:               item.ID,
			ProductID:        item.ProductID,
			PhraseID:         item.PhraseID,
			RecommendationID: item.RecommendationID,
			Placement:        item.Placement,
			OldBid:           item.OldBid,
			NewBid:           item.NewBid,
			Reason:           item.Reason,
			Source:           item.Source,
			WBStatus:         item.WBStatus,
			CanRollback:      item.CanRollback,
			RollbackBid:      item.RollbackBid,
			CreatedAt:        item.CreatedAt,
		}
	}
	return result
}

func campaignRecommendationsFromDomain(items []domain.CampaignRecommendationSummary) []CampaignRecommendationSummaryResponse {
	if len(items) == 0 {
		return nil
	}
	result := make([]CampaignRecommendationSummaryResponse, len(items))
	now := time.Now()
	for i, item := range items {
		taskAgeHours, taskDueAt, taskSLAHours, isOverdue := recommendationTaskMetadataFromFields(item.CreatedAt, item.Status, now)
		result[i] = CampaignRecommendationSummaryResponse{
			ID:            item.ID,
			PhraseID:      item.PhraseID,
			ProductID:     item.ProductID,
			Scope:         item.Scope,
			Title:         item.Title,
			Type:          item.Type,
			Severity:      item.Severity,
			Confidence:    item.Confidence,
			NextAction:    item.NextAction,
			Status:        item.Status,
			TaskCategory:  domain.RecommendationTaskCategory(item.Type),
			TaskOwnerRole: domain.RecommendationTaskOwnerRole(item.Type),
			TaskSLAHours:  taskSLAHours,
			TaskDueAt:     taskDueAt,
			TaskAgeHours:  taskAgeHours,
			IsOverdue:     isOverdue,
			CreatedAt:     item.CreatedAt,
		}
	}
	return result
}

func cabinetSummariesFromDomain(items []domain.CabinetSummary) []CabinetSummaryResponse {
	result := make([]CabinetSummaryResponse, len(items))
	for i, item := range items {
		result[i] = CabinetSummaryResponse{
			ID:                   item.ID,
			CabinetID:            item.CabinetID,
			IntegrationID:        item.IntegrationID,
			IntegrationName:      item.IntegrationName,
			CabinetName:          item.CabinetName,
			Status:               item.Status,
			FreshnessState:       item.FreshnessState,
			LastSync:             item.LastSync,
			LastAutoSync:         sellerCabinetAutoSyncFromDomain(item.LastAutoSync),
			CampaignsCount:       item.CampaignsCount,
			ProductsCount:        item.ProductsCount,
			QueriesCount:         item.QueriesCount,
			ActiveCampaignsCount: item.ActiveCampaignsCount,
		}
	}
	return result
}

func productSummariesFromDomain(items []domain.ProductAdsSummary) []ProductAdsSummaryResponse {
	result := make([]ProductAdsSummaryResponse, len(items))
	for i, item := range items {
		result[i] = ProductAdsSummaryFromDomain(item)
	}
	return result
}

func campaignSummariesFromDomain(items []domain.CampaignPerformanceSummary) []CampaignPerformanceSummaryResponse {
	result := make([]CampaignPerformanceSummaryResponse, len(items))
	for i, item := range items {
		result[i] = CampaignPerformanceSummaryFromDomain(item)
	}
	return result
}

func querySummariesFromDomain(items []domain.QueryPerformanceSummary) []QueryPerformanceSummaryResponse {
	result := make([]QueryPerformanceSummaryResponse, len(items))
	for i, item := range items {
		result[i] = QueryPerformanceSummaryFromDomain(item)
	}
	return result
}

func attentionItemsFromDomain(items []domain.AttentionItem) []AttentionItemResponse {
	result := make([]AttentionItemResponse, len(items))
	for i, item := range items {
		result[i] = AttentionItemResponse{
			Type:        item.Type,
			Title:       item.Title,
			Description: item.Description,
			Severity:    item.Severity,
			ActionLabel: item.ActionLabel,
			ActionPath:  item.ActionPath,
			SourceType:  item.SourceType,
			SourceID:    item.SourceID,
		}
	}
	return result
}

func adsMetricsFromDomain(metrics domain.AdsMetricsSummary) AdsMetricsSummaryResponse {
	return AdsMetricsSummaryResponse{
		Impressions:    metrics.Impressions,
		Clicks:         metrics.Clicks,
		Spend:          metrics.Spend,
		Orders:         metrics.Orders,
		Revenue:        metrics.Revenue,
		Atbs:           metrics.Atbs,
		Canceled:       metrics.Canceled,
		Shks:           metrics.Shks,
		CTR:            metrics.CTR,
		CPC:            metrics.CPC,
		CPM:            metrics.CPM,
		CPO:            metrics.CPO,
		ROAS:           metrics.ROAS,
		DRR:            metrics.DRR,
		ConversionRate: metrics.ConversionRate,
		CartRate:       metrics.CartRate,
		AvgPosition:    metrics.AvgPosition,
		DataMode:       metrics.DataMode,
	}
}

func productBusinessFromDomain(metrics domain.ProductBusinessSummary) ProductBusinessSummaryResponse {
	return ProductBusinessSummaryResponse{
		Orders:                             metrics.Orders,
		CanceledOrders:                     metrics.CanceledOrders,
		Sales:                              metrics.Sales,
		Returns:                            metrics.Returns,
		OrderedRevenue:                     metrics.OrderedRevenue,
		SoldRevenue:                        metrics.SoldRevenue,
		ReturnedRevenue:                    metrics.ReturnedRevenue,
		BuyoutRate:                         metrics.BuyoutRate,
		ReturnRate:                         metrics.ReturnRate,
		AdSpend:                            metrics.AdSpend,
		AdToSoldRevenue:                    metrics.AdToSoldRevenue,
		DataMode:                           metrics.DataMode,
		SalesFunnelOpenCount:               metrics.SalesFunnelOpenCount,
		SalesFunnelCartCount:               metrics.SalesFunnelCartCount,
		SalesFunnelOrderCount:              metrics.SalesFunnelOrderCount,
		SalesFunnelOpenToCartConversion:    metrics.SalesFunnelOpenToCartConversion,
		SalesFunnelCartToOrderConversion:   metrics.SalesFunnelCartToOrderConversion,
		SalesFunnelSource:                  metrics.SalesFunnelSource,
		SalesFunnelDataMode:                metrics.SalesFunnelDataMode,
		SalesFunnelCapturedAt:              metrics.SalesFunnelCapturedAt,
		CostPrice:                          metrics.CostPrice,
		LogisticsCost:                      metrics.LogisticsCost,
		OtherCosts:                         metrics.OtherCosts,
		TaxRatePercent:                     metrics.TaxRatePercent,
		CommissionPercent:                  metrics.CommissionPercent,
		TargetMarginPercent:                metrics.TargetMarginPercent,
		MaxAllowedDRR:                      metrics.MaxAllowedDRR,
		MarginBeforeAds:                    metrics.MarginBeforeAds,
		MarginBeforeAdsTotal:               metrics.MarginBeforeAdsTotal,
		MarginBeforeAdsPercent:             metrics.MarginBeforeAdsPercent,
		ProfitAfterAds:                     metrics.ProfitAfterAds,
		MarginalDRR:                        metrics.MarginalDRR,
		EconomicsSource:                    metrics.EconomicsSource,
		EconomicsDataMode:                  metrics.EconomicsDataMode,
		WBCommissionSubjectName:            metrics.WBCommissionSubjectName,
		WBCommissionMarketplacePercent:     metrics.WBCommissionMarketplacePercent,
		WBCommissionSupplierPercent:        metrics.WBCommissionSupplierPercent,
		WBCommissionPickupPercent:          metrics.WBCommissionPickupPercent,
		WBCommissionBookingPercent:         metrics.WBCommissionBookingPercent,
		WBCommissionSupplierExpressPercent: metrics.WBCommissionSupplierExpressPercent,
		WBCommissionDataMode:               metrics.WBCommissionDataMode,
	}
}

func adsPeriodCompareFromDomain(compare *domain.AdsPeriodCompare) *AdsPeriodCompareResponse {
	if compare == nil {
		return nil
	}
	return &AdsPeriodCompareResponse{
		Current:  adsMetricsFromDomain(compare.Current),
		Previous: adsMetricsFromDomain(compare.Previous),
		Delta: AdsMetricsDeltaResponse{
			Impressions:    compare.Delta.Impressions,
			Clicks:         compare.Delta.Clicks,
			Spend:          compare.Delta.Spend,
			Orders:         compare.Delta.Orders,
			Revenue:        compare.Delta.Revenue,
			CTR:            compare.Delta.CTR,
			CPC:            compare.Delta.CPC,
			CPO:            compare.Delta.CPO,
			ROAS:           compare.Delta.ROAS,
			ConversionRate: compare.Delta.ConversionRate,
		},
		Trend: compare.Trend,
	}
}

func entityRefsFromDomain(items []domain.AdsEntityRef) []AdsEntityRefResponse {
	result := make([]AdsEntityRefResponse, len(items))
	for i, item := range items {
		result[i] = AdsEntityRefResponse{
			ID:     item.ID,
			Label:  item.Label,
			WBID:   item.WBID,
			Count:  item.Count,
			Source: item.Source,
		}
	}
	return result
}

// --- Position responses ---

type PositionTrackingTargetResponse struct {
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

func PositionTrackingTargetFromDomain(target domain.PositionTrackingTarget) PositionTrackingTargetResponse {
	return PositionTrackingTargetResponse{
		ID:                target.ID,
		WorkspaceID:       target.WorkspaceID,
		ProductID:         target.ProductID,
		ProductTitle:      target.ProductTitle,
		Query:             target.Query,
		Region:            target.Region,
		IsActive:          target.IsActive,
		BaselinePosition:  target.BaselinePosition,
		BaselineCheckedAt: target.BaselineCheckedAt,
		LatestPosition:    target.LatestPosition,
		LatestPage:        target.LatestPage,
		LatestCheckedAt:   target.LatestCheckedAt,
		Delta:             target.Delta,
		SampleCount:       target.SampleCount,
		AlertCandidate:    target.AlertCandidate,
		AlertSeverity:     target.AlertSeverity,
		CreatedAt:         target.CreatedAt,
		UpdatedAt:         target.UpdatedAt,
	}
}

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

type SERPCompareItemResponse struct {
	WBProductID      int64  `json:"wb_product_id"`
	Title            string `json:"title"`
	IsOwnProduct     bool   `json:"is_own_product"`
	CurrentPosition  *int   `json:"current_position,omitempty"`
	PreviousPosition *int   `json:"previous_position,omitempty"`
	Delta            *int   `json:"delta,omitempty"`
}

func SERPCompareItemFromDomain(item domain.SERPCompareItem) SERPCompareItemResponse {
	return SERPCompareItemResponse{
		WBProductID:      item.WBProductID,
		Title:            item.Title,
		IsOwnProduct:     item.IsOwnProduct,
		CurrentPosition:  item.CurrentPosition,
		PreviousPosition: item.PreviousPosition,
		Delta:            item.Delta,
	}
}

type SERPComparisonResponse struct {
	PreviousSnapshotID *uuid.UUID                `json:"previous_snapshot_id,omitempty"`
	PreviousScannedAt  *time.Time                `json:"previous_scanned_at,omitempty"`
	CurrentOwnCount    int                       `json:"current_own_count"`
	PreviousOwnCount   int                       `json:"previous_own_count"`
	NewEntrantsCount   int                       `json:"new_entrants_count"`
	DroppedCount       int                       `json:"dropped_count"`
	OwnProductsGained  int                       `json:"own_products_gained"`
	OwnProductsLost    int                       `json:"own_products_lost"`
	NewEntrants        []SERPCompareItemResponse `json:"new_entrants"`
	DroppedItems       []SERPCompareItemResponse `json:"dropped_items"`
	BiggestMovers      []SERPCompareItemResponse `json:"biggest_movers"`
}

func SERPComparisonFromDomain(compare domain.SERPComparison) *SERPComparisonResponse {
	newEntrants := make([]SERPCompareItemResponse, len(compare.NewEntrants))
	for i, item := range compare.NewEntrants {
		newEntrants[i] = SERPCompareItemFromDomain(item)
	}
	dropped := make([]SERPCompareItemResponse, len(compare.DroppedItems))
	for i, item := range compare.DroppedItems {
		dropped[i] = SERPCompareItemFromDomain(item)
	}
	movers := make([]SERPCompareItemResponse, len(compare.BiggestMovers))
	for i, item := range compare.BiggestMovers {
		movers[i] = SERPCompareItemFromDomain(item)
	}

	return &SERPComparisonResponse{
		PreviousSnapshotID: compare.PreviousSnapshotID,
		PreviousScannedAt:  compare.PreviousScannedAt,
		CurrentOwnCount:    compare.CurrentOwnCount,
		PreviousOwnCount:   compare.PreviousOwnCount,
		NewEntrantsCount:   compare.NewEntrantsCount,
		DroppedCount:       compare.DroppedCount,
		OwnProductsGained:  compare.OwnProductsGained,
		OwnProductsLost:    compare.OwnProductsLost,
		NewEntrants:        newEntrants,
		DroppedItems:       dropped,
		BiggestMovers:      movers,
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
	Compare      *SERPComparisonResponse  `json:"compare,omitempty"`
}

// SERPSnapshotDetailFromDomain maps snapshot and items into a detail response.
func SERPSnapshotDetailFromDomain(snapshot domain.SERPSnapshot, items []domain.SERPResultItem, compare *domain.SERPComparison) SERPSnapshotDetailResponse {
	respItems := make([]SERPResultItemResponse, len(items))
	for i, item := range items {
		respItems[i] = SERPResultItemFromDomain(item)
	}

	var compareResp *SERPComparisonResponse
	if compare != nil {
		compareResp = SERPComparisonFromDomain(*compare)
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
		Compare:      compareResp,
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

// BidEstimateResponse is the public representation of a current bid estimate.
type BidEstimateResponse struct {
	PhraseID       uuid.UUID `json:"phrase_id"`
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	CompetitiveBid int64     `json:"competitive_bid"`
	LeadershipBid  int64     `json:"leadership_bid"`
	CPMMin         int64     `json:"cpm_min"`
	CapturedAt     time.Time `json:"captured_at"`
}

// BidEstimateFromDomain maps domain.BidSnapshot to BidEstimateResponse.
func BidEstimateFromDomain(b domain.BidSnapshot) BidEstimateResponse {
	return BidEstimateResponse{
		PhraseID:       b.PhraseID,
		WorkspaceID:    b.WorkspaceID,
		CompetitiveBid: b.CompetitiveBid,
		LeadershipBid:  b.LeadershipBid,
		CPMMin:         b.CPMMin,
		CapturedAt:     b.CapturedAt,
	}
}

// --- Recommendation responses ---

// RecommendationResponse is the public representation of a recommendation.
type RecommendationResponse struct {
	ID                uuid.UUID                               `json:"id"`
	WorkspaceID       uuid.UUID                               `json:"workspace_id"`
	CampaignID        *uuid.UUID                              `json:"campaign_id,omitempty"`
	PhraseID          *uuid.UUID                              `json:"phrase_id,omitempty"`
	ProductID         *uuid.UUID                              `json:"product_id,omitempty"`
	SellerCabinetID   *uuid.UUID                              `json:"seller_cabinet_id,omitempty"`
	Title             string                                  `json:"title"`
	Description       string                                  `json:"description"`
	Type              string                                  `json:"type"`
	Severity          string                                  `json:"severity"`
	Confidence        float64                                 `json:"confidence"`
	SourceMetrics     json.RawMessage                         `json:"source_metrics"`
	AnalysisWindow    *domain.RecommendationAnalysisWindow    `json:"analysis_window,omitempty"`
	PreviousWindow    *domain.RecommendationAnalysisWindow    `json:"previous_window,omitempty"`
	ConfidenceFactors []domain.RecommendationConfidenceFactor `json:"confidence_factors,omitempty"`
	Action            *domain.RecommendationAction            `json:"action,omitempty"`
	DecisionBasis     string                                  `json:"decision_basis,omitempty"`
	NextAction        *string                                 `json:"next_action,omitempty"`
	Status            string                                  `json:"status"`
	TaskCategory      string                                  `json:"task_category,omitempty"`
	TaskOwnerRole     string                                  `json:"task_owner_role,omitempty"`
	TaskSLAHours      int                                     `json:"task_sla_hours"`
	TaskDueAt         *time.Time                              `json:"task_due_at,omitempty"`
	TaskAgeHours      int                                     `json:"task_age_hours"`
	IsOverdue         bool                                    `json:"is_overdue"`
	Evidence          *SourceEvidenceResponse                 `json:"evidence,omitempty"`
	CreatedAt         time.Time                               `json:"created_at"`
	UpdatedAt         time.Time                               `json:"updated_at"`
}

// RecommendationFromDomain maps domain.Recommendation to RecommendationResponse.
func RecommendationFromDomain(r domain.Recommendation) RecommendationResponse {
	taskAgeHours, taskDueAt, taskSLAHours, isOverdue := recommendationTaskMetadata(r, time.Now())
	return RecommendationResponse{
		ID:                r.ID,
		WorkspaceID:       r.WorkspaceID,
		CampaignID:        r.CampaignID,
		PhraseID:          r.PhraseID,
		ProductID:         r.ProductID,
		SellerCabinetID:   r.SellerCabinetID,
		Title:             r.Title,
		Description:       r.Description,
		Type:              r.Type,
		Severity:          r.Severity,
		Confidence:        r.Confidence,
		SourceMetrics:     r.SourceMetrics,
		AnalysisWindow:    r.AnalysisWindow,
		PreviousWindow:    r.PreviousWindow,
		ConfidenceFactors: r.ConfidenceFactors,
		Action:            r.Action,
		DecisionBasis:     r.DecisionBasis,
		NextAction:        r.NextAction,
		Status:            r.Status,
		TaskCategory:      domain.RecommendationTaskCategory(r.Type),
		TaskOwnerRole:     domain.RecommendationTaskOwnerRole(r.Type),
		TaskSLAHours:      taskSLAHours,
		TaskDueAt:         taskDueAt,
		TaskAgeHours:      taskAgeHours,
		IsOverdue:         isOverdue,
		Evidence:          sourceEvidenceFromDomain(r.Evidence),
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

func recommendationTaskMetadata(r domain.Recommendation, now time.Time) (int, *time.Time, int, bool) {
	return recommendationTaskMetadataFromFields(r.CreatedAt, r.Status, now)
}

func recommendationTaskMetadataFromFields(createdAt time.Time, status string, now time.Time) (int, *time.Time, int, bool) {
	taskSLAHours := int(domain.RecommendationOverdueAfter.Hours())
	if createdAt.IsZero() || now.Before(createdAt) {
		return 0, nil, taskSLAHours, false
	}
	dueAt := createdAt.Add(domain.RecommendationOverdueAfter)
	ageHours := int(now.Sub(createdAt).Hours())
	isOverdue := status == domain.RecommendationStatusActive && now.Sub(createdAt) >= domain.RecommendationOverdueAfter
	return ageHours, &dueAt, taskSLAHours, isOverdue
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

type ExtensionTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
	WorkspaceID      string `json:"workspace_id"`
	Role             string `json:"role"`
}

type ExtensionIngestAcceptedResponse struct {
	Accepted int `json:"accepted"`
}

type ExtensionPageContextResponse struct {
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

func ExtensionPageContextFromDomain(item domain.ExtensionPageContext) ExtensionPageContextResponse {
	return ExtensionPageContextResponse{
		ID:              item.ID,
		SessionID:       item.SessionID,
		WorkspaceID:     item.WorkspaceID,
		UserID:          item.UserID,
		URL:             item.URL,
		PageType:        item.PageType,
		SellerCabinetID: item.SellerCabinetID,
		CampaignID:      item.CampaignID,
		PhraseID:        item.PhraseID,
		ProductID:       item.ProductID,
		Query:           item.Query,
		Region:          item.Region,
		ActiveFilters:   item.ActiveFilters,
		Metadata:        item.Metadata,
		CapturedAt:      item.CapturedAt,
		CreatedAt:       item.CreatedAt,
	}
}

type ExtensionNetworkCaptureResponse struct {
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
	Payload         json.RawMessage `json:"payload,omitempty"`
	CapturedAt      time.Time       `json:"captured_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

func ExtensionNetworkCaptureFromDomain(item domain.ExtensionNetworkCapture) ExtensionNetworkCaptureResponse {
	return ExtensionNetworkCaptureResponse{
		ID:              item.ID,
		SessionID:       item.SessionID,
		WorkspaceID:     item.WorkspaceID,
		UserID:          item.UserID,
		SellerCabinetID: item.SellerCabinetID,
		CampaignID:      item.CampaignID,
		PhraseID:        item.PhraseID,
		ProductID:       item.ProductID,
		PageType:        item.PageType,
		EndpointKey:     item.EndpointKey,
		Query:           item.Query,
		Region:          item.Region,
		Payload:         item.Payload,
		CapturedAt:      item.CapturedAt,
		CreatedAt:       item.CreatedAt,
	}
}

type ExtensionDOMRowSnapshotResponse struct {
	ID              uuid.UUID       `json:"id"`
	SessionID       uuid.UUID       `json:"session_id"`
	WorkspaceID     uuid.UUID       `json:"workspace_id"`
	UserID          uuid.UUID       `json:"user_id"`
	SellerCabinetID *uuid.UUID      `json:"seller_cabinet_id,omitempty"`
	CampaignID      *uuid.UUID      `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID      `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID      `json:"product_id,omitempty"`
	PageType        string          `json:"page_type"`
	TableRole       string          `json:"table_role"`
	RowKey          string          `json:"row_key"`
	Query           *string         `json:"query,omitempty"`
	Region          *string         `json:"region,omitempty"`
	VisibleText     string          `json:"visible_text"`
	Cells           json.RawMessage `json:"cells,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	Source          string          `json:"source"`
	Confidence      float64         `json:"confidence"`
	CapturedAt      time.Time       `json:"captured_at"`
	CreatedAt       time.Time       `json:"created_at"`
}

func ExtensionDOMRowSnapshotFromDomain(item domain.ExtensionDOMRowSnapshot) ExtensionDOMRowSnapshotResponse {
	return ExtensionDOMRowSnapshotResponse{
		ID:              item.ID,
		SessionID:       item.SessionID,
		WorkspaceID:     item.WorkspaceID,
		UserID:          item.UserID,
		SellerCabinetID: item.SellerCabinetID,
		CampaignID:      item.CampaignID,
		PhraseID:        item.PhraseID,
		ProductID:       item.ProductID,
		PageType:        item.PageType,
		TableRole:       item.TableRole,
		RowKey:          item.RowKey,
		Query:           item.Query,
		Region:          item.Region,
		VisibleText:     item.VisibleText,
		Cells:           item.Cells,
		Metadata:        item.Metadata,
		Source:          item.Source,
		Confidence:      item.Confidence,
		CapturedAt:      item.CapturedAt,
		CreatedAt:       item.CreatedAt,
	}
}

type ExtensionLiveBidSnapshotResponse struct {
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

func ExtensionLiveBidSnapshotFromDomain(item domain.ExtensionBidSnapshot) ExtensionLiveBidSnapshotResponse {
	return ExtensionLiveBidSnapshotResponse{
		ID:              item.ID,
		SessionID:       item.SessionID,
		WorkspaceID:     item.WorkspaceID,
		UserID:          item.UserID,
		SellerCabinetID: item.SellerCabinetID,
		CampaignID:      item.CampaignID,
		PhraseID:        item.PhraseID,
		Query:           item.Query,
		Region:          item.Region,
		VisibleBid:      item.VisibleBid,
		RecommendedBid:  item.RecommendedBid,
		CompetitiveBid:  item.CompetitiveBid,
		LeadershipBid:   item.LeadershipBid,
		CPMMin:          item.CPMMin,
		Source:          item.Source,
		Confidence:      item.Confidence,
		Metadata:        item.Metadata,
		CapturedAt:      item.CapturedAt,
		CreatedAt:       item.CreatedAt,
	}
}

type ExtensionLivePositionSnapshotResponse struct {
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

func ExtensionLivePositionSnapshotFromDomain(item domain.ExtensionPositionSnapshot) ExtensionLivePositionSnapshotResponse {
	return ExtensionLivePositionSnapshotResponse{
		ID:              item.ID,
		SessionID:       item.SessionID,
		WorkspaceID:     item.WorkspaceID,
		UserID:          item.UserID,
		SellerCabinetID: item.SellerCabinetID,
		CampaignID:      item.CampaignID,
		PhraseID:        item.PhraseID,
		ProductID:       item.ProductID,
		Query:           item.Query,
		Region:          item.Region,
		VisiblePosition: item.VisiblePosition,
		VisiblePage:     item.VisiblePage,
		PageSubtype:     item.PageSubtype,
		Source:          item.Source,
		Confidence:      item.Confidence,
		Metadata:        item.Metadata,
		CapturedAt:      item.CapturedAt,
		CreatedAt:       item.CreatedAt,
	}
}

type ExtensionUISignalResponse struct {
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

func ExtensionUISignalFromDomain(item domain.ExtensionUISignal) ExtensionUISignalResponse {
	return ExtensionUISignalResponse{
		ID:              item.ID,
		SessionID:       item.SessionID,
		WorkspaceID:     item.WorkspaceID,
		UserID:          item.UserID,
		SellerCabinetID: item.SellerCabinetID,
		CampaignID:      item.CampaignID,
		PhraseID:        item.PhraseID,
		ProductID:       item.ProductID,
		Query:           item.Query,
		Region:          item.Region,
		SignalType:      item.SignalType,
		Severity:        item.Severity,
		Title:           item.Title,
		Message:         item.Message,
		Confidence:      item.Confidence,
		Metadata:        item.Metadata,
		CapturedAt:      item.CapturedAt,
		CreatedAt:       item.CreatedAt,
	}
}

type ExtensionWidgetDataStatusResponse struct {
	Source             string                                `json:"source"`
	CapturedAt         *time.Time                            `json:"captured_at,omitempty"`
	FreshnessState     string                                `json:"freshness_state"`
	Confidence         float64                               `json:"confidence"`
	Coverage           string                                `json:"coverage"`
	ConfirmedInCabinet bool                                  `json:"confirmed_in_cabinet"`
	EvidenceCounts     ExtensionWidgetEvidenceCountsResponse `json:"evidence_counts"`
	Issues             []ExtensionWidgetIssueResponse        `json:"issues,omitempty"`
	NextActions        []ExtensionWidgetActionResponse       `json:"next_actions,omitempty"`
}

type ExtensionWidgetEvidenceCountsResponse struct {
	BidSnapshots      int `json:"bid_snapshots"`
	PositionSnapshots int `json:"position_snapshots"`
	UISignals         int `json:"ui_signals"`
}

type ExtensionWidgetIssueResponse struct {
	Stage      string `json:"stage"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	ActionPath string `json:"action_path,omitempty"`
}

type ExtensionWidgetActionResponse struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	ActionPath string `json:"action_path"`
	Tone       string `json:"tone,omitempty"`
}

type ExtensionWidgetPrimaryInsightResponse struct {
	Title      string                         `json:"title"`
	Message    string                         `json:"message"`
	Severity   string                         `json:"severity"`
	Source     string                         `json:"source"`
	Evidence   []string                       `json:"evidence,omitempty"`
	NextAction *ExtensionWidgetActionResponse `json:"next_action,omitempty"`
}

type ExtensionEvidenceSummaryResponse struct {
	WorkspaceID        uuid.UUID                       `json:"workspace_id"`
	GeneratedAt        time.Time                       `json:"generated_at"`
	LatestCapturedAt   *time.Time                      `json:"latest_captured_at,omitempty"`
	NetworkCaptures    int                             `json:"network_captures"`
	BidSnapshots       int                             `json:"bid_snapshots"`
	PositionSnapshots  int                             `json:"position_snapshots"`
	UISignals          int                             `json:"ui_signals"`
	EndpointCounts     map[string]int                  `json:"endpoint_counts"`
	SeverityCounts     map[string]int                  `json:"severity_counts"`
	FreshnessState     string                          `json:"freshness_state"`
	Coverage           string                          `json:"coverage"`
	ConfirmedInCabinet bool                            `json:"confirmed_in_cabinet"`
	Issues             []ExtensionWidgetIssueResponse  `json:"issues,omitempty"`
	NextActions        []ExtensionWidgetActionResponse `json:"next_actions,omitempty"`
}

type ExtensionEvidenceDebugCountsResponse struct {
	PageContexts      int `json:"page_contexts"`
	NetworkCaptures   int `json:"network_captures"`
	DOMRowSnapshots   int `json:"dom_row_snapshots"`
	BidSnapshots      int `json:"bid_snapshots"`
	PositionSnapshots int `json:"position_snapshots"`
	UISignals         int `json:"ui_signals"`
}

type ExtensionEvidenceDebugResponse struct {
	WorkspaceID       uuid.UUID                               `json:"workspace_id"`
	Scope             string                                  `json:"scope"`
	CampaignID        *uuid.UUID                              `json:"campaign_id,omitempty"`
	ProductID         *uuid.UUID                              `json:"product_id,omitempty"`
	PhraseID          *uuid.UUID                              `json:"phrase_id,omitempty"`
	Query             string                                  `json:"query,omitempty"`
	GeneratedAt       time.Time                               `json:"generated_at"`
	LatestCapturedAt  *time.Time                              `json:"latest_captured_at,omitempty"`
	Counts            ExtensionEvidenceDebugCountsResponse    `json:"counts"`
	DataStatus        ExtensionWidgetDataStatusResponse       `json:"data_status"`
	PageContexts      []ExtensionPageContextResponse          `json:"page_contexts"`
	NetworkCaptures   []ExtensionNetworkCaptureResponse       `json:"network_captures"`
	DOMRowSnapshots   []ExtensionDOMRowSnapshotResponse       `json:"dom_row_snapshots"`
	BidSnapshots      []ExtensionLiveBidSnapshotResponse      `json:"bid_snapshots"`
	PositionSnapshots []ExtensionLivePositionSnapshotResponse `json:"position_snapshots"`
	UISignals         []ExtensionUISignalResponse             `json:"ui_signals"`
	Issues            []ExtensionWidgetIssueResponse          `json:"issues,omitempty"`
	NextActions       []ExtensionWidgetActionResponse         `json:"next_actions,omitempty"`
}

type ExtensionEvidenceSupportSummaryResponse struct {
	SourceLabel        string `json:"source_label"`
	Readiness          string `json:"readiness"`
	CapturedSignals    int    `json:"captured_signals"`
	MissingSignals     int    `json:"missing_signals"`
	ConfirmedInCabinet bool   `json:"confirmed_in_cabinet"`
	FreshnessState     string `json:"freshness_state"`
	Coverage           string `json:"coverage"`
}

type ExtensionEvidenceSupportSectionResponse struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Status           string     `json:"status"`
	Detail           string     `json:"detail"`
	EvidenceCount    int        `json:"evidence_count"`
	LatestCapturedAt *time.Time `json:"latest_captured_at,omitempty"`
}

type ExtensionEvidenceSupportChecklistItemResponse struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Done       bool   `json:"done"`
	Detail     string `json:"detail"`
	ActionPath string `json:"action_path"`
}

type ExtensionEvidenceSupportReportResponse struct {
	WorkspaceID      uuid.UUID                                       `json:"workspace_id"`
	Scope            string                                          `json:"scope"`
	CampaignID       *uuid.UUID                                      `json:"campaign_id,omitempty"`
	ProductID        *uuid.UUID                                      `json:"product_id,omitempty"`
	PhraseID         *uuid.UUID                                      `json:"phrase_id,omitempty"`
	Query            string                                          `json:"query,omitempty"`
	GeneratedAt      time.Time                                       `json:"generated_at"`
	LatestCapturedAt *time.Time                                      `json:"latest_captured_at,omitempty"`
	Summary          ExtensionEvidenceSupportSummaryResponse         `json:"summary"`
	Sections         []ExtensionEvidenceSupportSectionResponse       `json:"sections"`
	Checklist        []ExtensionEvidenceSupportChecklistItemResponse `json:"checklist"`
	Issues           []ExtensionWidgetIssueResponse                  `json:"issues,omitempty"`
	NextActions      []ExtensionWidgetActionResponse                 `json:"next_actions,omitempty"`
}

func ExtensionEvidenceSummaryFromService(summary service.ExtensionEvidenceSummary) ExtensionEvidenceSummaryResponse {
	issues := make([]ExtensionWidgetIssueResponse, len(summary.Issues))
	for i, item := range summary.Issues {
		issues[i] = ExtensionWidgetIssueResponse{
			Stage:      item.Stage,
			Severity:   item.Severity,
			Message:    item.Message,
			ActionPath: item.ActionPath,
		}
	}
	actions := make([]ExtensionWidgetActionResponse, len(summary.NextActions))
	for i, item := range summary.NextActions {
		actions[i] = ExtensionWidgetActionResponse{
			ID:         item.ID,
			Label:      item.Label,
			ActionPath: item.ActionPath,
			Tone:       item.Tone,
		}
	}
	return ExtensionEvidenceSummaryResponse{
		WorkspaceID:        summary.WorkspaceID,
		GeneratedAt:        summary.GeneratedAt,
		LatestCapturedAt:   summary.LatestCapturedAt,
		NetworkCaptures:    summary.NetworkCaptures,
		BidSnapshots:       summary.BidSnapshots,
		PositionSnapshots:  summary.PositionSnapshots,
		UISignals:          summary.UISignals,
		EndpointCounts:     summary.EndpointCounts,
		SeverityCounts:     summary.SeverityCounts,
		FreshnessState:     summary.FreshnessState,
		Coverage:           summary.Coverage,
		ConfirmedInCabinet: summary.ConfirmedInCabinet,
		Issues:             issues,
		NextActions:        actions,
	}
}

func ExtensionEvidenceDebugFromService(debug service.ExtensionEvidenceDebug) ExtensionEvidenceDebugResponse {
	pageContexts := make([]ExtensionPageContextResponse, len(debug.PageContexts))
	for i, item := range debug.PageContexts {
		pageContexts[i] = ExtensionPageContextFromDomain(item)
	}
	networkCaptures := make([]ExtensionNetworkCaptureResponse, len(debug.NetworkCaptures))
	for i, item := range debug.NetworkCaptures {
		networkCaptures[i] = ExtensionNetworkCaptureFromDomain(item)
	}
	domRowSnapshots := make([]ExtensionDOMRowSnapshotResponse, len(debug.DOMRowSnapshots))
	for i, item := range debug.DOMRowSnapshots {
		domRowSnapshots[i] = ExtensionDOMRowSnapshotFromDomain(item)
	}
	bidSnapshots := make([]ExtensionLiveBidSnapshotResponse, len(debug.BidSnapshots))
	for i, item := range debug.BidSnapshots {
		bidSnapshots[i] = ExtensionLiveBidSnapshotFromDomain(item)
	}
	positionSnapshots := make([]ExtensionLivePositionSnapshotResponse, len(debug.PositionSnapshots))
	for i, item := range debug.PositionSnapshots {
		positionSnapshots[i] = ExtensionLivePositionSnapshotFromDomain(item)
	}
	uiSignals := make([]ExtensionUISignalResponse, len(debug.UISignals))
	for i, item := range debug.UISignals {
		uiSignals[i] = ExtensionUISignalFromDomain(item)
	}
	issues := make([]ExtensionWidgetIssueResponse, len(debug.Issues))
	for i, item := range debug.Issues {
		issues[i] = ExtensionWidgetIssueResponse{
			Stage:      item.Stage,
			Severity:   item.Severity,
			Message:    item.Message,
			ActionPath: item.ActionPath,
		}
	}
	actions := make([]ExtensionWidgetActionResponse, len(debug.NextActions))
	for i, item := range debug.NextActions {
		actions[i] = ExtensionWidgetActionResponse{
			ID:         item.ID,
			Label:      item.Label,
			ActionPath: item.ActionPath,
			Tone:       item.Tone,
		}
	}
	return ExtensionEvidenceDebugResponse{
		WorkspaceID:       debug.WorkspaceID,
		Scope:             debug.Scope,
		CampaignID:        debug.CampaignID,
		ProductID:         debug.ProductID,
		PhraseID:          debug.PhraseID,
		Query:             debug.Query,
		GeneratedAt:       debug.GeneratedAt,
		LatestCapturedAt:  debug.LatestCapturedAt,
		Counts:            ExtensionEvidenceDebugCountsResponse(debug.Counts),
		DataStatus:        ExtensionWidgetDataStatusFromService(debug.DataStatus),
		PageContexts:      pageContexts,
		NetworkCaptures:   networkCaptures,
		DOMRowSnapshots:   domRowSnapshots,
		BidSnapshots:      bidSnapshots,
		PositionSnapshots: positionSnapshots,
		UISignals:         uiSignals,
		Issues:            issues,
		NextActions:       actions,
	}
}

func ExtensionEvidenceSupportReportFromService(report service.ExtensionEvidenceSupportReport) ExtensionEvidenceSupportReportResponse {
	sections := make([]ExtensionEvidenceSupportSectionResponse, len(report.Sections))
	for i, item := range report.Sections {
		sections[i] = ExtensionEvidenceSupportSectionResponse{
			ID:               item.ID,
			Title:            item.Title,
			Status:           item.Status,
			Detail:           item.Detail,
			EvidenceCount:    item.EvidenceCount,
			LatestCapturedAt: item.LatestCapturedAt,
		}
	}
	checklist := make([]ExtensionEvidenceSupportChecklistItemResponse, len(report.Checklist))
	for i, item := range report.Checklist {
		checklist[i] = ExtensionEvidenceSupportChecklistItemResponse{
			ID:         item.ID,
			Label:      item.Label,
			Done:       item.Done,
			Detail:     item.Detail,
			ActionPath: item.ActionPath,
		}
	}
	issues := make([]ExtensionWidgetIssueResponse, len(report.Issues))
	for i, item := range report.Issues {
		issues[i] = ExtensionWidgetIssueResponse{
			Stage:      item.Stage,
			Severity:   item.Severity,
			Message:    item.Message,
			ActionPath: item.ActionPath,
		}
	}
	actions := make([]ExtensionWidgetActionResponse, len(report.NextActions))
	for i, item := range report.NextActions {
		actions[i] = ExtensionWidgetActionResponse{
			ID:         item.ID,
			Label:      item.Label,
			ActionPath: item.ActionPath,
			Tone:       item.Tone,
		}
	}
	return ExtensionEvidenceSupportReportResponse{
		WorkspaceID:      report.WorkspaceID,
		Scope:            report.Scope,
		CampaignID:       report.CampaignID,
		ProductID:        report.ProductID,
		PhraseID:         report.PhraseID,
		Query:            report.Query,
		GeneratedAt:      report.GeneratedAt,
		LatestCapturedAt: report.LatestCapturedAt,
		Summary: ExtensionEvidenceSupportSummaryResponse{
			SourceLabel:        report.Summary.SourceLabel,
			Readiness:          report.Summary.Readiness,
			CapturedSignals:    report.Summary.CapturedSignals,
			MissingSignals:     report.Summary.MissingSignals,
			ConfirmedInCabinet: report.Summary.ConfirmedInCabinet,
			FreshnessState:     report.Summary.FreshnessState,
			Coverage:           report.Summary.Coverage,
		},
		Sections:    sections,
		Checklist:   checklist,
		Issues:      issues,
		NextActions: actions,
	}
}

func ExtensionWidgetDataStatusFromService(status service.ExtensionWidgetDataStatus) ExtensionWidgetDataStatusResponse {
	issues := make([]ExtensionWidgetIssueResponse, len(status.Issues))
	for i, item := range status.Issues {
		issues[i] = ExtensionWidgetIssueResponse{
			Stage:      item.Stage,
			Severity:   item.Severity,
			Message:    item.Message,
			ActionPath: item.ActionPath,
		}
	}
	actions := make([]ExtensionWidgetActionResponse, len(status.NextActions))
	for i, item := range status.NextActions {
		actions[i] = ExtensionWidgetActionResponse{
			ID:         item.ID,
			Label:      item.Label,
			ActionPath: item.ActionPath,
			Tone:       item.Tone,
		}
	}
	return ExtensionWidgetDataStatusResponse{
		Source:             status.Source,
		CapturedAt:         status.CapturedAt,
		FreshnessState:     status.FreshnessState,
		Confidence:         status.Confidence,
		Coverage:           status.Coverage,
		ConfirmedInCabinet: status.ConfirmedInCabinet,
		EvidenceCounts: ExtensionWidgetEvidenceCountsResponse{
			BidSnapshots:      status.EvidenceCounts.BidSnapshots,
			PositionSnapshots: status.EvidenceCounts.PositionSnapshots,
			UISignals:         status.EvidenceCounts.UISignals,
		},
		Issues:      issues,
		NextActions: actions,
	}
}

func ExtensionWidgetPrimaryInsightFromService(insight service.ExtensionWidgetPrimaryInsight) ExtensionWidgetPrimaryInsightResponse {
	var nextAction *ExtensionWidgetActionResponse
	if insight.NextAction != nil {
		nextAction = &ExtensionWidgetActionResponse{
			ID:         insight.NextAction.ID,
			Label:      insight.NextAction.Label,
			ActionPath: insight.NextAction.ActionPath,
			Tone:       insight.NextAction.Tone,
		}
	}
	return ExtensionWidgetPrimaryInsightResponse{
		Title:      insight.Title,
		Message:    insight.Message,
		Severity:   insight.Severity,
		Source:     insight.Source,
		Evidence:   insight.Evidence,
		NextAction: nextAction,
	}
}

type ExtensionSearchWidgetResponse struct {
	Query            string                                  `json:"query"`
	Phrase           *PhraseResponse                         `json:"phrase,omitempty"`
	Frequency        *int                                    `json:"frequency,omitempty"`
	CompetitorsCount *int                                    `json:"competitors_count,omitempty"`
	KnownPositions   []PositionResponse                      `json:"known_positions"`
	BidEstimate      *BidEstimateResponse                    `json:"bid_estimate,omitempty"`
	LiveBidSnapshot  *ExtensionLiveBidSnapshotResponse       `json:"live_bid_snapshot,omitempty"`
	LivePositions    []ExtensionLivePositionSnapshotResponse `json:"live_positions"`
	UISignals        []ExtensionUISignalResponse             `json:"ui_signals"`
	PrimaryInsight   ExtensionWidgetPrimaryInsightResponse   `json:"primary_insight"`
	DataStatus       ExtensionWidgetDataStatusResponse       `json:"data_status"`
	Recommendations  []RecommendationResponse                `json:"recommendations"`
}

func ExtensionSearchWidgetFromService(widget service.ExtensionSearchWidget) ExtensionSearchWidgetResponse {
	positions := make([]PositionResponse, len(widget.KnownPositions))
	for i, position := range widget.KnownPositions {
		positions[i] = PositionFromDomain(position)
	}

	recommendations := make([]RecommendationResponse, len(widget.Recommendations))
	for i, recommendation := range widget.Recommendations {
		recommendations[i] = RecommendationFromDomain(recommendation)
	}

	var phrase *PhraseResponse
	if widget.Phrase != nil {
		value := PhraseFromDomain(*widget.Phrase)
		phrase = &value
	}

	var bidEstimate *BidEstimateResponse
	if widget.BidEstimate != nil {
		value := BidEstimateFromDomain(*widget.BidEstimate)
		bidEstimate = &value
	}

	var liveBidSnapshot *ExtensionLiveBidSnapshotResponse
	if widget.LiveBidSnapshot != nil {
		value := ExtensionLiveBidSnapshotFromDomain(*widget.LiveBidSnapshot)
		liveBidSnapshot = &value
	}

	livePositions := make([]ExtensionLivePositionSnapshotResponse, len(widget.LivePositions))
	for i, item := range widget.LivePositions {
		livePositions[i] = ExtensionLivePositionSnapshotFromDomain(item)
	}

	uiSignals := make([]ExtensionUISignalResponse, len(widget.UISignals))
	for i, item := range widget.UISignals {
		uiSignals[i] = ExtensionUISignalFromDomain(item)
	}

	return ExtensionSearchWidgetResponse{
		Query:            widget.Query,
		Phrase:           phrase,
		Frequency:        widget.Frequency,
		CompetitorsCount: widget.CompetitorsCount,
		KnownPositions:   positions,
		BidEstimate:      bidEstimate,
		LiveBidSnapshot:  liveBidSnapshot,
		LivePositions:    livePositions,
		UISignals:        uiSignals,
		PrimaryInsight:   ExtensionWidgetPrimaryInsightFromService(widget.PrimaryInsight),
		DataStatus:       ExtensionWidgetDataStatusFromService(widget.DataStatus),
		Recommendations:  recommendations,
	}
}

type ExtensionProductWidgetResponse struct {
	Product         ProductResponse                         `json:"product"`
	Positions       []PositionResponse                      `json:"positions"`
	LivePositions   []ExtensionLivePositionSnapshotResponse `json:"live_positions"`
	UISignals       []ExtensionUISignalResponse             `json:"ui_signals"`
	PrimaryInsight  ExtensionWidgetPrimaryInsightResponse   `json:"primary_insight"`
	DataStatus      ExtensionWidgetDataStatusResponse       `json:"data_status"`
	Recommendations []RecommendationResponse                `json:"recommendations"`
}

func ExtensionProductWidgetFromService(widget service.ExtensionProductWidget) ExtensionProductWidgetResponse {
	positions := make([]PositionResponse, len(widget.Positions))
	for i, position := range widget.Positions {
		positions[i] = PositionFromDomain(position)
	}

	recommendations := make([]RecommendationResponse, len(widget.Recommendations))
	for i, recommendation := range widget.Recommendations {
		recommendations[i] = RecommendationFromDomain(recommendation)
	}

	livePositions := make([]ExtensionLivePositionSnapshotResponse, len(widget.LivePositions))
	for i, item := range widget.LivePositions {
		livePositions[i] = ExtensionLivePositionSnapshotFromDomain(item)
	}

	uiSignals := make([]ExtensionUISignalResponse, len(widget.UISignals))
	for i, item := range widget.UISignals {
		uiSignals[i] = ExtensionUISignalFromDomain(item)
	}

	return ExtensionProductWidgetResponse{
		Product:         ProductFromDomain(widget.Product),
		Positions:       positions,
		LivePositions:   livePositions,
		UISignals:       uiSignals,
		PrimaryInsight:  ExtensionWidgetPrimaryInsightFromService(widget.PrimaryInsight),
		DataStatus:      ExtensionWidgetDataStatusFromService(widget.DataStatus),
		Recommendations: recommendations,
	}
}

type ExtensionCampaignWidgetResponse struct {
	Campaign        CampaignResponse                      `json:"campaign"`
	Stats           []CampaignStatResponse                `json:"stats"`
	Phrases         []PhraseResponse                      `json:"phrases"`
	LiveBids        []ExtensionLiveBidSnapshotResponse    `json:"live_bids"`
	UISignals       []ExtensionUISignalResponse           `json:"ui_signals"`
	PrimaryInsight  ExtensionWidgetPrimaryInsightResponse `json:"primary_insight"`
	DataStatus      ExtensionWidgetDataStatusResponse     `json:"data_status"`
	Recommendations []RecommendationResponse              `json:"recommendations"`
}

func ExtensionCampaignWidgetFromService(widget service.ExtensionCampaignWidget) ExtensionCampaignWidgetResponse {
	stats := make([]CampaignStatResponse, len(widget.Stats))
	for i, stat := range widget.Stats {
		stats[i] = CampaignStatFromDomain(stat)
	}

	phrases := make([]PhraseResponse, len(widget.Phrases))
	for i, phrase := range widget.Phrases {
		phrases[i] = PhraseFromDomain(phrase)
	}

	recommendations := make([]RecommendationResponse, len(widget.Recommendations))
	for i, recommendation := range widget.Recommendations {
		recommendations[i] = RecommendationFromDomain(recommendation)
	}

	liveBids := make([]ExtensionLiveBidSnapshotResponse, len(widget.LiveBids))
	for i, item := range widget.LiveBids {
		liveBids[i] = ExtensionLiveBidSnapshotFromDomain(item)
	}

	uiSignals := make([]ExtensionUISignalResponse, len(widget.UISignals))
	for i, item := range widget.UISignals {
		uiSignals[i] = ExtensionUISignalFromDomain(item)
	}

	return ExtensionCampaignWidgetResponse{
		Campaign:        CampaignFromDomain(widget.Campaign),
		Stats:           stats,
		Phrases:         phrases,
		LiveBids:        liveBids,
		UISignals:       uiSignals,
		PrimaryInsight:  ExtensionWidgetPrimaryInsightFromService(widget.PrimaryInsight),
		DataStatus:      ExtensionWidgetDataStatusFromService(widget.DataStatus),
		Recommendations: recommendations,
	}
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
	ID           uuid.UUID               `json:"id"`
	WorkspaceID  *uuid.UUID              `json:"workspace_id,omitempty"`
	TaskType     string                  `json:"task_type"`
	Status       string                  `json:"status"`
	StartedAt    time.Time               `json:"started_at"`
	FinishedAt   *time.Time              `json:"finished_at,omitempty"`
	ErrorMessage *string                 `json:"error_message,omitempty"`
	Metadata     json.RawMessage         `json:"metadata,omitempty"`
	Evidence     *SourceEvidenceResponse `json:"evidence,omitempty"`
	CreatedAt    time.Time               `json:"created_at"`
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
		Evidence:     sourceEvidenceFromDomain(j.Evidence),
		CreatedAt:    j.CreatedAt,
	}
}

// JobRunRetryResponse is the public response for manual job retries.
type JobRunRetryResponse struct {
	OriginalJobRunID uuid.UUID  `json:"original_job_run_id"`
	TaskType         string     `json:"task_type"`
	Status           string     `json:"status"`
	WorkspaceID      uuid.UUID  `json:"workspace_id"`
	ExportID         *uuid.UUID `json:"export_id,omitempty"`
}

// JobRunRetryFromService maps service.JobRunRetryResult to JobRunRetryResponse.
func JobRunRetryFromService(result service.JobRunRetryResult) JobRunRetryResponse {
	return JobRunRetryResponse{
		OriginalJobRunID: result.OriginalJobRunID,
		TaskType:         result.TaskType,
		Status:           result.Status,
		WorkspaceID:      result.WorkspaceID,
		ExportID:         result.ExportID,
	}
}

// SyncTriggerResponse is the public response for manual sync triggers.
type SyncTriggerResponse struct {
	TaskType    string    `json:"task_type"`
	Status      string    `json:"status"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	CabinetID   uuid.UUID `json:"cabinet_id"`
	JobRunID    uuid.UUID `json:"job_run_id"`
}

// WorkspaceTaskTriggerResponse is the public response for workspace-scoped task triggers.
type WorkspaceTaskTriggerResponse struct {
	TaskType    string    `json:"task_type"`
	Status      string    `json:"status"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
}

// WorkspaceTaskTriggerFromService maps service.WorkspaceTaskTriggerResult to WorkspaceTaskTriggerResponse.
func WorkspaceTaskTriggerFromService(result service.WorkspaceTaskTriggerResult) WorkspaceTaskTriggerResponse {
	return WorkspaceTaskTriggerResponse{
		TaskType:    result.TaskType,
		Status:      result.Status,
		WorkspaceID: result.WorkspaceID,
	}
}
