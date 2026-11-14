package service

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

func TestWBPricePollingNeverResendsUnknownUpload(t *testing.T) {
	var calls atomic.Int32
	var lastProduct atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/list/goods/filter" {
			var lookup struct {
				NmList []int64 `json:"nmList"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&lookup))
			require.Len(t, lookup.NmList, 1)
			price := int64(1000)
			if lookup.NmList[0] == 102 {
				price = 1800
			}
			_, _ = w.Write([]byte(wbObservedPriceBody(lookup.NmList[0], price, 0, false)))
			return
		}
		require.Equal(t, "/api/v2/upload/task", r.URL.Path)
		var input struct {
			Data []wb.PriceUpdateItem `json:"data"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
		require.Len(t, input.Data, 1)
		lastProduct.Store(input.Data[0].NmID)
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			require.NoError(t, err)
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":77},"error":false}`))
	}))
	defer server.Close()
	pool := testdb.New(t)
	ws := testdb.Workspace(t, pool)
	cab := testdb.WBCabinet(t, pool, ws)
	key := testdb.EncryptionKey
	token, err := crypto.Encrypt(testAdvertisingToken(), key)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, cab, token)
	require.NoError(t, err)
	q := sqlcgen.New(pool)
	s := NewRepricerService(q, wb.NewClient(&config.Config{WBAPIBaseURL: server.URL, WBAPIRateLimit: 10000}, zerolog.Nop()), key, zerolog.Nop())
	row, err := q.EnqueuePriceChange(context.Background(), sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(ws), SellerCabinetID: uuidToPgtype(cab), WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 900, Source: "manual"})
	require.NoError(t, err)
	_, err = s.drainCabinetPriceChanges(context.Background(), ws, cab)
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
	var status string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT wb_status FROM price_changes WHERE id=$1`, row.ID).Scan(&status))
	require.Equal(t, "submit_unknown", status)
	_, err = s.PollUploadTasks(context.Background(), ws)
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load(), "polling must not replay a request whose receipt was lost")
	_, err = q.EnqueuePriceChange(context.Background(), sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(ws), SellerCabinetID: uuidToPgtype(cab), WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 800, Source: "manual"})
	require.NoError(t, err)
	_, err = s.PollUploadTasks(context.Background(), ws)
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
	_, err = q.EnqueuePriceChange(context.Background(), sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(ws), SellerCabinetID: uuidToPgtype(cab), WbProductID: 102, OldPriceRub: 1800, NewPriceRub: 1500, Source: "manual"})
	require.NoError(t, err)
	_, err = s.PollUploadTasks(context.Background(), ws)
	require.NoError(t, err)
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, int64(102), lastProduct.Load(), "an unrelated product can still proceed")
	var target int64
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT wb_status,new_price_rub FROM price_changes WHERE id=$1`, row.ID).Scan(&status, &target))
	require.Equal(t, "submit_unknown", status)
	require.Equal(t, int64(900), target)
}

func TestWBPricePhysicalHoldSurvivesCabinetAliasAndSoftDeletion(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	firstWS, otherWS := testdb.Workspace(t, pool), testdb.Workspace(t, pool)
	first, other := testdb.WBCabinet(t, pool, firstWS), testdb.WBCabinet(t, pool, otherWS)
	row, err := q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(firstWS), SellerCabinetID: uuidToPgtype(first), WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 900, Source: "manual"})
	require.NoError(t, err)
	claimed, err := q.ClaimPendingPriceChanges(ctx, uuidToPgtype(firstWS), uuidToPgtype(first), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = q.MarkSubmittingPriceChangesUnknown(ctx, uuidToPgtype(firstWS), []pgtype.UUID{row.ID}, textToPgtype("response lost"))
	require.NoError(t, err)
	_, err = q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(otherWS), SellerCabinetID: uuidToPgtype(other), WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 800, Source: "manual"})
	require.NoError(t, err)
	claimed, err = q.ClaimPendingPriceChanges(ctx, uuidToPgtype(otherWS), uuidToPgtype(other), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Empty(t, claimed, "a physical nmID is shared across local aliases and workspaces")
	require.NoError(t, q.SoftDeleteSellerCabinet(ctx, uuidToPgtype(first)))
	claimed, err = q.ClaimPendingPriceChanges(ctx, uuidToPgtype(otherWS), uuidToPgtype(other), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Empty(t, claimed, "supported cabinet deletion must not release an uncertain command")
	_, err = q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(otherWS), SellerCabinetID: uuidToPgtype(other), WbProductID: 102, OldPriceRub: 1800, NewPriceRub: 1500, Source: "manual"})
	require.NoError(t, err)
	claimed, err = q.ClaimPendingPriceChanges(ctx, uuidToPgtype(otherWS), uuidToPgtype(other), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, int64(102), claimed[0].WbProductID)
}

func TestWBPricePhysicalHoldSerializesConcurrentAliases(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	type scope struct{ ws, cab uuid.UUID }
	var scopes []scope
	for i := 0; i < 2; i++ {
		ws := testdb.Workspace(t, pool)
		cab := testdb.WBCabinet(t, pool, ws)
		scopes = append(scopes, scope{ws, cab})
		_, err := q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(ws), SellerCabinetID: uuidToPgtype(cab), WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 900, Source: "manual"})
		require.NoError(t, err)
	}
	type result struct {
		count int
		err   error
	}
	start := make(chan struct{})
	done := make(chan result, 2)
	for _, item := range scopes {
		go func(item scope) {
			<-start
			rows, err := q.ClaimPendingPriceChanges(ctx, uuidToPgtype(item.ws), uuidToPgtype(item.cab), uuidToPgtype(uuid.New()), 1000)
			done <- result{len(rows), err}
		}(item)
	}
	close(start)
	count := 0
	for i := 0; i < 2; i++ {
		r := <-done
		require.NoError(t, r.err)
		count += r.count
	}
	require.Equal(t, 1, count, "only one local alias may enter the vendor transport phase")
}

func TestWBPricePhysicalHoldMigrationProtectsActiveAndConflictingHistory(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx := context.Background()
	ws := testdb.Workspace(t, pool)
	cab := testdb.WBCabinet(t, pool, ws)
	row, err := q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(ws), SellerCabinetID: uuidToPgtype(cab), WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 900, Source: "manual"})
	require.NoError(t, err)
	claimed, err := q.ClaimPendingPriceChanges(ctx, uuidToPgtype(ws), uuidToPgtype(cab), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	down, err := os.ReadFile("../../migrations/000068_wb_price_physical_hold.down.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(down))
	require.ErrorContains(t, err, "reconcile active WB price changes")
	_, err = q.UpdatePriceChangeStatus(ctx, sqlcgen.UpdatePriceChangeStatusParams{ID: row.ID, WbStatus: "failed", Error: textToPgtype("test fixture closed without HTTP")})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(down))
	require.NoError(t, err)
	up, err := os.ReadFile("../../migrations/000068_wb_price_physical_hold.up.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(up))
	require.NoError(t, err)
	// Rehearse an old database with conflicting active aliases. The upgrade
	// must stop for reconciliation instead of guessing which command won.
	_, err = pool.Exec(ctx, `DROP INDEX price_changes_one_transport_per_nm`)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		w := testdb.Workspace(t, pool)
		c := testdb.WBCabinet(t, pool, w)
		_, err = q.CreatePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(w), SellerCabinetID: uuidToPgtype(c), WbProductID: 202, OldPriceRub: 1000, NewPriceRub: 900, Source: "manual", WbStatus: "submitting"})
		require.NoError(t, err)
	}
	_, err = pool.Exec(ctx, string(up))
	require.ErrorContains(t, err, "reconcile concurrent WB price changes")
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM price_changes WHERE wb_product_id=202 AND wb_status='submitting'`).Scan(&count))
	require.Equal(t, 2, count, "migration must preserve both unresolved records")
}

func TestWBPricePhysicalHoldConflictRollsBackTheWholeClaim(t *testing.T) {
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstWS, secondWS := testdb.Workspace(t, pool), testdb.Workspace(t, pool)
	first, second := testdb.WBCabinet(t, pool, firstWS), testdb.WBCabinet(t, pool, secondWS)
	for _, item := range []struct {
		ws, cab uuid.UUID
		nm      int64
	}{{firstWS, first, 101}, {secondWS, second, 102}, {secondWS, second, 101}} {
		_, err := q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(item.ws), SellerCabinetID: uuidToPgtype(item.cab), WbProductID: item.nm, OldPriceRub: 1000, NewPriceRub: 900, Source: "manual"})
		require.NoError(t, err)
	}
	blocker, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer blocker.Rollback(context.Background())
	rows, err := q.WithTx(blocker).ClaimPendingPriceChanges(ctx, uuidToPgtype(firstWS), uuidToPgtype(first), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	secondConn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer secondConn.Release()
	pid := int32(secondConn.Conn().PgConn().PID())
	type result struct {
		rows []sqlcgen.PriceChange
		err  error
	}
	done := make(chan result, 1)
	go func() {
		rows, err := sqlcgen.New(secondConn.Conn()).ClaimPendingPriceChanges(ctx, uuidToPgtype(secondWS), uuidToPgtype(second), uuidToPgtype(uuid.New()), 1000)
		done <- result{rows, err}
	}()
	// Force the stale statement snapshot to encounter the committed unique
	// hold, rather than merely observing it through NOT EXISTS beforehand.
	require.Eventually(t, func() bool {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting)
		return err == nil && waiting
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, blocker.Commit(ctx))
	outcome := <-done
	require.NoError(t, outcome.err)
	require.Empty(t, outcome.rows, "a rolled-back RETURNING slice must never reach dispatch")
	var pending int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM price_changes WHERE seller_cabinet_id=$1 AND wb_status='pending'`, second).Scan(&pending))
	require.Equal(t, 2, pending)
	rows, err = q.ClaimPendingPriceChanges(ctx, uuidToPgtype(secondWS), uuidToPgtype(second), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(102), rows[0].WbProductID)
}
