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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

// --- CPO ---

func TestOzonListCPOProducts(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			listCPOFn: func(context.Context, uuid.UUID, uuid.UUID, int32, int32) ([]domain.OzonCPOProduct, int64, error) {
				return []domain.OzonCPOProduct{{}}, 3, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/cpo/products?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListCPOProducts(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		resp := decodeEnvelope(t, rec)
		assert.Equal(t, int64(3), resp.Meta.Total)
	})

	t.Run("not configured", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/cpo/products?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListCPOProducts(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("missing cabinet", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/cpo/products", "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListCPOProducts(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			listCPOFn: func(context.Context, uuid.UUID, uuid.UUID, int32, int32) ([]domain.OzonCPOProduct, int64, error) {
				return nil, 0, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/cpo/products?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.ListCPOProducts(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonCPOToggle(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	body := `{"cabinet_id":"` + cabinetID.String() + `","skus":[1,2,3]}`

	t.Run("enable success", func(t *testing.T) {
		var gotSKUs []int64
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			enableCPOFn: func(_ context.Context, _, _ uuid.UUID, skus []int64) error { gotSKUs = skus; return nil },
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/enable", body, workspaceID)
		rec := httptest.NewRecorder()
		h.EnableCPO(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []int64{1, 2, 3}, gotSKUs)
		assert.Contains(t, rec.Body.String(), "enabled")
	})

	t.Run("disable success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/disable", body, workspaceID)
		rec := httptest.NewRecorder()
		h.DisableCPO(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "disabled")
	})

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/enable", "{bad", workspaceID)
		rec := httptest.NewRecorder()
		h.EnableCPO(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("bad cabinet in body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/enable", `{"cabinet_id":"nope","skus":[1]}`, workspaceID)
		rec := httptest.NewRecorder()
		h.EnableCPO(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/enable", body, workspaceID)
		rec := httptest.NewRecorder()
		h.EnableCPO(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			enableCPOFn: func(context.Context, uuid.UUID, uuid.UUID, []int64) error { return apperror.ErrOzonAPIError },
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/enable", body, workspaceID)
		rec := httptest.NewRecorder()
		h.EnableCPO(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonSetCPOBids(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	body := `{"cabinet_id":"` + cabinetID.String() + `","bids":[{"sku":1,"bid_rub":5}]}`

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/bids", body, workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPOBids(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "applied")
	})

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/bids", "{bad", workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPOBids(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/bids", body, workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPOBids(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setCPOBidsFn: func(context.Context, uuid.UUID, uuid.UUID, []service.OzonCPOBidInput) error {
				return apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/bids", body, workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPOBids(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonSetCPORate(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	body := `{"cabinet_id":"` + cabinetID.String() + `","rate_pct":7}`

	t.Run("success", func(t *testing.T) {
		var got int
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setCPORateFn: func(_ context.Context, _, _ uuid.UUID, ratePct int) error { got = ratePct; return nil },
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/rate", body, workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPORate(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 7, got)
		assert.Contains(t, rec.Body.String(), "applied")
	})

	t.Run("bad rate -> 400", func(t *testing.T) {
		called := false
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setCPORateFn: func(context.Context, uuid.UUID, uuid.UUID, int) error { called = true; return nil },
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/rate", `{"cabinet_id":"`+cabinetID.String()+`","rate_pct":6}`, workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPORate(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "invalid rate never reaches the service")
	})

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/rate", "{bad", workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPORate(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/rate", body, workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPORate(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("tenancy 404", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setCPORateFn: func(context.Context, uuid.UUID, uuid.UUID, int) error {
				return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/rate", body, workspaceID)
		rec := httptest.NewRecorder()
		h.SetCPORate(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestOzonCPOActivateDeactivate(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	body := `{"cabinet_id":"` + cabinetID.String() + `"}`

	t.Run("activate success", func(t *testing.T) {
		var gotActive bool
		var called bool
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setCPOActiveFn: func(_ context.Context, _, _ uuid.UUID, active bool) error {
				called, gotActive = true, active
				return nil
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/activate", body, workspaceID)
		rec := httptest.NewRecorder()
		h.ActivateCPO(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, called)
		assert.True(t, gotActive)
		assert.Contains(t, rec.Body.String(), "activated")
	})

	t.Run("deactivate success", func(t *testing.T) {
		var gotActive = true
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setCPOActiveFn: func(_ context.Context, _, _ uuid.UUID, active bool) error { gotActive = active; return nil },
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/deactivate", body, workspaceID)
		rec := httptest.NewRecorder()
		h.DeactivateCPO(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.False(t, gotActive)
		assert.Contains(t, rec.Body.String(), "deactivated")
	})

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/activate", "{bad", workspaceID)
		rec := httptest.NewRecorder()
		h.ActivateCPO(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/deactivate", body, workspaceID)
		rec := httptest.NewRecorder()
		h.DeactivateCPO(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("tenancy 404", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			setCPOActiveFn: func(context.Context, uuid.UUID, uuid.UUID, bool) error {
				return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/cpo/activate", body, workspaceID)
		rec := httptest.NewRecorder()
		h.ActivateCPO(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestOzonBidLimits(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("success passthrough", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/limits?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.BidLimits(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("missing cabinet -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/limits", "", workspaceID)
		rec := httptest.NewRecorder()
		h.BidLimits(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, &fakeOzonActions{
			bidLimitsFn: func(context.Context, uuid.UUID, uuid.UUID) (json.RawMessage, error) {
				return nil, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/limits?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.BidLimits(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

// --- AI ---

func TestOzonAIListRuns_Success(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
		listRunsFn: func(context.Context, uuid.UUID, uuid.UUID, int32, int32) ([]domain.AIRun, int64, error) {
			return []domain.AIRun{{}}, 2, nil
		},
	}, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/ai/runs?cabinet_id="+cabinetID.String(), "", workspaceID)
	rec := httptest.NewRecorder()
	h.AIListRuns(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Equal(t, int64(2), resp.Meta.Total)
}

func TestOzonAIListRuns_MissingCabinet(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/ai/runs", "", uuid.New())
	rec := httptest.NewRecorder()
	h.AIListRuns(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOzonAIListDecisions(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	runID := uuid.New()

	t.Run("success with run_id and status", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			listDecisionsFn: func(_ context.Context, _, _ uuid.UUID, status string, rid *uuid.UUID, _, _ int32) ([]domain.AIDecision, int64, error) {
				assert.Equal(t, "pending", status)
				require.NotNil(t, rid)
				assert.Equal(t, runID, *rid)
				return []domain.AIDecision{{}}, 1, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/decisions?cabinet_id="+cabinetID.String()+"&status=pending&run_id="+runID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIListDecisions(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("invalid run_id -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/decisions?cabinet_id="+cabinetID.String()+"&run_id=nope", "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIListDecisions(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/decisions?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIListDecisions(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			listDecisionsFn: func(context.Context, uuid.UUID, uuid.UUID, string, *uuid.UUID, int32, int32) ([]domain.AIDecision, int64, error) {
				return nil, 0, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/decisions?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIListDecisions(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonAIImpact(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/impact?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIImpact(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("not configured", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/impact?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIImpact(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
			getImpactFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.AIImpactSummary, error) {
				return nil, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/ai/impact?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.AIImpact(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonAIRejectDecision_Success(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()
	decisionID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{
		rejectFn: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.AIDecision, error) {
			return &domain.AIDecision{ID: decisionID, Status: domain.AIDecisionStatusRejectedByUser}, nil
		},
	}, nil)
	req := ozonReqWithUser(t, http.MethodPost, "/ozon/ai/decisions/"+decisionID.String()+"/reject", "", workspaceID, userID)
	req = withURLParam(req, "id", decisionID.String())
	rec := httptest.NewRecorder()
	h.AIRejectDecision(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), decisionID.String())
}

func TestOzonAIDecisionAction_InvalidID(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
	req := ozonReqWithUser(t, http.MethodPost, "/ozon/ai/decisions/bad/reject", "", workspaceID, userID)
	req = withURLParam(req, "id", "bad")
	rec := httptest.NewRecorder()
	h.AIRejectDecision(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOzonAITriggerRun_BadBody(t *testing.T) {
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{}, nil)
	req := ozonReq(t, http.MethodPost, "/ozon/ai/run", "{bad", uuid.New())
	rec := httptest.NewRecorder()
	h.AITriggerRun(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestOzonAITriggerRun_EnqueueError(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithAI(&fakeOzonAI{},
		func(uuid.UUID) error { return apperror.ErrOzonAPIError })
	req := ozonReq(t, http.MethodPost, "/ozon/ai/run", `{"cabinet_id":"`+cabinetID.String()+`"}`, workspaceID)
	rec := httptest.NewRecorder()
	h.AITriggerRun(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// --- repricer ---

func TestOzonListPriceChanges_Success(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
		listPriceChangesFn: func(_ context.Context, _, _ uuid.UUID, sku *int64, _, _ int32) ([]domain.OzonPriceChange, int64, error) {
			require.NotNil(t, sku)
			assert.Equal(t, int64(42), *sku)
			return []domain.OzonPriceChange{{}}, 5, nil
		},
	}, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/price-changes?cabinet_id="+cabinetID.String()+"&sku=42", "", workspaceID)
	rec := httptest.NewRecorder()
	h.ListPriceChanges(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Equal(t, int64(5), resp.Meta.Total)
}

func TestOzonListPriceChanges_UpstreamError(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
		listPriceChangesFn: func(context.Context, uuid.UUID, uuid.UUID, *int64, int32, int32) ([]domain.OzonPriceChange, int64, error) {
			return nil, 0, apperror.ErrOzonAPIError
		},
	}, nil)
	req := ozonReq(t, http.MethodGet, "/ozon/price-changes?cabinet_id="+cabinetID.String(), "", workspaceID)
	rec := httptest.NewRecorder()
	h.ListPriceChanges(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestOzonRollbackPriceChange(t *testing.T) {
	workspaceID := uuid.New()
	changeID := uuid.New()

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			rollbackFn: func(_ context.Context, _, cid uuid.UUID) (*domain.OzonPriceChange, error) {
				assert.Equal(t, changeID, cid)
				return &domain.OzonPriceChange{ID: changeID}, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-changes/"+changeID.String()+"/rollback", "", workspaceID)
		req = withURLParam(req, "id", changeID.String())
		rec := httptest.NewRecorder()
		h.RollbackPriceChange(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("invalid id -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-changes/bad/rollback", "", workspaceID)
		req = withURLParam(req, "id", "bad")
		rec := httptest.NewRecorder()
		h.RollbackPriceChange(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-changes/x/rollback", "", workspaceID)
		rec := httptest.NewRecorder()
		h.RollbackPriceChange(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("missing workspace -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := httptest.NewRequest(http.MethodPost, "/ozon/price-changes/x/rollback", nil)
		req = withURLParam(req, "id", changeID.String())
		rec := httptest.NewRecorder()
		h.RollbackPriceChange(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			rollbackFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.OzonPriceChange, error) {
				return nil, apperror.ErrConflict
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-changes/"+changeID.String()+"/rollback", "", workspaceID)
		req = withURLParam(req, "id", changeID.String())
		rec := httptest.NewRecorder()
		h.RollbackPriceChange(rec, req)
		assert.Equal(t, http.StatusConflict, rec.Code)
	})
}

func TestOzonCreatePriceSchedule(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	body := `{"cabinet_id":"` + cabinetID.String() + `","sku":123,"scheduled_price_rub":990,"starts_at":"2026-08-10T00:00:00Z"}`

	t.Run("success -> 201", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			createScheduleFn: func(context.Context, uuid.UUID, uuid.UUID, domain.OzonPriceScheduleInput) (*domain.OzonPriceScheduleEntry, error) {
				return &domain.OzonPriceScheduleEntry{}, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-schedules", body, workspaceID)
		rec := httptest.NewRecorder()
		h.CreatePriceSchedule(rec, req)
		assert.Equal(t, http.StatusCreated, rec.Code)
	})

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-schedules", "{bad", workspaceID)
		rec := httptest.NewRecorder()
		h.CreatePriceSchedule(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-schedules", body, workspaceID)
		rec := httptest.NewRecorder()
		h.CreatePriceSchedule(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream validation error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			createScheduleFn: func(context.Context, uuid.UUID, uuid.UUID, domain.OzonPriceScheduleInput) (*domain.OzonPriceScheduleEntry, error) {
				return nil, apperror.ErrValidation
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/price-schedules", body, workspaceID)
		rec := httptest.NewRecorder()
		h.CreatePriceSchedule(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOzonCancelPriceSchedule(t *testing.T) {
	workspaceID := uuid.New()
	entryID := uuid.New()

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodDelete, "/ozon/price-schedules/"+entryID.String(), "", workspaceID)
		req = withURLParam(req, "id", entryID.String())
		rec := httptest.NewRecorder()
		h.CancelPriceSchedule(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "cancelled")
	})

	t.Run("invalid id -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodDelete, "/ozon/price-schedules/bad", "", workspaceID)
		req = withURLParam(req, "id", "bad")
		rec := httptest.NewRecorder()
		h.CancelPriceSchedule(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			cancelScheduleFn: func(context.Context, uuid.UUID, uuid.UUID) error { return apperror.ErrNotFound },
		}, nil)
		req := ozonReq(t, http.MethodDelete, "/ozon/price-schedules/"+entryID.String(), "", workspaceID)
		req = withURLParam(req, "id", entryID.String())
		rec := httptest.NewRecorder()
		h.CancelPriceSchedule(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodDelete, "/ozon/price-schedules/x", "", workspaceID)
		rec := httptest.NewRecorder()
		h.CancelPriceSchedule(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

func TestOzonPauseRepricer(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()
	until := time.Now().Add(2 * time.Hour)

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			setPauseFn: func(_ context.Context, _, _ uuid.UUID, hours int) (*time.Time, error) {
				assert.Equal(t, 2, hours)
				return &until, nil
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/prices/pause", `{"cabinet_id":"`+cabinetID.String()+`","hours":2}`, workspaceID)
		rec := httptest.NewRecorder()
		h.PauseRepricer(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/prices/pause", "{bad", workspaceID)
		rec := httptest.NewRecorder()
		h.PauseRepricer(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/prices/pause", "{}", workspaceID)
		rec := httptest.NewRecorder()
		h.PauseRepricer(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			setPauseFn: func(context.Context, uuid.UUID, uuid.UUID, int) (*time.Time, error) {
				return nil, apperror.ErrValidation
			},
		}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/prices/pause", `{"cabinet_id":"`+cabinetID.String()+`","hours":2}`, workspaceID)
		rec := httptest.NewRecorder()
		h.PauseRepricer(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestOzonRepricerHealth(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("success", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/repricer/health?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.RepricerHealth(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("not configured -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/repricer/health?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.RepricerHealth(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("upstream error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{
			healthFn: func(context.Context, uuid.UUID, uuid.UUID) (*domain.OzonRepricerHealth, error) {
				return nil, apperror.ErrOzonAPIError
			},
		}, nil)
		req := ozonReq(t, http.MethodGet, "/ozon/repricer/health?cabinet_id="+cabinetID.String(), "", workspaceID)
		rec := httptest.NewRecorder()
		h.RepricerHealth(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}

func TestOzonTriggerRepricerRun_BadBodyAndNoWorker(t *testing.T) {
	workspaceID := uuid.New()
	cabinetID := uuid.New()

	t.Run("bad body -> 400", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/repricer/run", "{bad", workspaceID)
		rec := httptest.NewRecorder()
		h.TriggerRepricerRun(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("no worker -> 503", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{}, nil)
		req := ozonReq(t, http.MethodPost, "/ozon/repricer/run", `{"cabinet_id":"`+cabinetID.String()+`"}`, workspaceID)
		rec := httptest.NewRecorder()
		h.TriggerRepricerRun(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("enqueue error", func(t *testing.T) {
		h := NewOzonHandler(&fakeOzonService{}, nil, nil).WithRepricer(&fakeOzonRepricer{},
			func(uuid.UUID) error { return apperror.ErrOzonAPIError })
		req := ozonReq(t, http.MethodPost, "/ozon/repricer/run", `{"cabinet_id":"`+cabinetID.String()+`"}`, workspaceID)
		rec := httptest.NewRecorder()
		h.TriggerRepricerRun(rec, req)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
	})
}
