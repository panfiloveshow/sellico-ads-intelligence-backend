package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

type mockAdsReadService struct {
	overviewFn func(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error)
}

func (m *mockAdsReadService) Overview(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error) {
	return m.overviewFn(ctx, workspaceID, dateFrom, dateTo, filter...)
}

func (m *mockAdsReadService) DataHealth(context.Context, uuid.UUID, time.Time, time.Time, service.OverviewFilter) (domain.AdsDataStatus, error) {
	return domain.AdsDataStatus{}, nil
}

func (m *mockAdsReadService) ListProductSummaries(context.Context, uuid.UUID, time.Time, time.Time, service.ProductSummaryFilter) ([]domain.ProductAdsSummary, error) {
	return nil, nil
}

func (m *mockAdsReadService) GetProductSummary(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time) (*domain.ProductAdsSummary, error) {
	return nil, nil
}

func (m *mockAdsReadService) ListCampaignSummaries(context.Context, uuid.UUID, time.Time, time.Time, service.CampaignSummaryFilter) ([]domain.CampaignPerformanceSummary, error) {
	return nil, nil
}

func (m *mockAdsReadService) GetCampaignSummary(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time) (*domain.CampaignPerformanceSummary, error) {
	return nil, nil
}

func (m *mockAdsReadService) ListQuerySummaries(context.Context, uuid.UUID, time.Time, time.Time, service.QuerySummaryFilter) ([]domain.QueryPerformanceSummary, error) {
	return nil, nil
}

func (m *mockAdsReadService) GetQuerySummary(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time) (*domain.QueryPerformanceSummary, error) {
	return nil, nil
}

func (m *mockAdsReadService) DebugNormQuery(context.Context, uuid.UUID, time.Time, time.Time, service.QuerySummaryFilter) (*service.NormQueryDebugReport, error) {
	return nil, nil
}

type mockAdsRecommendationLister struct {
	listFn func(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error)
}

func (m *mockAdsRecommendationLister) List(ctx context.Context, workspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
	return m.listFn(ctx, workspaceID, filter, limit, offset)
}

type mockAdsClientReportBuilder struct {
	buildFn func(reportDate, dateFrom, dateTo time.Time, overview domain.AdsOverview, recommendations []domain.Recommendation) string
}

func (m *mockAdsClientReportBuilder) BuildAgencyClientReport(reportDate, dateFrom, dateTo time.Time, overview domain.AdsOverview, recommendations []domain.Recommendation) string {
	return m.buildFn(reportDate, dateFrom, dateTo, overview, recommendations)
}

func TestAdsReadHandler_ClientAuditReportBuildsFromOverviewAndActiveRecommendations(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	overview := domain.AdsOverview{
		DataStatus: domain.AdsDataStatus{State: "ready", Reason: "fresh WB stats"},
		Attention:  []domain.AttentionItem{{Title: "Бюджет скоро закончится", Severity: domain.SeverityHigh}},
		Totals: domain.AdsOverviewTotals{
			Products:        12,
			Campaigns:       5,
			ActiveCampaigns: 3,
			Queries:         44,
		},
	}
	recommendations := []domain.Recommendation{
		{Title: "Снизить дорогой кластер", Status: domain.RecommendationStatusActive, SellerCabinetID: &cabinetID},
		{Title: "Чужой кабинет", Status: domain.RecommendationStatusActive, SellerCabinetID: uuidPtr(uuid.New())},
		{Title: "Неопределенный кабинет", Status: domain.RecommendationStatusActive},
	}

	adsSvc := &mockAdsReadService{
		overviewFn: func(_ context.Context, gotWorkspaceID uuid.UUID, dateFrom, dateTo time.Time, filter ...service.OverviewFilter) (*domain.AdsOverview, error) {
			assert.Equal(t, workspaceID, gotWorkspaceID)
			assert.Equal(t, "2026-04-29", dateFrom.Format("2006-01-02"))
			assert.Equal(t, "2026-05-28", dateTo.Format("2006-01-02"))
			require.Len(t, filter, 1)
			require.NotNil(t, filter[0].SellerCabinetID)
			assert.Equal(t, cabinetID, *filter[0].SellerCabinetID)
			return &overview, nil
		},
	}
	recSvc := &mockAdsRecommendationLister{
		listFn: func(_ context.Context, gotWorkspaceID uuid.UUID, filter service.RecommendationListFilter, limit, offset int32) ([]domain.Recommendation, error) {
			assert.Equal(t, workspaceID, gotWorkspaceID)
			assert.Equal(t, domain.RecommendationStatusActive, filter.Status)
			assert.Equal(t, int32(1000), limit)
			assert.Equal(t, int32(0), offset)
			return recommendations, nil
		},
	}
	reportBuilder := &mockAdsClientReportBuilder{
		buildFn: func(reportDate, dateFrom, dateTo time.Time, gotOverview domain.AdsOverview, gotRecommendations []domain.Recommendation) string {
			assert.False(t, reportDate.IsZero())
			assert.Equal(t, "2026-04-29", dateFrom.Format("2006-01-02"))
			assert.Equal(t, "2026-05-28", dateTo.Format("2006-01-02"))
			assert.Equal(t, overview.Totals.Campaigns, gotOverview.Totals.Campaigns)
			require.Len(t, gotRecommendations, 1)
			assert.Equal(t, "Снизить дорогой кластер", gotRecommendations[0].Title)
			return "Client WB Ads Audit\n- real report"
		},
	}

	h := NewAdsReadHandler(adsSvc).WithClientReports(reportBuilder, recSvc)
	req := httptest.NewRequest(http.MethodGet, "/ads/reports/client-audit?date_from=2026-04-29&date_to=2026-05-28&seller_cabinet_id="+cabinetID.String(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	rec := httptest.NewRecorder()

	h.ClientAuditReport(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	body := decodeClientAuditReport(t, resp.Data)
	assert.Equal(t, "client_wb_ads_audit", body.ReportType)
	assert.Equal(t, "2026-04-29", body.DateFrom)
	assert.Equal(t, "2026-05-28", body.DateTo)
	assert.Equal(t, "Client WB Ads Audit\n- real report", body.Report)
	assert.Equal(t, "ready", body.DataStatus.State)
	assert.Equal(t, 1, body.Recommendations)
	assert.Equal(t, 1, body.AttentionItems)
	assert.Equal(t, 3, body.ActiveCampaigns)
	assert.Equal(t, 5, body.Campaigns)
	assert.Equal(t, 12, body.Products)
	assert.Equal(t, 44, body.Queries)
}

func TestFilterRecommendationsBySellerCabinetKeepsAllWhenNoCabinetFilter(t *testing.T) {
	cabinetID := uuid.New()
	items := []domain.Recommendation{
		{Title: "Cabinet scoped", SellerCabinetID: &cabinetID},
		{Title: "Workspace scoped"},
	}

	filtered := filterRecommendationsBySellerCabinet(items, nil)

	assert.Equal(t, items, filtered)
}

func TestAdsReadHandler_ClientAuditReportRequiresConfiguredBuilder(t *testing.T) {
	h := NewAdsReadHandler(&mockAdsReadService{})
	req := httptest.NewRequest(http.MethodGet, "/ads/reports/client-audit", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, uuid.New()))
	rec := httptest.NewRecorder()

	h.ClientAuditReport(rec, req)

	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}

func decodeClientAuditReport(t *testing.T, data interface{}) dto.AdsClientAuditReportResponse {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var report dto.AdsClientAuditReportResponse
	require.NoError(t, json.Unmarshal(raw, &report))
	return report
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	return &id
}
