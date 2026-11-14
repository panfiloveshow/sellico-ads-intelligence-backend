package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/require"
)

// Test-only physical holds deliberately have a different local cabinet from
// the request. A local cabinet alias or its deletion must not release them.
func seedLegacyOzonHold(t *testing.T, pool *pgxpool.Pool, account, status string) {
	t.Helper()
	var resource any = uuid.NewString()
	if status == "failed" || status == "applied" {
		resource = nil
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO ozon_price_write_attempts
 (change_id,seller_cabinet_id,product_id,vendor_account_key,resource_key,status,price_cents,baseline,
  dispatched_at,acknowledged_at,verified_at)
 VALUES($1,$2,101,$3,$4,$5,10000,'{}',now(),
  CASE WHEN $5='applied' THEN now() ELSE NULL END,
  CASE WHEN $5='applied' THEN now() ELSE NULL END)`, uuid.New(), uuid.New(), strings.TrimPrefix(account, "ozon:"), resource, status)
	require.NoError(t, err)
}

func seedLegacyWBHold(t *testing.T, pool *pgxpool.Pool, status string, decision map[string]any) (pgtype.UUID, pgtype.UUID, sqlcgen.PriceChange) {
	t.Helper()
	ws := testdb.Workspace(t, pool)
	cab := testdb.WBCabinet(t, pool, ws)
	q := sqlcgen.New(pool)
	row, err := q.EnqueuePriceChange(context.Background(), sqlcgen.CreatePriceChangeParams{
		WorkspaceID: uuidToPgtype(ws), SellerCabinetID: uuidToPgtype(cab), WbProductID: 101,
		OldPriceRub: 1000, NewPriceRub: 900, Source: "manual",
	})
	require.NoError(t, err)
	raw, err := json.Marshal(decision)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `UPDATE price_changes SET wb_status=$2,decision_context=$3 WHERE id=$1`, row.ID, status, raw)
	require.NoError(t, err)
	return uuidToPgtype(ws), uuidToPgtype(cab), row
}

func assertNoLegacyGrant(t *testing.T, pool *pgxpool.Pool, request domain.LegacyPriceDispatchRequest, message string) {
	t.Helper()
	grant, err := NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(context.Background(), request)
	require.ErrorContains(t, err, message)
	require.False(t, grant.Authorized)
	var count int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_dispatch_authorizations").Scan(&count))
	require.Zero(t, count, "a rejected hold must not consume or issue a transport authorization")
}

func TestLegacyPriceDispatchRejectsOzonActivePhysicalHolds(t *testing.T) {
	for _, status := range []string{"submitting", "verifying", "submit_unknown"} {
		t.Run(status, func(t *testing.T) {
			pool := testdb.New(t)
			r := legacyPriceRequest("ozon")
			seedLegacyOzonHold(t, pool, r.AccountKey, status)
			assertNoLegacyGrant(t, pool, r, "active price write")
		})
	}
}

func TestLegacyPriceDispatchRejectsWBActivePhysicalHolds(t *testing.T) {
	for _, status := range []string{"submitting", "submit_unknown", "uploaded"} {
		t.Run(status, func(t *testing.T) {
			pool := testdb.New(t)
			r := legacyPriceRequest("wb")
			seedLegacyWBHold(t, pool, status, map[string]any{"wb_dispatch": map[string]any{"account_key": r.AccountKey}})
			assertNoLegacyGrant(t, pool, r, "active price write")
		})
	}
}

func TestLegacyPriceDispatchCannotInventBindingForOldWBHolds(t *testing.T) {
	for name, decision := range map[string]map[string]any{
		"absent":    {},
		"empty":     {"wb_dispatch": map[string]any{"account_key": ""}},
		"boolean":   {"wb_dispatch": map[string]any{"account_key": false}},
		"invalid":   {"wb_dispatch": map[string]any{"account_key": "wb:unknown"}},
		"oversized": {"wb_dispatch": map[string]any{"account_key": strings.Repeat("x", 5000)}},
	} {
		t.Run(name, func(t *testing.T) {
			pool := testdb.New(t)
			seedLegacyWBHold(t, pool, "submit_unknown", decision)
			assertNoLegacyGrant(t, pool, legacyPriceRequest("wb"), "price write account binding is unknown")
		})
	}
}

func TestLegacyPriceDispatchPreservesIndependentAccountsAndReleasedHolds(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		for _, status := range []string{"applied", "failed"} {
			t.Run(market+"/"+status, func(t *testing.T) {
				pool := testdb.New(t)
				r := legacyPriceRequest(market)
				other := market + ":" + strings.Repeat("e", 64)
				if market == "ozon" {
					seedLegacyOzonHold(t, pool, other, "submit_unknown")
					seedLegacyOzonHold(t, pool, r.AccountKey, status)
				} else {
					_, _, first := seedLegacyWBHold(t, pool, "submit_unknown", map[string]any{"wb_dispatch": map[string]any{"account_key": other}})
					// WB's unique active nmID is global. Release this separate
					// fixture's nmID before adding a terminal row of the same SKU.
					_, err := pool.Exec(context.Background(), "UPDATE price_changes SET wb_product_id=102 WHERE id=$1", first.ID)
					require.NoError(t, err)
					seedLegacyWBHold(t, pool, status, map[string]any{})
				}
				grant, err := NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(context.Background(), r)
				require.NoError(t, err)
				require.True(t, grant.Authorized)
			})
		}
	}
}

func TestWBDispatchMarkerBindsTheVerifiedAccountForLegacyBarrier(t *testing.T) {
	pool := testdb.New(t)
	r := legacyPriceRequest("wb")
	ws, cab, row := seedLegacyWBHold(t, pool, "submitting", map[string]any{"wb_write_protocol": "observed_v1"})
	batch := uuidToPgtype(uuid.New())
	_, err := pool.Exec(context.Background(), "UPDATE price_changes SET submission_batch_id=$2 WHERE id=$1", row.ID, batch)
	require.NoError(t, err)
	q := sqlcgen.New(pool)
	changed, err := q.BeginWBPriceDispatch(context.Background(), ws, cab, batch, []sqlcgen.WBPriceDispatchEvidence{{
		ChangeID: uuid.UUID(row.ID.Bytes).String(), NmID: 101, OldPriceRub: 1000, NewPriceRub: 900,
		Snapshot: json.RawMessage(`{"fixture_price":1000}`),
	}}, r.AccountKey)
	require.NoError(t, err)
	require.True(t, changed)
	var bound *string
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT decision_context#>>'{wb_dispatch,account_key}' FROM price_changes WHERE id=$1", row.ID).Scan(&bound))
	require.NotNil(t, bound)
	require.Equal(t, r.AccountKey, *bound)
	changed, err = q.BeginWBPriceDispatch(context.Background(), ws, cab, batch, []sqlcgen.WBPriceDispatchEvidence{{
		ChangeID: uuid.UUID(row.ID.Bytes).String(), NmID: 101, OldPriceRub: 1000, NewPriceRub: 900, Snapshot: json.RawMessage(`{}`),
	}}, "wb:"+strings.Repeat("e", 64))
	require.NoError(t, err)
	require.False(t, changed, "a consumed dispatch marker cannot be rebound to another account")
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT decision_context#>>'{wb_dispatch,account_key}' FROM price_changes WHERE id=$1", row.ID).Scan(&bound))
	require.Equal(t, r.AccountKey, *bound)
	assertNoLegacyGrant(t, pool, r, "active price write")
}

func TestLegacyPriceLookupIndexDowngradePreservesHoldAndDenial(t *testing.T) {
	pool := testdb.New(t)
	r := legacyPriceRequest("wb")
	_, _, row := seedLegacyWBHold(t, pool, "submit_unknown", map[string]any{"wb_dispatch": map[string]any{"account_key": r.AccountKey}})
	for _, suffix := range []string{"down", "up"} {
		raw, err := os.ReadFile("../../migrations/000079_legacy_price_active_hold_lookup." + suffix + ".sql")
		require.NoError(t, err)
		_, err = pool.Exec(context.Background(), string(raw))
		require.NoError(t, err)
		assertNoLegacyGrant(t, pool, r, "active price write")
		var status string
		require.NoError(t, pool.QueryRow(context.Background(), "SELECT wb_status FROM price_changes WHERE id=$1", row.ID).Scan(&status))
		require.Equal(t, "submit_unknown", status)
	}
}

func TestLegacyPriceHoldIsRecheckedAfterTheAccountTransactionCommits(t *testing.T) {
	pool := testdb.New(t)
	r := legacyPriceRequest("wb")
	_, _, row := seedLegacyWBHold(t, pool, "submit_unknown", map[string]any{"wb_dispatch": map[string]any{"account_key": "wb:" + strings.Repeat("e", 64)}})
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "price-execution:"+r.AccountKey)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "UPDATE price_changes SET decision_context=jsonb_build_object('wb_dispatch',jsonb_build_object('account_key',$2::text)) WHERE id=$1", row.ID, r.AccountKey)
	require.NoError(t, err)
	_, err = NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(ctx, r)
	require.ErrorIs(t, err, sqlcgen.ErrPriceExecutionAccountHeld)
	require.NoError(t, tx.Commit(ctx))
	assertNoLegacyGrant(t, pool, r, "active price write")
}

func TestLegacyPriceMissingHoldStorageNeverAuthorizes(t *testing.T) {
	for market, table := range map[string]string{"wb": "price_changes", "ozon": "ozon_price_write_attempts"} {
		t.Run(market, func(t *testing.T) {
			pool := testdb.New(t)
			_, err := pool.Exec(context.Background(), "ALTER TABLE "+table+" RENAME TO isolated_unavailable_hold_storage")
			require.NoError(t, err)
			grant, err := NewLegacyPriceDispatchService(sqlcgen.New(pool), true).Authorize(context.Background(), legacyPriceRequest(market))
			require.Error(t, err)
			require.False(t, grant.Authorized)
			var count int
			require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM legacy_price_dispatch_authorizations").Scan(&count))
			require.Zero(t, count)
		})
	}
}
