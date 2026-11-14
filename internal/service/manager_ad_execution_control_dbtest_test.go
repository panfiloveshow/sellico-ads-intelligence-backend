package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// A repository-only fixture. Public service tests below will obtain native
// account proof and exact current CRM identity before reaching this seam.
func executionControlProposal(t *testing.T, q *sqlcgen.Queries, r adguard.Request, mode string) domain.AdExecutionControlOperation {
	return executionControlProposalLifetime(t, q, r, mode, 5*time.Minute)
}
func executionControlProposalLifetime(t *testing.T, q *sqlcgen.Queries, r adguard.Request, mode string, lifetime time.Duration) domain.AdExecutionControlOperation {
	t.Helper()
	ctx := context.Background()
	before, err := q.AdExecutionControl(ctx, r.AccountKey, r.Marketplace)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := domain.AdExecutionControlOperation{ID: uuid.New(), WorkspaceID: uuid.New(), ActorID: uuid.New(), ExternalUserID: "42", ExternalWorkspaceID: "8", IntegrationID: "28", IntegrationName: "Isolated control fixture", CabinetID: uuid.New(), Marketplace: r.Marketplace, AccountKey: r.AccountKey, CredentialDigest: strings.Repeat("c", 64), Scope: "configured_advertising_account", TargetMode: mode, Before: before, Reason: "Explicit isolated personal control decision", Fingerprint: strings.Repeat("a", 64), Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(lifetime)}
	require.NoError(t, q.CreateAdExecutionControlOperation(ctx, o))
	return o
}
func applyExecutionControl(t *testing.T, q *sqlcgen.Queries, r adguard.Request, mode string) domain.AdExecutionControlOperation {
	t.Helper()
	o := executionControlProposal(t, q, r, mode)
	require.NoError(t, q.ApplyAdExecutionControl(context.Background(), o, func(context.Context) error { return nil }))
	return o
}

func TestAdExecutionControlStopsBothNativeClientsAndChangingGETWithoutSending(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	var writes, reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/client/token":
			_, _ = w.Write([]byte(`{"access_token":"isolated-token","expires_in":1800}`))
		case "/api/client/campaign":
			reads.Add(1)
			_, _ = w.Write([]byte(`{"list":[{"id":"101","title":"Native proof","state":"CAMPAIGN_STATE_RUNNING"}]}`))
		default:
			writes.Add(1)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	token := testAdvertisingToken()
	wbRequest, err := adguard.NewWBRequest("GET", srv.URL+"/adv/v0/pause?id=101", token, nil)
	require.NoError(t, err)
	wbStop := applyExecutionControl(t, q, wbRequest, "stopped")
	wbc := wb.NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop()).WithAdGuard(q)
	require.True(t, adguard.WasNotSent(wbc.PauseCampaign(ctx, token, 101)))
	require.True(t, adguard.WasNotSent(wbc.StartCampaign(ctx, token, 102)))
	creds := ozon.Credentials{PerfClientID: "isolated-control-account", PerfClientSecret: "isolated-only-secret"}
	or := adTestRequest(t, creds.PerfClientID, []string{"101"})
	applyExecutionControl(t, q, or, "stopped")
	oc := ozon.NewPerfClient(&config.Config{OzonPerfAPIBaseURL: srv.URL}, zerolog.Nop()).WithAdGuard(q)
	require.True(t, adguard.WasNotSent(oc.ActivateCampaign(ctx, creds, 101)))
	require.True(t, adguard.WasNotSent(oc.SetAllSKUPromoRate(ctx, creds, 7)))
	require.True(t, adguard.WasNotSent(oc.DeactivateAllSKUPromo(ctx, creds)))
	observed, err := oc.ReadCampaignContext(ctx, creds, 101)
	require.NoError(t, err)
	require.Equal(t, "CAMPAIGN_STATE_RUNNING", observed.Status)
	require.Equal(t, int32(1), reads.Load())
	require.Zero(t, writes.Load())
	// A changed credential token with the same WB seller claim uses the same stop.
	differentToken := strings.Replace(token, ".test", ".other-signature", 1)
	require.True(t, adguard.WasNotSent(wbc.PauseCampaign(ctx, differentToken, 103)))
	require.Zero(t, writes.Load())
	row, err := q.GetAdExecutionControlOperation(ctx, wbStop.ID, wbStop.WorkspaceID, wbStop.ActorID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", row.Status)
	require.Equal(t, "stopped", row.ResultControl.Mode)
	require.False(t, row.NativeCampaignsChanged)
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	require.NotContains(t, string(raw), wbRequest.AccountKey)
	require.NotContains(t, string(raw), strings.Repeat("c", 64))
	require.NotContains(t, string(raw), "credential_digest")
}

func TestAdExecutionControlManualOnlyRejectsOldConsentAndLegacyButAllowsNewOneUseCommand(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "manual-control-account", []string{"101"})
	old := time.Now().Add(-time.Minute)
	applyExecutionControl(t, q, r, "stopped")
	applyExecutionControl(t, q, r, "manual_only")
	actor, workspace := uuidToPgtype(uuid.New()), uuidToPgtype(uuid.New())
	require.ErrorIs(t, q.ReserveApprovedAdCommand(ctx, uuid.New(), r, workspace, actor, strings.Repeat("a", 64), old, old.Add(5*time.Minute)), sqlcgen.ErrAdExecutionStopped)
	_, err := q.BeginAdvertisingMutation(ctx, r)
	require.ErrorIs(t, err, sqlcgen.ErrAdExecutionStopped)
	id := uuid.New()
	created := time.Now()
	require.NoError(t, q.ReserveApprovedAdCommand(ctx, id, r, workspace, actor, strings.Repeat("a", 64), created, created.Add(time.Minute)))
	gate := adguard.WithApprovedCommandGate(ctx, id, func(context.Context, adguard.Request) error { return nil })
	lease, err := q.BeginAdvertisingMutation(gate, r)
	require.NoError(t, err)
	require.NoError(t, q.CompleteAdvertisingMutation(ctx, lease, adguard.Result(200, []byte(`{}`), nil)))
	_, err = q.BeginAdvertisingMutation(gate, r)
	require.Error(t, err)
	other := adTestRequest(t, "manual-control-account", []string{"102"})
	_, err = q.BeginAdvertisingMutation(ctx, other)
	require.ErrorIs(t, err, sqlcgen.ErrAdExecutionStopped)
	state, err := q.AdExecutionControl(ctx, r.AccountKey, r.Marketplace)
	require.NoError(t, err)
	require.Equal(t, "manual_only", state.Mode)
	require.EqualValues(t, 2, state.Version)
}

func TestAdExecutionControlEpochFencesReservedCommandEvenAfterManualReopening(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "epoch-control-account", []string{"101"})
	id := uuid.New()
	created := time.Now()
	require.NoError(t, q.ReserveApprovedAdCommand(ctx, id, r, uuidToPgtype(uuid.New()), uuidToPgtype(uuid.New()), strings.Repeat("a", 64), created, created.Add(time.Minute)))
	applyExecutionControl(t, q, r, "stopped")
	applyExecutionControl(t, q, r, "manual_only")
	gate := adguard.WithApprovedCommandGate(ctx, id, func(context.Context, adguard.Request) error { return nil })
	_, err := q.BeginAdvertisingMutation(gate, r)
	require.ErrorIs(t, err, sqlcgen.ErrAdExecutionStopped)
	command, err := q.ReadNativeAdCommand(ctx, id)
	require.NoError(t, err)
	require.False(t, command.StartedAt.Valid)
	require.Equal(t, "approved", command.Status)
}

func TestAdExecutionControlAllowsReceiptOfStartedRequestAndNeverReleasesUnknownHold(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "inflight-control-account", []string{"101"})
	lease, err := q.BeginAdvertisingMutation(ctx, r)
	require.NoError(t, err)
	applyExecutionControl(t, q, r, "stopped")
	require.NoError(t, q.CompleteAdvertisingMutation(ctx, lease, adguard.Result(0, nil, errors.New("isolated lost result"))))
	state, err := q.AdExecutionControl(ctx, r.AccountKey, r.Marketplace)
	require.NoError(t, err)
	require.Equal(t, "stopped", state.Mode)
	row, err := q.ReadNativeAdCommand(ctx, lease.ID)
	require.NoError(t, err)
	require.Equal(t, "unknown", row.Status)
	var holds int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1`, lease.ID).Scan(&holds))
	require.Equal(t, 1, holds)
	_, err = q.BeginAdvertisingMutation(ctx, adTestRequest(t, "inflight-control-account", []string{"102"}))
	require.ErrorIs(t, err, sqlcgen.ErrAdExecutionStopped)
}

func TestAdExecutionControlConcurrentApprovalsAreOneDurableVersionAndOldProposalIsStale(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "parallel-control-account", []string{"101"})
	first := executionControlProposal(t, q, r, "stopped")
	stale := executionControlProposal(t, q, r, "manual_only")
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- q.ApplyAdExecutionControl(ctx, first, func(context.Context) error { return nil })
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, sqlcgen.ErrAdResourceHeld)
		}
	}
	require.GreaterOrEqual(t, successes, 1)
	state, err := q.AdExecutionControl(ctx, r.AccountKey, r.Marketplace)
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Version)
	require.ErrorIs(t, q.ApplyAdExecutionControl(ctx, stale, func(context.Context) error { return nil }), sqlcgen.ErrAdExecutionControlChanged)
}

func TestAdExecutionControlRechecksAccessUnderNativeLockAndExpiryRollsBackEverything(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "access-control-account", []string{"101"})
	o := executionControlProposal(t, q, r, "stopped")
	require.Error(t, q.ApplyAdExecutionControl(ctx, o, func(context.Context) error { return errors.New("isolated revoked permission") }))
	state, err := q.AdExecutionControl(ctx, r.AccountKey, r.Marketplace)
	require.NoError(t, err)
	require.EqualValues(t, 0, state.Version)
	entered, released := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- q.ApplyAdExecutionControl(ctx, o, func(context.Context) error { close(entered); <-released; return nil })
	}()
	<-entered
	_, err = q.BeginAdvertisingMutation(ctx, r)
	require.ErrorIs(t, err, sqlcgen.ErrAdResourceHeld)
	close(released)
	require.NoError(t, <-done)
	expiredRequest := adTestRequest(t, "expired-control-account", []string{"101"})
	expired := executionControlProposal(t, q, expiredRequest, "stopped")
	_, err = pool.Exec(ctx, `UPDATE manager_ad_execution_control_operations SET created_at=created_at-interval '10 minutes',expires_at=expires_at-interval '10 minutes' WHERE id=$1`, expired.ID)
	require.NoError(t, err)
	// Metadata corruption cannot substitute a different consent lifetime.
	require.Error(t, q.ApplyAdExecutionControl(ctx, expired, func(context.Context) error { return nil }))
	state, err = q.AdExecutionControl(ctx, expiredRequest.AccountKey, expiredRequest.Marketplace)
	require.NoError(t, err)
	require.EqualValues(t, 0, state.Version)
}

func TestAdExecutionControlDowngradeCannotDiscardAnyPersonalAudit(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "down-control-account", []string{"101"})
	executionControlProposal(t, q, r, "stopped")
	migration, err := os.ReadFile("../../migrations/000076_ad_account_execution_control.down.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(migration))
	require.ErrorContains(t, err, "cannot discard personal account execution controls")
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM manager_ad_execution_control_operations`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAdExecutionControlConsentExpiringDuringFinalAccessCheckRollsBackControlAndAudit(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "expiry-during-access-account", []string{"101"})
	o := executionControlProposalLifetime(t, q, r, "stopped", 2*time.Second)
	err := q.ApplyAdExecutionControl(ctx, o, func(context.Context) error {
		require.True(t, time.Now().Before(o.ExpiresAt))
		time.Sleep(time.Until(o.ExpiresAt) + 20*time.Millisecond)
		return nil
	})
	require.ErrorIs(t, err, sqlcgen.ErrAdExecutionControlChanged)
	state, err := q.AdExecutionControl(ctx, r.AccountKey, r.Marketplace)
	require.NoError(t, err)
	require.Equal(t, "legacy", state.Mode)
	require.Zero(t, state.Version)
	saved, err := q.GetAdExecutionControlOperation(ctx, o.ID, o.WorkspaceID, o.ActorID)
	require.NoError(t, err)
	require.Equal(t, "awaiting_confirmation", saved.Status)
	require.Nil(t, saved.ResultControl)
	require.NoError(t, q.ExpireAdExecutionControlOperation(ctx, saved))
	saved, err = q.GetAdExecutionControlOperation(ctx, o.ID, o.WorkspaceID, o.ActorID)
	require.NoError(t, err)
	require.Equal(t, "expired", saved.Status)
}

func controlChecks() map[string]bool {
	return map[string]bool{"advertising_account_scope_checked": true, "advertising_service_effect_checked": true, "advertising_reopening_checked": true}
}
func preparePersonalExecutionControl(t *testing.T, s *ManagerAdExecutionControlService, e *pauseEnv, mode string) *domain.AdExecutionControlOperation {
	t.Helper()
	ctx := context.Background()
	read, err := s.Context(ctx, managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign)
	require.NoError(t, err)
	o, err := s.Prepare(ctx, managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, mode, "Личное ограничение команд сервиса", read.ContextFingerprint)
	require.NoError(t, err)
	return o
}
func approvePersonalExecutionControl(s *ManagerAdExecutionControlService, e *pauseEnv, o *domain.AdExecutionControlOperation) (*domain.AdExecutionControlOperation, error) {
	return s.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint, controlChecks())
}

func TestAdExecutionControlPersonalLifecycleStopsAccountAndReopensOnlyNewManualApproval(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			s := NewManagerAdExecutionControlService(e.q, e.authority, e.svc.context, true)
			ctx := context.Background()
			// This native proposal predates the account restriction. Reopening must not wake it.
			old := e.prepare(t)
			o := preparePersonalExecutionControl(t, s, e, "stopped")
			require.Equal(t, "legacy", o.Before.Mode)
			require.Equal(t, "configured_advertising_account", o.Scope)
			require.Equal(t, "Native campaign", o.Proof.CampaignName)
			require.True(t, o.ApprovalAvailable)
			require.Zero(t, e.api.writes.Load())
			require.False(t, e.api.paused)
			_, err := s.Approve(ctx, managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint, nil)
			require.Error(t, err)
			_, err = s.Receipt(ctx, managerAdsIdentity(), e.workspace, uuid.New(), o.ID, "personal-token")
			require.Error(t, err)
			stopped, err := approvePersonalExecutionControl(s, e, o)
			require.NoError(t, err)
			require.Equal(t, "succeeded", stopped.Status)
			require.Equal(t, "stopped", stopped.ResultControl.Mode)
			require.False(t, stopped.NativeCampaignsChanged)
			require.Zero(t, e.api.writes.Load())
			require.False(t, e.api.paused)
			oldClosed, err := e.approve(old)
			require.NoError(t, err)
			require.Equal(t, "failed", oldClosed.Status)
			require.Zero(t, e.api.writes.Load())
			openProposal := preparePersonalExecutionControl(t, s, e, "manual_only")
			opened, err := approvePersonalExecutionControl(s, e, openProposal)
			require.NoError(t, err)
			require.Equal(t, "manual_only", opened.CurrentControl.Mode)
			require.Zero(t, e.api.writes.Load())
			require.False(t, e.api.paused)
			// Historical agreement and current account mode are distinct in the DTO.
			historical, err := s.Receipt(ctx, managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
			require.NoError(t, err)
			require.Equal(t, "stopped", historical.ResultControl.Mode)
			require.Equal(t, "manual_only", historical.CurrentControl.Mode)
			repeated, err := approvePersonalExecutionControl(s, e, o)
			require.NoError(t, err)
			require.EqualValues(t, 2, repeated.CurrentControl.Version)
			if dir := os.Getenv("SELYA_AD_EXECUTION_CONTROL_CONTRACT_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				values := map[string]any{"stop-proposal": o, "stop-saved": stopped, "manual-proposal": openProposal, "manual-saved": opened, "historical-after-opening": historical}
				for name, value := range values {
					raw, marshalErr := json.Marshal(map[string]any{"data": value})
					require.NoError(t, marshalErr)
					require.NoError(t, os.WriteFile(filepath.Join(dir, market+"-"+name+".json"), raw, 0600))
				}
			}
			nowPersonal := e.prepare(t)
			done, err := e.approve(nowPersonal)
			require.NoError(t, err)
			require.Equal(t, "succeeded", done.Status)
			require.True(t, done.Result.FieldsVerified)
			require.Equal(t, int32(1), e.api.writes.Load())
			beforeWrites := e.api.writes.Load()
			target, err := e.svc.context.resolveTarget(ctx, managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign)
			require.NoError(t, err)
			legacy, err := e.svc.transport(target, "resume")
			require.NoError(t, err)
			require.True(t, adguard.WasNotSent(legacy.send(ctx)))
			require.Equal(t, beforeWrites, e.api.writes.Load())
			raw, err := json.Marshal(historical)
			require.NoError(t, err)
			require.NotContains(t, string(raw), e.token)
			require.NotContains(t, string(raw), "AccountKey")
			require.NotContains(t, string(raw), "credential_digest")
			require.NotContains(t, string(raw), "actor_id")
		})
	}
}
func TestAdExecutionControlPersonalRightsCredentialVersionAndTamperedScopeAreRechecked(t *testing.T) {
	for _, scenario := range []string{"revoked_after_lookup", "rotated_key", "payload_tamper", "not_personal", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			e := newPauseEnv(t, "wb", "ok")
			s := NewManagerAdExecutionControlService(e.q, e.authority, e.svc.context, true)
			o := preparePersonalExecutionControl(t, s, e, "stopped")
			ctx := context.Background()
			switch scenario {
			case "revoked_after_lookup":
				e.authority.revokeAt = e.authority.calls.Load() + 3
			case "rotated_key":
				encrypted, err := crypto.Encrypt(e.token+"-rotation", testdb.EncryptionKey)
				require.NoError(t, err)
				_, err = e.pool.Exec(ctx, `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, e.cabinet, encrypted)
				require.NoError(t, err)
			case "payload_tamper":
				_, err := e.pool.Exec(ctx, `UPDATE manager_ad_execution_control_operations SET immutable_payload=jsonb_set(immutable_payload,'{operation,target_mode}','"manual_only"') WHERE id=$1`, o.ID)
				require.NoError(t, err)
			case "not_personal":
				e.authority.personal = false
			case "disabled":
				s.enabled = false
			}
			_, err := approvePersonalExecutionControl(s, e, o)
			require.Error(t, err)
			state, err := e.q.AdExecutionControl(ctx, o.AccountKey, o.Marketplace)
			require.NoError(t, err)
			require.EqualValues(t, 0, state.Version)
			require.Zero(t, e.api.writes.Load())
		})
	}
}
func TestAdExecutionControlCurrentContextAndStaleProposalCannotMoveAnotherAccountVersion(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	s := NewManagerAdExecutionControlService(e.q, e.authority, e.svc.context, true)
	ctx := context.Background()
	initial, err := s.Context(ctx, managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign)
	require.NoError(t, err)
	stop := preparePersonalExecutionControl(t, s, e, "stopped")
	stale := preparePersonalExecutionControl(t, s, e, "manual_only")
	_, err = approvePersonalExecutionControl(s, e, stop)
	require.NoError(t, err)
	_, err = approvePersonalExecutionControl(s, e, stale)
	require.Error(t, err)
	_, err = s.Prepare(ctx, managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "manual_only", "Проверенная причина", initial.ContextFingerprint)
	require.Error(t, err)
	current, err := s.Context(ctx, managerAdsIdentity(), e.workspace, "personal-token", "28", e.campaign)
	require.NoError(t, err)
	require.EqualValues(t, 1, current.Control.Version)
	require.Equal(t, "stopped", current.Control.Mode)
	require.Zero(t, e.api.writes.Load())
}
func TestAdExecutionControlCancellationExpiryAndDisabledReadNeverReplayControlChange(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	s := NewManagerAdExecutionControlService(e.q, e.authority, e.svc.context, true)
	ctx := context.Background()
	cancelled := preparePersonalExecutionControl(t, s, e, "stopped")
	row, err := s.Cancel(ctx, managerAdsIdentity(), e.workspace, e.actor, cancelled.ID, "personal-token", cancelled.Fingerprint)
	require.NoError(t, err)
	require.Equal(t, "cancelled", row.Status)
	row, err = approvePersonalExecutionControl(s, e, cancelled)
	require.NoError(t, err)
	require.Equal(t, "cancelled", row.Status)
	require.EqualValues(t, 0, row.CurrentControl.Version)
	past := *preparePersonalExecutionControl(t, s, e, "stopped")
	past.ID = uuid.New()
	past.CreatedAt = past.CreatedAt.Add(-10 * time.Minute)
	past.ExpiresAt = past.ExpiresAt.Add(-10 * time.Minute)
	past.Fingerprint = controlOperationFingerprint(past)
	require.NoError(t, e.q.CreateAdExecutionControlOperation(ctx, past))
	row, err = approvePersonalExecutionControl(s, e, &past)
	require.NoError(t, err)
	require.Equal(t, "expired", row.Status)
	require.EqualValues(t, 0, row.CurrentControl.Version)
	stopped := preparePersonalExecutionControl(t, s, e, "stopped")
	_, err = approvePersonalExecutionControl(s, e, stopped)
	require.NoError(t, err)
	s.enabled = false
	row, err = s.Receipt(ctx, managerAdsIdentity(), e.workspace, e.actor, stopped.ID, "personal-token")
	require.NoError(t, err)
	require.False(t, row.ApprovalAvailable)
	require.Equal(t, "stopped", row.CurrentControl.Mode)
	require.Error(t, e.wb.StartCampaign(ctx, e.token, 101))
	require.Zero(t, e.api.writes.Load())
}
func TestAdExecutionControlManualRestrictionCannotBeRemovedByCampaignHandover(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	original := e.prepare(t)
	done, err := e.approve(original)
	require.NoError(t, err)
	require.True(t, done.Result.ControlHoldActive)
	s := NewManagerAdExecutionControlService(e.q, e.authority, e.svc.context, true)
	op := preparePersonalExecutionControl(t, s, e, "manual_only")
	_, err = approvePersonalExecutionControl(s, e, op)
	require.NoError(t, err)
	handover, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "handover")
	require.NoError(t, err)
	closed, err := e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, handover.ID, "personal-token", handover.Fingerprint, map[string]bool{"campaign_checked": true, "handover_impact_checked": true})
	require.NoError(t, err)
	require.Equal(t, "failed", closed.Status)
	var holds int
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1`, original.ID).Scan(&holds))
	require.Greater(t, holds, 0)
	require.Equal(t, int32(1), e.api.writes.Load())
}

func TestAdExecutionControlCommittedDecisionIsNotReportedRejectedByLaterAuthFailure(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	s := NewManagerAdExecutionControlService(e.q, e.authority, e.svc.context, true)
	o := preparePersonalExecutionControl(t, s, e, "stopped")
	e.authority.denyAt = e.authority.calls.Load() + 4 // Next request after the final under-lock identity check.
	row, err := approvePersonalExecutionControl(s, e, o)
	require.NoError(t, err)
	require.Equal(t, "succeeded", row.Status)
	require.Equal(t, "stopped", row.ResultControl.Mode)
	_, err = s.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
	require.Error(t, err)
	saved, err := e.q.GetAdExecutionControlOperation(context.Background(), o.ID, e.workspace, e.actor)
	require.NoError(t, err)
	require.Equal(t, "succeeded", saved.Status)
	require.Zero(t, e.api.writes.Load())
}
