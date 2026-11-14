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
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/require"
)

func (e *pauseEnv) prepareControl(t *testing.T, action string) *domain.ManagerAdOperation {
	t.Helper()
	o, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, action)
	require.NoError(t, err)
	return o
}
func (e *pauseEnv) approveControl(o *domain.ManagerAdOperation) (*domain.ManagerAdOperation, error) {
	impact := map[string]string{"pause": "pause_impact_checked", "resume": "resume_spend_checked", "handover": "handover_impact_checked"}[o.Action]
	return e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint, map[string]bool{"campaign_checked": true, impact: true})
}
func (e *pauseEnv) assertLegacyBlocked(t *testing.T) {
	t.Helper()
	var err error
	if e.api.market == "wb" {
		err = e.wb.StartCampaign(context.Background(), e.token, 101)
	} else {
		err = e.perf.ActivateCampaign(context.Background(), ozonClientCreds(testdb.OzonCredentials()), 101)
	}
	require.True(t, adguard.WasNotSent(err), "managed or unresolved control must block a legacy write")
}

func TestManagerAdControlResumeThenHandover(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			pause, err := e.approve(e.prepare(t))
			require.NoError(t, err)
			require.Equal(t, "succeeded", pause.Status)
			resume := e.prepareControl(t, "resume")
			require.Equal(t, int32(1), e.api.writes.Load(), "preparation must not activate a campaign")
			_, err = e.approve(resume)
			require.Error(t, err, "pause checks cannot authorize new spending")
			resumed, err := e.approveControl(resume)
			require.NoError(t, err)
			require.Equal(t, "succeeded", resumed.Status)
			require.True(t, resumed.Result.ControlHoldActive)
			require.Equal(t, int32(2), e.api.writes.Load())
			previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token")
			require.NoError(t, err)
			require.False(t, previous.Result.ControlHoldActive)
			e.assertLegacyBlocked(t)
			handover := e.prepareControl(t, "handover")
			require.Equal(t, int32(2), e.api.writes.Load())
			released, err := e.approveControl(handover)
			require.NoError(t, err)
			require.Equal(t, "succeeded", released.Status)
			require.True(t, released.Result.FieldsVerified)
			require.False(t, released.Result.ControlHoldActive)
			require.Equal(t, int32(2), e.api.writes.Load(), "handover changes only local control, with no vendor mutation")
			released, err = e.approveControl(handover)
			require.NoError(t, err)
			require.Equal(t, "succeeded", released.Status)
			if market == "wb" {
				err = e.wb.PauseCampaign(context.Background(), e.token, 101)
			} else {
				err = e.perf.DeactivateCampaign(context.Background(), ozonClientCreds(testdb.OzonCredentials()), 101)
			}
			require.NoError(t, err)
			require.Equal(t, int32(3), e.api.writes.Load(), "old control is available only after the separate personal handover")
		})
	}
}

func TestManagerAdControlDefiniteResumeRejectionRestoresPause(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			pause, err := e.approve(e.prepare(t))
			require.NoError(t, err)
			resume := e.prepareControl(t, "resume")
			e.api.mu.Lock()
			e.api.mode = "reject"
			e.api.mu.Unlock()
			failed, err := e.approveControl(resume)
			require.NoError(t, err)
			require.Equal(t, "failed", failed.Status)
			previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token")
			require.NoError(t, err)
			require.True(t, previous.Result.ControlHoldActive)
			e.assertLegacyBlocked(t)
			require.Equal(t, int32(2), e.api.writes.Load())
		})
	}
}

func TestManagerAdControlUnknownResumeCannotReleaseOrReplay(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			_, err := e.approve(e.prepare(t))
			require.NoError(t, err)
			resume := e.prepareControl(t, "resume")
			e.api.mu.Lock()
			e.api.mode = "unknown"
			e.api.mu.Unlock()
			pending, err := e.approveControl(resume)
			require.NoError(t, err)
			require.Equal(t, "unknown", pending.Status)
			_, err = e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "handover")
			require.Error(t, err)
			pending, err = e.approveControl(resume)
			require.NoError(t, err)
			require.Equal(t, "unknown", pending.Status)
			e.assertLegacyBlocked(t)
			require.Equal(t, int32(2), e.api.writes.Load())
		})
	}
}

func TestManagerAdControlResumeRequiresConfirmedBudgetAndManagedPause(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			_, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "resume")
			require.Error(t, err)
			require.Zero(t, e.api.writes.Load())
			_, err = e.approve(e.prepare(t))
			require.NoError(t, err)
			for _, kind := range []string{"missing", "zero", "currency"} {
				t.Run(kind, func(t *testing.T) {
					e.api.mu.Lock()
					e.api.budget = 0
					e.api.budgetMissing = kind == "missing"
					e.api.budgetCurrency = "RUB"
					e.api.weeklyBudget = "0"
					if kind == "missing" {
						e.api.weeklyBudget = ""
					}
					if kind == "currency" {
						e.api.budget = 1000
						e.api.budgetCurrency = "KZT"
					}
					e.api.mu.Unlock()
					_, err = e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "resume")
					require.Error(t, err)
					require.Equal(t, int32(1), e.api.writes.Load())
				})
			}
			e.assertLegacyBlocked(t)
		})
	}
}

func TestManagerAdControlResumePreflightRestoresOldControl(t *testing.T) {
	for _, kind := range []string{"rights", "snapshot", "budget"} {
		t.Run(kind, func(t *testing.T) {
			e := newPauseEnv(t, "ozon", "ok")
			pause, err := e.approve(e.prepare(t))
			require.NoError(t, err)
			resume := e.prepareControl(t, "resume")
			underHold := e.authority.calls.Load() + 2
			if kind == "rights" {
				e.authority.revokeAt = underHold
			} else {
				e.authority.hook = func(n int32) {
					if n == underHold {
						e.api.mu.Lock()
						defer e.api.mu.Unlock()
						if kind == "budget" {
							e.api.weeklyBudget = "123456789"
						} else {
							e.api.name = "Changed native name"
						}
					}
				}
			}
			failed, err := e.approveControl(resume)
			require.NoError(t, err)
			require.Equal(t, "failed", failed.Status)
			require.Equal(t, int32(1), e.api.writes.Load())
			old, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token")
			require.NoError(t, err)
			require.True(t, old.Result.ControlHoldActive)
			require.Nil(t, old.Result.ControlTransferredTo)
			e.assertLegacyBlocked(t)
		})
	}
}

func TestManagerAdControlUnsentExpiryAndAbortRestorePreviousPause(t *testing.T) {
	for _, kind := range []string{"expired", "abort"} {
		t.Run(kind, func(t *testing.T) {
			e := newPauseEnv(t, "ozon", "ok")
			pause, err := e.approve(e.prepare(t))
			require.NoError(t, err)
			resume := e.prepareControl(t, "resume")
			require.NoError(t, e.q.ClaimManagerAdOperation(context.Background(), *resume))
			require.NoError(t, e.q.ReserveManagerAdCommand(context.Background(), *resume))
			if kind == "expired" {
				resume.CreatedAt = resume.CreatedAt.Add(-10 * time.Minute)
				resume.ExpiresAt = resume.ExpiresAt.Add(-10 * time.Minute)
				resume.Fingerprint = adOperationFingerprint(*resume)
				_, err = e.pool.Exec(context.Background(), `UPDATE manager_ad_operations SET created_at=$2,expires_at=$3,fingerprint=$4 WHERE id=$1`, resume.ID, resume.CreatedAt, resume.ExpiresAt, resume.Fingerprint)
				require.NoError(t, err)
				_, err = e.pool.Exec(context.Background(), `UPDATE ad_native_commands SET approval_expires_at=$2,approval_fingerprint=$3 WHERE id=$1`, resume.ID, resume.ExpiresAt, resume.Fingerprint)
				require.NoError(t, err)
			} else {
				require.NoError(t, e.q.RejectManagerAdBeforeSend(context.Background(), *resume, "preflight_changed"))
			}
			current, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, resume.ID, "personal-token")
			require.NoError(t, err)
			if kind == "expired" {
				require.Equal(t, "expired", current.Status)
			} else {
				require.Equal(t, "failed", current.Status)
			}
			old, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token")
			require.NoError(t, err)
			require.True(t, old.Result.ControlHoldActive)
			command, err := e.q.ReadNativeAdCommand(context.Background(), resume.ID)
			require.NoError(t, err)
			require.Equal(t, "rejected", command.Status)
			require.False(t, command.StartedAt.Valid)
			e.assertLegacyBlocked(t)
			require.Equal(t, int32(1), e.api.writes.Load())
		})
	}
}

func TestManagerAdControlLostResumeCanBeObservedAndNeverResent(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	_, err := e.approve(e.prepare(t))
	require.NoError(t, err)
	resume := e.prepareControl(t, "resume")
	e.api.mu.Lock()
	e.api.mode = "lost"
	e.api.mu.Unlock()
	current, err := e.approveControl(resume)
	require.NoError(t, err)
	require.Equal(t, "succeeded", current.Status)
	require.True(t, current.Result.FieldsVerified)
	require.False(t, current.Result.SubmissionAcknowledged)
	require.True(t, current.Result.ControlHoldActive)
	_, err = e.approveControl(resume)
	require.NoError(t, err)
	require.Equal(t, int32(2), e.api.writes.Load())
	e.assertLegacyBlocked(t)
}

func TestManagerAdControlConcurrentResumeSendsOnce(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	_, err := e.approve(e.prepare(t))
	require.NoError(t, err)
	resume := e.prepareControl(t, "resume")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.approveControl(resume) }()
	}
	wg.Wait()
	current, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, resume.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "succeeded", current.Status)
	require.Equal(t, int32(2), e.api.writes.Load())
}

func TestManagerAdControlHandoverCannotBeReusedAndLosingProposalCloses(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	_, err := e.approve(e.prepare(t))
	require.NoError(t, err)
	winner := e.prepareControl(t, "handover")
	loser := e.prepareControl(t, "resume")
	// The loser was already claimed, but did not acquire or transfer any resource.
	require.NoError(t, e.q.ClaimManagerAdOperation(context.Background(), *loser))
	released, err := e.approveControl(winner)
	require.NoError(t, err)
	require.Equal(t, "succeeded", released.Status)
	require.Error(t, e.q.ReserveManagerAdCommand(context.Background(), *loser))
	require.NoError(t, e.q.RejectManagerAdBeforeSend(context.Background(), *loser, "reservation_unavailable"))
	closed, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, loser.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "failed", closed.Status)
	_, err = e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "handover")
	require.Error(t, err)
	require.Equal(t, int32(1), e.api.writes.Load())
}

func TestManagerAdControlStartedTransportCannotBeAbortedAsUnsent(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	_, err := e.approve(e.prepare(t))
	require.NoError(t, err)
	resume := e.prepareControl(t, "resume")
	require.NoError(t, e.q.ClaimManagerAdOperation(context.Background(), *resume))
	require.NoError(t, e.q.ReserveManagerAdCommand(context.Background(), *resume))
	// A transport transaction won the row lock, but its commit is not visible to
	// the aborting caller yet. The abort must wait and recheck the started marker.
	tx, err := e.pool.Begin(context.Background())
	require.NoError(t, err)
	defer tx.Rollback(context.Background())
	var nativePID int
	require.NoError(t, tx.QueryRow(context.Background(), `SELECT pg_backend_pid()`).Scan(&nativePID))
	_, err = tx.Exec(context.Background(), `UPDATE ad_native_commands SET status='dispatching',started_at=statement_timestamp() WHERE id=$1`, resume.ID)
	require.NoError(t, err)
	aborted := make(chan error, 1)
	go func() { aborted <- e.q.RejectManagerAdBeforeSend(context.Background(), *resume, "preflight_changed") }()
	require.Eventually(t, func() bool {
		var blocked bool
		err := e.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, nativePID).Scan(&blocked)
		return err == nil && blocked
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit(context.Background()))
	require.Error(t, <-aborted)
	command, err := e.q.ReadNativeAdCommand(context.Background(), resume.ID)
	require.NoError(t, err)
	require.True(t, command.StartedAt.Valid)
	e.assertLegacyBlocked(t)
}

func TestManagerAdControlMigrationCannotEraseControlHistory(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000072_manager_ad_control.down.sql")
	require.NoError(t, err)
	for _, history := range []bool{false, true} {
		t.Run(map[bool]string{false: "pause_only", true: "with_resume_history"}[history], func(t *testing.T) {
			e := newPauseEnv(t, "ozon", "ok")
			pause := e.prepare(t)
			if history {
				_, err = e.approve(pause)
				require.NoError(t, err)
				_ = e.prepareControl(t, "resume")
			}
			_, err = e.pool.Exec(context.Background(), string(raw))
			if history {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestManagerAdControlPublicWireContracts(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			pausePrepared := e.prepare(t)
			pause, err := e.approve(pausePrepared)
			require.NoError(t, err)
			resume := e.prepareControl(t, "resume")
			resumed, err := e.approveControl(resume)
			require.NoError(t, err)
			require.Equal(t, "succeeded", resumed.Status)
			handover := e.prepareControl(t, "handover")
			released, err := e.approveControl(handover)
			require.NoError(t, err)
			require.Equal(t, "succeeded", released.Status)
			previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token")
			require.NoError(t, err)
			if dir := os.Getenv("SELYA_AD_CONTROL_TEST_CONTRACT_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				values := map[string]*domain.ManagerAdOperation{"pause-prepared": pausePrepared, "pause-transferred": previous, "resume-prepared": resume, "resume-finished": resumed, "handover-prepared": handover, "handover-finished": released}
				for name, value := range values {
					raw, err := json.Marshal(map[string]any{"data": value})
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(dir, market+"-"+name+".json"), raw, 0600))
				}
			}
		})
	}
}

func TestManagerAdControlCanPauseResumedCampaignWithoutReleasingItToOldRules(t *testing.T) {
	e := newPauseEnv(t, "ozon", "ok")
	_, err := e.approve(e.prepare(t))
	require.NoError(t, err)
	resume, err := e.approveControl(e.prepareControl(t, "resume"))
	require.NoError(t, err)
	require.Equal(t, "succeeded", resume.Status)
	pauseAgain := e.prepareControl(t, "pause")
	require.NotNil(t, pauseAgain.PreviousOperationID)
	require.Equal(t, resume.ID, *pauseAgain.PreviousOperationID)
	paused, err := e.approveControl(pauseAgain)
	require.NoError(t, err)
	require.Equal(t, "succeeded", paused.Status)
	require.True(t, paused.Result.ControlHoldActive)
	require.Equal(t, "CAMPAIGN_STATE_INACTIVE", *paused.Result.VendorState)
	require.Equal(t, int32(3), e.api.writes.Load())
	previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, resume.ID, "personal-token")
	require.NoError(t, err)
	require.False(t, previous.Result.ControlHoldActive)
	require.Equal(t, paused.ID, *previous.Result.ControlTransferredTo)
	e.assertLegacyBlocked(t)
}

func TestManagerAdControlHandoverFinalChecksCannotReleaseOldDecision(t *testing.T) {
	for _, kind := range []string{"rights", "snapshot"} {
		t.Run(kind, func(t *testing.T) {
			e := newPauseEnv(t, "ozon", "ok")
			pause, err := e.approve(e.prepare(t))
			require.NoError(t, err)
			handover := e.prepareControl(t, "handover")
			finalCheck := e.authority.calls.Load() + 2
			if kind == "rights" {
				e.authority.revokeAt = finalCheck
			} else {
				e.authority.hook = func(n int32) {
					if n == finalCheck {
						e.api.mu.Lock()
						e.api.name = "Changed before local release"
						e.api.mu.Unlock()
					}
				}
			}
			rejected, err := e.approveControl(handover)
			require.NoError(t, err)
			require.Equal(t, "failed", rejected.Status)
			require.Equal(t, int32(1), e.api.writes.Load())
			previous, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, pause.ID, "personal-token")
			require.NoError(t, err)
			require.True(t, previous.Result.ControlHoldActive)
			require.Nil(t, previous.Result.ControlTransferredTo)
			e.assertLegacyBlocked(t)
		})
	}
}
