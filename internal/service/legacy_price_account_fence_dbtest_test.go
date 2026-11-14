package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/require"
)

// A v1 grant predates these candidates. Its admission deadline does not reveal
// whether the separate Products process reached Native or when Native finishes.
func legacyFenceCandidate(t *testing.T, pool *pgxpool.Pool, request domain.LegacyPriceDispatchRequest, manual bool) (func() error, func()) {
	t.Helper()
	ctx := context.Background()
	q := sqlcgen.New(pool)
	decision := map[string]any{}
	if manual {
		decision["manager_approval"] = map[string]any{"execution_control_version": 1, "expires_at": time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	}
	if request.Marketplace == "wb" {
		decision["wb_write_protocol"] = "observed_v1"
		ws, cab, row := seedLegacyWBHold(t, pool, "submitting", decision)
		batch := uuidToPgtype(uuid.New())
		_, err := pool.Exec(ctx, "UPDATE price_changes SET submission_batch_id=$2 WHERE id=$1", row.ID, batch)
		require.NoError(t, err)
		return func() error {
				changed, err := q.BeginWBPriceDispatch(ctx, ws, cab, batch, []sqlcgen.WBPriceDispatchEvidence{{
					ChangeID: uuid.UUID(row.ID.Bytes).String(), NmID: 101, OldPriceRub: 1000, NewPriceRub: 900, Snapshot: json.RawMessage(`{"price":1000}`),
				}}, request.AccountKey)
				if err == nil && !changed {
					return sqlcgen.ErrPriceExecutionControlChanged
				}
				return err
			}, func() {
				var started bool
				require.NoError(t, pool.QueryRow(ctx, "SELECT decision_context ? 'wb_dispatch' FROM price_changes WHERE id=$1", row.ID).Scan(&started))
				require.False(t, started, "the one-use WB Native marker must not commit")
			}
	}
	ws := testdb.Workspace(t, pool)
	cab := uuidToPgtype(testdb.OzonCabinet(t, pool, ws, domain.OzonCredentials{ClientID: "test-only-client", APIKey: "test-only-key"}))
	row, err := q.InsertOzonPriceChange(ctx, sqlcgen.InsertOzonPriceChangeParams{SellerCabinetID: cab, Sku: 101, OfferID: textToPgtype("TEST-101"),
		OldPriceRub: floatToPgNumeric(1000), NewPriceRub: floatToPgNumeric(900), Source: "manual", Status: "pending"})
	require.NoError(t, err)
	raw, err := json.Marshal(decision)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE ozon_price_changes SET decision_context=$2 WHERE id=$1", row.ID, raw)
	require.NoError(t, err)
	account := strings.TrimPrefix(request.AccountKey, "ozon:")
	require.NoError(t, q.ClaimOzonPriceWrite(ctx, row.ID, cab, 101, account, ozonPriceResourceKey(account, 101), 100000, 90000, []byte(`{"price_cents":100000}`), 10))
	return func() error { return q.MarkOzonPricesDispatched(ctx, []pgtype.UUID{row.ID}) }, func() {
		lease, err := q.GetOzonPriceLease(ctx, row.ID)
		require.NoError(t, err)
		require.False(t, lease.DispatchedAt.Valid, "the Ozon Native marker must not commit")
	}
}

func TestLegacyPricePermitFencesNewNativeMarkersAfterAdmissionDeadlineAndManualOpening(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		for _, state := range []string{"fresh", "expired", "manual_only"} {
			t.Run(market+"/"+state, func(t *testing.T) {
				pool := testdb.New(t)
				request := legacyPriceRequest(market)
				grant, err := NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(context.Background(), request)
				require.NoError(t, err)
				require.True(t, grant.Authorized)
				if state == "expired" {
					// Simulate a retained historical grant, not a running timer.
					_, err = pool.Exec(context.Background(), `UPDATE legacy_price_dispatch_authorizations SET
 issued_at=issued_at-interval '1 day',expires_at=expires_at-interval '1 day',sent_at=sent_at-interval '1 day' WHERE command_id=$1`, request.CommandID)
					require.NoError(t, err)
				}
				if state == "manual_only" {
					legacyPriceAccountControl(t, sqlcgen.New(pool), request, "manual_only")
				}
				begin, assertUndispatched := legacyFenceCandidate(t, pool, request, state == "manual_only")
				require.ErrorIs(t, begin(), sqlcgen.ErrLegacyPriceActiveWrite)
				assertUndispatched()
			})
		}
	}
}

func TestLegacyPricePermitFencesAnotherRawCommandButKeepsNonceReplay(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			pool := testdb.New(t)
			s := NewLegacyPriceDispatchService(sqlcgen.New(pool), true)
			request := legacyPriceRequest(market)
			_, err := s.Authorize(context.Background(), request)
			require.NoError(t, err)
			_, err = s.Authorize(context.Background(), request)
			require.ErrorIs(t, err, sqlcgen.ErrLegacyPriceDispatchReplay)
			request.CommandID = uuid.NewString()
			_, err = s.Authorize(context.Background(), request)
			require.ErrorIs(t, err, sqlcgen.ErrLegacyPriceActiveWrite)
			var count int
			require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_dispatch_authorizations").Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

func TestLegacyPricePermitConcurrentRawCommandsHaveOneWinner(t *testing.T) {
	pool := testdb.New(t)
	s := NewLegacyPriceDispatchService(sqlcgen.New(pool), true)
	start := make(chan struct{})
	results := make(chan error, 12)
	var group sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		request := legacyPriceRequest("wb")
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := s.Authorize(context.Background(), request)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var success int
	for err := range results {
		if err == nil {
			success++
		} else {
			require.True(t, errors.Is(err, sqlcgen.ErrLegacyPriceActiveWrite) || errors.Is(err, sqlcgen.ErrPriceExecutionAccountHeld), "%v", err)
		}
	}
	require.Equal(t, 1, success)
}

func TestLegacyPricePermitLeavesAnotherPhysicalAccountEligible(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			pool := testdb.New(t)
			request := legacyPriceRequest(market)
			_, err := NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(context.Background(), request)
			require.NoError(t, err)
			request.AccountKey = market + ":" + strings.Repeat("e", 64)
			begin, _ := legacyFenceCandidate(t, pool, request, false)
			require.NoError(t, begin())
			query := "SELECT count(*) FROM price_changes WHERE decision_context ? 'wb_dispatch'"
			if market == "ozon" {
				query = "SELECT count(*) FROM ozon_price_write_attempts WHERE dispatched_at IS NOT NULL"
			}
			var count int
			require.NoError(t, pool.QueryRow(context.Background(), query).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

func TestLegacyPricePermitMissingStorageCannotCommitNativeMarker(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			pool := testdb.New(t)
			request := legacyPriceRequest(market)
			_, err := NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(context.Background(), request)
			require.NoError(t, err)
			begin, assertUndispatched := legacyFenceCandidate(t, pool, request, false)
			_, err = pool.Exec(context.Background(), "ALTER TABLE legacy_price_dispatch_authorizations RENAME TO retained_legacy_authorizations")
			require.NoError(t, err)
			require.Error(t, begin())
			assertUndispatched()
			var count int
			require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM retained_legacy_authorizations").Scan(&count))
			require.Equal(t, 1, count, "the isolated failure simulation retains the issued audit")
		})
	}
}
