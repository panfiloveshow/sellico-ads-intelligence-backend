package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// ozonReqWithUser attaches both workspace and user ids to the request context.
func ozonReqWithUser(t *testing.T, method, target, body string, workspaceID, userID uuid.UUID) *http.Request {
	t.Helper()
	req := ozonReq(t, method, target, body, workspaceID)
	return req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
}

// --- GetCampaign success ---

func TestOzonGetCampaign_Success(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{
		getCampaignFn: func(_ context.Context, ws, cmp uuid.UUID) (*domain.OzonCampaign, []domain.OzonCampaignProduct, error) {
			assert.Equal(t, workspaceID, ws)
			assert.Equal(t, campaignID, cmp)
			return &domain.OzonCampaign{ID: campaignID}, []domain.OzonCampaignProduct{{}}, nil
		},
	}, nil, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/campaigns/"+campaignID.String(), "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec := httptest.NewRecorder()
	h.GetCampaign(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), campaignID.String())
}

func TestOzonGetCampaign_MissingWorkspace(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/ozon/campaigns/x", nil)
	req = withURLParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.GetCampaign(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "VALIDATION_ERROR", firstErrorCode(t, rec))
}

func TestOzonCampaignStats_UpstreamError(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{
		listStatsFn: func(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time) ([]domain.OzonCampaignStat, error) {
			return nil, apperror.ErrOzonAPIError
		},
	}, nil, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/campaigns/"+campaignID.String()+"/stats", "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec := httptest.NewRecorder()
	h.CampaignStats(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// --- ListPrices ---

func TestOzonListPrices(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("success with meta", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{
			listPricesFn: func(_ context.Context, _, _ uuid.UUID, search string, _, _ int32) ([]domain.OzonProductPrice, int64, error) {
				assert.Equal(t, "phone", search)
				return []domain.OzonProductPrice{{}}, 7, nil
			},
		}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices?cabinet_id="+cabinetID.String()+"&search=phone", "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListPrices(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		resp := decodeEnvelope(t, rec)
		require.NotNil(t, resp.Meta)
		assert.Equal(t, int64(7), resp.Meta.Total)
	})

	t.Run("missing cabinet_id -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices", "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListPrices(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing workspace -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/ozon/prices?cabinet_id="+cabinetID.String(), nil)
		rec := httptest.NewRecorder()
		h.ListPrices(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{
			listPricesFn: func(context.Context, uuid.UUID, uuid.UUID, string, int32, int32) ([]domain.OzonProductPrice, int64, error) {
				return nil, 0, apperror.ErrNotFound
			},
		}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/prices?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListPrices(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// --- ListSearchQueries ---

func TestOzonCPOOverview(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("success", func(t *testing.T) {
		promoID := int64(25626134)
		h := NewOzonHandler(&fakeOzonService{
			cpoOverviewFn: func(_ context.Context, _, gotCabinet uuid.UUID) (*domain.OzonCPOOverview, error) {
				assert.Equal(t, cabinetID, gotCabinet)
				return &domain.OzonCPOOverview{
					Enabled:            true,
					PromoCampaignID:    &promoID,
					PromoCampaignTitle: "Оплата за заказ",
					ProductsCount:      3,
					Stats7d:            domain.OzonCPOStats7d{Views: 300, SpendRub: 150, RevenueRub: 1500, DRR: 10},
				}, nil
			},
		}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/cpo/overview?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.CPOOverview(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "25626134")
		assert.Contains(t, rec.Body.String(), "\"products_count\":3")
	})

	t.Run("missing cabinet -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/cpo/overview", "", workspaceID)
		rec := httptest.NewRecorder()
		h.CPOOverview(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOzonListSearchQueries(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("success with sku+days", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{
			listSearchQueryFn: func(_ context.Context, _, _ uuid.UUID, sku *int64, search string, days int, _, _ int32) ([]domain.OzonSearchQueryStat, int64, error) {
				require.NotNil(t, sku)
				assert.Equal(t, int64(555), *sku)
				assert.Equal(t, 14, days)
				return []domain.OzonSearchQueryStat{{}}, 1, nil
			},
		}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/search-queries?cabinet_id="+cabinetID.String()+"&sku=555&days=14", "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListSearchQueries(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("default days when absent", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{
			listSearchQueryFn: func(_ context.Context, _, _ uuid.UUID, sku *int64, _ string, days int, _, _ int32) ([]domain.OzonSearchQueryStat, int64, error) {
				assert.Nil(t, sku)
				assert.Equal(t, 30, days)
				return nil, 0, nil
			},
		}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/search-queries?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListSearchQueries(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	for _, tc := range []struct{ name, query string }{
		{"bad sku", "&sku=abc"},
		{"days zero", "&days=0"},
		{"days over 90", "&days=91"},
		{"days not int", "&days=xx"},
	} {
		t.Run(tc.name+" -> 400", func(t *testing.T) {
			h := NewOzonHandler(&fakeOzonService{}, nil, nil)
			req := ozonReq(t, http.MethodGet, "/ozon/search-queries?cabinet_id="+cabinetID.String()+tc.query, "", workspaceID)
			rec := httptest.NewRecorder()
			h.ListSearchQueries(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}

	t.Run("missing cabinet -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/search-queries", "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListSearchQueries(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{
			listSearchQueryFn: func(context.Context, uuid.UUID, uuid.UUID, *int64, string, int, int32, int32) ([]domain.OzonSearchQueryStat, int64, error) {
				return nil, 0, apperror.ErrOzonAPIError
			},
		}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/search-queries?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListSearchQueries(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonTriggerSync_EnqueueError(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, func(uuid.UUID) error { return apperror.ErrOzonAPIError })
	req := ozonReq(t, http.MethodPost, "/ozon/sync?cabinet_id="+cabinetID.String(), "", workspaceID)
	rec := httptest.NewRecorder()
	h.TriggerSync(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// --- ActivateCampaign / DeactivateCampaign / UpdateBudget success + branches ---

func TestOzonActivateDeactivate_Success(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)

	req := ozonReq(t, http.MethodPost, "/ozon/campaigns/"+campaignID.String()+"/activate", "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec := httptest.NewRecorder()
	h.ActivateCampaign(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "activated")

	req = ozonReq(t, http.MethodPost, "/ozon/campaigns/"+campaignID.String()+"/deactivate", "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec = httptest.NewRecorder()
	h.DeactivateCampaign(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "deactivated")
}

func TestOzonActivate_InvalidIDAndUpstream(t *testing.T) {
	workspaceID := uuid.New()
	// Invalid campaign id.
	h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
	req := ozonReq(t, http.MethodPost, "/ozon/campaigns/bad/activate", "", workspaceID)
	req = withURLParam(req, "id", "bad")
	rec := httptest.NewRecorder()
	h.ActivateCampaign(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Upstream error.
	campaignID := uuid.New()
	h = NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
		activateFn: func(context.Context, uuid.UUID, uuid.UUID) error { return apperror.ErrOzonAPIError },
	}, nil)
	req = ozonReq(t, http.MethodPost, "/ozon/campaigns/"+campaignID.String()+"/activate", "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec = httptest.NewRecorder()
	h.ActivateCampaign(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestOzonDeactivate_NotConfigured(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil)
	req := ozonReq(t, http.MethodPost, "/ozon/campaigns/x/deactivate", "", uuid.New())
	rec := httptest.NewRecorder()
	h.DeactivateCampaign(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestOzonUpdateBudget(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			updateBudgetFn: func(_ context.Context, _, _ uuid.UUID, daily, weekly *int64) error {
				require.NotNil(t, daily)
				assert.Equal(t, int64(500), *daily)
				assert.Nil(t, weekly)
				return nil
			},
		}, nil)
		req := ozonReq(t, http.MethodPatch, "/ozon/campaigns/"+campaignID.String()+"/budget", `{"daily_budget_rub":500}`, workspaceID)
		req = withURLParam(req, "id", campaignID.String())
		rec := httptest.NewRecorder()
		h.UpdateBudget(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPatch, "/ozon/campaigns/"+campaignID.String()+"/budget", "{bad", workspaceID)
		req = withURLParam(req, "id", campaignID.String())
		rec := httptest.NewRecorder()
		h.UpdateBudget(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPatch, "/ozon/campaigns/x/budget", "{}", workspaceID)
		rec := httptest.NewRecorder()
		h.UpdateBudget(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			updateBudgetFn: func(context.Context, uuid.UUID, uuid.UUID, *int64, *int64) error { return apperror.ErrValidation },
		}, nil)
		req := ozonReq(t, http.MethodPatch, "/ozon/campaigns/"+campaignID.String()+"/budget", `{"weekly_budget_rub":100}`, workspaceID)
		req = withURLParam(req, "id", campaignID.String())
		rec := httptest.NewRecorder()
		h.UpdateBudget(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOzonSetProductBids_Success(t *testing.T) {
	workspaceID := uuid.New()
	campaignID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
		setBidsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.OzonBidInput) ([]domain.OzonBidChange, error) {
			return []domain.OzonBidChange{{}}, nil
		},
	}, nil)
	req := ozonReq(t, http.MethodPut, "/ozon/campaigns/"+campaignID.String()+"/products/bids", `{"items":[{"sku":1,"bid_rub":10}]}`, workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec := httptest.NewRecorder()
	h.SetProductBids(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOzonCompetitiveBids_InvalidIDAndError(t *testing.T) {
	workspaceID := uuid.New()

	// Invalid campaign id → 400.
	h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/campaigns/bad/bids/competitive", "", workspaceID)
	req = withURLParam(req, "id", "bad")
	rec := httptest.NewRecorder()
	h.CompetitiveBids(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Upstream error → 502.
	campaignID := uuid.New()
	h = NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
		competitiveFn: func(context.Context, uuid.UUID, uuid.UUID) ([]ozon.CompetitiveBid, error) {
			return nil, apperror.ErrOzonAPIError
		},
	}, nil)
	req = ozonReq(t, http.MethodGet, "/ozon/campaigns/"+campaignID.String()+"/bids/competitive", "", workspaceID)
	req = withURLParam(req, "id", campaignID.String())
	rec = httptest.NewRecorder()
	h.CompetitiveBids(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}
