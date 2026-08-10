package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// --- fakes ---

type fakeOzonService struct {
	resolveFn         func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error)
	listCampaignsFn   func(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCampaignWithStats, int64, error)
	getCampaignFn     func(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.OzonCampaign, []domain.OzonCampaignProduct, error)
	listStatsFn       func(ctx context.Context, workspaceID, campaignID uuid.UUID, from, to time.Time) ([]domain.OzonCampaignStat, error)
	listPricesFn      func(ctx context.Context, workspaceID, cabinetID uuid.UUID, search string, limit, offset int32) ([]domain.OzonProductPrice, int64, error)
	listSearchQueryFn func(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, search string, days int, limit, offset int32) ([]domain.OzonSearchQueryStat, int64, error)
	cpoOverviewFn     func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonCPOOverview, error)
}

func (f *fakeOzonService) ResolveOzonCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error) {
	if f.resolveFn == nil {
		return &domain.SellerCabinet{}, nil
	}
	return f.resolveFn(ctx, workspaceID, cabinetID)
}

func (f *fakeOzonService) ListCampaignsWithStats(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCampaignWithStats, int64, error) {
	if f.listCampaignsFn == nil {
		return nil, 0, nil
	}
	return f.listCampaignsFn(ctx, workspaceID, cabinetID, limit, offset)
}

func (f *fakeOzonService) GetCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.OzonCampaign, []domain.OzonCampaignProduct, error) {
	if f.getCampaignFn == nil {
		return &domain.OzonCampaign{}, nil, nil
	}
	return f.getCampaignFn(ctx, workspaceID, campaignID)
}

func (f *fakeOzonService) ListCampaignStats(ctx context.Context, workspaceID, campaignID uuid.UUID, from, to time.Time) ([]domain.OzonCampaignStat, error) {
	if f.listStatsFn == nil {
		return nil, nil
	}
	return f.listStatsFn(ctx, workspaceID, campaignID, from, to)
}

func (f *fakeOzonService) ListPrices(ctx context.Context, workspaceID, cabinetID uuid.UUID, search string, limit, offset int32) ([]domain.OzonProductPrice, int64, error) {
	if f.listPricesFn == nil {
		return nil, 0, nil
	}
	return f.listPricesFn(ctx, workspaceID, cabinetID, search, limit, offset)
}

func (f *fakeOzonService) ListSearchQueries(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, search string, days int, limit, offset int32) ([]domain.OzonSearchQueryStat, int64, error) {
	if f.listSearchQueryFn == nil {
		return nil, 0, nil
	}
	return f.listSearchQueryFn(ctx, workspaceID, cabinetID, sku, search, days, limit, offset)
}

func (f *fakeOzonService) GetCPOOverview(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonCPOOverview, error) {
	if f.cpoOverviewFn == nil {
		return &domain.OzonCPOOverview{}, nil
	}
	return f.cpoOverviewFn(ctx, workspaceID, cabinetID)
}

type fakeOzonActions struct {
	setBidsFn        func(ctx context.Context, workspaceID, campaignID uuid.UUID, items []service.OzonBidInput) ([]domain.OzonBidChange, error)
	listBidChangesFn func(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaignID *uuid.UUID, limit, offset int32) ([]domain.OzonBidChange, int64, error)
	competitiveFn    func(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]ozon.CompetitiveBid, error)
	activateFn       func(ctx context.Context, workspaceID, campaignID uuid.UUID) error
	deactivateFn     func(ctx context.Context, workspaceID, campaignID uuid.UUID) error
	updateBudgetFn   func(ctx context.Context, workspaceID, campaignID uuid.UUID, dailyRub, weeklyRub *int64) error
	listCPOFn        func(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCPOProduct, int64, error)
	enableCPOFn      func(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error
	disableCPOFn     func(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error
	setCPOBidsFn     func(ctx context.Context, workspaceID, cabinetID uuid.UUID, bids []service.OzonCPOBidInput) error
	setCPORateFn     func(ctx context.Context, workspaceID, cabinetID uuid.UUID, ratePct int) error
	setCPOActiveFn   func(ctx context.Context, workspaceID, cabinetID uuid.UUID, active bool) error
	bidLimitsFn      func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (json.RawMessage, error)
}

func (f *fakeOzonActions) ActivateCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	if f.activateFn == nil {
		return nil
	}
	return f.activateFn(ctx, workspaceID, campaignID)
}
func (f *fakeOzonActions) DeactivateCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	if f.deactivateFn == nil {
		return nil
	}
	return f.deactivateFn(ctx, workspaceID, campaignID)
}
func (f *fakeOzonActions) UpdateBudget(ctx context.Context, workspaceID, campaignID uuid.UUID, dailyRub, weeklyRub *int64) error {
	if f.updateBudgetFn == nil {
		return nil
	}
	return f.updateBudgetFn(ctx, workspaceID, campaignID, dailyRub, weeklyRub)
}

func (f *fakeOzonActions) SetProductBids(ctx context.Context, workspaceID, campaignID uuid.UUID, items []service.OzonBidInput) ([]domain.OzonBidChange, error) {
	if f.setBidsFn == nil {
		return nil, nil
	}
	return f.setBidsFn(ctx, workspaceID, campaignID, items)
}

func (f *fakeOzonActions) GetCompetitiveBids(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]ozon.CompetitiveBid, error) {
	if f.competitiveFn == nil {
		return nil, nil
	}
	return f.competitiveFn(ctx, workspaceID, campaignID)
}

func (f *fakeOzonActions) ListBidChanges(ctx context.Context, workspaceID, cabinetID uuid.UUID, campaignID *uuid.UUID, limit, offset int32) ([]domain.OzonBidChange, int64, error) {
	if f.listBidChangesFn == nil {
		return nil, 0, nil
	}
	return f.listBidChangesFn(ctx, workspaceID, cabinetID, campaignID, limit, offset)
}

func (f *fakeOzonActions) ListCPOProducts(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCPOProduct, int64, error) {
	if f.listCPOFn == nil {
		return nil, 0, nil
	}
	return f.listCPOFn(ctx, workspaceID, cabinetID, limit, offset)
}
func (f *fakeOzonActions) EnableCPO(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error {
	if f.enableCPOFn == nil {
		return nil
	}
	return f.enableCPOFn(ctx, workspaceID, cabinetID, skus)
}
func (f *fakeOzonActions) DisableCPO(ctx context.Context, workspaceID, cabinetID uuid.UUID, skus []int64) error {
	if f.disableCPOFn == nil {
		return nil
	}
	return f.disableCPOFn(ctx, workspaceID, cabinetID, skus)
}
func (f *fakeOzonActions) SetCPOBids(ctx context.Context, workspaceID, cabinetID uuid.UUID, bids []service.OzonCPOBidInput) error {
	if f.setCPOBidsFn == nil {
		return nil
	}
	return f.setCPOBidsFn(ctx, workspaceID, cabinetID, bids)
}
func (f *fakeOzonActions) SetCPORate(ctx context.Context, workspaceID, cabinetID uuid.UUID, ratePct int) error {
	if f.setCPORateFn == nil {
		return nil
	}
	return f.setCPORateFn(ctx, workspaceID, cabinetID, ratePct)
}
func (f *fakeOzonActions) SetCPOActive(ctx context.Context, workspaceID, cabinetID uuid.UUID, active bool) error {
	if f.setCPOActiveFn == nil {
		return nil
	}
	return f.setCPOActiveFn(ctx, workspaceID, cabinetID, active)
}
func (f *fakeOzonActions) GetBidLimits(ctx context.Context, workspaceID, cabinetID uuid.UUID) (json.RawMessage, error) {
	if f.bidLimitsFn == nil {
		return json.RawMessage(`{}`), nil
	}
	return f.bidLimitsFn(ctx, workspaceID, cabinetID)
}

type fakeOzonAI struct {
	checkManualFn   func(ctx context.Context, workspaceID, cabinetID uuid.UUID) error
	approveFn       func(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error)
	rejectFn        func(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error)
	listRunsFn      func(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.AIRun, int64, error)
	listDecisionsFn func(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, runID *uuid.UUID, limit, offset int32) ([]domain.AIDecision, int64, error)
	getImpactFn     func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIImpactSummary, error)
	approveBatchFn  func(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult
	rejectBatchFn   func(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult
	weeklyReportFn  func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonAIWeeklyReport, error)
	readinessFn     func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIReadiness, error)
}

func (f *fakeOzonAI) ListRuns(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.AIRun, int64, error) {
	if f.listRunsFn == nil {
		return nil, 0, nil
	}
	return f.listRunsFn(ctx, workspaceID, cabinetID, limit, offset)
}

func (f *fakeOzonAI) ListDecisions(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, runID *uuid.UUID, limit, offset int32) ([]domain.AIDecision, int64, error) {
	if f.listDecisionsFn == nil {
		return nil, 0, nil
	}
	return f.listDecisionsFn(ctx, workspaceID, cabinetID, status, runID, limit, offset)
}

func (f *fakeOzonAI) CheckManualRunAllowed(ctx context.Context, workspaceID, cabinetID uuid.UUID) error {
	if f.checkManualFn == nil {
		return nil
	}
	return f.checkManualFn(ctx, workspaceID, cabinetID)
}

func (f *fakeOzonAI) ApproveDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error) {
	if f.approveFn == nil {
		return &domain.AIDecision{}, nil
	}
	return f.approveFn(ctx, workspaceID, decisionID, userID)
}

func (f *fakeOzonAI) RejectDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error) {
	if f.rejectFn == nil {
		return &domain.AIDecision{}, nil
	}
	return f.rejectFn(ctx, workspaceID, decisionID, userID)
}

func (f *fakeOzonAI) GetImpact(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIImpactSummary, error) {
	if f.getImpactFn == nil {
		return &domain.AIImpactSummary{}, nil
	}
	return f.getImpactFn(ctx, workspaceID, cabinetID)
}

func (f *fakeOzonAI) ApproveDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult {
	if f.approveBatchFn == nil {
		return nil
	}
	return f.approveBatchFn(ctx, workspaceID, ids, userID)
}

func (f *fakeOzonAI) RejectDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult {
	if f.rejectBatchFn == nil {
		return nil
	}
	return f.rejectBatchFn(ctx, workspaceID, ids, userID)
}

func (f *fakeOzonAI) GetLatestWeeklyReport(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonAIWeeklyReport, error) {
	if f.weeklyReportFn == nil {
		return nil, nil
	}
	return f.weeklyReportFn(ctx, workspaceID, cabinetID)
}

func (f *fakeOzonAI) GetReadiness(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIReadiness, error) {
	if f.readinessFn == nil {
		return &domain.AIReadiness{}, nil
	}
	return f.readinessFn(ctx, workspaceID, cabinetID)
}

type fakeOzonRepricer struct {
	applyManualFn      func(ctx context.Context, workspaceID, cabinetID uuid.UUID, items []service.OzonManualPriceInput, userID uuid.UUID) ([]domain.OzonPriceChange, error)
	resolveFn          func(ctx context.Context, workspaceID, cabinetID uuid.UUID) error
	listSchedulesFn    func(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, limit, offset int32) ([]domain.OzonPriceScheduleEntry, int64, error)
	heatmapFn          func(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku int64, metric string) (*domain.OrdersHeatmap, error)
	listPriceChangesFn func(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, limit, offset int32) ([]domain.OzonPriceChange, int64, error)
	rollbackFn         func(ctx context.Context, workspaceID, changeID uuid.UUID) (*domain.OzonPriceChange, error)
	createScheduleFn   func(ctx context.Context, workspaceID, cabinetID uuid.UUID, in domain.OzonPriceScheduleInput) (*domain.OzonPriceScheduleEntry, error)
	cancelScheduleFn   func(ctx context.Context, workspaceID, entryID uuid.UUID) error
	setPauseFn         func(ctx context.Context, workspaceID, cabinetID uuid.UUID, hours int) (*time.Time, error)
	healthFn           func(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonRepricerHealth, error)
}

func (f *fakeOzonRepricer) ListPriceChanges(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, limit, offset int32) ([]domain.OzonPriceChange, int64, error) {
	if f.listPriceChangesFn == nil {
		return nil, 0, nil
	}
	return f.listPriceChangesFn(ctx, workspaceID, cabinetID, sku, limit, offset)
}

func (f *fakeOzonRepricer) Rollback(ctx context.Context, workspaceID, changeID uuid.UUID) (*domain.OzonPriceChange, error) {
	if f.rollbackFn == nil {
		return &domain.OzonPriceChange{}, nil
	}
	return f.rollbackFn(ctx, workspaceID, changeID)
}

func (f *fakeOzonRepricer) ApplyManual(ctx context.Context, workspaceID, cabinetID uuid.UUID, items []service.OzonManualPriceInput, userID uuid.UUID) ([]domain.OzonPriceChange, error) {
	if f.applyManualFn == nil {
		return nil, nil
	}
	return f.applyManualFn(ctx, workspaceID, cabinetID, items, userID)
}

func (f *fakeOzonRepricer) ResolveWorkspaceCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) error {
	if f.resolveFn == nil {
		return nil
	}
	return f.resolveFn(ctx, workspaceID, cabinetID)
}

func (f *fakeOzonRepricer) CreateSchedule(ctx context.Context, workspaceID, cabinetID uuid.UUID, in domain.OzonPriceScheduleInput) (*domain.OzonPriceScheduleEntry, error) {
	if f.createScheduleFn == nil {
		return &domain.OzonPriceScheduleEntry{}, nil
	}
	return f.createScheduleFn(ctx, workspaceID, cabinetID, in)
}

func (f *fakeOzonRepricer) ListSchedules(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, limit, offset int32) ([]domain.OzonPriceScheduleEntry, int64, error) {
	if f.listSchedulesFn == nil {
		return nil, 0, nil
	}
	return f.listSchedulesFn(ctx, workspaceID, cabinetID, status, limit, offset)
}

func (f *fakeOzonRepricer) CancelSchedule(ctx context.Context, workspaceID, entryID uuid.UUID) error {
	if f.cancelScheduleFn == nil {
		return nil
	}
	return f.cancelScheduleFn(ctx, workspaceID, entryID)
}

func (f *fakeOzonRepricer) SetPause(ctx context.Context, workspaceID, cabinetID uuid.UUID, hours int) (*time.Time, error) {
	if f.setPauseFn == nil {
		return nil, nil
	}
	return f.setPauseFn(ctx, workspaceID, cabinetID, hours)
}

func (f *fakeOzonRepricer) Health(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonRepricerHealth, error) {
	if f.healthFn == nil {
		return &domain.OzonRepricerHealth{}, nil
	}
	return f.healthFn(ctx, workspaceID, cabinetID)
}

func (f *fakeOzonRepricer) OrdersHeatmap(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku int64, metric string) (*domain.OrdersHeatmap, error) {
	if f.heatmapFn == nil {
		return &domain.OrdersHeatmap{}, nil
	}
	return f.heatmapFn(ctx, workspaceID, cabinetID, sku, metric)
}

// --- request helpers ---

func ozonReq(t *testing.T, method, target, body string, workspaceID uuid.UUID) *http.Request {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
	return req
}

func withURLParam(req *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func firstErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	resp := decodeEnvelope(t, rec)
	require.NotEmpty(t, resp.Errors, "expected error envelope, body: %s", rec.Body.String())
	return resp.Errors[0].Code
}

// --- phase 1: reads ---

func TestOzonListCampaigns_Validation(t *testing.T) {
	workspaceID := uuid.New()
	tests := []struct {
		name         string
		target       string
		useWorkspace bool
	}{
		{name: "missing cabinet_id", target: "/ozon/campaigns", useWorkspace: true},
		{name: "malformed cabinet_id", target: "/ozon/campaigns?cabinet_id=not-a-uuid", useWorkspace: true},
		{name: "nil cabinet_id", target: "/ozon/campaigns?cabinet_id=" + uuid.Nil.String(), useWorkspace: true},
		{name: "missing workspace context", target: "/ozon/campaigns?cabinet_id=" + uuid.New().String(), useWorkspace: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := NewOzonHandler(&fakeOzonService{
				listCampaignsFn: func(context.Context, uuid.UUID, uuid.UUID, int32, int32) ([]domain.OzonCampaignWithStats, int64, error) {
					called = true
					return nil, 0, nil
				},
			}, nil, nil)

			var req *http.Request
			if tc.useWorkspace {
				req = ozonReq(t, http.MethodGet, tc.target, "", workspaceID)
			} else {
				req = httptest.NewRequest(http.MethodGet, tc.target, nil)
			}
			rec := httptest.NewRecorder()
			h.ListCampaigns(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, "VALIDATION_ERROR", firstErrorCode(t, rec))
			assert.False(t, called, "service must not be called on validation failure")
		})
	}
}

func TestOzonListCampaigns_PaginationMeta(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{
		listCampaignsFn: func(_ context.Context, wsID, cabID uuid.UUID, limit, offset int32) ([]domain.OzonCampaignWithStats, int64, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, cabinetID, cabID)
			assert.Equal(t, int32(10), limit)
			assert.Equal(t, int32(20), offset) // page 3, per_page 10
			return []domain.OzonCampaignWithStats{{}, {}}, 42, nil
		},
	}, nil, nil)

	req := ozonReq(t, http.MethodGet, "/ozon/campaigns?cabinet_id="+cabinetID.String()+"&page=3&per_page=10", "", workspaceID)
	rec := httptest.NewRecorder()
	h.ListCampaigns(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.NotNil(t, resp.Meta)
	assert.Equal(t, 3, resp.Meta.Page)
	assert.Equal(t, 10, resp.Meta.PerPage)
	assert.Equal(t, int64(42), resp.Meta.Total)
}

func TestOzonListCampaigns_UpstreamErrorEnvelope(t *testing.T) {
	workspaceID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{
		listCampaignsFn: func(context.Context, uuid.UUID, uuid.UUID, int32, int32) ([]domain.OzonCampaignWithStats, int64, error) {
			return nil, 0, apperror.New(apperror.ErrOzonAPIError, "ozon responded 500")
		},
	}, nil, nil)

	req := ozonReq(t, http.MethodGet, "/ozon/campaigns?cabinet_id="+uuid.New().String(), "", workspaceID)
	rec := httptest.NewRecorder()
	h.ListCampaigns(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, "OZON_API_ERROR", firstErrorCode(t, rec))
}

func TestOzonListCampaigns_UnknownErrorMapsToInternal(t *testing.T) {
	workspaceID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{
		listCampaignsFn: func(context.Context, uuid.UUID, uuid.UUID, int32, int32) ([]domain.OzonCampaignWithStats, int64, error) {
			return nil, 0, assert.AnError
		},
	}, nil, nil)

	req := ozonReq(t, http.MethodGet, "/ozon/campaigns?cabinet_id="+uuid.New().String(), "", workspaceID)
	rec := httptest.NewRecorder()
	h.ListCampaigns(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "INTERNAL_ERROR", firstErrorCode(t, rec))
}

func TestOzonCampaignStats_DateValidation(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	tests := []struct {
		name     string
		query    string
		wantCode int
	}{
		{name: "defaults ok", query: "", wantCode: http.StatusOK},
		{name: "bad from format", query: "?from=07-08-2026", wantCode: http.StatusBadRequest},
		{name: "bad to format", query: "?to=notadate", wantCode: http.StatusBadRequest},
		{name: "from after to", query: "?from=2026-08-05&to=2026-08-01", wantCode: http.StatusBadRequest},
		{name: "explicit range ok", query: "?from=2026-08-01&to=2026-08-05", wantCode: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewOzonHandler(&fakeOzonService{}, nil, nil)
			req := ozonReq(t, http.MethodGet, "/ozon/campaigns/"+campaignID.String()+"/stats"+tc.query, "", workspaceID)
			req = withURLParam(req, "id", campaignID.String())
			rec := httptest.NewRecorder()
			h.CampaignStats(rec, req)
			assert.Equal(t, tc.wantCode, rec.Code)
		})
	}
}

func TestOzonGetCampaign_InvalidID(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/campaigns/oops", "", uuid.New())
	req = withURLParam(req, "id", "oops")
	rec := httptest.NewRecorder()
	h.GetCampaign(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOzonTriggerSync(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("tenancy gate: foreign cabinet 404, nothing enqueued", func(t *testing.T) {
		enqueued := 0
		h := NewOzonHandler(&fakeOzonService{
			resolveFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.SellerCabinet, error) {
				return nil, apperror.ErrNotFound
			},
		}, nil, func(uuid.UUID) error { enqueued++; return nil })

		req := ozonReq(t, http.MethodPost, "/ozon/sync?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.TriggerSync(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "NOT_FOUND", firstErrorCode(t, rec))
		assert.Zero(t, enqueued)
	})

	t.Run("no worker configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/sync?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.TriggerSync(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "NOT_CONFIGURED", firstErrorCode(t, rec))
	})

	t.Run("success -> 202 with the cabinet id", func(t *testing.T) {
		var got uuid.UUID
		h := NewOzonHandler(&fakeOzonService{}, nil, func(id uuid.UUID) error { got = id; return nil })
		req := ozonReq(t, http.MethodPost, "/ozon/sync?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.TriggerSync(rec, req)
		assert.Equal(t, http.StatusAccepted, rec.Code)
		assert.Equal(t, cabinetID, got)
	})
}

// --- phase 2: actions ---

func TestOzonActionsNotConfigured(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil)
	workspaceID := uuid.New()

	endpoints := map[string]func(http.ResponseWriter, *http.Request){
		"ActivateCampaign": h.ActivateCampaign,
		"SetProductBids":   h.SetProductBids,
		"ListBidChanges":   h.ListBidChanges,
		"BidLimits":        h.BidLimits,
	}
	for name, fn := range endpoints {
		t.Run(name, func(t *testing.T) {
			req := ozonReq(t, http.MethodPost, "/ozon/whatever", "{}", workspaceID)
			rec := httptest.NewRecorder()
			fn(rec, req)
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
			assert.Equal(t, "NOT_CONFIGURED", firstErrorCode(t, rec))
		})
	}
}

func TestOzonListBidChanges_CampaignOnlyIsAllowed(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
		listBidChangesFn: func(_ context.Context, wsID, cabID uuid.UUID, cmpID *uuid.UUID, _, _ int32) ([]domain.OzonBidChange, int64, error) {
			assert.Equal(t, workspaceID, wsID)
			assert.Equal(t, uuid.Nil, cabID, "cabinet must stay nil when only campaign_id is given")
			if assert.NotNil(t, cmpID) {
				assert.Equal(t, campaignID, *cmpID)
			}
			return []domain.OzonBidChange{{}}, 1, nil
		},
	}, nil)

	req := ozonReq(t, http.MethodGet, "/ozon/bid-changes?campaign_id="+campaignID.String(), "", workspaceID)
	rec := httptest.NewRecorder()
	h.ListBidChanges(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.NotNil(t, resp.Meta)
	assert.Equal(t, int64(1), resp.Meta.Total)
}

func TestOzonListBidChanges_Validation(t *testing.T) {
	workspaceID := uuid.New()
	tests := []struct {
		name  string
		query string
	}{
		{name: "neither cabinet nor campaign", query: ""},
		{name: "invalid campaign_id", query: "?campaign_id=xyz"},
		{name: "invalid cabinet_id", query: "?cabinet_id=xyz"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
				listBidChangesFn: func(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, int32, int32) ([]domain.OzonBidChange, int64, error) {
					called = true
					return nil, 0, nil
				},
			}, nil)
			req := ozonReq(t, http.MethodGet, "/ozon/bid-changes"+tc.query, "", workspaceID)
			rec := httptest.NewRecorder()
			h.ListBidChanges(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.False(t, called)
		})
	}
}

func TestOzonSetProductBids(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()

	t.Run("invalid body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPut, "/ozon/campaigns/"+campaignID.String()+"/products/bids", "{not json", workspaceID)
		req = withURLParam(req, "id", campaignID.String())
		rec := httptest.NewRecorder()
		h.SetProductBids(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream ozon error -> 502 envelope", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setBidsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.OzonBidInput) ([]domain.OzonBidChange, error) {
				return nil, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodPut, "/ozon/campaigns/"+campaignID.String()+"/products/bids", `{"items":[{"sku":1,"bid_rub":10}]}`, workspaceID)
		req = withURLParam(req, "id", campaignID.String())
		rec := httptest.NewRecorder()
		h.SetProductBids(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Equal(t, "OZON_API_ERROR", firstErrorCode(t, rec))
	})
}

func TestOzonCompetitiveBids_NilBecomesEmptyArray(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
		competitiveFn: func(context.Context, uuid.UUID, uuid.UUID) ([]ozon.CompetitiveBid, error) {
			return nil, nil
		},
	}, nil)

	req := ozonReq(t, http.MethodGet, "/ozon/campaigns/"+campaignID.String()+"/bids/competitive", "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec := httptest.NewRecorder()
	h.CompetitiveBids(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`, "nil slice must serialize as [], not null")
}

// --- phase 3: AI ---

func TestOzonAINotConfigured(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/ai/runs?cabinet_id="+uuid.New().String(), "", uuid.New())
	rec := httptest.NewRecorder()
	h.AIListRuns(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "NOT_CONFIGURED", firstErrorCode(t, rec))
}

func TestOzonAIApproveDecision(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()
	decisionID := uuid.New()

	newReq := func(withUser bool) *http.Request {
		req := ozonReq(t, http.MethodPost, "/ozon/ai/decisions/"+decisionID.String()+"/approve", "", workspaceID)
		if withUser {
			req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
		}
		return withURLParam(req, "id", decisionID.String())
	}

	t.Run("missing user id -> 401", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
		rec := httptest.NewRecorder()
		h.AIApproveDecision(rec, newReq(false))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, "UNAUTHORIZED", firstErrorCode(t, rec))
	})

	t.Run("foreign decision -> 404 passthrough", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			approveFn: func(_ context.Context, wsID, decID, uID uuid.UUID) (*domain.AIDecision, error) {
				assert.Equal(t, workspaceID, wsID)
				assert.Equal(t, decisionID, decID)
				assert.Equal(t, userID, uID)
				return nil, apperror.ErrNotFound
			},
		}, nil)
		rec := httptest.NewRecorder()
		h.AIApproveDecision(rec, newReq(true))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "NOT_FOUND", firstErrorCode(t, rec))
	})

	t.Run("success -> 200 with decision", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			approveFn: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.AIDecision, error) {
				return &domain.AIDecision{ID: decisionID, Status: domain.AIDecisionStatusApproved}, nil
			},
		}, nil)
		rec := httptest.NewRecorder()
		h.AIApproveDecision(rec, newReq(true))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), decisionID.String())
	})
}

func TestOzonAIDecisionsBatch(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()
	id1 := uuid.New()
	id2 := uuid.New()

	newReq := func(body string, withUser bool) *http.Request {
		req := ozonReq(t, http.MethodPost, "/ozon/ai/decisions/approve-batch", body, workspaceID)
		if withUser {
			req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
		}
		return req
	}

	t.Run("missing user id -> 401", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
		rec := httptest.NewRecorder()
		h.AIApproveDecisionsBatch(rec, newReq(`{"ids":["`+id1.String()+`"]}`, false))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("empty ids -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
		rec := httptest.NewRecorder()
		h.AIApproveDecisionsBatch(rec, newReq(`{"ids":[]}`, true))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("invalid id -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
		rec := httptest.NewRecorder()
		h.AIApproveDecisionsBatch(rec, newReq(`{"ids":["not-a-uuid"]}`, true))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("success -> per-id results", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			approveBatchFn: func(_ context.Context, wsID uuid.UUID, ids []uuid.UUID, uID uuid.UUID) []domain.AIDecisionBatchResult {
				assert.Equal(t, workspaceID, wsID)
				assert.Equal(t, userID, uID)
				require.Len(t, ids, 2)
				return []domain.AIDecisionBatchResult{
					{ID: ids[0], OK: true},
					{ID: ids[1], OK: false, Error: "guardrail rejected"},
				}
			},
		}, nil)
		rec := httptest.NewRecorder()
		h.AIApproveDecisionsBatch(rec, newReq(`{"ids":["`+id1.String()+`","`+id2.String()+`"]}`, true))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), id1.String())
		assert.Contains(t, rec.Body.String(), "guardrail rejected")
	})
}

func TestOzonAIReadinessAndWeeklyReport(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("readiness success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			readinessFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.AIReadiness, error) {
				return &domain.AIReadiness{CurrentLevel: 1, RecommendNextLevel: true, Reason: "ok"}, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/readiness?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIReadiness(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "recommend_next_level")
	})

	t.Run("weekly report null when none", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			weeklyReportFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.OzonAIWeeklyReport, error) {
				return nil, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/weekly-report?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIWeeklyReport(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "null")
	})
}

func TestOzonAITriggerRun(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	body := `{"cabinet_id":"` + cabinetID.String() + `"}`

	t.Run("run already in flight -> 409 passthrough", func(t *testing.T) {
		enqueued := 0
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			checkManualFn: func(context.Context, uuid.UUID, uuid.UUID) error {
				return apperror.New(apperror.ErrConflict, "ai run already in progress")
			},
		}, func(uuid.UUID) error { enqueued++; return nil })
		req := ozonReq(t, http.MethodPost, "/ozon/ai/run", body, workspaceID)
		rec := httptest.NewRecorder()
		h.AITriggerRun(rec, req)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "CONFLICT", firstErrorCode(t, rec))
		assert.Zero(t, enqueued)
	})

	t.Run("worker not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/ai/run", body, workspaceID)
		rec := httptest.NewRecorder()
		h.AITriggerRun(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("success -> 202", func(t *testing.T) {
		var got uuid.UUID
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, func(id uuid.UUID) error { got = id; return nil })
		req := ozonReq(t, http.MethodPost, "/ozon/ai/run", body, workspaceID)
		rec := httptest.NewRecorder()
		h.AITriggerRun(rec, req)
		assert.Equal(t, http.StatusAccepted, rec.Code)
		assert.Equal(t, cabinetID, got)
	})
}

// --- phase 4/5: repricer ---

func TestOzonRepricerNotConfigured(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/price-changes?cabinet_id="+uuid.New().String(), "", uuid.New())
	rec := httptest.NewRecorder()
	h.ListPriceChanges(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "NOT_CONFIGURED", firstErrorCode(t, rec))
}

func TestOzonListPriceChanges_SKUValidation(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	for _, sku := range []string{"abc", "-5", "0"} {
		t.Run("sku="+sku, func(t *testing.T) {
			h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
			req := ozonReq(t, http.MethodGet, "/ozon/price-changes?cabinet_id="+cabinetID.String()+"&sku="+sku, "", workspaceID)
			rec := httptest.NewRecorder()
			h.ListPriceChanges(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestOzonBulkPrices_PartialFailureStillReturnsRows(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	userID := uuid.New()

	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
		applyManualFn: func(_ context.Context, _, _ uuid.UUID, items []service.OzonManualPriceInput, uID uuid.UUID) ([]domain.OzonPriceChange, error) {
			assert.Equal(t, userID, uID)
			assert.Len(t, items, 2)
			// One row applied, one failed — the handler must still return 200
			// with the rows because changes != nil.
			return []domain.OzonPriceChange{{}, {}}, apperror.ErrOzonAPIError
		},
	}, nil)

	body := `{"cabinet_id":"` + cabinetID.String() + `","items":[{"sku":1,"new_price_rub":100},{"sku":2,"new_price_rub":200}]}`
	req := ozonReq(t, http.MethodPost, "/ozon/prices/bulk", body, workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rec := httptest.NewRecorder()
	h.BulkPrices(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
}

func TestOzonBulkPrices_RequestLevelErrorWithNilRows(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
		applyManualFn: func(context.Context, uuid.UUID, uuid.UUID, []service.OzonManualPriceInput, uuid.UUID) ([]domain.OzonPriceChange, error) {
			return nil, apperror.ErrNotFound
		},
	}, nil)

	body := `{"cabinet_id":"` + cabinetID.String() + `","items":[{"sku":1,"new_price_rub":100}]}`
	req := ozonReq(t, http.MethodPost, "/ozon/prices/bulk", body, workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.New()))
	rec := httptest.NewRecorder()
	h.BulkPrices(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "NOT_FOUND", firstErrorCode(t, rec))
}

func TestOzonBulkPrices_MissingUser(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
	body := `{"cabinet_id":"` + cabinetID.String() + `","items":[]}`
	req := ozonReq(t, http.MethodPost, "/ozon/prices/bulk", body, workspaceID)
	rec := httptest.NewRecorder()
	h.BulkPrices(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestOzonListPriceSchedules_StatusValidation(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	valid := []string{"", domain.OzonScheduleStatusPending, domain.OzonScheduleStatusApplied, domain.OzonScheduleStatusReverted, domain.OzonScheduleStatusCancelled, domain.OzonScheduleStatusFailed}
	for _, status := range valid {
		t.Run("valid status "+status, func(t *testing.T) {
			h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
				listSchedulesFn: func(_ context.Context, _, _ uuid.UUID, gotStatus string, _, _ int32) ([]domain.OzonPriceScheduleEntry, int64, error) {
					assert.Equal(t, status, gotStatus)
					return nil, 0, nil
				},
			}, nil)
			req := ozonReq(t, http.MethodGet, "/ozon/price-schedules?cabinet_id="+cabinetID.String()+"&status="+status, "", workspaceID)
			rec := httptest.NewRecorder()
			h.ListPriceSchedules(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}

	t.Run("invalid status -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/price-schedules?cabinet_id="+cabinetID.String()+"&status=running", "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListPriceSchedules(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOzonPricesHeatmap_Validation(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("invalid metric -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices/heatmap?cabinet_id="+cabinetID.String()+"&metric=revenue", "", workspaceID)
		rec := httptest.NewRecorder()
		h.PricesHeatmap(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "revenue metric is WB-only and must be rejected for ozon")
	})

	t.Run("invalid sku -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices/heatmap?cabinet_id="+cabinetID.String()+"&sku=-1", "", workspaceID)
		rec := httptest.NewRecorder()
		h.PricesHeatmap(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("valid metric passthrough", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			heatmapFn: func(_ context.Context, _, _ uuid.UUID, sku int64, metric string) (*domain.OrdersHeatmap, error) {
				assert.Equal(t, int64(123), sku)
				assert.Equal(t, domain.HeatmapMetricOrders, metric)
				return &domain.OrdersHeatmap{}, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices/heatmap?cabinet_id="+cabinetID.String()+"&sku=123&metric=orders", "", workspaceID)
		rec := httptest.NewRecorder()
		h.PricesHeatmap(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestOzonTriggerRepricerRun_EnqueuesWorkspaceNotCabinet(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	var enqueuedID uuid.UUID
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, func(id uuid.UUID) error {
		enqueuedID = id
		return nil
	})

	body := `{"cabinet_id":"` + cabinetID.String() + `"}`
	req := ozonReq(t, http.MethodPost, "/ozon/repricer/run", body, workspaceID)
	rec := httptest.NewRecorder()
	h.TriggerRepricerRun(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, workspaceID, enqueuedID, "repricer run is enqueued per workspace, not per cabinet")
}

func TestOzonTriggerRepricerRun_TenancyGate(t *testing.T) {
	workspaceID := uuid.New()
	enqueued := 0
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
		resolveFn: func(context.Context, uuid.UUID, uuid.UUID) error {
			return apperror.ErrNotFound
		},
	}, func(uuid.UUID) error { enqueued++; return nil })

	body := `{"cabinet_id":"` + uuid.New().String() + `"}`
	req := ozonReq(t, http.MethodPost, "/ozon/repricer/run", body, workspaceID)
	rec := httptest.NewRecorder()
	h.TriggerRepricerRun(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, enqueued)
}
