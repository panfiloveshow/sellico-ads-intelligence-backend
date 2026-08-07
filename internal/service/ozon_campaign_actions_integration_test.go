package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

func TestOzonActions_CampaignLifecycle(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-actions-lifecycle")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{}
	svc := newCampaignActions(fx.db, perf)

	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 3001, "CAMPAIGN_STATE_INACTIVE", nil, nil)
	campaignID := uuid.UUID(c.ID.Bytes)

	require.NoError(t, svc.ActivateCampaign(ctx, fx.workspaceID, campaignID))
	require.Len(t, perf.activateCalls, 1)

	require.NoError(t, svc.DeactivateCampaign(ctx, fx.workspaceID, campaignID))
	require.Len(t, perf.deactivateCalls, 1)

	// mirrored state check
	fresh, err := fx.db.Queries.GetOzonCampaignByID(ctx, uuidToPgtype(campaignID))
	require.NoError(t, err)
	assert.Equal(t, "CAMPAIGN_STATE_INACTIVE", pgTextValue(fresh.State))

	t.Run("upstream error propagates", func(t *testing.T) {
		perf.writeErr = errors.New("ozon down")
		err := svc.ActivateCampaign(ctx, fx.workspaceID, campaignID)
		require.Error(t, err)
		perf.writeErr = nil
	})

	t.Run("foreign workspace hidden", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-actions-lifecycle-other")
		err := svc.ActivateCampaign(ctx, other, campaignID)
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})
}

func TestOzonActions_UpdateBudget(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-actions-budget")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{}
	svc := newCampaignActions(fx.db, perf)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 3101, "CAMPAIGN_STATE_RUNNING", nil, nil)
	campaignID := uuid.UUID(c.ID.Bytes)

	t.Run("both nil rejected", func(t *testing.T) {
		require.Error(t, svc.UpdateBudget(ctx, fx.workspaceID, campaignID, nil, nil))
	})
	t.Run("negative rejected", func(t *testing.T) {
		neg := int64(-1)
		require.Error(t, svc.UpdateBudget(ctx, fx.workspaceID, campaignID, &neg, nil))
	})
	t.Run("weekly applied", func(t *testing.T) {
		weekly := int64(7000)
		require.NoError(t, svc.UpdateBudget(ctx, fx.workspaceID, campaignID, nil, &weekly))
		require.Len(t, perf.budgetCalls, 1)
		fresh, err := fx.db.Queries.GetOzonCampaignByID(ctx, uuidToPgtype(campaignID))
		require.NoError(t, err)
		assert.EqualValues(t, 7000, fresh.WeeklyBudgetRub.Int64)
	})
}

func TestOzonActions_SetProductBids(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-actions-bids")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{}
	svc := newCampaignActions(fx.db, perf)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 3201, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 777, 10)
	campaignID := uuid.UUID(c.ID.Bytes)

	t.Run("validation", func(t *testing.T) {
		_, err := svc.SetProductBids(ctx, fx.workspaceID, campaignID, nil)
		require.Error(t, err)
		_, err = svc.SetProductBids(ctx, fx.workspaceID, campaignID, []OzonBidInput{{SKU: 0, BidRub: 5}})
		require.Error(t, err)
	})

	t.Run("applied path audits + mirrors", func(t *testing.T) {
		res, err := svc.SetProductBids(ctx, fx.workspaceID, campaignID, []OzonBidInput{{SKU: 777, BidRub: 25}})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, domain.OzonBidStatusApplied, res[0].Status)
		assert.Equal(t, 1, perf.bidCalls)

		changes, total, err := svc.ListBidChanges(ctx, fx.workspaceID, fx.cabinetID, nil, 50, 0)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, changes, 1)
		assert.Equal(t, domain.OzonBidStatusApplied, changes[0].Status)
	})

	t.Run("failed write marks audit failed", func(t *testing.T) {
		perf.writeErr = errors.New("bid rejected")
		res, err := svc.SetProductBids(ctx, fx.workspaceID, campaignID, []OzonBidInput{{SKU: 777, BidRub: 30}})
		require.Error(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, domain.OzonBidStatusFailed, res[0].Status)
		assert.NotEmpty(t, res[0].Error)
		perf.writeErr = nil
	})
}

func TestOzonActions_GetCompetitiveBids(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-actions-competitive")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{competitive: []ozon.CompetitiveBid{{SKU: 5, BidRub: 12}}}
	svc := newCampaignActions(fx.db, perf)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 3301, "CAMPAIGN_STATE_RUNNING", nil, nil)
	campaignID := uuid.UUID(c.ID.Bytes)

	t.Run("no products yields empty", func(t *testing.T) {
		bids, err := svc.GetCompetitiveBids(ctx, fx.workspaceID, campaignID)
		require.NoError(t, err)
		assert.Empty(t, bids)
	})

	seedOzonCampaignProduct(t, fx.db, c.ID, 5, 10)
	t.Run("returns upstream bids", func(t *testing.T) {
		bids, err := svc.GetCompetitiveBids(ctx, fx.workspaceID, campaignID)
		require.NoError(t, err)
		require.Len(t, bids, 1)
		assert.EqualValues(t, 12, bids[0].BidRub)
	})

	t.Run("upstream error becomes ozon api error", func(t *testing.T) {
		perf.competitiveErr = errors.New("no competitive data")
		_, err := svc.GetCompetitiveBids(ctx, fx.workspaceID, campaignID)
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrOzonAPIError))
	})
}

func TestOzonActions_ListBidChangesByCampaign(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-actions-listbid")
	defer cleanup()
	ctx := context.Background()
	svc := newCampaignActions(fx.db, &fakePerfClient{})
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 3401, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 9, 10)
	campaignID := uuid.UUID(c.ID.Bytes)
	_, err := svc.SetProductBids(ctx, fx.workspaceID, campaignID, []OzonBidInput{{SKU: 9, BidRub: 15}})
	require.NoError(t, err)

	t.Run("by campaign id resolves cabinet", func(t *testing.T) {
		rows, total, err := svc.ListBidChanges(ctx, fx.workspaceID, uuid.Nil, &campaignID, 50, 0)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
	})

	t.Run("missing cabinet and campaign rejected", func(t *testing.T) {
		_, _, err := svc.ListBidChanges(ctx, fx.workspaceID, uuid.Nil, nil, 50, 0)
		require.Error(t, err)
	})
}

func TestOzonActions_CPO(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-actions-cpo")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{cpoProducts: []ozon.CPOProduct{{SKU: 61, BidRub: 5, Enabled: true}}}
	svc := newCampaignActions(fx.db, perf)
	seedOzonProduct(t, fx.db, fx.cabinetID, 61, 61, "ART-61", "CPO Item")

	t.Run("list refreshes mirror from api", func(t *testing.T) {
		rows, total, err := svc.ListCPOProducts(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		assert.Equal(t, "CPO Item", rows[0].Name)
		assert.True(t, rows[0].Enabled)
	})

	t.Run("enable audits + toggles", func(t *testing.T) {
		require.NoError(t, svc.EnableCPO(ctx, fx.workspaceID, fx.cabinetID, []int64{61}))
		assert.Equal(t, 1, perf.enableCalls)
	})

	t.Run("disable", func(t *testing.T) {
		require.NoError(t, svc.DisableCPO(ctx, fx.workspaceID, fx.cabinetID, []int64{61}))
		assert.Equal(t, 1, perf.disableCalls)
	})

	t.Run("validation", func(t *testing.T) {
		require.Error(t, svc.EnableCPO(ctx, fx.workspaceID, fx.cabinetID, nil))
		require.Error(t, svc.EnableCPO(ctx, fx.workspaceID, fx.cabinetID, []int64{0}))
	})

	t.Run("set cpo bids", func(t *testing.T) {
		require.Error(t, svc.SetCPOBids(ctx, fx.workspaceID, fx.cabinetID, nil))
		require.NoError(t, svc.SetCPOBids(ctx, fx.workspaceID, fx.cabinetID, []OzonCPOBidInput{{SKU: 61, BidRub: 7}}))
		assert.Equal(t, 1, perf.cpoBidCalls)
	})
}

func TestOzonActions_GetBidLimitsCached(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-actions-limits")
	defer cleanup()
	ctx := context.Background()
	perf := &fakePerfClient{bidLimits: json.RawMessage(`{"limits":[]}`)}
	svc := newCampaignActions(fx.db, perf)

	payload, err := svc.GetBidLimits(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"limits":[]}`, string(payload))

	// Second call hits the cache; tenancy still enforced.
	_, err = svc.GetBidLimits(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)

	t.Run("prices-only cabinet has no performance creds", func(t *testing.T) {
		ws := newOzonWorkspace(t, fx.db, "ozon-actions-limits-po")
		po := seedOzonCabinet(t, fx.db, ws, pricesOnlyOzonCreds())
		_, err := svc.GetBidLimits(ctx, ws, po)
		require.Error(t, err)
	})
}
