package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func testAdvertisingToken() string {
	p, _ := json.Marshal(map[string]any{"sid": "seller-transport-test"})
	return "test." + base64.RawURLEncoding.EncodeToString(p) + ".test"
}
func adTestRequest(t *testing.T, account string, resources []string) adguard.Request {
	t.Helper()
	r, err := adguard.NewRequest("ozon", adguard.AccountKey("ozon", account), "PATCH", "https://performance.test/api/client/campaign/101", resources, []byte(`{"weeklyBudget":"1000000000"}`))
	require.NoError(t, err)
	return r
}

func TestAdGuardApprovedCommandIsOneUseAndBlocksLegacyUntilObservation(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	token := testAdvertisingToken()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer srv.Close()
	r, err := adguard.NewWBRequest("GET", srv.URL+"/adv/v0/pause?id=101", token, nil)
	require.NoError(t, err)
	id := uuid.New()
	require.NoError(t, q.ReserveApprovedAdCommand(ctx, id, r, uuidToPgtype(uuid.New()), uuidToPgtype(uuid.New()), strings.Repeat("a", 64), time.Now(), time.Now().Add(time.Minute)))
	c := wb.NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop()).WithAdGuard(q)
	wrong := adguard.WithApprovedCommandGate(ctx, id, func(context.Context, adguard.Request) error { return nil })
	require.Error(t, c.StartCampaign(wrong, token, 101))
	require.Zero(t, calls.Load())
	require.NoError(t, c.PauseCampaign(wrong, token, 101))
	require.Equal(t, int32(1), calls.Load())
	require.Error(t, c.PauseCampaign(wrong, token, 101))
	require.Equal(t, int32(1), calls.Load())
	require.Error(t, c.StartCampaign(ctx, token, 101))
	require.Equal(t, int32(1), calls.Load())
	row, err := q.ReadNativeAdCommand(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "acknowledged", row.Status)
	require.True(t, row.StartedAt.Valid)
	var holds int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM ad_native_resource_holds WHERE command_id=$1`, id).Scan(&holds))
	require.Equal(t, 1, holds)
}

func TestAdGuardNativeUnknownBlocksRepeatAcrossAliasAndSurvivesAge(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			_, _ = w.Write([]byte(`{"access_token":"test-only-token","expires_in":1800,"token_type":"Bearer"}`))
			return
		}
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()
	c := ozon.NewPerfClient(&config.Config{OzonPerfAPIBaseURL: srv.URL}, zerolog.Nop()).WithAdGuard(q)
	creds := ozon.Credentials{PerfClientID: "test-performance", PerfClientSecret: "test-only-secret"}
	err := c.ActivateCampaign(ctx, creds, 101)
	require.Error(t, err)
	require.True(t, adguard.IsUnknown(err))
	require.Equal(t, int32(1), calls.Load())
	err = c.DeactivateCampaign(ctx, creds, 101)
	require.Error(t, err)
	require.True(t, adguard.WasNotSent(err))
	require.Equal(t, int32(1), calls.Load())
	creds.PerfClientID = "alias-same-campaign"
	err = c.ActivateCampaign(ctx, creds, 101)
	require.Error(t, err)
	require.True(t, adguard.WasNotSent(err))
	require.Equal(t, int32(1), calls.Load())
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM ad_native_commands`).Scan(&id))
	_, err = pool.Exec(ctx, `UPDATE ad_native_commands SET started_at=now()-interval '30 days' WHERE id=$1`, id)
	require.NoError(t, err)
	require.NoError(t, q.ExpireUnsentApprovedAdCommand(ctx, id))
	row, err := q.ReadNativeAdCommand(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "unknown", row.Status)
	_, err = q.BeginAdvertisingMutation(ctx, adTestRequest(t, "another-alias", []string{"101"}))
	require.ErrorIs(t, err, sqlcgen.ErrAdResourceHeld)
}

func TestAdGuardOnlyExpiredUnsentApprovalsCanBeReleasedByTime(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "lease-test", []string{"101"})
	id := uuid.New()
	require.NoError(t, q.ReserveApprovedAdCommand(ctx, id, r, uuidToPgtype(uuid.New()), uuidToPgtype(uuid.New()), strings.Repeat("b", 64), time.Now(), time.Now().Add(time.Minute)))
	_, err := pool.Exec(ctx, `UPDATE ad_native_commands SET approval_expires_at=now()-interval '1 second' WHERE id=$1`, id)
	require.NoError(t, err)
	_, err = q.BeginAdvertisingMutation(adguard.WithApprovedCommandGate(ctx, id, func(context.Context, adguard.Request) error { return nil }), r)
	require.ErrorIs(t, err, sqlcgen.ErrAdCommandChanged)
	require.NoError(t, q.ExpireUnsentApprovedAdCommand(ctx, id))
	row, err := q.ReadNativeAdCommand(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "rejected", row.Status)
	require.False(t, row.StartedAt.Valid)
	_, err = q.BeginAdvertisingMutation(ctx, r)
	require.NoError(t, err)
}

func TestAdGuardSerializesAliasesAndScopesGlobalOperationsToAccount(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	var wg sync.WaitGroup
	var admitted atomic.Int32
	start := make(chan struct{})
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			<-start
			_, err := q.BeginAdvertisingMutation(ctx, adTestRequest(t, uuid.NewString(), []string{"101"}))
			if err == nil {
				admitted.Add(1)
			} else {
				errs <- err
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	require.Equal(t, int32(1), admitted.Load())
	for err := range errs {
		require.ErrorIs(t, err, sqlcgen.ErrAdResourceHeld)
	}
	global := adTestRequest(t, "global-test", nil)
	lease, err := q.BeginAdvertisingMutation(ctx, global)
	require.NoError(t, err)
	_, err = q.BeginAdvertisingMutation(ctx, adTestRequest(t, "global-test", []string{"999"}))
	require.ErrorIs(t, err, sqlcgen.ErrAdResourceHeld)
	_, err = q.BeginAdvertisingMutation(ctx, adTestRequest(t, "different-account", []string{"999"}))
	require.NoError(t, err)
	require.NoError(t, q.CompleteAdvertisingMutation(ctx, lease, adguard.Result(429, nil, errors.New("vendor refusal"))))
	_, err = q.BeginAdvertisingMutation(ctx, adTestRequest(t, "global-test", []string{"998"}))
	require.NoError(t, err)
}

func TestAdGuardPersistsUnknownAfterRequestCancellation(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		cancel()
		conn, _, err := w.(http.Hijacker).Hijack()
		require.NoError(t, err)
		_ = conn.Close()
	}))
	defer srv.Close()
	c := wb.NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop()).WithAdGuard(q)
	err := c.PauseCampaign(ctx, testAdvertisingToken(), 101)
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
	var status string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT status FROM ad_native_commands`).Scan(&status))
	require.Equal(t, "unknown", status)
}

func TestAdGuardDowngradeRefusesUnknownAndAllowsClosedJournal(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000070_ad_native_commands.down.sql")
	require.NoError(t, err)
	for _, unknown := range []bool{true, false} {
		t.Run(map[bool]string{true: "unresolved", false: "closed"}[unknown], func(t *testing.T) {
			pool := testdb.New(t)
			q := sqlcgen.New(pool)
			ctx := context.Background()
			lease, err := q.BeginAdvertisingMutation(ctx, adTestRequest(t, "rollback-test", []string{"101"}))
			require.NoError(t, err)
			if unknown {
				require.NoError(t, q.CompleteAdvertisingMutation(ctx, lease, adguard.Result(503, nil, errors.New("uncertain"))))
			} else {
				require.NoError(t, q.CompleteAdvertisingMutation(ctx, lease, adguard.Result(200, []byte(`{}`), nil)))
			}
			tx, err := pool.Begin(ctx)
			require.NoError(t, err)
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, string(raw))
			if unknown {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAdGuardWBLegacyLifecycleAuditCannotLabelAnUncertainWriteFailed(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	ws := testdb.Workspace(t, pool)
	cab := testdb.WBCabinet(t, pool, ws)
	token, err := crypto.Encrypt(testAdvertisingToken(), testdb.EncryptionKey)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, cab, token)
	require.NoError(t, err)
	var campaign uuid.UUID
	err = pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,campaign_type,bid_type,payment_type) VALUES($1,$2,101,'Campaign test','active',9,'manual','cpm') RETURNING id`, ws, cab).Scan(&campaign)
	require.NoError(t, err)
	u, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{Email: uuid.NewString() + "@test.invalid", PasswordHash: "test-only", Name: "Test manager"})
	require.NoError(t, err)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer srv.Close()
	client := wb.NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop()).WithAdGuard(q)
	s := NewCampaignActionService(q, client, testdb.EncryptionKey, zerolog.Nop())
	err = s.PauseCampaign(ctx, ws, campaign, uuidFromPgtype(u.ID))
	require.Error(t, err)
	require.True(t, adguard.IsUnknown(err))
	require.Equal(t, int32(1), calls.Load())
	var state string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM wb_bid_actions WHERE campaign_id=$1`, campaign).Scan(&state))
	require.Equal(t, "unknown", state)
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id=$1`, campaign).Scan(&state))
	require.Equal(t, "active", state)
}

// CPO is account-wide even though Ozon exposes it as GET. Its unresolved
// outcome must block both another CPO action and individual campaign writes.
func TestAdGuardOzonCPOGETKeepsAnAccountWideUnknownHold(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			_, _ = w.Write([]byte(`{"access_token":"test-only-token","expires_in":1800}`))
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := ozon.NewPerfClient(&config.Config{OzonPerfAPIBaseURL: srv.URL}, zerolog.Nop()).WithAdGuard(q)
	creds := ozon.Credentials{PerfClientID: "test-cpo-account", PerfClientSecret: "test-only-secret"}
	err := c.SetAllSKUPromoRate(ctx, creds, 7)
	require.True(t, adguard.IsUnknown(err))
	require.Equal(t, int32(1), calls.Load())
	require.True(t, adguard.WasNotSent(c.DeactivateAllSKUPromo(ctx, creds)))
	require.True(t, adguard.WasNotSent(c.ActivateCampaign(ctx, creds, 101)))
	require.Equal(t, int32(1), calls.Load())
	var state string
	var global bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT c.status,h.global_scope FROM ad_native_commands c JOIN ad_native_resource_holds h ON h.command_id=c.id`).Scan(&state, &global))
	require.Equal(t, "unknown", state)
	require.True(t, global)
}

func TestApprovedAdCommandCannotEnterTransportWithoutFreshPersonalGate(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	r := adTestRequest(t, "personal-gate-test", []string{"101"})
	id := uuid.New()
	require.NoError(t, q.ReserveApprovedAdCommand(ctx, id, r, uuidToPgtype(uuid.New()), uuidToPgtype(uuid.New()), strings.Repeat("a", 64), time.Now(), time.Now().Add(time.Minute)))
	_, err := q.BeginAdvertisingMutation(adguard.WithApprovedCommand(ctx, id), r)
	require.Error(t, err, "an approved ID alone cannot authorize transport without a fresh personal gate")
	row, err := q.ReadNativeAdCommand(ctx, id)
	require.NoError(t, err)
	require.False(t, row.StartedAt.Valid)
}
