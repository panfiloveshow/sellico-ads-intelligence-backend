package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/require"
)

func legacyPriceRequest(market string) domain.LegacyPriceDispatchRequest {
	endpoint := "/v1/product/import/prices"
	account := "ozon:" + strings.Repeat("b", 64)
	if market == "wb" {
		endpoint = "/api/v2/upload/task"
		account, _ = adguard.WBAccountKey(testAdvertisingToken())
	}
	return domain.LegacyPriceDispatchRequest{Version: "legacy_price_dispatch_v1", Origin: "products", CommandID: uuid.NewString(), SentAt: time.Now().Unix(), Marketplace: market, Method: "POST", AccountKey: account, Endpoint: endpoint, PayloadSHA256: strings.Repeat("a", 64)}
}

func legacyPriceAccountControl(t *testing.T, q *sqlcgen.Queries, r domain.LegacyPriceDispatchRequest, mode string) {
	t.Helper()
	ctx := context.Background()
	before, err := q.PriceExecutionControl(ctx, r.AccountKey, r.Marketplace)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := domain.PriceExecutionControlOperation{ID: uuid.New(), WorkspaceID: uuid.New(), ActorID: uuid.New(), ExternalUserID: "7", ExternalWorkspaceID: "3", IntegrationID: "28", IntegrationName: "Isolated legacy transport", CabinetID: uuid.New(), Marketplace: r.Marketplace, AccountKey: r.AccountKey, CredentialDigest: strings.Repeat("a", 64), Scope: priceAccountControlScope, TargetMode: mode, Before: before, Proof: domain.PriceExecutionAccountProof{ResourceID: "101", Source: "WB Prices API", ObservedAt: now}, Reason: "Explicit isolated stop", Fingerprint: strings.Repeat("b", 64), Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if r.Marketplace == "ozon" {
		o.Proof.Source = "Ozon Seller API"
	}
	require.NoError(t, q.CreatePriceExecutionControlOperation(ctx, o))
	require.NoError(t, q.ApplyPriceExecutionControl(ctx, o, func(context.Context) error { return nil }))
}

func TestLegacyPriceDispatchIsOneUseAndStopOrManualOpeningCannotGrantRawCommands(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			pool := testdb.New(t)
			q := sqlcgen.New(pool)
			s := NewLegacyPriceDispatchService(q, true)
			r := legacyPriceRequest(market)
			grant, err := s.Authorize(context.Background(), r)
			require.NoError(t, err)
			require.True(t, grant.Authorized)
			require.Equal(t, r.CommandID, grant.CommandID)
			require.Equal(t, r.PayloadSHA256, grant.PayloadSHA256)
			require.Equal(t, r.Endpoint, grant.Endpoint)
			require.Equal(t, "POST", grant.Method)
			require.Zero(t, grant.ControlVersion)
			require.EqualValues(t, 5000, grant.ValidForMS)
			require.Equal(t, 5*time.Second, grant.ExpiresAt.Sub(grant.IssuedAt))
			_, err = s.Authorize(context.Background(), r)
			require.ErrorIs(t, err, sqlcgen.ErrLegacyPriceDispatchReplay)
			r.PayloadSHA256 = strings.Repeat("c", 64)
			_, err = s.Authorize(context.Background(), r)
			require.ErrorIs(t, err, sqlcgen.ErrLegacyPriceDispatchReplay)
			legacyPriceAccountControl(t, q, r, "stopped")
			r.CommandID = uuid.NewString()
			_, err = s.Authorize(context.Background(), r)
			require.ErrorIs(t, err, sqlcgen.ErrPriceExecutionStopped)
			legacyPriceAccountControl(t, q, r, "manual_only")
			r.CommandID = uuid.NewString()
			_, err = s.Authorize(context.Background(), r)
			require.ErrorIs(t, err, sqlcgen.ErrPriceExecutionStopped)
			var count int
			require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_dispatch_authorizations").Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

func TestLegacyPriceDispatchUsesTheSameAccountLockAndRechecksRequestAgeInSQL(t *testing.T) {
	pool := testdb.New(t)
	s := NewLegacyPriceDispatchService(sqlcgen.New(pool), true)
	r := legacyPriceRequest("wb")
	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	_, err = tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "price-execution:"+r.AccountKey)
	require.NoError(t, err)
	_, err = s.Authorize(context.Background(), r)
	require.ErrorIs(t, err, sqlcgen.ErrPriceExecutionAccountHeld)
	require.NoError(t, tx.Rollback(context.Background()))
	for _, delta := range []time.Duration{-time.Minute, time.Minute} {
		r.SentAt = time.Now().Add(delta).Unix()
		_, err = s.Authorize(context.Background(), r)
		require.ErrorIs(t, err, sqlcgen.ErrLegacyPriceDispatchReplay)
	}
	r.SentAt = time.Now().Unix()
	_, err = s.Authorize(context.Background(), r)
	require.NoError(t, err)
}

// Retired Products senders: valid, correctly signed requests on both
// platforms record no admission, so no new account-wide hold can appear and
// personal manager prices of the account stay dispatchable.
func TestRetiredLegacyPriceDispatchRecordsNoAdmissionOnEitherPlatform(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	s := NewLegacyPriceDispatchService(q, false)
	for _, market := range []string{"wb", "ozon"} {
		r := legacyPriceRequest(market)
		grant, err := s.Authorize(context.Background(), r)
		require.ErrorIs(t, err, ErrLegacyPriceDispatchRetired)
		require.False(t, grant.Authorized)
		held, err := q.LegacyPriceHeldCount(context.Background(), r.AccountKey)
		require.NoError(t, err)
		require.Zero(t, held)
		bad := r
		bad.Origin = "browser"
		_, err = s.Authorize(context.Background(), bad)
		require.ErrorIs(t, err, ErrInvalidLegacyPriceDispatch, "malformed requests keep their own answer")
	}
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_dispatch_authorizations").Scan(&n))
	require.Zero(t, n)
	var nilService *LegacyPriceDispatchService
	_, err := nilService.Authorize(context.Background(), legacyPriceRequest("wb"))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrLegacyPriceDispatchRetired), "a missing service is unavailable, not retired")
}

func TestLegacyPriceDispatchRejectsUnverifiedScopeAndUnavailableStorage(t *testing.T) {
	pool := testdb.New(t)
	s := NewLegacyPriceDispatchService(sqlcgen.New(pool), true)
	for _, mutate := range []func(*domain.LegacyPriceDispatchRequest){
		func(r *domain.LegacyPriceDispatchRequest) { r.Origin = "browser" },
		func(r *domain.LegacyPriceDispatchRequest) { r.Version = "manual_price_approval" },
		func(r *domain.LegacyPriceDispatchRequest) { r.Method = "GET" },
		func(r *domain.LegacyPriceDispatchRequest) { r.CommandID = uuid.Nil.String() },
		func(r *domain.LegacyPriceDispatchRequest) { r.AccountKey = "wb:opaque" },
		func(r *domain.LegacyPriceDispatchRequest) { r.Marketplace = "ozon" },
		func(r *domain.LegacyPriceDispatchRequest) { r.PayloadSHA256 = "" },
		func(r *domain.LegacyPriceDispatchRequest) { r.Endpoint = "/api/v2/list/goods/filter" },
		func(r *domain.LegacyPriceDispatchRequest) { r.Endpoint += "?other=1" },
		func(r *domain.LegacyPriceDispatchRequest) { r.SentAt = 0 },
	} {
		r := legacyPriceRequest("wb")
		mutate(&r)
		_, err := s.Authorize(context.Background(), r)
		require.ErrorIs(t, err, ErrInvalidLegacyPriceDispatch)
	}
	_, err := pool.Exec(context.Background(), "ALTER TABLE legacy_price_dispatch_authorizations RENAME TO isolated_missing_authorizations")
	require.NoError(t, err)
	_, err = s.Authorize(context.Background(), legacyPriceRequest("wb"))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrInvalidLegacyPriceDispatch))
}

func TestLegacyPriceDispatchDowngradeCannotDiscardIssuedAudit(t *testing.T) {
	pool := testdb.New(t)
	_, err := NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(context.Background(), legacyPriceRequest("wb"))
	require.NoError(t, err)
	raw, err := os.ReadFile("../../migrations/000078_legacy_price_dispatch_authorizations.down.sql")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), string(raw))
	require.ErrorContains(t, err, "cannot discard legacy price dispatch")
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_dispatch_authorizations").Scan(&count))
	require.Equal(t, 1, count)
}
