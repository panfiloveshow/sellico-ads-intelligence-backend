package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

// This vendor boundary owns its observed prices independently of the database
// mirror. A lost response may still change the vendor's state.
type observedPriceWriter struct {
	mu         sync.Mutex
	prices     map[int64]ozon.PriceSnapshot
	writes     int
	lost       bool
	deferAck   bool
	readErr    error
	reads      int
	onRead     func(*observedPriceWriter)
	results    []ozon.PriceUpdateResult
	entered    chan struct{}
	release    chan struct{}
	enterOnce  sync.Once
	afterWrite func()
	pool       *pgxpool.Pool
	creds      domain.OzonCredentials
}

func (v *observedPriceWriter) ReadPriceSnapshots(_ context.Context, _ ozon.Credentials, ids []int64) (map[int64]ozon.PriceSnapshot, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	if v.onRead != nil {
		v.onRead(v)
	}
	if v.readErr != nil {
		return nil, v.readErr
	}
	rows := make(map[int64]ozon.PriceSnapshot)
	for _, id := range ids {
		if row, ok := v.prices[id]; ok {
			rows[id] = row
		}
	}
	return rows, nil
}

func (v *observedPriceWriter) UpdatePrices(ctx context.Context, _ ozon.Credentials, items []ozon.PriceUpdate) ([]ozon.PriceUpdateResult, error) {
	v.mu.Lock()
	v.writes++
	var result []ozon.PriceUpdateResult
	for _, item := range items {
		if !v.deferAck {
			p := v.prices[item.ProductID]
			p.PriceCents = int64(item.PriceRub*100 + 0.5)
			if item.OldPriceRub != nil {
				c := int64(*item.OldPriceRub*100 + 0.5)
				p.OldPriceCents = &c
			}
			if item.MinPriceRub != nil {
				c := int64(*item.MinPriceRub*100 + 0.5)
				p.MinPriceCents = &c
			}
			v.prices[item.ProductID] = p
		}
		result = append(result, ozon.PriceUpdateResult{ProductID: item.ProductID, Updated: true})
	}
	if v.results != nil {
		result = v.results
	}
	lost, entered, release, afterWrite := v.lost, v.entered, v.release, v.afterWrite
	v.mu.Unlock()
	if afterWrite != nil {
		afterWrite()
	}
	if entered != nil {
		v.enterOnce.Do(func() { close(entered) })
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if lost {
		return nil, errors.New("connection lost after request")
	}
	return result, nil
}

func newObservedRepricer(t *testing.T, v *observedPriceWriter) (*OzonRepricerService, *sqlcgen.Queries, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)
	ws := testdb.Workspace(t, pool)
	v.pool = pool
	v.creds = testdb.OzonCredentials()
	cab := testdb.OzonCabinet(t, pool, ws, v.creds)
	q := sqlcgen.New(pool)
	for _, p := range v.prices {
		err := q.UpsertOzonProductPrice(context.Background(), sqlcgen.UpsertOzonProductPriceParams{
			SellerCabinetID: uuidToPgtype(cab), Sku: p.ProductID, OfferID: textToPgtype(p.OfferID), PriceRub: floatToPgNumeric(float64(p.PriceCents) / 100),
		})
		require.NoError(t, err)
	}
	return &OzonRepricerService{queries: q, sellerClient: v, encryptionKey: testdb.EncryptionKey, logger: zerolog.Nop()}, q, ws, cab
}

func TestOzonPriceLostResponseQuarantinesProduct(t *testing.T) {
	v := &observedPriceWriter{prices: map[int64]ozon.PriceSnapshot{101: {ProductID: 101, OfferID: "TEST-101", PriceCents: 100000}}, lost: true}
	s, q, ws, cab := newObservedRepricer(t, v)
	ctx := context.Background()
	rows, err := s.ApplyManual(ctx, ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.Error(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "submit_unknown", rows[0].Status, "transport failure cannot establish rejection")
	fresh, err := s.ReconcilePriceChange(ctx, ws, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "submit_unknown", fresh.Status, "an identical observed price cannot attribute the lost command")
	_, _ = s.ApplyManual(ctx, ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 800}}, uuid.New())
	require.Equal(t, 1, v.writes, "unresolved request must prevent another price command")
	mirror, err := q.GetOzonProductPriceBySku(ctx, sqlcgen.GetOzonProductPriceBySkuParams{SellerCabinetID: uuidToPgtype(cab), Sku: 101})
	require.NoError(t, err)
	require.Equal(t, 1000.0, pgNumericToFloat(mirror.PriceRub), "unacknowledged observation must not become an optimistic success")
}

func TestOzonPriceReceiptRequiresObservedPrice(t *testing.T) {
	v := &observedPriceWriter{prices: map[int64]ozon.PriceSnapshot{101: {ProductID: 101, OfferID: "TEST-101", PriceCents: 100000}}, deferAck: true}
	s, q, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "verifying", rows[0].Status)
	mirror, err := q.GetOzonProductPriceBySku(context.Background(), sqlcgen.GetOzonProductPriceBySkuParams{SellerCabinetID: uuidToPgtype(cab), Sku: 101})
	require.NoError(t, err)
	require.Equal(t, 1000.0, pgNumericToFloat(mirror.PriceRub))
	require.NotEqual(t, domain.OzonPriceStatusApplied, rows[0].Status)
}

func observedVendor() *observedPriceWriter {
	return &observedPriceWriter{prices: map[int64]ozon.PriceSnapshot{101: {ProductID: 101, OfferID: "TEST-101", PriceCents: 100000}}}
}

func TestOzonPriceLateReadbackSurvivesServiceRestart(t *testing.T) {
	v := observedVendor()
	v.deferAck = true
	s, q, ws, cab := newObservedRepricer(t, v)
	ctx := context.Background()
	rows, err := s.ApplyManual(ctx, ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900.01}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "verifying", rows[0].Status)
	p := v.prices[101]
	p.PriceCents = 90001
	v.prices[101] = p
	restarted := &OzonRepricerService{queries: q, sellerClient: v, encryptionKey: testdb.EncryptionKey, logger: zerolog.Nop()}
	fresh, err := restarted.ReconcilePriceChange(ctx, ws, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "applied", fresh.Status)
	require.NotNil(t, fresh.AppliedAt)
	a, err := q.GetOzonPriceLease(ctx, uuidToPgtype(rows[0].ID))
	require.NoError(t, err)
	require.False(t, a.ResourceKey.Valid)
	require.True(t, a.AcknowledgedAt.Valid)
	require.True(t, a.VerifiedAt.Valid)
	mirror, err := q.GetOzonProductPriceBySku(ctx, sqlcgen.GetOzonProductPriceBySkuParams{SellerCabinetID: uuidToPgtype(cab), Sku: 101})
	require.NoError(t, err)
	require.Equal(t, 900.01, pgNumericToFloat(mirror.PriceRub))
	require.Equal(t, 1, v.writes)
}

func TestOzonPriceManualCapturesLiveBaselineAndRollbackRejectsVendorDrift(t *testing.T) {
	v := observedVendor()
	s, _, ws, cab := newObservedRepricer(t, v)
	p := v.prices[101]
	p.PriceCents = 120025
	v.prices[101] = p // DB mirror remains 1000
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "applied", rows[0].Status)
	require.Equal(t, 1200.25, *rows[0].OldPriceRub)
	p.PriceCents = 90001
	v.prices[101] = p // one kopeck is material; mirror still 900
	_, err = s.Rollback(context.Background(), ws, rows[0].ID)
	require.ErrorContains(t, err, "current price no longer matches")
	require.Equal(t, 1, v.writes)
}

func TestOzonPriceDriftBetweenCaptureAndDispatchSendsNothing(t *testing.T) {
	v := observedVendor()
	v.onRead = func(v *observedPriceWriter) {
		if v.reads == 2 {
			p := v.prices[101]
			p.PriceCents = 100001
			v.prices[101] = p
		}
	}
	s, _, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "failed", rows[0].Status)
	require.Equal(t, 0, v.writes)
}

func TestOzonPriceRechecksSourceAfterAcquiringProductHold(t *testing.T) {
	v := observedVendor()
	v.onRead = func(v *observedPriceWriter) {
		if v.reads == 3 {
			var held bool
			require.NoError(t, v.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM ozon_price_write_attempts WHERE product_id=101 AND resource_key IS NOT NULL AND dispatched_at IS NULL)`).Scan(&held))
			require.True(t, held, "source recheck must run while the product hold is owned")
			p := v.prices[101]
			p.PriceCents = 100001 // changed while this command was acquiring its hold
			v.prices[101] = p
		}
	}
	s, _, ws, cab := newObservedRepricer(t, v)
	_, _ = s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.Zero(t, v.writes, "the source must be rechecked while the physical product hold is owned")
}

func TestOzonPriceOptionalFieldsRequireExactReadback(t *testing.T) {
	v := observedVendor()
	v.deferAck = true
	s, _, ws, cab := newObservedRepricer(t, v)
	old, minimum := 1200.25, 850.01
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900.01, OldPriceRub: &old, MinPriceRub: &minimum}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "verifying", rows[0].Status)
	p := v.prices[101]
	p.PriceCents = 90001
	v.prices[101] = p
	fresh, err := s.ReconcilePriceChange(context.Background(), ws, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "verifying", fresh.Status)
	o, m := int64(120025), int64(85001)
	p.OldPriceCents = &o
	v.prices[101] = p
	fresh, err = s.ReconcilePriceChange(context.Background(), ws, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "verifying", fresh.Status)
	p.MinPriceCents = &m
	v.prices[101] = p
	fresh, err = s.ReconcilePriceChange(context.Background(), ws, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "applied", fresh.Status)
}

func TestOzonPriceDuplicateAndForeignReceiptsStayUnknown(t *testing.T) {
	for _, results := range [][]ozon.PriceUpdateResult{
		{{ProductID: 101, Updated: true}, {ProductID: 101, Updated: true}},
		{{ProductID: 101, Updated: true}, {ProductID: 999, Updated: true}},
		{{ProductID: 101, OfferID: "FOREIGN-OFFER", Updated: true}},
		{},
		{{ProductID: 101, Updated: false}},
		{{ProductID: 101, Updated: true, Errors: []string{"contradictory"}}},
	} {
		t.Run(fmt.Sprint(results), func(t *testing.T) {
			v := observedVendor()
			v.results = results
			s, _, ws, cab := newObservedRepricer(t, v)
			rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
			require.Error(t, err)
			require.Equal(t, "submit_unknown", rows[0].Status)
			require.Equal(t, 1, v.writes)
		})
	}
}

func TestOzonPriceConcurrentAliasAndKeyRotationShareHold(t *testing.T) {
	v := observedVendor()
	v.entered = make(chan struct{})
	v.release = make(chan struct{})
	s, _, ws, cab := newObservedRepricer(t, v)
	aliasCreds := v.creds
	aliasCreds.APIKey = "rotated-test-key"
	alias := testdb.OzonCabinet(t, v.pool, ws, aliasCreds)
	finished := make(chan error, 1)
	go func() {
		_, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
		finished <- err
	}()
	select {
	case <-v.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first dispatch did not start")
	}
	rows, err := s.ApplyManual(context.Background(), ws, alias, []OzonManualPriceInput{{SKU: 101, PriceRub: 800}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "failed", rows[0].Status)
	close(v.release)
	require.NoError(t, <-finished)
	require.Equal(t, 1, v.writes)
}

func TestOzonPriceCabinetDeletionCannotEraseUnknownHold(t *testing.T) {
	v := observedVendor()
	v.lost = true
	s, q, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.Error(t, err)
	_, err = v.pool.Exec(context.Background(), `DELETE FROM seller_cabinets WHERE id=$1`, cab)
	require.NoError(t, err)
	alias := testdb.OzonCabinet(t, v.pool, ws, v.creds)
	other, err := s.ApplyManual(context.Background(), ws, alias, []OzonManualPriceInput{{SKU: 101, PriceRub: 800}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "failed", other[0].Status)
	a, err := q.GetOzonPriceLease(context.Background(), uuidToPgtype(rows[0].ID))
	require.NoError(t, err)
	require.True(t, a.ResourceKey.Valid)
	require.Equal(t, 1, v.writes)
}

func TestOzonPriceInputRejectsDuplicateAndFractionalMoney(t *testing.T) {
	v := observedVendor()
	s, _, ws, cab := newObservedRepricer(t, v)
	for _, items := range [][]OzonManualPriceInput{
		{{SKU: 101, PriceRub: 900.001}},
		{{SKU: 101, PriceRub: 900}, {SKU: 101, PriceRub: 800}},
		{{SKU: 101, PriceRub: math.NaN()}},
		{{SKU: 101, PriceRub: math.Inf(1)}},
	} {
		_, err := s.ApplyManual(context.Background(), ws, cab, items, uuid.New())
		require.Error(t, err)
	}
	require.Zero(t, v.reads)
	require.Zero(t, v.writes)
}

func TestOzonPriceReconcileChecksOwnershipAndAccountBeforeVendorRead(t *testing.T) {
	v := observedVendor()
	v.deferAck = true
	s, _, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	reads := v.reads
	other := testdb.Workspace(t, v.pool)
	_, err = s.ReconcilePriceChange(context.Background(), other, rows[0].ID)
	require.Error(t, err)
	require.Equal(t, reads, v.reads)
	changed := v.creds
	changed.ClientID = "another-test-account"
	encoded, err := json.Marshal(changed)
	require.NoError(t, err)
	blob, err := crypto.Encrypt(string(encoded), testdb.EncryptionKey)
	require.NoError(t, err)
	_, err = v.pool.Exec(context.Background(), `UPDATE seller_cabinets SET encrypted_credentials=$2 WHERE id=$1`, cab, blob)
	require.NoError(t, err)
	_, err = s.ReconcilePriceChange(context.Background(), ws, rows[0].ID)
	require.ErrorContains(t, err, "seller identity changed")
	require.Equal(t, reads, v.reads)
}

func TestOzonPriceRecoverUndispatchedAndDispatchedClaims(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		t.Run(fmt.Sprint(dispatched), func(t *testing.T) {
			v := observedVendor()
			s, q, _, cab := newObservedRepricer(t, v)
			ctx := context.Background()
			row, err := q.InsertOzonPriceChange(ctx, sqlcgen.InsertOzonPriceChangeParams{SellerCabinetID: uuidToPgtype(cab), Sku: 101, OfferID: textToPgtype("TEST-101"), OldPriceRub: floatToPgNumeric(1000), NewPriceRub: floatToPgNumeric(900), Source: "manual", Status: "pending"})
			require.NoError(t, err)
			key, err := ozonPriceAccountKey(ozon.Credentials{ClientID: v.creds.ClientID})
			require.NoError(t, err)
			err = q.ClaimOzonPriceWrite(ctx, row.ID, uuidToPgtype(cab), 101, key, ozonPriceResourceKey(key, 101), 100000, 90000, []byte(`{"price_cents":100000}`), 10)
			require.NoError(t, err)
			if dispatched {
				require.NoError(t, q.MarkOzonPricesDispatched(ctx, []pgtype.UUID{row.ID}))
			}
			_, err = v.pool.Exec(ctx, `UPDATE ozon_price_write_attempts SET claimed_at=now()-interval '4 minutes' WHERE change_id=$1`, row.ID)
			require.NoError(t, err)
			require.NoError(t, q.RecoverOzonPriceClaims(ctx, uuidToPgtype(cab)))
			a, err := q.GetOzonPriceLease(ctx, row.ID)
			require.NoError(t, err)
			if dispatched {
				require.Equal(t, "submit_unknown", a.Status)
				require.True(t, a.ResourceKey.Valid)
			} else {
				require.Equal(t, "failed", a.Status)
				require.False(t, a.ResourceKey.Valid)
			}
			require.Error(t, q.MarkOzonPricesDispatched(ctx, []pgtype.UUID{row.ID}), "stale worker must not dispatch after recovery")
			require.NoError(t, s.applyIntents(ctx, cab, ozon.Credentials{ClientID: v.creds.ClientID}, []ozonPriceIntent{{ChangeID: row.ID, SKU: 101, NewPriceRub: 900}}))
			require.Zero(t, v.writes)
		})
	}
}

func TestOzonPriceBudgetSharedAcrossCabinetAliases(t *testing.T) {
	v := observedVendor()
	s, _, ws, cab := newObservedRepricer(t, v)
	alias := testdb.OzonCabinet(t, v.pool, ws, v.creds)
	for i := 0; i < 10; i++ {
		id := cab
		if i%2 != 0 {
			id = alias
		}
		rows, err := s.ApplyManual(context.Background(), ws, id, []OzonManualPriceInput{{SKU: 101, PriceRub: float64(990 - i)}}, uuid.New())
		require.NoError(t, err)
		require.Equal(t, "applied", rows[0].Status)
	}
	third := testdb.OzonCabinet(t, v.pool, ws, v.creds)
	rows, err := s.ApplyManual(context.Background(), ws, third, []OzonManualPriceInput{{SKU: 101, PriceRub: 800}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "failed", rows[0].Status)
	require.Equal(t, 10, v.writes)
}

func TestOzonPricePartialReceiptHoldsOnlyUnacknowledgedProduct(t *testing.T) {
	v := observedVendor()
	v.prices[102] = ozon.PriceSnapshot{ProductID: 102, OfferID: "TEST-102", PriceCents: 200000}
	v.results = []ozon.PriceUpdateResult{{ProductID: 101, Updated: true}}
	s, q, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}, {SKU: 102, PriceRub: 1900}}, uuid.New())
	require.Error(t, err)
	require.Len(t, rows, 2)
	byID := map[int64]domain.OzonPriceChange{}
	for _, r := range rows {
		byID[r.SKU] = r
	}
	require.Equal(t, "applied", byID[101].Status)
	require.Equal(t, "submit_unknown", byID[102].Status)
	a, err := q.GetOzonPriceLease(context.Background(), uuidToPgtype(byID[102].ID))
	require.NoError(t, err)
	require.True(t, a.ResourceKey.Valid)
	v.results = nil
	rows, err = s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 800}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "applied", rows[0].Status)
	require.Equal(t, 2, v.writes)
}

func TestOzonPriceExplicitRejectionReleasesProduct(t *testing.T) {
	v := observedVendor()
	v.deferAck = true
	v.results = []ozon.PriceUpdateResult{{ProductID: 101, Updated: false, Errors: []string{"PRICE_ERROR"}}}
	s, q, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "failed", rows[0].Status)
	a, err := q.GetOzonPriceLease(context.Background(), uuidToPgtype(rows[0].ID))
	require.NoError(t, err)
	require.False(t, a.ResourceKey.Valid)
	v.deferAck = false
	v.results = nil
	rows, err = s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	require.Equal(t, "applied", rows[0].Status)
	require.Equal(t, 2, v.writes)
}

func TestOzonPriceWorkspaceSweepRecoversWithoutStrategiesWhilePaused(t *testing.T) {
	v := observedVendor()
	v.deferAck = true
	s, q, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	_, err = v.pool.Exec(context.Background(), `UPDATE seller_cabinets SET repricer_paused_until=now()+interval '1 day' WHERE id=$1`, cab)
	require.NoError(t, err)
	p := v.prices[101]
	p.PriceCents = 90000
	v.prices[101] = p
	n, err := s.RunForWorkspace(context.Background(), ws)
	require.NoError(t, err)
	require.Zero(t, n)
	fresh, err := q.GetOzonPriceChangeByID(context.Background(), uuidToPgtype(rows[0].ID))
	require.NoError(t, err)
	require.Equal(t, "applied", fresh.Status)
	require.Equal(t, 1, v.writes)
}

func TestOzonPriceCalendarUnknownNeverCreatesAnotherCommand(t *testing.T) {
	v := observedVendor()
	v.lost = true
	s, q, ws, cab := newObservedRepricer(t, v)
	now := time.Now().UTC()
	entry, err := s.CreateSchedule(context.Background(), ws, cab, domain.OzonPriceScheduleInput{SKU: 101, ScheduledPriceRub: 900, StartsAt: now.Add(time.Minute)})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		n, err := s.ExecuteDueSchedules(context.Background(), now.Add(time.Duration(2+i)*time.Minute))
		require.NoError(t, err)
		require.Zero(t, n)
	}
	require.Equal(t, 1, v.writes)
	phase, err := q.OzonSchedulePriceChange(context.Background(), uuidToPgtype(entry.ID), "apply")
	require.NoError(t, err)
	require.Equal(t, "submit_unknown", phase.Status)
	fresh, err := q.GetOzonPriceScheduleEntry(context.Background(), uuidToPgtype(entry.ID))
	require.NoError(t, err)
	require.Equal(t, "pending", fresh.Status)
	require.Contains(t, fresh.Error.String, "submit_unknown")
	require.Error(t, s.CancelSchedule(context.Background(), ws, entry.ID), "cancellation cannot undo an uncertain dispatch")
	var count int
	require.NoError(t, v.pool.QueryRow(context.Background(), `SELECT count(*) FROM ozon_price_changes WHERE decision_context->>'schedule_id'=$1`, entry.ID.String()).Scan(&count))
	require.Equal(t, 1, count)
}

func TestOzonPriceCalendarLateVerificationThenRevert(t *testing.T) {
	v := observedVendor()
	v.deferAck = true
	s, q, ws, cab := newObservedRepricer(t, v)
	now := time.Now().UTC()
	end := now.Add(10 * time.Minute)
	entry, err := s.CreateSchedule(context.Background(), ws, cab, domain.OzonPriceScheduleInput{SKU: 101, ScheduledPriceRub: 900, StartsAt: now.Add(time.Minute), EndsAt: &end})
	require.NoError(t, err)
	n, err := s.ExecuteDueSchedules(context.Background(), now.Add(2*time.Minute))
	require.NoError(t, err)
	require.Zero(t, n)
	p := v.prices[101]
	p.PriceCents = 90000
	v.prices[101] = p
	n, err = s.ExecuteDueSchedules(context.Background(), now.Add(3*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, v.writes)
	v.deferAck = false
	n, err = s.ExecuteDueSchedules(context.Background(), now.Add(11*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 2, v.writes)
	fresh, err := q.GetOzonPriceScheduleEntry(context.Background(), uuidToPgtype(entry.ID))
	require.NoError(t, err)
	require.Equal(t, "reverted", fresh.Status)
	require.Equal(t, int64(100000), v.prices[101].PriceCents)
}

func TestOzonPriceCalendarCancelAndLegacyCommandsDoNotDispatch(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			v := observedVendor()
			s, q, ws, cab := newObservedRepricer(t, v)
			now := time.Now().UTC()
			entry, err := s.CreateSchedule(context.Background(), ws, cab, domain.OzonPriceScheduleInput{SKU: 101, ScheduledPriceRub: 900, StartsAt: now.Add(time.Minute)})
			require.NoError(t, err)
			if legacy {
				payload, _ := json.Marshal(map[string]string{"schedule_id": entry.ID.String(), "reason": "schedule"})
				_, err = q.InsertOzonPriceChange(context.Background(), sqlcgen.InsertOzonPriceChangeParams{SellerCabinetID: uuidToPgtype(cab), Sku: 101, OldPriceRub: floatToPgNumeric(1000), NewPriceRub: floatToPgNumeric(900), Source: "manual", Status: "pending", DecisionContext: payload})
				require.NoError(t, err)
			} else {
				require.NoError(t, s.CancelSchedule(context.Background(), ws, entry.ID))
			}
			if legacy {
				require.Error(t, s.CancelSchedule(context.Background(), ws, entry.ID), "legacy dispatched phases cannot be presented as cancelled")
			}
			n, err := s.ExecuteDueSchedules(context.Background(), now.Add(2*time.Minute))
			require.NoError(t, err)
			require.Zero(t, n)
			require.Zero(t, v.writes)
		})
	}
}

func TestOzonPriceRecoveryMigrationRefusesToDropActiveHolds(t *testing.T) {
	v := observedVendor()
	v.deferAck = true
	s, _, ws, cab := newObservedRepricer(t, v)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	down, err := os.ReadFile("../../migrations/000067_ozon_price_write_recovery.down.sql")
	require.NoError(t, err)
	_, err = v.pool.Exec(context.Background(), string(down))
	require.ErrorContains(t, err, "reconcile active Ozon price attempts")
	p := v.prices[101]
	p.PriceCents = 90000
	v.prices[101] = p
	_, err = s.ReconcilePriceChange(context.Background(), ws, rows[0].ID)
	require.NoError(t, err)
	_, err = v.pool.Exec(context.Background(), string(down))
	require.NoError(t, err)
	up, err := os.ReadFile("../../migrations/000067_ozon_price_write_recovery.up.sql")
	require.NoError(t, err)
	_, err = v.pool.Exec(context.Background(), string(up))
	require.NoError(t, err)
	var status string
	require.NoError(t, v.pool.QueryRow(context.Background(), `SELECT status FROM ozon_price_changes WHERE id=$1`, rows[0].ID).Scan(&status))
	require.Equal(t, "applied", status)
}

func TestOzonPriceDisconnectedCallerStillRecordsAcknowledgement(t *testing.T) {
	v := observedVendor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v.afterWrite = cancel
	s, q, ws, cab := newObservedRepricer(t, v)
	rows, _ := s.ApplyManual(ctx, ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.Len(t, rows, 1)
	a, err := q.GetOzonPriceLease(context.Background(), uuidToPgtype(rows[0].ID))
	require.NoError(t, err)
	require.True(t, a.AcknowledgedAt.Valid, "caller cancellation must not discard a known receipt")
	fresh, err := s.ReconcilePriceChange(context.Background(), ws, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "applied", fresh.Status)
	require.Equal(t, 1, v.writes)
}

func TestOzonPriceReceiptPersistenceFailureNeverReplays(t *testing.T) {
	v := observedVendor()
	s, q, ws, cab := newObservedRepricer(t, v)
	_, err := v.pool.Exec(context.Background(), `CREATE FUNCTION fail_price_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.status='verifying' THEN RAISE EXCEPTION 'injected receipt storage failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER fail_price_receipt BEFORE UPDATE ON ozon_price_write_attempts FOR EACH ROW EXECUTE FUNCTION fail_price_receipt();`)
	require.NoError(t, err)
	rows, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.Error(t, err)
	require.Len(t, rows, 1)
	a, err := q.GetOzonPriceLease(context.Background(), uuidToPgtype(rows[0].ID))
	require.NoError(t, err)
	require.True(t, a.DispatchedAt.Valid)
	require.False(t, a.AcknowledgedAt.Valid)
	require.True(t, a.ResourceKey.Valid)
	_, err = v.pool.Exec(context.Background(), `DROP TRIGGER fail_price_receipt ON ozon_price_write_attempts`)
	require.NoError(t, err)
	_, err = v.pool.Exec(context.Background(), `UPDATE ozon_price_write_attempts SET claimed_at=now()-interval '4 minutes' WHERE change_id=$1`, rows[0].ID)
	require.NoError(t, err)
	fresh, err := s.ReconcilePriceChange(context.Background(), ws, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "submit_unknown", fresh.Status)
	_, _ = s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 800}}, uuid.New())
	require.Equal(t, 1, v.writes)
}

func TestOzonPriceLateOldObservationCannotOverwriteNewVerifiedPrice(t *testing.T) {
	v := observedVendor()
	s, q, ws, cab := newObservedRepricer(t, v)
	first, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
	require.NoError(t, err)
	_, err = s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 800}}, uuid.New())
	require.NoError(t, err)
	require.NoError(t, q.ObserveOzonPrice(context.Background(), uuidToPgtype(first[0].ID), sqlcgen.OzonPriceObservation{JSON: []byte(`{"price_cents":90000}`), PriceCents: 90000}))
	mirror, err := q.GetOzonProductPriceBySku(context.Background(), sqlcgen.GetOzonProductPriceBySkuParams{SellerCabinetID: uuidToPgtype(cab), Sku: 101})
	require.NoError(t, err)
	require.Equal(t, 800.0, pgNumericToFloat(mirror.PriceRub))
}

func TestOzonPriceRollbackReturnsRecordedUnknownAndFinalizesLateVerification(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			v := observedVendor()
			s, q, ws, cab := newObservedRepricer(t, v)
			first, err := s.ApplyManual(context.Background(), ws, cab, []OzonManualPriceInput{{SKU: 101, PriceRub: 900}}, uuid.New())
			require.NoError(t, err)
			v.lost = lost
			v.deferAck = !lost
			reversal, err := s.Rollback(context.Background(), ws, first[0].ID)
			require.NotNil(t, reversal, "an uncertain rollback must retain its recorded ID")
			if lost {
				require.Error(t, err)
				require.Equal(t, "submit_unknown", reversal.Status)
			} else {
				require.NoError(t, err)
				require.Equal(t, "verifying", reversal.Status)
				p := v.prices[101]
				p.PriceCents = 100000
				v.prices[101] = p
				fresh, err := s.ReconcilePriceChange(context.Background(), ws, reversal.ID)
				require.NoError(t, err)
				require.Equal(t, "applied", fresh.Status)
				original, err := q.GetOzonPriceChangeByID(context.Background(), uuidToPgtype(first[0].ID))
				require.NoError(t, err)
				require.Equal(t, "rolled_back", original.Status)
			}
			require.Equal(t, 2, v.writes)
		})
	}
}
