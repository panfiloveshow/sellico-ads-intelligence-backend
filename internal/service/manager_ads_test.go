package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type managerIntegrationStub struct {
	market string
	calls  int
	err    error
}

func (s *managerIntegrationStub) VerifyManagerIntegration(_ context.Context, token, workspace, id string) (*domain.ManagerIntegration, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if token != "personal-token" || workspace != "3" || id != "28" {
		return nil, errors.New("unexpected manager scope")
	}
	return &domain.ManagerIntegration{ID: id, WorkspaceID: workspace, Marketplace: s.market, Name: "Test store"}, nil
}
func managerAdsIdentity() domain.ManagerAccess {
	return domain.ManagerAccess{ExternalUserID: "42", ExternalWorkspaceID: "3", Permissions: []string{"marketing.view", "integrations.view"}}
}

func TestManagerAdsOzonPeriodPrecisionComparisonAndMissingRows(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	workspace := testdb.Workspace(t, pool)
	cabinet := testdb.OzonCabinet(t, pool, workspace, testdb.OzonCredentials())
	_, err := pool.Exec(ctx, `UPDATE seller_cabinets SET external_integration_id='28' WHERE id=$1`, cabinet)
	require.NoError(t, err)
	campaign := testdb.OzonCampaign(t, pool, cabinet, 101, "Observed", "CAMPAIGN_STATE_RUNNING")
	missing := testdb.OzonCampaign(t, pool, cabinet, 102, "No rows", "CAMPAIGN_STATE_RUNNING")
	from, _ := time.Parse(time.DateOnly, "2026-10-01")
	to := from.AddDate(0, 0, 1)
	testdb.OzonCampaignStat(t, pool, campaign, from, 100, 10, 1.15, 1, 11.10)
	testdb.OzonCampaignStat(t, pool, campaign, to, 200, 20, 2.35, 2, 22.20)
	testdb.OzonCampaignStat(t, pool, campaign, from.AddDate(0, 0, -2), 50, 0, 0, 0, 0)
	testdb.OzonCampaignStat(t, pool, campaign, from.AddDate(0, 0, -30), 9999, 9999, 9999, 99, 99999)
	verifier := &managerIntegrationStub{market: "ozon"}
	svc := NewManagerAdsService(sqlcgen.New(pool), verifier)
	report, err := svc.Campaigns(ctx, managerAdsIdentity(), workspace, "personal-token", "28", from, to, 1, 25, true)
	require.NoError(t, err)
	require.Len(t, report.Items, 2)
	assert.Equal(t, "2026-09-29", report.Comparison.DateFrom)
	assert.Equal(t, "2026-09-30", report.Comparison.DateTo)
	row := report.Items[0]
	assert.Equal(t, campaign, row.ID)
	require.NotNil(t, row.Metrics)
	assert.Equal(t, int64(2), row.ObservedDays)
	assert.Equal(t, "3.50", row.Metrics.SpendRub)
	require.NotNil(t, row.Metrics.RevenueRub)
	assert.Equal(t, "33.30", *row.Metrics.RevenueRub)
	assert.Equal(t, int64(3), *row.Metrics.Orders)
	assert.Equal(t, "10.5105", *row.Metrics.DRRPercent)
	assert.Nil(t, row.DataUpdatedAt)
	require.NotNil(t, row.PreviousMetrics)
	assert.Equal(t, "0.00", row.PreviousMetrics.SpendRub)
	assert.Nil(t, row.PreviousMetrics.DRRPercent)
	assert.Nil(t, row.PreviousMetrics.ROAS)
	assert.Equal(t, missing, report.Items[1].ID)
	assert.Nil(t, report.Items[1].Metrics)
	assert.True(t, report.Partial)
	assert.False(t, report.HasMore)
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "encrypted_credentials")
	assert.NotContains(t, string(raw), "api-key-")
	otherWorkspace := testdb.Workspace(t, pool)
	_, err = svc.Campaigns(ctx, managerAdsIdentity(), otherWorkspace, "personal-token", "28", from, to, 1, 25, false)
	require.Error(t, err)
	assert.True(t, apperror.Is(err, apperror.ErrNotFound))
}

func TestManagerAdsWBUnknownAttributionAndPagination(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	workspace := testdb.Workspace(t, pool)
	cabinet := testdb.WBCabinet(t, pool, workspace)
	_, err := pool.Exec(ctx, `UPDATE seller_cabinets SET external_integration_id='28' WHERE id=$1`, cabinet)
	require.NoError(t, err)
	var campaign uuid.UUID
	err = pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,bid_type,payment_type) VALUES($1,$2,101,'WB test','active','manual','cpc') RETURNING id`, workspace, cabinet).Scan(&campaign)
	require.NoError(t, err)
	from, _ := time.Parse(time.DateOnly, "2026-10-01")
	to := from.AddDate(0, 0, 1)
	_, err = pool.Exec(ctx, `INSERT INTO campaign_stats(campaign_id,date,impressions,clicks,spend,orders,revenue) VALUES($1,$2,0,0,1,NULL,NULL),($1,$3,100,2,2,1,100)`, campaign, from, to)
	require.NoError(t, err)
	verifier := &managerIntegrationStub{market: "wb"}
	svc := NewManagerAdsService(sqlcgen.New(pool), verifier)
	report, err := svc.Campaigns(ctx, managerAdsIdentity(), workspace, "personal-token", "28", from, to, 1, 1, false)
	require.NoError(t, err)
	require.Len(t, report.Items, 1)
	metrics := report.Items[0].Metrics
	require.NotNil(t, metrics)
	assert.Equal(t, "3.00", metrics.SpendRub)
	assert.Nil(t, metrics.Orders)
	assert.Nil(t, metrics.RevenueRub)
	assert.Nil(t, metrics.DRRPercent)
	assert.NotNil(t, report.Items[0].DataUpdatedAt)
	assert.Equal(t, "1 RUB (legacy stored precision)", report.MonetaryPrecision)
	report, err = svc.Campaigns(ctx, managerAdsIdentity(), workspace, "personal-token", "28", from, to, 2, 1, false)
	require.NoError(t, err)
	assert.Empty(t, report.Items)
	assert.Equal(t, int64(1), report.Total)
}

func TestManagerAdsCannotReadUsingRevokedPermissionsOrAnUnavailableCRM(t *testing.T) {
	verifier := &managerIntegrationStub{market: "wb"}
	svc := NewManagerAdsService(sqlcgen.New(nil), verifier)
	from, _ := time.Parse(time.DateOnly, "2026-10-01")
	identity := managerAdsIdentity()
	identity.Permissions = []string{"marketing.view"}
	_, err := svc.Campaigns(context.Background(), identity, uuid.New(), "personal-token", "28", from, from, 1, 25, false)
	require.Error(t, err)
	assert.Zero(t, verifier.calls)
	verifier.err = errors.New("CRM unavailable")
	_, err = svc.Campaigns(context.Background(), managerAdsIdentity(), uuid.New(), "personal-token", "28", from, from, 1, 25, false)
	require.Error(t, err)
	assert.Equal(t, 1, verifier.calls)
}
