package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestManagerAdBudgetContextUsesOwnedNativeCampaignWithoutMutation(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			e.authority.personal = false // read access must not require a personal write identity.
			e.api.paymentType = "cpc"
			e.api.dailyLimitEnabled = true
			e.api.dailyLimitKopecks = 100001
			e.api.spentTodayKopecks = 35001
			e.api.minimumDailyLimitKopecks = 100000
			e.api.dailyLimitCurrency = "RUB"
			period := "daily"
			amount := "1000.01"
			if market == "ozon" {
				period = "weekly"
				amount = "100.000001"
			}
			view, err := e.svc.context.CampaignBudgetContext(context.Background(), managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign, period)
			require.NoError(t, err)
			require.Equal(t, "3", view.WorkspaceID)
			require.Equal(t, "28", view.IntegrationID)
			require.Equal(t, e.campaign, view.CampaignID)
			require.Equal(t, amount, *view.Budget.AmountRub)
			require.True(t, view.Budget.PeriodConfirmed)
			require.True(t, view.Partial)
			require.False(t, view.ExecutionAllowed)
			require.Equal(t, "not_configured", view.ExecutionPolicy)
			require.Empty(t, view.AvailableActions)
			require.Nil(t, view.DataUpdatedAt)
			require.Nil(t, view.Budget.MaximumAmountRub)
			require.Equal(t, int32(0), e.api.writes.Load())
			var commands int
			require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM ad_native_commands`).Scan(&commands))
			require.Zero(t, commands)
			if dir := os.Getenv("SELYA_AD_BUDGET_TEST_CONTRACT_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				raw, err := json.Marshal(map[string]any{"data": view})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, market+"-context.json"), raw, 0600))
			}
			_, err = e.svc.context.CampaignBudgetContext(context.Background(), managerAdsIdentity(), uuid.New(), "personal-token", "28", e.campaign, period)
			require.Error(t, err)
			_, err = e.svc.context.CampaignBudgetContext(context.Background(), managerAdsIdentity(), e.workspace, "personal-token", "29", e.campaign, period)
			require.Error(t, err)
			denied := managerAdsIdentity()
			denied.Permissions = []string{}
			_, err = e.svc.context.CampaignBudgetContext(context.Background(), denied, e.workspace, "personal-token", "28", e.campaign, period)
			require.Error(t, err)
		})
	}
}

func TestManagerAdBudgetContextNeverUsesLegacyBudgetAsFallback(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok") // legacy snapshot has dailyBudget=1000; payment=cpm.
	_, err := e.svc.context.CampaignBudgetContext(context.Background(), managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign, "daily")
	require.Error(t, err)
	e.api.paymentType = "cpc"
	e.api.dailyLimitCurrency = "UZS"
	e.api.minimumDailyLimitKopecks = 100000
	_, err = e.svc.context.CampaignBudgetContext(context.Background(), managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign, "daily")
	require.Error(t, err)
	e2 := newPauseEnv(t, "ozon", "ok")
	e2.api.weeklyBudget = "0"
	view, err := e2.svc.context.CampaignBudgetContext(context.Background(), managerAdsIdentity(), e2.workspace, "personal-token", "28", e2.campaign, "weekly")
	require.NoError(t, err)
	require.False(t, view.Budget.Enabled)
	require.False(t, view.Budget.PeriodConfirmed)
	require.Equal(t, "0", *view.Budget.AmountRub)
	require.Equal(t, int32(0), e.api.writes.Load())
	require.Equal(t, int32(0), e2.api.writes.Load())
}
