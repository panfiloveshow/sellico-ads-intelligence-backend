package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// noWorkspaceReq builds a request with NO workspace id in context (and an
// optional url param), to exercise the missing-workspace branches of the
// shared workspaceAndCabinet/workspaceAndCampaign helpers.
func noWorkspaceReq(method, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

func TestOzonMissingWorkspaceBranches(t *testing.T) {
	cabinetID := uuid.New()

	t.Run("workspaceAndCabinet via ListCPOProducts", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := noWorkspaceReq(http.MethodGet, "/ozon/cpo/products?cabinet_id="+cabinetID.String())
		rec := httptest.NewRecorder()
		h.ListCPOProducts(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "VALIDATION_ERROR", firstErrorCode(t, rec))
	})

	t.Run("workspaceAndCampaign via ActivateCampaign", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := noWorkspaceReq(http.MethodPost, "/ozon/campaigns/x/activate")
		req = withURLParam(req, "id", uuid.New().String())
		rec := httptest.NewRecorder()
		h.ActivateCampaign(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("TriggerSync missing workspace", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, func(uuid.UUID) error { return nil })
		req := noWorkspaceReq(http.MethodPost, "/ozon/sync?cabinet_id="+cabinetID.String())
		rec := httptest.NewRecorder()
		h.TriggerSync(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("ListBidChanges missing workspace", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := noWorkspaceReq(http.MethodGet, "/ozon/bid-changes?cabinet_id="+cabinetID.String())
		rec := httptest.NewRecorder()
		h.ListBidChanges(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOzonGetCampaign_UpstreamError(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{
		getCampaignFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.OzonCampaign, []domain.OzonCampaignProduct, error) {
			return nil, nil, apperror.ErrOzonAPIError
		},
	}, nil, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/campaigns/"+campaignID.String(), "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec := httptest.NewRecorder()
	h.GetCampaign(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestOzonDeactivate_InvalidIDAndUpstream(t *testing.T) {
	workspaceID := uuid.New()

	h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
	req := ozonReq(t, http.MethodPost, "/ozon/campaigns/bad/deactivate", "", workspaceID)
	req = withURLParam(req, "id", "bad")
	rec := httptest.NewRecorder()
	h.DeactivateCampaign(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	campaignID := uuid.New()
	h = NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
		deactivateFn: func(context.Context, uuid.UUID, uuid.UUID) error { return apperror.ErrOzonAPIError },
	}, nil)
	req = ozonReq(t, http.MethodPost, "/ozon/campaigns/"+campaignID.String()+"/deactivate", "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec = httptest.NewRecorder()
	h.DeactivateCampaign(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestOzonBulkPrices_BadBodyAndNotConfigured(t *testing.T) {
	workspaceID := uuid.New()

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReqWithUser(t, http.MethodPost, "/ozon/prices/bulk", "{bad", workspaceID, uuid.New())
		rec := httptest.NewRecorder()
		h.BulkPrices(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/prices/bulk", "{}", workspaceID)
		rec := httptest.NewRecorder()
		h.BulkPrices(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("bad cabinet -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReqWithUser(t, http.MethodPost, "/ozon/prices/bulk", `{"cabinet_id":"nope","items":[]}`, workspaceID, uuid.New())
		rec := httptest.NewRecorder()
		h.BulkPrices(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOzonListPriceSchedules_Branches(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/price-schedules?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListPriceSchedules(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("missing cabinet -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/price-schedules", "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListPriceSchedules(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			listSchedulesFn: func(context.Context, uuid.UUID, uuid.UUID, string, int32, int32) ([]domain.OzonPriceScheduleEntry, int64, error) {
				return nil, 0, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/price-schedules?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListPriceSchedules(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonPricesHeatmap_Branches(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices/heatmap?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.PricesHeatmap(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("missing cabinet -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices/heatmap", "", workspaceID)
		rec := httptest.NewRecorder()
		h.PricesHeatmap(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			heatmapFn: func(context.Context, uuid.UUID, uuid.UUID, int64, string) (*domain.OrdersHeatmap, error) {
				return nil, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices/heatmap?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.PricesHeatmap(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonListBidChanges_CabinetOnlyAndUpstream(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("cabinet only success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			listBidChangesFn: func(_ context.Context, _, cab uuid.UUID, cmp *uuid.UUID, _, _ int32) ([]domain.OzonBidChange, int64, error) {
				assert.Equal(t, cabinetID, cab)
				assert.Nil(t, cmp)
				return nil, 0, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/bid-changes?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListBidChanges(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			listBidChangesFn: func(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, int32, int32) ([]domain.OzonBidChange, int64, error) {
				return nil, 0, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/bid-changes?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListBidChanges(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonAITriggerRun_MissingWorkspace(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, func(uuid.UUID) error { return nil })
	// Valid body (so the failure is workspace, not body) but no workspace ctx.
	req := httptest.NewRequest(http.MethodPost, "/ozon/ai/run", strings.NewReader(`{"cabinet_id":"`+uuid.New().String()+`"}`))
	rec := httptest.NewRecorder()
	h.AITriggerRun(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOzonAIDecisionAction_MissingWorkspace(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
	req := noWorkspaceReq(http.MethodPost, "/ozon/ai/decisions/x/approve")
	req = withURLParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.AIApproveDecision(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
