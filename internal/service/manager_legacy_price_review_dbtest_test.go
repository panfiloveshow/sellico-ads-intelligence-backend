package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/require"
)

const legacyReviewReason = "Сверил историю цен в кабинете"
const legacyReviewSupport = "Обращение в поддержку площадки №TEST-0001: незавершённых загрузок цен нет"

// The fixture records one real v1 admission for the same physical account the
// personal service resolves from the cabinet. No marketplace is contacted: the
// Native transports are the isolated test doubles of the price fixtures.
type legacyReviewFixture struct {
	pool      *pgxpool.Pool
	q         *sqlcgen.Queries
	workspace uuid.UUID
	actor     uuid.UUID
	cabinet   uuid.UUID
	authority *priceControlAuthority
	control   *ManagerPriceExecutionControlService
	review    *ManagerLegacyPriceReviewService
	native    func() int
	request   domain.LegacyPriceDispatchRequest
}

func legacyReviewChecks() map[string]bool {
	return map[string]bool{"legacy_history_and_support_checked": true, "legacy_no_native_work_in_progress": true,
		"legacy_late_effect_risk_accepted": true, "legacy_new_approvals_only": true}
}

// mode "" keeps the original legacy v0 path open; aged moves the retained
// admission into the past instead of waiting for its five-second window.
func newLegacyReviewFixture(t *testing.T, market, mode string, aged bool) *legacyReviewFixture {
	t.Helper()
	ctx := context.Background()
	f := &legacyReviewFixture{authority: &priceControlAuthority{pauseAuthority: pauseAuthority{market: market, personal: true}}}
	key := []byte(testdb.EncryptionKey)
	if market == "wb" {
		w, _, actor, _ := priceControlWBFixture(t, testAdvertisingToken())
		f.pool, f.q, f.workspace, f.actor, f.cabinet = w.pool, w.q, w.ws, actor, w.cab
		f.native = func() int { return int(w.uploads.Load()) }
		f.control = NewManagerPriceExecutionControlService(f.q, f.authority, w.svc.wbClient, nil, key, true)
		f.review = NewManagerLegacyPriceReviewService(f.q, f.authority, w.svc.wbClient, nil, key, true)
	} else {
		s, v, ws, cab, actor, _ := newManagerOzonFixture(t)
		f.pool, f.q, f.workspace, f.actor, f.cabinet = v.pool, s.queries, ws, actor, cab
		f.native = func() int { return v.writes }
		f.control = NewManagerPriceExecutionControlService(f.q, f.authority, nil, s.sellerClient, key, true)
		f.review = NewManagerLegacyPriceReviewService(f.q, f.authority, nil, s.sellerClient, key, true)
	}
	target := mode
	if target == "" {
		target = "stopped"
	}
	_, proposal := preparePersonalPriceControl(t, f.control, f.workspace, f.actor, target)
	f.request = legacyPriceRequest(market)
	f.request.AccountKey = proposal.AccountKey
	grant, err := NewLegacyPriceDispatchService(f.q, true).Authorize(ctx, f.request)
	require.NoError(t, err)
	require.True(t, grant.Authorized)
	if mode != "" {
		_, err = f.control.Approve(ctx, priceControlIdentity(), f.workspace, f.actor, proposal.ID, "personal-token", proposal.Fingerprint, priceControlChecks())
		require.NoError(t, err)
	}
	if aged {
		_, err = f.pool.Exec(ctx, `UPDATE legacy_price_dispatch_authorizations SET
 issued_at=issued_at-interval '1 day',expires_at=expires_at-interval '1 day',sent_at=sent_at-interval '1 day' WHERE command_id=$1`, f.request.CommandID)
		require.NoError(t, err)
	}
	return f
}

func (f *legacyReviewFixture) journal(t *testing.T) *domain.LegacyPriceJournalContext {
	t.Helper()
	c, err := f.review.Context(context.Background(), priceControlIdentity(), f.workspace, "personal-token", "28", "101", "")
	require.NoError(t, err)
	return c
}

func (f *legacyReviewFixture) entry(t *testing.T) domain.LegacyPriceJournalItem {
	t.Helper()
	for _, item := range f.journal(t).Items {
		if item.CommandID.String() == f.request.CommandID {
			return item
		}
	}
	t.Fatal("the recorded admission is missing from its own account journal")
	return domain.LegacyPriceJournalItem{}
}

func (f *legacyReviewFixture) prepare(t *testing.T) *domain.LegacyPriceReviewOperation {
	t.Helper()
	entry := f.entry(t)
	o, err := f.review.Prepare(context.Background(), priceControlIdentity(), f.workspace, f.actor, "personal-token", "28", "101",
		entry.CommandID, entry.ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport)
	require.NoError(t, err)
	return o
}

func (f *legacyReviewFixture) approve(o *domain.LegacyPriceReviewOperation, checks map[string]bool) (*domain.LegacyPriceReviewOperation, error) {
	return f.review.Approve(context.Background(), priceControlIdentity(), f.workspace, f.actor, o.ID, "personal-token", o.Fingerprint, checks)
}

// legacyReviewScopeJSON is the part of a receipt its chat owner stores at
// preparation and requires unchanged, as serialized on the wire, ever after.
func legacyReviewScopeJSON(t *testing.T, o *domain.LegacyPriceReviewOperation) string {
	t.Helper()
	raw, err := json.Marshal(o)
	require.NoError(t, err)
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &wire))
	scope := map[string]json.RawMessage{}
	for _, key := range []string{"id", "fingerprint", "workspace_id", "integration_id", "integration_name", "marketplace", "scope",
		"entry", "before", "account_proof", "reason", "support_reference", "created_at", "expires_at"} {
		require.Contains(t, wire, key)
		scope[key] = wire[key]
	}
	out, err := json.Marshal(scope)
	require.NoError(t, err)
	return string(out)
}

func (f *legacyReviewFixture) held(t *testing.T) int64 {
	t.Helper()
	n, err := f.q.LegacyPriceHeldCount(context.Background(), f.request.AccountKey)
	require.NoError(t, err)
	return n
}

func (f *legacyReviewFixture) resolutions(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_personal_reviews WHERE status='resolved_manually'").Scan(&n))
	return n
}

func TestLegacyPricePersonalReviewReleasesOnlyTheReviewedAdmissionWithoutNativeCommand(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			ctx := context.Background()
			f := newLegacyReviewFixture(t, market, "manual_only", true)
			before := f.journal(t)
			require.Equal(t, "legacy_price_journal_v1", before.Version)
			require.Equal(t, legacyPriceReviewScope, before.Scope)
			require.EqualValues(t, 1, before.HeldCount)
			require.True(t, before.ApprovalAvailable)
			require.Len(t, before.Items, 1)
			require.Nil(t, before.NextCursor)
			item := before.Items[0]
			kind := "seller_prices_and_discounts"
			if market == "ozon" {
				kind = "seller_prices"
			}
			require.Equal(t, kind, item.OperationKind)
			require.Equal(t, "unresolved", item.Status)
			require.True(t, item.HoldActive)
			require.True(t, item.PersonalReviewAvailable)
			require.False(t, item.NativeOutcomeVerified)
			require.Nil(t, item.ResolutionID)

			// A withdrawn proposal keeps the admission held and never becomes a receipt.
			discarded := f.prepare(t)
			cancelled, err := f.review.Cancel(ctx, priceControlIdentity(), f.workspace, f.actor, discarded.ID, "personal-token", discarded.Fingerprint)
			require.NoError(t, err)
			require.Equal(t, "cancelled", cancelled.Status)
			require.False(t, cancelled.ApprovalAvailable)
			require.True(t, *cancelled.CurrentHoldActive)
			require.Nil(t, cancelled.ResolvedAt)
			require.Nil(t, cancelled.Confirmations)
			require.Equal(t, legacyReviewScopeJSON(t, discarded), legacyReviewScopeJSON(t, cancelled))
			_, err = f.approve(discarded, legacyReviewChecks())
			require.NoError(t, err, "a withdrawn proposal answers with its saved receipt")
			require.EqualValues(t, 1, f.held(t))
			require.Zero(t, f.resolutions(t))

			proposal := f.prepare(t)
			require.Equal(t, "awaiting_confirmation", proposal.Status)
			require.True(t, proposal.ApprovalAvailable)
			require.True(t, *proposal.CurrentHoldActive)
			require.Equal(t, proposal.CreatedAt.Add(5*time.Minute), proposal.ExpiresAt)
			require.Equal(t, legacyReviewSupport, proposal.SupportReference)

			_, err = f.approve(proposal, nil)
			require.True(t, apperror.Is(err, apperror.ErrValidation), "%v", err)
			partial := legacyReviewChecks()
			partial["legacy_late_effect_risk_accepted"] = false
			_, err = f.approve(proposal, partial)
			require.True(t, apperror.Is(err, apperror.ErrValidation), "%v", err)
			extra := legacyReviewChecks()
			extra["native_outcome_verified"] = true
			_, err = f.approve(proposal, extra)
			require.Error(t, err)
			_, err = f.review.Approve(ctx, priceControlIdentity(), f.workspace, f.actor, proposal.ID, "personal-token", strings.Repeat("0", 64), legacyReviewChecks())
			require.True(t, apperror.Is(err, apperror.ErrConflict), "%v", err)
			_, err = f.review.Receipt(ctx, priceControlIdentity(), f.workspace, uuid.New(), proposal.ID, "personal-token")
			require.True(t, apperror.Is(err, apperror.ErrNotFound), "%v", err)
			_, err = f.review.Receipt(ctx, priceControlIdentity(), uuid.New(), f.actor, proposal.ID, "personal-token")
			require.Error(t, err)
			require.EqualValues(t, 1, f.held(t))
			require.Zero(t, f.resolutions(t))

			resolved, err := f.approve(proposal, legacyReviewChecks())
			require.NoError(t, err)
			require.Equal(t, "resolved_manually", resolved.Status)
			// The chat owner compares the saved scope with every later receipt byte
			// for byte; a database time zone must not look like a changed review.
			require.Equal(t, legacyReviewScopeJSON(t, proposal), legacyReviewScopeJSON(t, resolved))
			require.Equal(t, time.UTC, resolved.ResolvedAt.Location())
			require.NotNil(t, resolved.ResolvedAt)
			require.False(t, *resolved.CurrentHoldActive)
			require.Zero(t, *resolved.CurrentHeldCount)
			require.False(t, resolved.ApprovalAvailable)
			require.False(t, resolved.NativeOutcomeVerified)
			require.False(t, resolved.NativePricesChanged)
			require.Equal(t, legacyReviewChecks(), resolved.Confirmations)
			require.Equal(t, "manual_only", resolved.CurrentControl.Mode)
			require.EqualValues(t, 1, resolved.CurrentControl.Version)
			require.Zero(t, f.native(), "a personal resolution never sends a Native price command")

			repeated, err := f.approve(proposal, legacyReviewChecks())
			require.NoError(t, err)
			require.Equal(t, "resolved_manually", repeated.Status)
			require.True(t, resolved.ResolvedAt.Equal(*repeated.ResolvedAt))
			require.Equal(t, 1, f.resolutions(t))

			after := f.journal(t)
			require.Zero(t, after.HeldCount)
			require.Equal(t, "resolved_manually", after.Items[0].Status)
			require.False(t, after.Items[0].HoldActive)
			require.False(t, after.Items[0].PersonalReviewAvailable)
			require.False(t, after.Items[0].NativeOutcomeVerified)
			require.Equal(t, proposal.ID, *after.Items[0].ResolutionID)
			_, err = f.review.Prepare(ctx, priceControlIdentity(), f.workspace, f.actor, "personal-token", "28", "101",
				item.CommandID, after.Items[0].ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport)
			require.True(t, apperror.Is(err, apperror.ErrConflict), "%v", err)

			f.review.enabled = false
			read, err := f.review.Receipt(ctx, priceControlIdentity(), f.workspace, f.actor, proposal.ID, "personal-token")
			require.NoError(t, err)
			require.Equal(t, "resolved_manually", read.Status)
			f.review.enabled = true

			raw, err := json.Marshal(map[string]any{"journal": after, "receipt": resolved})
			require.NoError(t, err)
			for _, private := range []string{"account_key", "credential_digest", "grant_fingerprint", "actor_id", "external_user_id", "payload_sha256", "request_fingerprint",
				proposal.AccountKey, proposal.CredentialDigest, proposal.GrantFingerprint, f.request.PayloadSHA256} {
				require.NotContains(t, string(raw), private)
			}

			// The reviewed admission no longer fences a new personally approved
			// Native command of the same physical account.
			begin, _ := legacyFenceCandidate(t, f.pool, f.request, true)
			require.NoError(t, begin())
			require.Zero(t, f.native())

			if dir := os.Getenv("SELYA_LEGACY_PRICE_REVIEW_CONTRACT_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				for name, value := range map[string]any{"journal-held": before, "review-proposal": proposal, "review-resolved": resolved, "journal-resolved": after,
					"review-cancelled-proposal": discarded, "review-cancelled": cancelled} {
					data, e := json.Marshal(map[string]any{"data": value})
					require.NoError(t, e)
					require.NoError(t, os.WriteFile(filepath.Join(dir, market+"-"+name+".json"), data, 0600))
				}
			}
		})
	}
}

func TestLegacyPriceReviewRequiresClosedLegacyPathExpiredWindowAndCurrentJournalContext(t *testing.T) {
	ctx := context.Background()
	prepare := func(f *legacyReviewFixture, command uuid.UUID, fingerprint, reason, support string) error {
		_, err := f.review.Prepare(ctx, priceControlIdentity(), f.workspace, f.actor, "personal-token", "28", "101", command, fingerprint, reason, support)
		return err
	}
	t.Run("legacy path still open", func(t *testing.T) {
		f := newLegacyReviewFixture(t, "wb", "", true)
		c := f.journal(t)
		require.Equal(t, "legacy", c.Control.Mode)
		require.False(t, c.ApprovalAvailable)
		require.False(t, c.Items[0].PersonalReviewAvailable)
		require.EqualValues(t, 1, c.HeldCount)
		err := prepare(f, c.Items[0].CommandID, c.Items[0].ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport)
		require.True(t, apperror.Is(err, apperror.ErrConflict), "%v", err)
	})
	t.Run("admission window still open", func(t *testing.T) {
		f := newLegacyReviewFixture(t, "ozon", "stopped", false)
		entry := f.entry(t)
		require.False(t, entry.PersonalReviewAvailable)
		err := prepare(f, entry.CommandID, entry.ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport)
		require.True(t, apperror.Is(err, apperror.ErrConflict), "%v", err)
	})
	t.Run("stale or foreign input", func(t *testing.T) {
		f := newLegacyReviewFixture(t, "wb", "stopped", true)
		entry := f.entry(t)
		require.True(t, entry.PersonalReviewAvailable)
		require.True(t, apperror.Is(prepare(f, entry.CommandID, strings.Repeat("0", 64), legacyReviewReason, legacyReviewSupport), apperror.ErrConflict))
		require.True(t, apperror.Is(prepare(f, uuid.New(), entry.ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport), apperror.ErrNotFound))
		require.True(t, apperror.Is(prepare(f, entry.CommandID, entry.ReviewContextFingerprint, legacyReviewReason, "нет"), apperror.ErrValidation))
		require.True(t, apperror.Is(prepare(f, entry.CommandID, entry.ReviewContextFingerprint, "", legacyReviewSupport), apperror.ErrValidation))
		require.True(t, apperror.Is(prepare(f, entry.CommandID, entry.ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport+"\x00"), apperror.ErrValidation))
		_, err := f.review.Prepare(ctx, priceControlIdentity(), f.workspace, uuid.Nil, "personal-token", "28", "101", entry.CommandID, entry.ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport)
		require.True(t, apperror.Is(err, apperror.ErrForbidden), "%v", err)
		var saved int
		require.NoError(t, f.pool.QueryRow(ctx, "SELECT count(*) FROM legacy_price_personal_reviews").Scan(&saved))
		require.Zero(t, saved)
		// A control change makes the earlier journal fingerprint stale.
		_, opening := preparePersonalPriceControl(t, f.control, f.workspace, f.actor, "manual_only")
		_, err = f.control.Approve(ctx, priceControlIdentity(), f.workspace, f.actor, opening.ID, "personal-token", opening.Fingerprint, priceControlChecks())
		require.NoError(t, err)
		require.True(t, apperror.Is(prepare(f, entry.CommandID, entry.ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport), apperror.ErrConflict))
		require.EqualValues(t, 1, f.held(t))
	})
}

func TestLegacyPriceReviewRechecksAccessControlAndImmutableSnapshotBeforeRelease(t *testing.T) {
	for _, scenario := range []string{"revoked_under_lock", "not_personal", "rotated_key", "snapshot_tamper", "disabled", "control_changed", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			f := newLegacyReviewFixture(t, "wb", "stopped", true)
			proposal := f.prepare(t)
			wantStatus := "awaiting_confirmation"
			switch scenario {
			case "revoked_under_lock":
				// owned, two context lookups, then the first check inside the account lock.
				f.authority.revokeAt = f.authority.calls.Load() + 4
			case "not_personal":
				f.authority.personal = false
			case "rotated_key":
				encrypted, e := crypto.Encrypt(testAdvertisingToken()+"-rotated", testdb.EncryptionKey)
				require.NoError(t, e)
				_, e = f.pool.Exec(ctx, `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, f.cabinet, encrypted)
				require.NoError(t, e)
			case "snapshot_tamper":
				_, e := f.pool.Exec(ctx, `UPDATE legacy_price_personal_reviews SET snapshot=jsonb_set(snapshot,'{support_reference}','"Подменённая ссылка на разбор"') WHERE id=$1`, proposal.ID)
				require.NoError(t, e)
			case "disabled":
				f.review.enabled = false
			case "control_changed":
				_, opening := preparePersonalPriceControl(t, f.control, f.workspace, f.actor, "manual_only")
				_, e := f.control.Approve(ctx, priceControlIdentity(), f.workspace, f.actor, opening.ID, "personal-token", opening.Fingerprint, priceControlChecks())
				require.NoError(t, e)
			case "expired":
				// Move the whole immutable proposal into the past with its matching fingerprint.
				aged := *proposal
				aged.CreatedAt, aged.ExpiresAt = proposal.CreatedAt.Add(-10*time.Minute), proposal.ExpiresAt.Add(-10*time.Minute)
				aged.Fingerprint = legacyPriceReviewFingerprint(aged)
				_, e := f.pool.Exec(ctx, `UPDATE legacy_price_personal_reviews SET created_at=$2,expires_at=$3,fingerprint=$4 WHERE id=$1`, proposal.ID, aged.CreatedAt, aged.ExpiresAt, aged.Fingerprint)
				require.NoError(t, e)
				proposal.Fingerprint, wantStatus = aged.Fingerprint, "expired"
			case "cancelled":
				cancelled, e := f.review.Cancel(ctx, priceControlIdentity(), f.workspace, f.actor, proposal.ID, "personal-token", proposal.Fingerprint)
				require.NoError(t, e)
				require.Equal(t, "cancelled", cancelled.Status)
				require.False(t, cancelled.ApprovalAvailable)
				wantStatus = "cancelled"
			}
			result, err := f.approve(proposal, legacyReviewChecks())
			if scenario == "expired" || scenario == "cancelled" {
				require.NoError(t, err)
				require.Equal(t, wantStatus, result.Status)
				require.False(t, result.ApprovalAvailable)
				require.True(t, *result.CurrentHoldActive)
			} else {
				require.Error(t, err)
			}
			require.EqualValues(t, 1, f.held(t))
			require.Zero(t, f.resolutions(t))
			require.Zero(t, f.native())
			var status string
			require.NoError(t, f.pool.QueryRow(ctx, "SELECT status FROM legacy_price_personal_reviews WHERE id=$1", proposal.ID).Scan(&status))
			require.Equal(t, wantStatus, status)
			begin, assertUndispatched := legacyFenceCandidate(t, f.pool, f.request, false)
			require.Error(t, begin())
			assertUndispatched()
		})
	}
}

func TestLegacyPriceReviewCannotReleaseWhilePhysicalPriceWorkIsActiveOrUnbound(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			f := newLegacyReviewFixture(t, market, "manual_only", true)
			proposal := f.prepare(t)
			begin, assertUndispatched := legacyFenceCandidate(t, f.pool, f.request, true)
			require.ErrorIs(t, begin(), sqlcgen.ErrLegacyPriceActiveWrite)
			assertUndispatched()
			_, err := f.approve(proposal, legacyReviewChecks())
			require.True(t, errors.Is(err, sqlcgen.ErrLegacyPriceActiveWrite) || errors.Is(err, sqlcgen.ErrLegacyPriceBindingUnknown), "%v", err)
			require.EqualValues(t, 1, f.held(t))
			require.Zero(t, f.resolutions(t))
			require.Zero(t, f.native())
		})
	}
}

func TestLegacyPriceReviewConcurrentApprovalsCommitExactlyOneResolution(t *testing.T) {
	f := newLegacyReviewFixture(t, "wb", "stopped", true)
	first, second := f.prepare(t), f.prepare(t)
	require.NotEqual(t, first.ID, second.ID)
	start := make(chan struct{})
	results := make(chan error, 8)
	var group sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		proposal := first
		if i%2 == 1 {
			proposal = second
		}
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := f.approve(proposal, legacyReviewChecks())
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			require.True(t, errors.Is(err, sqlcgen.ErrLegacyPriceReviewChanged) || errors.Is(err, sqlcgen.ErrPriceExecutionAccountHeld) || apperror.Is(err, apperror.ErrConflict), "%v", err)
		}
	}
	// Contention may defer every attempt; one uncontended approval must then win.
	if f.resolutions(t) == 0 {
		_, err := f.approve(first, legacyReviewChecks())
		require.NoError(t, err)
	}
	require.Equal(t, 1, f.resolutions(t))
	require.Zero(t, f.held(t))
	var pending int
	require.NoError(t, f.pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_personal_reviews WHERE status='awaiting_confirmation'").Scan(&pending))
	require.Equal(t, 1, pending, "the losing proposal stays an unconfirmed draft and cannot release anything")
	loser := second
	var winner uuid.UUID
	require.NoError(t, f.pool.QueryRow(context.Background(), "SELECT id FROM legacy_price_personal_reviews WHERE status='resolved_manually'").Scan(&winner))
	if winner == second.ID {
		loser = first
	}
	_, err := f.approve(loser, legacyReviewChecks())
	require.Error(t, err)
	require.Equal(t, 1, f.resolutions(t))
	require.Zero(t, f.native())
}

func TestLegacyPriceJournalIsAccountScopedPagedAndKeepsOtherAdmissionsHeld(t *testing.T) {
	ctx := context.Background()
	f := newLegacyReviewFixture(t, "wb", "manual_only", true)
	// Admissions that predate the account fence can coexist; each stays held on its own.
	_, err := f.pool.Exec(ctx, `INSERT INTO legacy_price_dispatch_authorizations
 (command_id,origin,account_key,marketplace,endpoint,payload_sha256,request_fingerprint,sent_at,issued_at,expires_at)
 SELECT gen_random_uuid(),'products',$1,'wb','/api/v2/upload/task/size',repeat('c',64),md5(i::text)||md5((i+1000)::text),t,t,t+interval '5 seconds'
 FROM generate_series(1,26) i CROSS JOIN LATERAL (SELECT statement_timestamp()-interval '2 days'+make_interval(secs=>i) AS t) x`, f.request.AccountKey)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `INSERT INTO legacy_price_dispatch_authorizations
 (command_id,origin,account_key,marketplace,endpoint,payload_sha256,request_fingerprint,sent_at,issued_at,expires_at)
 VALUES(gen_random_uuid(),'products',$1,'wb','/api/v2/upload/task',repeat('d',64),repeat('e',64),statement_timestamp()-interval '3 days',statement_timestamp()-interval '3 days',statement_timestamp()-interval '3 days'+interval '5 seconds')`, "wb:"+strings.Repeat("f", 64))
	require.NoError(t, err)

	first := f.journal(t)
	require.EqualValues(t, 27, first.HeldCount)
	require.Len(t, first.Items, 25)
	require.NotNil(t, first.NextCursor)
	require.Equal(t, "size_prices", first.Items[0].OperationKind)
	second, err := f.review.Context(ctx, priceControlIdentity(), f.workspace, "personal-token", "28", "101", *first.NextCursor)
	require.NoError(t, err)
	require.Len(t, second.Items, 2)
	require.Nil(t, second.NextCursor)
	seen := map[uuid.UUID]bool{}
	for _, item := range append(first.Items, second.Items...) {
		require.False(t, seen[item.CommandID], "pages must not repeat an admission")
		seen[item.CommandID] = true
		require.True(t, item.HoldActive)
	}
	require.Len(t, seen, 27)
	require.Equal(t, f.request.CommandID, second.Items[1].CommandID.String(), "the journal is ordered by admission time")
	for _, bad := range []string{"not-a-cursor", strings.Repeat("A", 600), "e30"} {
		_, err = f.review.Context(ctx, priceControlIdentity(), f.workspace, "personal-token", "28", "101", bad)
		require.True(t, apperror.Is(err, apperror.ErrValidation), "%q: %v", bad, err)
	}

	// The recorded admission is on the second page; its fingerprint comes from that page.
	own := second.Items[1]
	require.True(t, own.PersonalReviewAvailable)
	proposal, err := f.review.Prepare(ctx, priceControlIdentity(), f.workspace, f.actor, "personal-token", "28", "101",
		own.CommandID, own.ReviewContextFingerprint, legacyReviewReason, legacyReviewSupport)
	require.NoError(t, err)
	resolved, err := f.approve(proposal, legacyReviewChecks())
	require.NoError(t, err)
	require.False(t, *resolved.CurrentHoldActive)
	require.EqualValues(t, 26, *resolved.CurrentHeldCount)
	begin, assertUndispatched := legacyFenceCandidate(t, f.pool, f.request, true)
	require.ErrorIs(t, begin(), sqlcgen.ErrLegacyPriceActiveWrite, "other unreviewed admissions keep the account fenced")
	assertUndispatched()
	require.Zero(t, f.native())
}

// The fence trusts only the exact receipt shape committed by ApplyLegacyPriceReview.
func TestLegacyPriceFenceIgnoresForgedMismatchedOrPrematureResolutionRows(t *testing.T) {
	ctx := context.Background()
	f := newLegacyReviewFixture(t, "ozon", "stopped", true)
	proposal := f.prepare(t)
	_, err := f.pool.Exec(ctx, `UPDATE legacy_price_personal_reviews SET status='resolved_manually' WHERE id=$1`, proposal.ID)
	require.Error(t, err, "a resolution without time and confirmations violates the ledger")
	_, err = f.pool.Exec(ctx, `UPDATE legacy_price_personal_reviews SET status='resolved_manually',resolved_at=statement_timestamp(),confirmations='{"legacy_history_and_support_checked":true}'::jsonb WHERE id=$1`, proposal.ID)
	require.Error(t, err, "all four confirmations are part of the stored receipt")
	checks, err := json.Marshal(legacyReviewChecks())
	require.NoError(t, err)
	forge := func(snapshot string, fingerprint string, created time.Time) {
		t.Helper()
		_, e := f.pool.Exec(ctx, `INSERT INTO legacy_price_personal_reviews
 (id,workspace_id,actor_id,cabinet_id,external_user_id,external_workspace_id,integration_id,integration_name,marketplace,account_key,
 credential_digest,grant_id,grant_fingerprint,fingerprint,snapshot,status,confirmations,created_at,expires_at,resolved_at)
 VALUES(gen_random_uuid(),$1,$2,$3,'42','3','28','Forged','ozon',$4,repeat('a',64),$5,$6,repeat('b',64),$7::jsonb,'resolved_manually',$8::jsonb,$9::timestamptz,$9::timestamptz+interval '5 minutes',$9::timestamptz+interval '1 minute')`,
			f.workspace, f.actor, f.cabinet, f.request.AccountKey, f.request.CommandID, fingerprint, snapshot, string(checks), created)
		require.NoError(t, e)
	}
	var grantFingerprint string
	var admissionEnd time.Time
	require.NoError(t, f.pool.QueryRow(ctx, "SELECT request_fingerprint,expires_at FROM legacy_price_dispatch_authorizations WHERE command_id=$1", f.request.CommandID).Scan(&grantFingerprint, &admissionEnd))
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err = f.pool.Exec(ctx, "DROP INDEX legacy_price_one_personal_resolution")
	require.NoError(t, err)
	forge(`{"before":{"mode":"legacy","version":0}}`, grantFingerprint, now)
	forge(`{"before":{"mode":"stopped","version":0}}`, grantFingerprint, now)
	forge(`{"before":{"mode":"stopped","version":1}}`, strings.Repeat("9", 64), now)
	forge(`{"before":{"mode":"stopped","version":1}}`, grantFingerprint, admissionEnd.Add(-time.Hour))
	require.EqualValues(t, 1, f.held(t))
	begin, assertUndispatched := legacyFenceCandidate(t, f.pool, f.request, false)
	require.Error(t, begin())
	assertUndispatched()
	entry := f.entry(t)
	require.Equal(t, "unresolved", entry.Status)
	require.True(t, entry.HoldActive)
}

// A new financial proposal is prepared from the price context. It must show
// the account-wide hold before any approval is requested. The count follows
// only committed personal resolutions: neither a pending review nor the hold
// itself changes the fingerprint an earlier restriction proposal was bound to.
func TestPriceExecutionContextReportsLegacyHoldsUntilPersonalResolution(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			ctx := context.Background()
			f := newLegacyReviewFixture(t, market, "manual_only", true)
			read := func() *domain.PriceExecutionControlContext {
				c, err := f.control.Context(ctx, priceControlIdentity(), f.workspace, "personal-token", "28", "101")
				require.NoError(t, err)
				return c
			}
			before := read()
			require.EqualValues(t, 1, before.LegacyHeldCount)
			require.Equal(t, "manual_only", before.Control.Mode)
			proposal := f.prepare(t)
			pending := read()
			require.EqualValues(t, 1, pending.LegacyHeldCount, "a pending review releases nothing")
			require.Equal(t, before.ContextFingerprint, pending.ContextFingerprint)
			_, err := f.approve(proposal, legacyReviewChecks())
			require.NoError(t, err)
			after := read()
			require.Zero(t, after.LegacyHeldCount)
			require.Equal(t, before.ContextFingerprint, after.ContextFingerprint, "the hold count is outside the context fingerprint")
			raw, err := json.Marshal(after)
			require.NoError(t, err)
			require.Contains(t, string(raw), `"legacy_held_count":0`)
			require.Zero(t, f.native(), "reading holds sends no Native request")
		})
	}
}

func TestPriceExecutionContextWithoutLegacyAdmissionsReportsNoHold(t *testing.T) {
	ctx := context.Background()
	f := newLegacyReviewFixture(t, "wb", "manual_only", true)
	_, err := f.pool.Exec(ctx, "UPDATE legacy_price_dispatch_authorizations SET account_key='wb:'||repeat('0',64) WHERE command_id=$1", f.request.CommandID)
	require.NoError(t, err)
	c, err := f.control.Context(ctx, priceControlIdentity(), f.workspace, "personal-token", "28", "101")
	require.NoError(t, err)
	require.Zero(t, c.LegacyHeldCount, "an admission of another physical account is not this account's hold")
}

func TestLegacyPriceReviewLedgerSurvivesDowngradeAndFailsClosedWithoutStorage(t *testing.T) {
	ctx := context.Background()
	f := newLegacyReviewFixture(t, "wb", "manual_only", true)
	_, err := f.approve(f.prepare(t), legacyReviewChecks())
	require.NoError(t, err)
	raw, err := os.ReadFile("../../migrations/000080_legacy_price_personal_reviews.down.sql")
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, string(raw))
	require.NoError(t, err)
	require.Equal(t, 1, f.resolutions(t), "a downgrade keeps personal receipts")
	raw, err = os.ReadFile("../../migrations/000080_legacy_price_personal_reviews.up.sql")
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, string(raw))
	require.NoError(t, err, "the additive migration can be applied again")
	require.Zero(t, f.held(t))
	_, err = f.pool.Exec(ctx, "ALTER TABLE legacy_price_personal_reviews RENAME TO retained_personal_reviews")
	require.NoError(t, err)
	begin, assertUndispatched := legacyFenceCandidate(t, f.pool, f.request, true)
	require.Error(t, begin(), "missing review storage is not an empty journal")
	assertUndispatched()
	_, err = f.review.Context(ctx, priceControlIdentity(), f.workspace, "personal-token", "28", "101", "")
	require.Error(t, err)
	_, err = f.control.Context(ctx, priceControlIdentity(), f.workspace, "personal-token", "28", "101")
	require.Error(t, err, "a price context without journal storage must not report an empty hold count")
}
