package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func budgetEnvironment(t *testing.T, market, mode string) *pauseEnv {
	t.Helper()
	e := newPauseEnv(t, market, mode)
	if market == "wb" {
		e.api.paymentType = "cpc"
		e.api.dailyLimitEnabled = true
		e.api.dailyLimitKopecks = 100001
		e.api.minimumDailyLimitKopecks = 100000
		e.api.spentTodayKopecks = 35001
		e.api.dailyLimitCurrency = "RUB"
	}
	return e
}

func TestManagerAdBudgetDisableRequiresFourthCheckAndHistoricalTypeSurvivesPause(t *testing.T) {
	e := budgetEnvironment(t, "ozon", "ok")
	off := e.prepareBudget(t, budgetTarget(false, "", nil))
	_, err := e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, off.ID, "personal-token", off.Fingerprint, budgetChecks(false))
	require.Error(t, err)
	require.Equal(t, int32(0), e.api.writes.Load())
	closed, err := e.approveBudget(off)
	require.NoError(t, err)
	require.Equal(t, "succeeded", closed.Status)
	require.False(t, closed.Result.Budget.Enabled)
	require.False(t, closed.Result.Budget.PeriodConfirmed)
	pause, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "pause")
	require.NoError(t, err)
	_, err = e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token", pause.Fingerprint, map[string]bool{"campaign_checked": true, "pause_impact_checked": true})
	require.NoError(t, err)
	on := e.prepareBudget(t, budgetTarget(true, "120", nil))
	require.NotNil(t, on.BudgetPlan.PeriodEvidence)
	require.Equal(t, off.ID, on.BudgetPlan.PeriodEvidence.OperationID)
	captureBudgetContract(t, "ozon-history-prepared", on)
	require.False(t, on.BudgetPlan.Before.PeriodConfirmed)
	final, err := e.approveBudget(on)
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	captureBudgetContract(t, "ozon-history-finished", final)
	require.Equal(t, "120", *final.Result.Budget.AmountRub)
	require.Equal(t, "CAMPAIGN_STATE_INACTIVE", *final.Result.VendorState)
	require.Equal(t, int32(3), e.api.writes.Load())
}

func TestManagerAdBudgetUnknownOriginalTypeAndConflictingPeriodsCannotBeGuessed(t *testing.T) {
	for _, kind := range []string{"no_history", "other_period", "conflicting_periods"} {
		t.Run(kind, func(t *testing.T) {
			e := budgetEnvironment(t, "ozon", "ok")
			e.api.weeklyBudget = "0"
			if kind == "other_period" {
				e.api.dailyBudgetOzon = "100000000"
			}
			if kind == "conflicting_periods" {
				e.api.dailyBudgetOzon = "100000000"
				e.api.weeklyBudget = "100000001"
			}
			_, err := e.svc.PrepareBudget(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "weekly", budgetTarget(true, "120", nil), &domain.ManagerAdBudgetConstraints{MaxBudgetRub: "200", MaxAbsoluteChangeRub: "150"})
			require.Error(t, err)
			require.Equal(t, int32(0), e.api.writes.Load())
		})
	}
}

func TestManagerAdBudgetReadbackAndUnknownNeverReplay(t *testing.T) {
	for _, mode := range []string{"reject", "unknown", "lost", "pending"} {
		t.Run(mode, func(t *testing.T) {
			e := budgetEnvironment(t, "ozon", mode)
			op := e.prepareBudget(t, budgetTarget(true, "120.01", nil))
			current, err := e.approveBudget(op)
			require.NoError(t, err)
			switch mode {
			case "reject":
				require.Equal(t, "failed", current.Status)
			case "lost":
				require.Equal(t, "succeeded", current.Status)
				require.False(t, current.Result.SubmissionAcknowledged)
			default:
				require.Contains(t, []string{"unknown", "verifying"}, current.Status)
			}
			require.Equal(t, int32(1), e.api.writes.Load())
			if mode == "pending" || mode == "unknown" {
				e.api.mu.Lock()
				e.api.weeklyBudget = "120010000"
				e.api.mu.Unlock()
				current, err = e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token")
				require.NoError(t, err)
				require.Equal(t, "succeeded", current.Status)
			}
			_, err = e.approveBudget(op)
			require.NoError(t, err)
			require.Equal(t, int32(1), e.api.writes.Load())
		})
	}
}

func TestManagerAdBudgetDefiniteRejectionRestoresPreviousProtection(t *testing.T) {
	e := budgetEnvironment(t, "ozon", "ok")
	first, err := e.approveBudget(e.prepareBudget(t, budgetTarget(true, "120", nil)))
	require.NoError(t, err)
	e.api.mu.Lock()
	e.api.mode = "reject"
	e.api.mu.Unlock()
	second := e.prepareBudget(t, budgetTarget(true, "140", nil))
	require.NotNil(t, second.PreviousOperationID)
	require.Equal(t, first.ID, *second.PreviousOperationID)
	failed, err := e.approveBudget(second)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, first.ID, "personal-token")
	require.NoError(t, err)
	require.True(t, previous.Result.ControlHoldActive)
	require.Nil(t, previous.Result.ControlTransferredTo)
	e.assertLegacyBlocked(t)
}

func TestManagerAdBudgetFreshRightsAndMandatoryMinimumAreCheckedUnderHold(t *testing.T) {
	for _, kind := range []string{"rights", "minimum"} {
		t.Run(kind, func(t *testing.T) {
			e := budgetEnvironment(t, "wb", "ok")
			carry := false
			op := e.prepareBudget(t, budgetTarget(true, "1200", &carry))
			underHold := e.authority.calls.Load() + 2
			e.authority.hook = func(n int32) {
				if n == underHold {
					var count int
					require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1`, op.ID).Scan(&count))
					require.Equal(t, 1, count)
					command, err := e.q.ReadNativeAdCommand(context.Background(), op.ID)
					require.NoError(t, err)
					require.False(t, command.StartedAt.Valid)
					if kind == "minimum" {
						e.api.mu.Lock()
						e.api.minimumDailyLimitKopecks = 130000
						e.api.mu.Unlock()
					}
				}
			}
			if kind == "rights" {
				e.authority.revokeAt = underHold
			}
			_, _ = e.approveBudget(op)
			stored, err := e.q.GetManagerAdOperation(context.Background(), op.ID, e.workspace, e.actor)
			require.NoError(t, err)
			require.Equal(t, "failed", stored.Status)
			require.Equal(t, int32(0), e.api.writes.Load())
		})
	}
}

func TestManagerAdBudgetMovingSpentCounterDoesNotReplaceImmutableLimit(t *testing.T) {
	e := budgetEnvironment(t, "wb", "ok")
	carry := true
	op := e.prepareBudget(t, budgetTarget(true, "1200", &carry))
	underHold := e.authority.calls.Load() + 2
	e.authority.hook = func(n int32) {
		if n == underHold {
			e.api.mu.Lock()
			e.api.spentTodayKopecks = 35101
			e.api.mu.Unlock()
		}
	}
	final, err := e.approveBudget(op)
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.Equal(t, "350.01", *final.BudgetPlan.Before.SpentTodayRub)
	require.Equal(t, "351.01", *final.Result.Budget.SpentTodayRub)
	require.True(t, *final.Result.Budget.CarryOverEnabled)
	require.Equal(t, "1200", *final.Result.Budget.AmountRub)
}

func TestManagerAdBudgetExplicitNominalConstraintsAndNoOp(t *testing.T) {
	before := domain.NativeAdBudget{Period: "weekly", PeriodConfirmed: true, Enabled: true, Currency: "RUB"}
	old := "100.000001"
	before.AmountRub = &old
	for _, row := range []struct {
		target, max, delta string
		allowed            bool
	}{{"120.01", "200", "20.009999", true}, {"120.01", "200", "20.009998", false}, {"120.01", "120", "100", false}, {"100.000001", "200", "0", false}} {
		target := budgetTarget(true, row.target, nil)
		_, err := managerBudgetConstraints(before, target, &domain.ManagerAdBudgetConstraints{MaxBudgetRub: row.max, MaxAbsoluteChangeRub: row.delta})
		require.Equal(t, row.allowed, err == nil, row)
	}
	_, err := canonicalBudgetTarget("ozon", "weekly", budgetTarget(true, "120.001", nil))
	require.Error(t, err)
	_, err = managerBudgetConstraints(before, budgetTarget(true, "120", nil), nil)
	require.Error(t, err)
}

func TestManagerAdBudgetConcurrentApprovalMakesOneNativeChange(t *testing.T) {
	e := budgetEnvironment(t, "ozon", "ok")
	op := e.prepareBudget(t, budgetTarget(true, "120", nil))
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.approveBudget(op) }()
	}
	wg.Wait()
	final, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.Equal(t, int32(1), e.api.writes.Load())
}

func TestManagerAdBudgetHistoryPreventsLossyDowngrade(t *testing.T) {
	e := budgetEnvironment(t, "ozon", "ok")
	_ = e.prepareBudget(t, budgetTarget(true, "120", nil))
	raw, err := os.ReadFile("../../migrations/000074_manager_ad_budgets.down.sql")
	require.NoError(t, err)
	_, err = e.pool.Exec(context.Background(), string(raw))
	require.ErrorContains(t, err, "budget history prevents downgrade")
}
func budgetTarget(enabled bool, amount string, carry *bool) domain.ManagerAdBudgetTarget {
	if !enabled {
		return domain.ManagerAdBudgetTarget{Enabled: false}
	}
	return domain.ManagerAdBudgetTarget{Enabled: true, AmountRub: &amount, CarryOverEnabled: carry}
}
func budgetChecks(unlimited bool) map[string]bool {
	checks := map[string]bool{"budget_target_checked": true, "budget_spend_checked": true, "vendor_limits_checked": true}
	if unlimited {
		checks["unlimited_spend_checked"] = true
	}
	return checks
}
func (e *pauseEnv) prepareBudget(t *testing.T, target domain.ManagerAdBudgetTarget) *domain.ManagerAdOperation {
	t.Helper()
	period := "weekly"
	if e.api.market == "wb" {
		period = "daily"
	}
	var caps *domain.ManagerAdBudgetConstraints
	if target.Enabled {
		caps = &domain.ManagerAdBudgetConstraints{MaxBudgetRub: "2000", MaxAbsoluteChangeRub: "300"}
	}
	op, err := e.svc.PrepareBudget(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, period, target, caps)
	require.NoError(t, err)
	return op
}
func (e *pauseEnv) approveBudget(op *domain.ManagerAdOperation) (*domain.ManagerAdOperation, error) {
	checks := budgetChecks(!op.BudgetPlan.Target.Enabled)
	if op.BudgetPlan.Target.Enabled && op.BudgetPlan.Target.CarryOverEnabled != nil && *op.BudgetPlan.Target.CarryOverEnabled && (!op.BudgetPlan.Before.Enabled || op.BudgetPlan.Before.CarryOverEnabled == nil || !*op.BudgetPlan.Before.CarryOverEnabled) {
		checks["carry_over_spend_checked"] = true
	}
	return e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token", op.Fingerprint, checks)
}

func TestManagerAdBudgetPersonallyApprovedExactNativeResult(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := budgetEnvironment(t, market, "ok")
			amount := "120.01"
			var carry *bool
			if market == "wb" {
				amount = "1200.01"
				v := false
				carry = &v
			}
			op := e.prepareBudget(t, budgetTarget(true, amount, carry))
			captureBudgetContract(t, market+"-on-prepared", op)
			require.Equal(t, "set_budget", op.Action)
			require.Equal(t, int32(0), e.api.writes.Load())
			_, err := e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token", op.Fingerprint, map[string]bool{"campaign_checked": true, "resume_spend_checked": true})
			require.Error(t, err)
			require.Equal(t, int32(0), e.api.writes.Load())
			final, err := e.approveBudget(op)
			require.NoError(t, err)
			require.Equal(t, "succeeded", final.Status)
			require.True(t, final.Result.BudgetVerified)
			require.True(t, final.Result.FieldsVerified)
			captureBudgetContract(t, market+"-on-finished", final)
			require.Equal(t, amount, *final.Result.Budget.AmountRub)
			require.True(t, final.Result.ControlHoldActive)
			require.Equal(t, int32(1), e.api.writes.Load())
			e.assertLegacyBlocked(t)
		})
	}
}

func captureBudgetContract(t *testing.T, name string, value any) {
	t.Helper()
	if dir := os.Getenv("SELYA_AD_BUDGET_APPROVAL_TEST_CONTRACT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		raw, err := json.Marshal(map[string]any{"data": value})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".json"), raw, 0600))
	}
}
func TestManagerAdBudgetWBDisableAndCarryConsent(t *testing.T) {
	for _, mode := range []string{"off", "carry"} {
		t.Run(mode, func(t *testing.T) {
			e := budgetEnvironment(t, "wb", "ok")
			carry := true
			target := budgetTarget(false, "", nil)
			if mode == "carry" {
				target = budgetTarget(true, "1200.01", &carry)
			}
			op := e.prepareBudget(t, target)
			_, err := e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, op.ID, "personal-token", op.Fingerprint, budgetChecks(false))
			require.Error(t, err)
			require.Equal(t, int32(0), e.api.writes.Load())
			final, err := e.approveBudget(op)
			require.NoError(t, err)
			require.Equal(t, "succeeded", final.Status)
			require.Equal(t, target.Enabled, final.Result.Budget.Enabled)
			require.NotNil(t, final.Result.MirrorUpdated)
			require.False(t, *final.Result.MirrorUpdated)
			require.Equal(t, int32(1), e.api.writes.Load())
			captureBudgetContract(t, "wb-"+mode+"-prepared", op)
			captureBudgetContract(t, "wb-"+mode+"-finished", final)
		})
	}
}
func TestManagerAdBudgetHistoryCannotOverrideTamperingOrCurrentPeriod(t *testing.T) {
	for _, kind := range []string{"fingerprint", "foreign_scope", "contradictory_period"} {
		t.Run(kind, func(t *testing.T) {
			e := budgetEnvironment(t, "ozon", "ok")
			off, err := e.approveBudget(e.prepareBudget(t, budgetTarget(false, "", nil)))
			require.NoError(t, err)
			pause, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "pause")
			require.NoError(t, err)
			_, err = e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token", pause.Fingerprint, map[string]bool{"campaign_checked": true, "pause_impact_checked": true})
			require.NoError(t, err)
			if kind == "fingerprint" {
				_, err = e.pool.Exec(context.Background(), `UPDATE manager_ad_operations SET fingerprint=repeat('f',64) WHERE id=$1`, off.ID)
			}
			if kind == "foreign_scope" {
				_, err = e.pool.Exec(context.Background(), `UPDATE manager_ad_operations SET external_workspace_id='4' WHERE id=$1`, off.ID)
			}
			if kind == "contradictory_period" {
				e.api.mu.Lock()
				e.api.dailyBudgetOzon = "100000000"
				e.api.mu.Unlock()
			}
			require.NoError(t, err)
			_, err = e.svc.PrepareBudget(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "weekly", budgetTarget(true, "120", nil), &domain.ManagerAdBudgetConstraints{MaxBudgetRub: "200", MaxAbsoluteChangeRub: "150"})
			require.Error(t, err)
			require.Equal(t, int32(2), e.api.writes.Load())
		})
	}
}
func TestManagerAdBudgetUnsentExpiryRestoresPreviousBudget(t *testing.T) {
	e := budgetEnvironment(t, "ozon", "ok")
	first, err := e.approveBudget(e.prepareBudget(t, budgetTarget(true, "120", nil)))
	require.NoError(t, err)
	second := e.prepareBudget(t, budgetTarget(true, "140", nil))
	second.CreatedAt = time.Now().UTC().Truncate(time.Microsecond).Add(-4*time.Minute - 59*time.Second)
	second.ExpiresAt = second.CreatedAt.Add(5 * time.Minute)
	second.Fingerprint = adOperationFingerprint(*second)
	_, err = e.pool.Exec(context.Background(), `UPDATE manager_ad_operations SET created_at=$2,expires_at=$3,fingerprint=$4 WHERE id=$1`, second.ID, second.CreatedAt, second.ExpiresAt, second.Fingerprint)
	require.NoError(t, err)
	require.NoError(t, e.q.ClaimManagerAdOperation(context.Background(), *second))
	require.NoError(t, e.q.ReserveManagerAdCommand(context.Background(), *second))
	time.Sleep(time.Until(second.ExpiresAt) + 20*time.Millisecond)
	current, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, second.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "expired", current.Status)
	previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, first.ID, "personal-token")
	require.NoError(t, err)
	require.True(t, previous.Result.ControlHoldActive)
	require.Nil(t, previous.Result.ControlTransferredTo)
	require.Equal(t, int32(1), e.api.writes.Load())
	e.assertLegacyBlocked(t)
}
