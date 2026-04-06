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

// SellerCabinetFromDomain maps domain.SellerCabinet to SellerCabinetResponse.
func SellerCabinetFromDomain(sc domain.SellerCabinet) SellerCabinetResponse {
	publicID := sc.ID.String()
	if sc.ExternalIntegrationID != nil && *sc.ExternalIntegrationID != "" {
		publicID = *sc.ExternalIntegrationID
	}

	marketplace := "WildBerries"
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

func sellerCabinetAutoSyncFromDomain(sync *domain.SellerCabinetAutoSyncSummary) *SellerCabinetAutoSyncResponse {
	if sync == nil {
		return nil
	}
	return &SellerCabinetAutoSyncResponse{
		JobRunID:       sync.JobRunID,
		Status:         sync.Status,
		ResultState:    sync.ResultState,
		FreshnessState: sync.FreshnessState,
		FinishedAt:     sync.FinishedAt,
		Cabinets:       sync.Cabinets,
		Campaigns:      sync.Campaigns,
		CampaignStats:  sync.CampaignStats,
		Phrases:        sync.Phrases,
		PhraseStats:    sync.PhraseStats,
		Products:       sync.Products,
		SyncIssues:     sync.SyncIssues,
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

type AdsMetricsSummaryResponse struct {
	Impressions    int64   `json:"impressions"`
	Clicks         int64   `json:"clicks"`
	Spend          int64   `json:"spend"`
	Orders         int64   `json:"orders"`
	Revenue        int64   `json:"revenue"`
	CTR            float64 `json:"ctr"`
	CPC            float64 `json:"cpc"`
	ConversionRate float64 `json:"conversion_rate"`
	DataMode       string  `json:"data_mode"`
}

type AdsMetricsDeltaResponse struct {
	Impressions    int64   `json:"impressions"`
	Clicks         int64   `json:"clicks"`
	Spend          int64   `json:"spend"`
	Orders         int64   `json:"orders"`
	Revenue        int64   `json:"revenue"`
	CTR            float64 `json:"ctr"`
	CPC            float64 `json:"cpc"`
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
	Source             string     `json:"source"`
	CapturedAt         *time.Time `json:"captured_at,omitempty"`
	FreshnessState     string     `json:"freshness_state"`
	Confidence         float64    `json:"confidence"`
	Coverage           string     `json:"coverage"`
	ConfirmedInCabinet bool       `json:"confirmed_in_cabinet"`
}

func sourceEvidenceFromDomain(evidence *domain.SourceEvidence) *SourceEvidenceResponse {
	if evidence == nil {
		return nil
	}
	return &SourceEvidenceResponse{
		Source:             evidence.Source,
		CapturedAt:         evidence.CapturedAt,
		FreshnessState:     evidence.FreshnessState,
		Confidence:         evidence.Confidence,
		Coverage:           evidence.Coverage,
		ConfirmedInCabinet: evidence.ConfirmedInCabinet,
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
	ID               uuid.UUID                 `json:"id"`
	WorkspaceID      uuid.UUID                 `json:"workspace_id"`
	SellerCabinetID  uuid.UUID                 `json:"seller_cabinet_id"`
	IntegrationID    *string                   `json:"integration_id,omitempty"`
	IntegrationName  string                    `json:"integration_name"`
	CabinetName      string                    `json:"cabinet_name"`
	WBProductID      int64                     `json:"wb_product_id"`
	Title            string                    `json:"title"`
	Brand            *string                   `json:"brand,omitempty"`
	Category         *string                   `json:"category,omitempty"`
	ImageURL         *string                   `json:"image_url,omitempty"`
	Price            *int64                    `json:"price,omitempty"`
	CampaignsCount   int                       `json:"campaigns_count"`
	QueriesCount     int                       `json:"queries_count"`
	HealthStatus     string                    `json:"health_status"`
	HealthReason     *string                   `json:"health_reason,omitempty"`
	PrimaryAction    *string                   `json:"primary_action,omitempty"`
	FreshnessState   string                    `json:"freshness_state"`
	Performance      AdsMetricsSummaryResponse `json:"performance"`
	PeriodCompare    *AdsPeriodCompareResponse `json:"period_compare,omitempty"`
	RelatedCampaigns []AdsEntityRefResponse    `json:"related_campaigns,omitempty"`
	TopQueries       []AdsEntityRefResponse    `json:"top_queries,omitempty"`
	WasteQueries     []AdsEntityRefResponse    `json:"waste_queries,omitempty"`
	WinningQueries   []AdsEntityRefResponse    `json:"winning_queries,omitempty"`
	Evidence         *SourceEvidenceResponse   `json:"evidence,omitempty"`
	DataCoverageNote *string                   `json:"data_coverage_note,omitempty"`
	CreatedAt        time.Time                 `json:"created_at"`
	UpdatedAt        time.Time                 `json:"updated_at"`
}

type CampaignPerformanceSummaryResponse struct {
	ID              uuid.UUID                 `json:"id"`
	WorkspaceID     uuid.UUID                 `json:"workspace_id"`
	SellerCabinetID uuid.UUID                 `json:"seller_cabinet_id"`
	IntegrationID   *string                   `json:"integration_id,omitempty"`
	IntegrationName string                    `json:"integration_name"`
	CabinetName     string                    `json:"cabinet_name"`
	WBCampaignID    int64                     `json:"wb_campaign_id"`
	Name            string                    `json:"name"`
	Status          string                    `json:"status"`
	CampaignType    int                       `json:"campaign_type"`
	BidType         string                    `json:"bid_type"`
	PaymentType     string                    `json:"payment_type"`
	DailyBudget     *int64                    `json:"daily_budget,omitempty"`
	LastSync        *time.Time                `json:"last_sync,omitempty"`
	HealthStatus    string                    `json:"health_status"`
	HealthReason    *string                   `json:"health_reason,omitempty"`
	PrimaryAction   *string                   `json:"primary_action,omitempty"`
	FreshnessState  string                    `json:"freshness_state"`
	Performance     AdsMetricsSummaryResponse `json:"performance"`
	PeriodCompare   *AdsPeriodCompareResponse `json:"period_compare,omitempty"`
	RelatedProducts []AdsEntityRefResponse    `json:"related_products,omitempty"`
	TopQueries      []AdsEntityRefResponse    `json:"top_queries,omitempty"`
	WasteQueries    []AdsEntityRefResponse    `json:"waste_queries,omitempty"`
	WinningQueries  []AdsEntityRefResponse    `json:"winning_queries,omitempty"`
	Evidence        *SourceEvidenceResponse   `json:"evidence,omitempty"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}

type QueryPerformanceSummaryResponse struct {
	ID              uuid.UUID                 `json:"id"`
	WorkspaceID     uuid.UUID                 `json:"workspace_id"`
	CampaignID      uuid.UUID                 `json:"campaign_id"`
	SellerCabinetID uuid.UUID                 `json:"seller_cabinet_id"`
	IntegrationID   *string                   `json:"integration_id,omitempty"`
	IntegrationName string                    `json:"integration_name"`
	CabinetName     string                    `json:"cabinet_name"`
	CampaignName    string                    `json:"campaign_name"`
	WBCampaignID    int64                     `json:"wb_campaign_id"`
	WBClusterID     int64                     `json:"wb_cluster_id"`
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
	Cabinets        int `json:"cabinets"`
	Products        int `json:"products"`
	Campaigns       int `json:"campaigns"`
	Queries         int `json:"queries"`
	ActiveCampaigns int `json:"active_campaigns"`
	AttentionItems  int `json:"attention_items"`
}

type AdsOverviewResponse struct {
	LastAutoSync       *SellerCabinetAutoSyncResponse       `json:"last_auto_sync,omitempty"`
	PerformanceCompare *AdsPeriodCompareResponse            `json:"performance_compare,omitempty"`
	Evidence           *SourceEvidenceResponse              `json:"evidence,omitempty"`
	Cabinets           []CabinetSummaryResponse             `json:"cabinets"`
	Attention          []AttentionItemResponse              `json:"attention"`
	TopProducts        []ProductAdsSummaryResponse          `json:"top_products"`
	TopCampaigns       []CampaignPerformanceSummaryResponse `json:"top_campaigns"`
	TopQueries         []QueryPerformanceSummaryResponse    `json:"top_queries"`
	Totals             AdsOverviewTotalsResponse            `json:"totals"`
}

func AdsOverviewFromDomain(overview domain.AdsOverview) AdsOverviewResponse {
	return AdsOverviewResponse{
		LastAutoSync:       sellerCabinetAutoSyncFromDomain(overview.LastAutoSync),
		PerformanceCompare: adsPeriodCompareFromDomain(overview.PerformanceCompare),
		Evidence:           sourceEvidenceFromDomain(overview.Evidence),
		Cabinets:           cabinetSummariesFromDomain(overview.Cabinets),
		Attention:          attentionItemsFromDomain(overview.Attention),
		TopProducts:        productSummariesFromDomain(overview.TopProducts),
		TopCampaigns:       campaignSummariesFromDomain(overview.TopCampaigns),
		TopQueries:         querySummariesFromDomain(overview.TopQueries),
		Totals: AdsOverviewTotalsResponse{
			Cabinets:        overview.Totals.Cabinets,
			Products:        overview.Totals.Products,
			Campaigns:       overview.Totals.Campaigns,
			Queries:         overview.Totals.Queries,
			ActiveCampaigns: overview.Totals.ActiveCampaigns,
			AttentionItems:  overview.Totals.AttentionItems,
		},
	}
}

func ProductAdsSummaryFromDomain(product domain.ProductAdsSummary) ProductAdsSummaryResponse {
	return ProductAdsSummaryResponse{
		ID:               product.ID,
		WorkspaceID:      product.WorkspaceID,
		SellerCabinetID:  product.SellerCabinetID,
		IntegrationID:    product.IntegrationID,
		IntegrationName:  product.IntegrationName,
		CabinetName:      product.CabinetName,
		WBProductID:      product.WBProductID,
		Title:            product.Title,
		Brand:            product.Brand,
		Category:         product.Category,
		ImageURL:         product.ImageURL,
		Price:            product.Price,
		CampaignsCount:   product.CampaignsCount,
		QueriesCount:     product.QueriesCount,
		HealthStatus:     product.HealthStatus,
		HealthReason:     product.HealthReason,
		PrimaryAction:    product.PrimaryAction,
		FreshnessState:   product.FreshnessState,
		Performance:      adsMetricsFromDomain(product.Performance),
		PeriodCompare:    adsPeriodCompareFromDomain(product.PeriodCompare),
		RelatedCampaigns: entityRefsFromDomain(product.RelatedCampaigns),
		TopQueries:       entityRefsFromDomain(product.TopQueries),
		WasteQueries:     entityRefsFromDomain(product.WasteQueries),
		WinningQueries:   entityRefsFromDomain(product.WinningQueries),
		Evidence:         sourceEvidenceFromDomain(product.Evidence),
		DataCoverageNote: product.DataCoverageNote,
		CreatedAt:        product.CreatedAt,
		UpdatedAt:        product.UpdatedAt,
	}
}

func CampaignPerformanceSummaryFromDomain(campaign domain.CampaignPerformanceSummary) CampaignPerformanceSummaryResponse {
	return CampaignPerformanceSummaryResponse{
		ID:              campaign.ID,
		WorkspaceID:     campaign.WorkspaceID,
		SellerCabinetID: campaign.SellerCabinetID,
		IntegrationID:   campaign.IntegrationID,
		IntegrationName: campaign.IntegrationName,
		CabinetName:     campaign.CabinetName,
		WBCampaignID:    campaign.WBCampaignID,
		Name:            campaign.Name,
		Status:          campaign.Status,
		CampaignType:    campaign.CampaignType,
		BidType:         campaign.BidType,
		PaymentType:     campaign.PaymentType,
		DailyBudget:     campaign.DailyBudget,
		LastSync:        campaign.LastSync,
		HealthStatus:    campaign.HealthStatus,
		HealthReason:    campaign.HealthReason,
		PrimaryAction:   campaign.PrimaryAction,
		FreshnessState:  campaign.FreshnessState,
		Performance:     adsMetricsFromDomain(campaign.Performance),
		PeriodCompare:   adsPeriodCompareFromDomain(campaign.PeriodCompare),
		RelatedProducts: entityRefsFromDomain(campaign.RelatedProducts),
		TopQueries:      entityRefsFromDomain(campaign.TopQueries),
		WasteQueries:    entityRefsFromDomain(campaign.WasteQueries),
		WinningQueries:  entityRefsFromDomain(campaign.WinningQueries),
		Evidence:        sourceEvidenceFromDomain(campaign.Evidence),
		CreatedAt:       campaign.CreatedAt,
		UpdatedAt:       campaign.UpdatedAt,
	}
}

func QueryPerformanceSummaryFromDomain(query domain.QueryPerformanceSummary) QueryPerformanceSummaryResponse {
	return QueryPerformanceSummaryResponse{
		ID:              query.ID,
		WorkspaceID:     query.WorkspaceID,
		CampaignID:      query.CampaignID,
		SellerCabinetID: query.SellerCabinetID,
		IntegrationID:   query.IntegrationID,
		IntegrationName: query.IntegrationName,
		CabinetName:     query.CabinetName,
		CampaignName:    query.CampaignName,
		WBCampaignID:    query.WBCampaignID,
		WBClusterID:     query.WBClusterID,
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
		CTR:            metrics.CTR,
		CPC:            metrics.CPC,
		ConversionRate: metrics.ConversionRate,
		DataMode:       metrics.DataMode,
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
	ID              uuid.UUID               `json:"id"`
	WorkspaceID     uuid.UUID               `json:"workspace_id"`
	CampaignID      *uuid.UUID              `json:"campaign_id,omitempty"`
	PhraseID        *uuid.UUID              `json:"phrase_id,omitempty"`
	ProductID       *uuid.UUID              `json:"product_id,omitempty"`
	SellerCabinetID *uuid.UUID              `json:"seller_cabinet_id,omitempty"`
	Title           string                  `json:"title"`
	Description     string                  `json:"description"`
	Type            string                  `json:"type"`
	Severity        string                  `json:"severity"`
	Confidence      float64                 `json:"confidence"`
	SourceMetrics   json.RawMessage         `json:"source_metrics"`
	NextAction      *string                 `json:"next_action,omitempty"`
	Status          string                  `json:"status"`
	Evidence        *SourceEvidenceResponse `json:"evidence,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}

// RecommendationFromDomain maps domain.Recommendation to RecommendationResponse.
func RecommendationFromDomain(r domain.Recommendation) RecommendationResponse {
	return RecommendationResponse{
		ID:              r.ID,
		WorkspaceID:     r.WorkspaceID,
		CampaignID:      r.CampaignID,
		PhraseID:        r.PhraseID,
		ProductID:       r.ProductID,
		SellerCabinetID: r.SellerCabinetID,
		Title:           r.Title,
		Description:     r.Description,
		Type:            r.Type,
		Severity:        r.Severity,
		Confidence:      r.Confidence,
		SourceMetrics:   r.SourceMetrics,
		NextAction:      r.NextAction,
		Status:          r.Status,
		Evidence:        sourceEvidenceFromDomain(r.Evidence),
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
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
	Source             string     `json:"source"`
	CapturedAt         *time.Time `json:"captured_at,omitempty"`
	FreshnessState     string     `json:"freshness_state"`
	Confidence         float64    `json:"confidence"`
	Coverage           string     `json:"coverage"`
	ConfirmedInCabinet bool       `json:"confirmed_in_cabinet"`
}

func ExtensionWidgetDataStatusFromService(status service.ExtensionWidgetDataStatus) ExtensionWidgetDataStatusResponse {
	return ExtensionWidgetDataStatusResponse{
		Source:             status.Source,
		CapturedAt:         status.CapturedAt,
		FreshnessState:     status.FreshnessState,
		Confidence:         status.Confidence,
		Coverage:           status.Coverage,
		ConfirmedInCabinet: status.ConfirmedInCabinet,
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
		DataStatus:       ExtensionWidgetDataStatusFromService(widget.DataStatus),
		Recommendations:  recommendations,
	}
}

type ExtensionProductWidgetResponse struct {
	Product         ProductResponse                         `json:"product"`
	Positions       []PositionResponse                      `json:"positions"`
	LivePositions   []ExtensionLivePositionSnapshotResponse `json:"live_positions"`
	UISignals       []ExtensionUISignalResponse             `json:"ui_signals"`
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
		DataStatus:      ExtensionWidgetDataStatusFromService(widget.DataStatus),
		Recommendations: recommendations,
	}
}

type ExtensionCampaignWidgetResponse struct {
	Campaign        CampaignResponse                   `json:"campaign"`
	Stats           []CampaignStatResponse             `json:"stats"`
	Phrases         []PhraseResponse                   `json:"phrases"`
	LiveBids        []ExtensionLiveBidSnapshotResponse `json:"live_bids"`
	UISignals       []ExtensionUISignalResponse        `json:"ui_signals"`
	DataStatus      ExtensionWidgetDataStatusResponse  `json:"data_status"`
	Recommendations []RecommendationResponse           `json:"recommendations"`
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
