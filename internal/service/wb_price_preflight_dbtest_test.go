package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/stretchr/testify/require"
)

func TestWBPriceSourceDriftStopsBeforeHTTP(t *testing.T) {
	for name, body := range map[string]string{
		"changed price":         wbObservedPriceBody(101, 999, 17, false),
		"changed discount":      wbObservedPriceBody(101, 1000, 18, false),
		"different second size": wbObservedPriceBody(101, 1000, 17, true),
		"missing article":       `{"error":false,"data":{"listGoods":[]}}`,
		"malformed response":    `{"error":false,"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newWBPriceReadbackFixture(t)
			f.prices.Store(body)
			row := f.enqueue(t, 101, 901)
			_, err := f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
			require.Error(t, err)
			require.Zero(t, f.uploads.Load(), "source drift or an unknown source must not start the price mutation")
			require.Equal(t, "failed", f.status(t, row), "this command is definitely unsent")
		})
	}
}

func TestWBPricePreflightPersistsActualSizesAndDispatchMarker(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	_, err := f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
	require.NoError(t, err)
	var baseline []byte
	var protocol string
	var started bool
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT decision_context->'wb_preflight',decision_context->>'wb_write_protocol',decision_context ? 'wb_dispatch' FROM price_changes WHERE id=$1`, row.ID).Scan(&baseline, &protocol, &started))
	require.Equal(t, "observed_v1", protocol)
	require.True(t, started)
	require.Contains(t, string(baseline), `"price_cents": 100000`)
	require.Contains(t, string(baseline), `"discounted_price_cents": 83000`)
	require.Equal(t, "uploaded", f.status(t, row))
}

func TestWBPriceClaimCannotEnterTransportTwice(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	claimed, err := f.q.ClaimPendingPriceChanges(context.Background(), uuidToPgtype(f.ws), uuidToPgtype(f.cab), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = f.svc.uploadClaimedChunk(context.Background(), f.ws, f.cab, testAdvertisingToken(), claimed)
	require.NoError(t, err)
	_, err = f.svc.uploadClaimedChunk(context.Background(), f.ws, f.cab, testAdvertisingToken(), claimed)
	require.Error(t, err)
	require.Equal(t, int32(1), f.uploads.Load())
	require.Equal(t, "uploaded", f.status(t, row))
}

func TestWBPriceRecoveredUnsentClaimCannotDispatchLater(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	ctx := context.Background()
	claimed, err := f.q.ClaimPendingPriceChanges(ctx, uuidToPgtype(f.ws), uuidToPgtype(f.cab), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE price_changes SET updated_at=now()-interval '31 minutes' WHERE id=$1`, row.ID)
	require.NoError(t, err)
	count, _, err := f.q.RecoverStaleRepricerState(ctx, uuidToPgtype(f.ws), pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Minute), Valid: true})
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	require.Equal(t, "failed", f.status(t, row), "new protocol proves that transport never started")
	_, err = f.svc.uploadClaimedChunk(ctx, f.ws, f.cab, testAdvertisingToken(), claimed)
	require.Error(t, err)
	require.Zero(t, f.uploads.Load(), "an expired worker cannot send after its unsent claim was recovered")
}

func TestWBPriceLegacySubmittingClaimIsNotAssumedUnsent(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	ctx := context.Background()
	_, err := f.q.ClaimPendingPriceChanges(ctx, uuidToPgtype(f.ws), uuidToPgtype(f.cab), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE price_changes SET decision_context='{}',updated_at=now()-interval '31 minutes' WHERE id=$1`, row.ID)
	require.NoError(t, err)
	_, _, err = f.q.RecoverStaleRepricerState(ctx, uuidToPgtype(f.ws), pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Minute), Valid: true})
	require.NoError(t, err)
	require.Equal(t, "submit_unknown", f.status(t, row))
	require.Zero(t, f.uploads.Load())
}

func TestWBPriceStartedRecoveryKeepsHoldAndAcceptsLateReceipt(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	t.Cleanup(open)
	f.onUpload.Store(func() { close(entered); <-release })
	done := make(chan error, 1)
	go func() { _, err := f.svc.drainCabinetPriceChanges(ctx, f.ws, f.cab); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("price transport did not start")
	}
	_, err := f.pool.Exec(ctx, `UPDATE price_changes SET updated_at=now()-interval '31 minutes' WHERE id=$1`, row.ID)
	require.NoError(t, err)
	_, _, err = f.q.RecoverStaleRepricerState(ctx, uuidToPgtype(f.ws), pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Minute), Valid: true})
	require.NoError(t, err)
	require.Equal(t, "submit_unknown", f.status(t, row))
	active, err := f.q.HasInFlightPriceChange(ctx, uuidToPgtype(f.ws), 101)
	require.NoError(t, err)
	require.True(t, active)
	open()
	require.NoError(t, <-done)
	require.Equal(t, "uploaded", f.status(t, row), "a late native receipt must not be discarded after recovery")
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	require.Equal(t, 1, f.poll(t))
	require.Equal(t, "applied", f.status(t, row))
	require.Equal(t, int32(1), f.uploads.Load())
}

func TestWBPriceCanceledPreflightIsDefinitelyUnsent(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	claimed, err := f.q.ClaimPendingPriceChanges(context.Background(), uuidToPgtype(f.ws), uuidToPgtype(f.cab), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = f.svc.uploadClaimedChunk(ctx, f.ws, f.cab, testAdvertisingToken(), claimed)
	require.Error(t, err)
	require.Zero(t, f.uploads.Load())
	require.Equal(t, "failed", f.status(t, row), "cancellation cannot discard the known-unsent outcome")
}

type wbPriceCancelAtReceiptDB struct {
	sqlcgen.DBTX
	cancel context.CancelFunc
	once   sync.Once
}

func (db *wbPriceCancelAtReceiptDB) Begin(ctx context.Context) (pgx.Tx, error) {
	return db.DBTX.(interface {
		Begin(context.Context) (pgx.Tx, error)
	}).Begin(ctx)
}

func (db *wbPriceCancelAtReceiptDB) QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row {
	// Inject a client disconnect after WB returned its receipt, immediately
	// before the journal can persist it. Real SQL still runs against testdb.
	if strings.Contains(query, "INSERT INTO price_upload_tasks") {
		db.once.Do(db.cancel)
	}
	return db.DBTX.QueryRow(ctx, query, args...)
}

func TestWBPriceCallerCancellationCannotDiscardKnownReceipt(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	claimed, err := f.q.ClaimPendingPriceChanges(context.Background(), uuidToPgtype(f.ws), uuidToPgtype(f.cab), uuidToPgtype(uuid.New()), 1000)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.queries = sqlcgen.New(&wbPriceCancelAtReceiptDB{DBTX: f.pool, cancel: cancel})
	taskID, err := f.svc.uploadClaimedChunk(ctx, f.ws, f.cab, testAdvertisingToken(), claimed)
	require.NoError(t, err)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.NotEqual(t, uuid.Nil, taskID)
	require.Equal(t, "uploaded", f.status(t, row), "the native receipt is recoverable after the HTTP caller leaves")
	require.Equal(t, int32(1), f.uploads.Load())
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	require.Equal(t, 1, f.poll(t))
	require.Equal(t, "applied", f.status(t, row))
}
