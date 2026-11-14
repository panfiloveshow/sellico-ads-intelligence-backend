package service

import (
	"context"
	"encoding/json"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func launchChecks() map[string]bool {
	return map[string]bool{"campaign_checked": true, "launch_settings_checked": true, "launch_spend_checked": true}
}
func (e *pauseEnv) prepareLaunch(t *testing.T) *domain.ManagerAdOperation {
	t.Helper()
	o, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "launch")
	require.NoError(t, err)
	return o
}
func (e *pauseEnv) approveLaunch(o *domain.ManagerAdOperation) (*domain.ManagerAdOperation, error) {
	return e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint, launchChecks())
}
func launchEnvironment(t *testing.T, market string) *pauseEnv {
	e := newPauseEnv(t, market, "ok")
	e.api.paused = true
	if market == "wb" {
		e.api.ready = true
	}
	return e
}
func TestManagerAdLaunchOneConfiguredCampaignAndWireContracts(t *testing.T) {
	for _, kind := range []string{"wb-ready", "wb-paused", "wb-cpc", "ozon"} {
		t.Run(kind, func(t *testing.T) {
			market := "wb"
			if kind == "ozon" {
				market = "ozon"
			}
			e := launchEnvironment(t, market)
			if kind == "wb-paused" {
				e.api.ready = false
			}
			if kind == "wb-cpc" {
				e.api.paymentType = "cpc"
				e.api.dailyLimitEnabled = true
				e.api.dailyLimitKopecks = 100001
				e.api.minimumDailyLimitKopecks = 100000
				e.api.dailyLimitCurrency = "RUB"
			}
			prepared := e.prepareLaunch(t)
			require.NotNil(t, prepared.LaunchPlan)
			require.False(t, prepared.LaunchPlan.Settings.EligibilityVerified)
			require.False(t, prepared.LaunchPlan.Settings.VendorLimitsVerified)
			require.Equal(t, "202", prepared.LaunchPlan.Settings.Products[0].ProductID)
			require.Equal(t, int32(0), e.api.writes.Load())
			_, err := e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, prepared.ID, "personal-token", prepared.Fingerprint, map[string]bool{"campaign_checked": true, "resume_spend_checked": true})
			require.Error(t, err)
			final, err := e.approveLaunch(prepared)
			require.NoError(t, err)
			require.Equal(t, "succeeded", final.Status)
			require.True(t, final.Result.LaunchVerified)
			require.True(t, final.Result.ControlHoldActive)
			require.Equal(t, int32(1), e.api.writes.Load())
			e.assertLegacyBlocked(t)
			if dir := os.Getenv("SELYA_AD_LAUNCH_TEST_CONTRACT_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				for name, value := range map[string]any{"prepared": prepared, "finished": final} {
					raw, err := json.Marshal(map[string]any{"data": value})
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(dir, kind+"-"+name+".json"), raw, 0600))
				}
			}
			pause := e.prepare(t)
			paused, err := e.approve(pause)
			require.NoError(t, err)
			require.Equal(t, "succeeded", paused.Status)
			previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, prepared.ID, "personal-token")
			require.NoError(t, err)
			require.False(t, previous.Result.ControlHoldActive)
			require.Equal(t, pause.ID, *previous.Result.ControlTransferredTo)
		})
	}
}
func TestManagerAdLaunchMissingInvalidConfigurationAndBudgetCannotStart(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		for _, kind := range []string{"empty", "duplicate", "zero_bid", "below_minimum", "budget", "budget_invalid", "already_active"} {
			t.Run(market+"/"+kind, func(t *testing.T) {
				e := launchEnvironment(t, market)
				switch kind {
				case "empty":
					e.api.emptyProducts = true
				case "duplicate":
					e.api.duplicateProduct = true
				case "zero_bid":
					e.api.bidWB = 0
					e.api.bidOzon = "0"
				case "below_minimum":
					e.api.minimumWB = 6000
					e.api.bidOzon = "0"
				case "budget_invalid":
					e.api.budgetMissing = true
					e.api.weeklyBudget = "invalid"
				case "budget":
					e.api.budget = 0
					e.api.weeklyBudget = "0"
				case "already_active":
					e.api.paused = false
					e.api.ready = false
				}
				_, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "launch")
				require.Error(t, err)
				require.Equal(t, int32(0), e.api.writes.Load())
			})
		}
	}
}
func TestManagerAdLaunchRightsAndEveryConfiguredBidCheckedUnderHold(t *testing.T) {
	for _, kind := range []string{"rights", "bid", "placement_bid"} {
		t.Run(kind, func(t *testing.T) {
			e := launchEnvironment(t, "wb")
			o := e.prepareLaunch(t)
			underHold := e.authority.calls.Load() + 2
			e.authority.hook = func(n int32) {
				if n == underHold {
					var holds int
					require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1`, o.ID).Scan(&holds))
					require.Equal(t, 1, holds)
					command, err := e.q.ReadNativeAdCommand(context.Background(), o.ID)
					require.NoError(t, err)
					require.False(t, command.StartedAt.Valid)
					e.api.mu.Lock()
					if kind == "bid" {
						e.api.bidWB++
					}
					if kind == "placement_bid" {
						e.api.bidRecommendations++
					}
					e.api.mu.Unlock()
				}
			}
			if kind == "rights" {
				e.authority.revokeAt = underHold
			}
			_, _ = e.approveLaunch(o)
			require.Equal(t, int32(0), e.api.writes.Load())
		})
	}
}
func TestManagerAdLaunchUnknownAndChangedReadbackNeverReplay(t *testing.T) {
	for _, kind := range []string{"lost", "unknown", "pending"} {
		t.Run(kind, func(t *testing.T) {
			e := launchEnvironment(t, "ozon")
			e.api.mode = kind
			o := e.prepareLaunch(t)
			current, err := e.approveLaunch(o)
			require.NoError(t, err)
			if kind == "lost" {
				require.Equal(t, "succeeded", current.Status)
				require.False(t, current.Result.SubmissionAcknowledged)
			} else {
				require.Contains(t, []string{"unknown", "verifying"}, current.Status)
				e.api.mu.Lock()
				e.api.paused = false
				e.api.bidOzon = "12350000"
				e.api.mu.Unlock()
				current, err = e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
				require.NoError(t, err)
				require.NotEqual(t, "succeeded", current.Status)
				e.api.mu.Lock()
				e.api.bidOzon = "12340000"
				e.api.mu.Unlock()
				current, err = e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
				require.NoError(t, err)
				require.Equal(t, "succeeded", current.Status)
			}
			_, err = e.approveLaunch(o)
			require.NoError(t, err)
			require.Equal(t, int32(1), e.api.writes.Load())
		})
	}
}
func TestManagerAdLaunchConcurrentApprovalOneNativeCommand(t *testing.T) {
	e := launchEnvironment(t, "ozon")
	o := e.prepareLaunch(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.approveLaunch(o) }()
	}
	wg.Wait()
	current, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "succeeded", current.Status)
	require.Equal(t, int32(1), e.api.writes.Load())
}
func TestManagerAdLaunchRejectedTransferRestoresPreviousBudget(t *testing.T) {
	e := launchEnvironment(t, "ozon")
	first, err := e.approveBudget(e.prepareBudget(t, budgetTarget(true, "120", nil)))
	require.NoError(t, err)
	o := e.prepareLaunch(t)
	require.Equal(t, first.ID, *o.PreviousOperationID)
	e.api.mode = "reject"
	failed, err := e.approveLaunch(o)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	old, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, first.ID, "personal-token")
	require.NoError(t, err)
	require.True(t, old.Result.ControlHoldActive)
	e.assertLegacyBlocked(t)
}
func TestManagerAdLaunchHistoryPreventsDowngrade(t *testing.T) {
	e := launchEnvironment(t, "ozon")
	_ = e.prepareLaunch(t)
	raw, err := os.ReadFile("../../migrations/000075_manager_ad_launch.down.sql")
	require.NoError(t, err)
	_, err = e.pool.Exec(context.Background(), string(raw))
	require.ErrorContains(t, err, "launch history prevents downgrade")
}

func TestManagerAdLaunchRejectsUnknownProductPagination(t *testing.T) {
	e := launchEnvironment(t, "ozon")
	e.api.productPagination = true
	_, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "launch")
	require.Error(t, err)
	require.Equal(t, int32(0), e.api.writes.Load())
}
