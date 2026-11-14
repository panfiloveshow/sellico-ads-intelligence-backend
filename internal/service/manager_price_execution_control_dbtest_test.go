package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/require"
)

type priceControlAuthority struct{ pauseAuthority }

func (a *priceControlAuthority) VerifyManagerAccess(ctx context.Context, token, workspace string) (*domain.ManagerAccess, error) {
	identity, err := a.pauseAuthority.VerifyManagerAccess(ctx, token, workspace)
	if err == nil {
		identity.Permissions = append(identity.Permissions, "products.view", "unit_economics.view", "unit_economics.calculate")
	}
	return identity, err
}
func priceControlIdentity() domain.ManagerAccess {
	identity := managerAdsIdentity()
	identity.Permissions = append(identity.Permissions, "products.view", "unit_economics.view", "unit_economics.calculate", "marketing.manage")
	return identity
}
func priceControlChecks() map[string]bool {
	return map[string]bool{"price_account_scope_checked": true, "price_service_effect_checked": true, "price_reopening_checked": true}
}
func preparePersonalPriceControl(t *testing.T, s *ManagerPriceExecutionControlService, workspace, actor uuid.UUID, mode string) (*domain.PriceExecutionControlContext, *domain.PriceExecutionControlOperation) {
	t.Helper()
	ctx := context.Background()
	current, err := s.Context(ctx, priceControlIdentity(), workspace, "personal-token", "28", "101")
	require.NoError(t, err)
	proposal, err := s.Prepare(ctx, priceControlIdentity(), workspace, actor, "personal-token", "28", "101", mode, "Сотрудник проверяет команды цены", current.ContextFingerprint)
	require.NoError(t, err)
	return current, proposal
}

func TestPriceAccountPersonalLifecycleKeepsNativePricesUnchangedAndSeparatesCurrentFromHistory(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			ctx := context.Background()
			authority := &priceControlAuthority{pauseAuthority: pauseAuthority{market: market, personal: true}}
			var q *sqlcgen.Queries
			var workspace, actor uuid.UUID
			var control *ManagerPriceExecutionControlService
			var nativeCount func() int
			if market == "wb" {
				f, _, a, _ := priceControlWBFixture(t, testAdvertisingToken())
				q = f.q
				workspace = f.ws
				actor = a
				nativeCount = func() int { return int(f.uploads.Load()) }
				control = NewManagerPriceExecutionControlService(q, authority, f.svc.wbClient, nil, []byte(testdb.EncryptionKey), true)
			} else {
				s, v, ws, _, a, _ := newManagerOzonFixture(t)
				q = s.queries
				workspace = ws
				actor = a
				nativeCount = func() int { return v.writes }
				control = NewManagerPriceExecutionControlService(q, authority, nil, s.sellerClient, []byte(testdb.EncryptionKey), true)
			}
			initial, proposal := preparePersonalPriceControl(t, control, workspace, actor, "stopped")
			require.Equal(t, "legacy", initial.Control.Mode)
			require.True(t, initial.ApprovalAvailable)
			require.Equal(t, "101", proposal.Proof.ResourceID)
			require.Equal(t, priceAccountControlScope, proposal.Scope)
			_, err := control.Approve(ctx, priceControlIdentity(), workspace, actor, proposal.ID, "personal-token", proposal.Fingerprint, nil)
			require.Error(t, err)
			require.Zero(t, nativeCount())
			_, err = control.Receipt(ctx, priceControlIdentity(), workspace, uuid.New(), proposal.ID, "personal-token")
			require.Error(t, err)
			stopped, err := control.Approve(ctx, priceControlIdentity(), workspace, actor, proposal.ID, "personal-token", proposal.Fingerprint, priceControlChecks())
			require.NoError(t, err)
			require.Equal(t, "succeeded", stopped.Status)
			require.Equal(t, "stopped", stopped.ResultControl.Mode)
			require.False(t, stopped.NativePricesChanged)
			require.Zero(t, nativeCount())
			openingContext, opening := preparePersonalPriceControl(t, control, workspace, actor, "manual_only")
			require.EqualValues(t, 1, openingContext.Control.Version)
			opened, err := control.Approve(ctx, priceControlIdentity(), workspace, actor, opening.ID, "personal-token", opening.Fingerprint, priceControlChecks())
			require.NoError(t, err)
			require.EqualValues(t, 2, opened.CurrentControl.Version)
			require.Equal(t, "manual_only", opened.CurrentControl.Mode)
			require.Zero(t, nativeCount())
			historical, err := control.Receipt(ctx, priceControlIdentity(), workspace, actor, proposal.ID, "personal-token")
			require.NoError(t, err)
			require.Equal(t, "stopped", historical.ResultControl.Mode)
			require.Equal(t, "manual_only", historical.CurrentControl.Mode)
			repeated, err := control.Approve(ctx, priceControlIdentity(), workspace, actor, proposal.ID, "personal-token", proposal.Fingerprint, priceControlChecks())
			require.NoError(t, err)
			require.EqualValues(t, 2, repeated.CurrentControl.Version)
			manualContext, err := control.Context(ctx, priceControlIdentity(), workspace, "personal-token", "28", "101")
			require.NoError(t, err)
			control.enabled = false
			read, err := control.Receipt(ctx, priceControlIdentity(), workspace, actor, opening.ID, "personal-token")
			require.NoError(t, err)
			require.Equal(t, "manual_only", read.CurrentControl.Mode)
			require.False(t, read.ApprovalAvailable)
			raw, err := json.Marshal(historical)
			require.NoError(t, err)
			for _, private := range []string{"account_key", "credential_digest", "actor_id", "external_user_id", proposal.AccountKey, proposal.CredentialDigest} {
				require.NotContains(t, string(raw), private)
			}
			if dir := os.Getenv("SELYA_PRICE_EXECUTION_CONTROL_CONTRACT_DIR"); dir != "" {
				require.NoError(t, os.MkdirAll(dir, 0700))
				for name, value := range map[string]any{"initial-context": initial, "manual-context": manualContext, "stop-proposal": proposal, "stop-saved": stopped, "opening-context": openingContext, "manual-proposal": opening, "manual-saved": opened, "historical-after-opening": historical} {
					data, e := json.Marshal(map[string]any{"data": value})
					require.NoError(t, e)
					require.NoError(t, os.WriteFile(filepath.Join(dir, market+"-"+name+".json"), data, 0600))
				}
			}
		})
	}
}

func TestPriceAccountControlRechecksPersonalAccessAndImmutableScopeUnderItsAccountLock(t *testing.T) {
	for _, scenario := range []string{"revoked_after_lookup", "not_personal", "rotated_key", "payload_tamper", "disabled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			f, _, actor, _ := priceControlWBFixture(t, testAdvertisingToken())
			authority := &priceControlAuthority{pauseAuthority: pauseAuthority{market: "wb", personal: true}}
			s := NewManagerPriceExecutionControlService(f.q, authority, f.svc.wbClient, nil, []byte(testdb.EncryptionKey), true)
			_, proposal := preparePersonalPriceControl(t, s, f.ws, actor, "stopped")
			switch scenario {
			case "revoked_after_lookup":
				authority.revokeAt = authority.calls.Load() + 3
			case "not_personal":
				authority.personal = false
			case "rotated_key":
				encrypted, e := crypto.Encrypt(testAdvertisingToken()+"-rotated", testdb.EncryptionKey)
				require.NoError(t, e)
				_, e = f.pool.Exec(ctx, `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, f.cab, encrypted)
				require.NoError(t, e)
			case "payload_tamper":
				_, e := f.pool.Exec(ctx, `UPDATE manager_price_execution_control_operations SET immutable_payload=jsonb_set(immutable_payload,'{operation,target_mode}','"manual_only"') WHERE id=$1`, proposal.ID)
				require.NoError(t, e)
			case "disabled":
				s.enabled = false
			}
			_, err := s.Approve(ctx, priceControlIdentity(), f.ws, actor, proposal.ID, "personal-token", proposal.Fingerprint, priceControlChecks())
			require.Error(t, err)
			state, err := f.q.PriceExecutionControl(ctx, proposal.AccountKey, "wb")
			require.NoError(t, err)
			require.Zero(t, state.Version)
			require.Zero(t, f.uploads.Load())
		})
	}
}

func priceControlWBFixture(t *testing.T, token string) (*wbPriceReadbackFixture, ApprovedPriceCommand, uuid.UUID, string) {
	t.Helper()
	f, c, actor := newManagerWBFixture(t)
	encrypted, err := crypto.Encrypt(token, testdb.EncryptionKey)
	require.NoError(t, err)
	_, err = f.pool.Exec(context.Background(), `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, f.cab, encrypted)
	require.NoError(t, err)
	c.ExecutorCredentialsDigest = ManagerPriceCredentialsDigest("wb", "", token)
	account, err := adguard.WBAccountKey(token)
	require.NoError(t, err)
	return f, c, actor, account
}

func setPriceExecutionTestControl(t *testing.T, pool *pgxpool.Pool, market, account, mode string, version int64) {
	t.Helper()
	ctx := context.Background()
	// Before the implementation this test-only schema demonstrates that a
	// persisted stop is ignored. Once migration 077 exists its real table wins.
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS price_account_execution_controls (
 account_key text PRIMARY KEY,marketplace text NOT NULL,mode text NOT NULL,
 version bigint NOT NULL,changed_at timestamptz NOT NULL,changed_by_operation uuid NOT NULL)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO price_account_execution_controls(account_key,marketplace,mode,version,changed_at,changed_by_operation)
 VALUES($1,$2,$3,$4,statement_timestamp(),$5) ON CONFLICT(account_key) DO UPDATE SET mode=EXCLUDED.mode,version=EXCLUDED.version,changed_at=EXCLUDED.changed_at`, account, market, mode, version, uuid.New())
	require.NoError(t, err)
}

func TestPriceAccountStopFencesBothNativeExecutorsBeforeHTTP(t *testing.T) {
	ctx := context.Background()
	t.Run("wb", func(t *testing.T) {
		f, c, actor, account := priceControlWBFixture(t, testAdvertisingToken())
		setPriceExecutionTestControl(t, f.pool, "wb", account, "stopped", 1)
		_, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
		require.Zero(t, f.uploads.Load(), "a durable account stop must prevent the native WB upload")
		require.Error(t, err)
	})
	t.Run("ozon", func(t *testing.T) {
		s, v, ws, cab, actor, c := newManagerOzonFixture(t)
		_, creds, err := s.resolveCabinet(ctx, ws, cab)
		require.NoError(t, err)
		account, err := ozonPriceAccountKey(creds)
		require.NoError(t, err)
		setPriceExecutionTestControl(t, v.pool, "ozon", "ozon:"+account, "stopped", 1)
		_, err = s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
		require.Zero(t, v.writes, "a durable account stop must prevent the native Ozon price import")
		require.Error(t, err)
	})
}

func TestPriceAccountManualOpeningCannotReviveAnEarlierFinancialProposal(t *testing.T) {
	ctx := context.Background()
	t.Run("wb", func(t *testing.T) {
		f, c, actor, account := priceControlWBFixture(t, testAdvertisingToken())
		setPriceExecutionTestControl(t, f.pool, "wb", account, "stopped", 1)
		setPriceExecutionTestControl(t, f.pool, "wb", account, "manual_only", 2)
		_, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
		require.Error(t, err)
		require.Zero(t, f.uploads.Load())
		version := int64(2)
		c.ExecutionControlVersion = &version
		c.OperationID = uuid.New()
		receipt, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
		require.NoError(t, err)
		require.NotNil(t, receipt)
		require.Equal(t, int32(1), f.uploads.Load())
		_, err = f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
		require.NoError(t, err)
		require.Equal(t, int32(1), f.uploads.Load())
	})
	t.Run("ozon", func(t *testing.T) {
		s, v, ws, cab, actor, c := newManagerOzonFixture(t)
		_, creds, err := s.resolveCabinet(ctx, ws, cab)
		require.NoError(t, err)
		key, err := ozonPriceAccountKey(creds)
		require.NoError(t, err)
		account := "ozon:" + key
		setPriceExecutionTestControl(t, v.pool, "ozon", account, "stopped", 1)
		setPriceExecutionTestControl(t, v.pool, "ozon", account, "manual_only", 2)
		_, err = s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
		require.Error(t, err)
		require.Zero(t, v.writes)
		version := int64(2)
		c.ExecutionControlVersion = &version
		c.OperationID = uuid.New()
		receipt, err := s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
		require.NoError(t, err)
		require.Equal(t, "succeeded", receipt.Status)
		require.Equal(t, 1, v.writes)
		_, err = s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
		require.NoError(t, err)
		require.Equal(t, 1, v.writes)
	})
}

func TestPriceAccountWBStopFollowsSellerClaimAcrossChangedCredentialTokens(t *testing.T) {
	first := testAdvertisingToken()
	other := strings.Replace(first, ".test", ".changed-signature", 1)
	require.NotEqual(t, first, other)
	f, c, actor, account := priceControlWBFixture(t, other)
	key, err := adguard.WBAccountKey(first)
	require.NoError(t, err)
	require.Equal(t, key, account)
	setPriceExecutionTestControl(t, f.pool, "wb", key, "stopped", 1)
	_, err = f.svc.ApplyApprovedManagerPrice(context.Background(), f.ws, f.cab, actor, c)
	require.Error(t, err)
	require.Zero(t, f.uploads.Load())
}
