package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestManagerAdContextUsesCurrentNativeStateAndRejectsForeignCampaigns(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			pool := testdb.New(t)
			q := sqlcgen.New(pool)
			ctx := context.Background()
			workspace := testdb.Workspace(t, pool)
			var cabinet, campaign, foreign uuid.UUID
			api := newFakeOzonAPI()
			srv := httptest.NewServer(api)
			defer srv.Close()
			path := "/api/client/campaign"
			if market == "ozon" {
				cabinet = testdb.OzonCabinet(t, pool, workspace, testdb.OzonCredentials())
				campaign = testdb.OzonCampaign(t, pool, cabinet, 101, "Stored old name", "CAMPAIGN_STATE_RUNNING")
				otherCab := testdb.OzonCabinet(t, pool, workspace, testdb.OzonCredentials())
				foreign = testdb.OzonCampaign(t, pool, otherCab, 102, "Other cabinet", "CAMPAIGN_STATE_RUNNING")
				api.setJSON(path, map[string]any{"list": []any{map[string]any{"id": "101", "title": "Native name", "state": "CAMPAIGN_STATE_INACTIVE", "weeklyBudget": "100000001"}}})
			} else {
				path = "/api/advert/v2/adverts"
				cabinet = testdb.WBCabinet(t, pool, workspace)
				encrypted, err := crypto.Encrypt("native-test-only-token", testdb.EncryptionKey)
				require.NoError(t, err)
				_, err = pool.Exec(ctx, `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, cabinet, encrypted)
				require.NoError(t, err)
				err = pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,bid_type,payment_type) VALUES($1,$2,101,'Stored old name','active','manual','cpc') RETURNING id`, workspace, cabinet).Scan(&campaign)
				require.NoError(t, err)
				other := testdb.WBCabinet(t, pool, workspace)
				err = pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,bid_type,payment_type) VALUES($1,$2,102,'Other cabinet','active','manual','cpc') RETURNING id`, workspace, other).Scan(&foreign)
				require.NoError(t, err)
				api.setJSON(path, map[string]any{"adverts": []any{map[string]any{"id": 101, "name": "Native name", "status": 11, "dailyBudget": 0}}})
			}
			_, err := pool.Exec(ctx, `UPDATE seller_cabinets SET external_integration_id='28' WHERE id=$1`, cabinet)
			require.NoError(t, err)
			cfg := &config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 1000, OzonPerfAPIBaseURL: srv.URL}
			verifier := &managerIntegrationStub{market: market}
			s := NewManagerAdContextService(q, verifier, wb.NewClient(cfg, zerolog.Nop()).WithAdGuard(q), ozon.NewPerfClient(cfg, zerolog.Nop()).WithAdGuard(q), testdb.EncryptionKey)
			read := func(id uuid.UUID) {
				row, err := s.CampaignContext(ctx, managerAdsIdentity(), workspace, "personal-token", "28", id)
				require.Error(t, err)
				require.Nil(t, row)
			}
			read(foreign)
			require.Zero(t, api.callCount(path), "foreign campaign must not reach native credentials/API")
			row, err := s.CampaignContext(ctx, managerAdsIdentity(), workspace, "personal-token", "28", campaign)
			require.NoError(t, err)
			require.Equal(t, "Native name", row.Name)
			require.Equal(t, campaign, row.CampaignID)
			require.True(t, row.Partial)
			require.Nil(t, row.DataUpdatedAt)
			require.Equal(t, "not_configured", row.ExecutionPolicy)
			require.Empty(t, row.AvailableActions)
			if market == "ozon" {
				require.Equal(t, "CAMPAIGN_STATE_INACTIVE", row.Status)
				require.Equal(t, "100.000001", *row.WeeklyBudgetRub)
				require.Nil(t, row.DailyBudgetRub)
			} else {
				require.Equal(t, "11", row.Status)
				require.Equal(t, "0", *row.DailyBudgetRub)
				require.Nil(t, row.WeeklyBudgetRub)
			}
			raw, err := json.Marshal(row)
			require.NoError(t, err)
			require.False(t, strings.Contains(string(raw), "native-test-only-token"))
			require.Equal(t, 1, api.callCount(path))
			var commands int
			require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM ad_native_commands`).Scan(&commands))
			require.Zero(t, commands)
			api.fail(path, 403)
			read(campaign)
			require.Equal(t, 2, api.callCount(path), "native failure must not return the stored RUNNING state")
			verifier.err = sellico.ErrManagerScopeDenied
			read(campaign)
			require.Equal(t, 2, api.callCount(path))
			verifier.err = nil
			identity := managerAdsIdentity()
			identity.Permissions = []string{"integrations.view"}
			_, err = s.CampaignContext(ctx, identity, workspace, "personal-token", "28", campaign)
			require.Error(t, err)
			require.Equal(t, 2, api.callCount(path))
			otherWorkspace := testdb.Workspace(t, pool)
			_, err = s.CampaignContext(ctx, managerAdsIdentity(), otherWorkspace, "personal-token", "28", campaign)
			require.Error(t, err)
			require.Equal(t, 2, api.callCount(path))
		})
	}
}
