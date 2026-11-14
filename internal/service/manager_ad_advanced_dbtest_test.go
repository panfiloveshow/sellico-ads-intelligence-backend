package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerAdAttributionAndManualResolutionWithoutReplay(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	workspace := testdb.Workspace(t, pool)
	actor := uuid.New()
	cabinet := testdb.WBCabinet(t, pool, workspace)
	_, err := pool.Exec(ctx, `UPDATE seller_cabinets SET external_integration_id='28', status='active' WHERE id=$1`, cabinet)
	require.NoError(t, err)

	var campManual, campAuto uuid.UUID
	err = pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,campaign_type,bid_type,payment_type) VALUES($1,$2,501,'Manual CPC','9',9,'manual','cpc') RETURNING id`, workspace, cabinet).Scan(&campManual)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,campaign_type,bid_type,payment_type) VALUES($1,$2,502,'Auto Unified','9',8,'unified','cpm') RETURNING id`, workspace, cabinet).Scan(&campAuto)
	require.NoError(t, err)

	from, _ := time.Parse(time.DateOnly, "2026-10-01")
	to := from.AddDate(0, 0, 2)
	_, err = pool.Exec(ctx, `INSERT INTO campaign_stats(campaign_id,date,impressions,clicks,spend,orders,revenue) VALUES
		($1,$3,1000,50,600,3,9000),
		($2,$4,2000,40,400,0,0)`, campManual, campAuto, from, from.AddDate(0, 0, 1))
	require.NoError(t, err)

	auth := &pauseAuthority{market: "wb", personal: true}
	queries := sqlcgen.New(pool)
	svc := NewManagerAdAdvancedService(queries, auth, true)

	identity := managerAdsIdentity()
	identity.PersonalAccountVerified = true
	identity.Permissions = append(identity.Permissions, "marketing.manage")

	attr, err := svc.Attribution(ctx, identity, workspace, "personal-token", "28", from, to, nil)
	require.NoError(t, err)
	assert.Equal(t, "1000.00", attr.TotalSpendRub)
	assert.Equal(t, int64(3), attr.AttributedOrders)
	assert.Equal(t, "9000.00", attr.AttributedRevenueRub)
	assert.Equal(t, "400.00", attr.UnattributedSpendRub)
	require.NotNil(t, attr.ObservedCPORub)
	assert.Equal(t, "333.33", *attr.ObservedCPORub)
	assert.True(t, attr.AttributionVerified)
	assert.Equal(t, int64(2), attr.ObservedDays)
	assert.Equal(t, int64(1), attr.MissingDaysCount)
	require.Len(t, attr.Strategies, 2)

	now := time.Now().UTC().Truncate(time.Microsecond)
	opID := uuid.New()
	req := adguard.Request{
		Marketplace: "wb",
		AccountKey:  "wb:" + strings.Repeat("1", 64),
		Method:      "GET",
		URL:         "https://advert-api.wildberries.ru/adv/v0/pause?id=501",
		Resources:   []string{"campaign:501"},
		Fingerprint: strings.Repeat("2", 64),
	}
	op := domain.ManagerAdOperation{
		ID:                  opID,
		WorkspaceID:         workspace,
		ActorID:             actor,
		ExternalUserID:      "42",
		ExternalWorkspaceID: "3",
		IntegrationID:       "28",
		IntegrationName:     "Test shop",
		CabinetID:           cabinet,
		CampaignID:          campManual,
		ExternalID:          "501",
		Marketplace:         "wb",
		OperationType:       "ad_change",
		Action:              "pause",
		Snapshot:            domain.NativeAdCampaign{ExternalID: "501", Status: "9"},
		TargetStatus:        "11",
		Source:              "WB Advertising API",
		Request:             req,
		CredentialDigest:    strings.Repeat("3", 64),
		Status:              "awaiting_confirmation",
		CreatedAt:           now,
		ExpiresAt:           now.Add(5 * time.Minute),
	}
	op.Fingerprint = adOperationFingerprint(op)
	require.NoError(t, queries.CreateManagerAdOperation(ctx, op))
	_, err = pool.Exec(ctx, `UPDATE manager_ad_operations SET status='unknown', message_code='receipt_unavailable' WHERE id=$1`, opID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO ad_native_commands(id,marketplace,account_key,request_fingerprint,method,endpoint,resources,origin,workspace_id,actor_id,approval_fingerprint,approval_expires_at,status,started_at)
		VALUES($1,'wb',$2,$3,'GET','/adv/v0/pause','["campaign:501"]'::jsonb,'manager',$4,$5,$6,$7,'unknown',now())`,
		opID, req.AccountKey, req.Fingerprint, workspace, actor, op.Fingerprint, op.ExpiresAt)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO ad_native_resource_holds(resource_key,account_key,command_id,global_scope) VALUES('wb:'||$1||':campaign:501',$1,$2,false)`, req.AccountKey, opID)
	require.NoError(t, err)

	prepared, err := svc.PrepareResolution(ctx, identity, workspace, actor, "personal-token", opID, "28", "wildberries", campManual,
		"Таймаут ответа WB, лично проверил статус кампании в кабинете",
		"WB кабинет кампания 501 на паузе в 12:00")
	require.NoError(t, err)
	assert.Equal(t, "awaiting_confirmation", prepared.Status)
	assert.True(t, prepared.ControlHoldActive)
	assert.False(t, prepared.NativeOutcomeVerified)
	assert.False(t, prepared.NativeCampaignsChanged)

	approved, err := svc.ApproveResolution(ctx, identity, workspace, actor, opID, prepared.ID, "personal-token", prepared.Fingerprint, map[string]bool{
		"ad_cabinet_state_checked":     true,
		"ad_no_pending_mutation":       true,
		"ad_late_effect_risk_accepted": true,
		"ad_hold_release_understood":   true,
	})
	require.NoError(t, err)
	assert.Equal(t, "resolved_manually", approved.Status)
	assert.NotNil(t, approved.ResolvedAt)
	assert.False(t, approved.ControlHoldActive)
	assert.False(t, approved.NativeOutcomeVerified)
	assert.False(t, approved.NativeCampaignsChanged)

	var opStatus, cmdStatus string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM manager_ad_operations WHERE id=$1`, opID).Scan(&opStatus))
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM ad_native_commands WHERE id=$1`, opID).Scan(&cmdStatus))
	assert.Equal(t, "resolved_manually", opStatus)
	assert.Equal(t, "unknown", cmdStatus)
}
