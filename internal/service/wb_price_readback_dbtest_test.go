package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

type wbPriceReadbackFixture struct {
	pool                   *pgxpool.Pool
	q                      *sqlcgen.Queries
	svc                    *RepricerService
	ws, cab                uuid.UUID
	history, goods, prices atomic.Value
	uploads                atomic.Int32
	onUpload               atomic.Value
}

func newWBPriceReadbackFixture(t *testing.T) *wbPriceReadbackFixture {
	t.Helper()
	f := &wbPriceReadbackFixture{pool: testdb.New(t)}
	f.q = sqlcgen.New(f.pool)
	f.ws = testdb.Workspace(t, f.pool)
	f.cab = testdb.WBCabinet(t, f.pool, f.ws)
	f.history.Store(`{"data":{"uploadID":77,"status":3},"error":false}`)
	f.goods.Store(`{"data":{"uploadID":77,"historyGoods":[]},"error":false}`)
	f.prices.Store(wbObservedPriceBody(101, 1000, 17, false))
	f.onUpload.Store(func() {})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/upload/task":
			f.uploads.Add(1)
			f.onUpload.Load().(func())()
			_, _ = w.Write([]byte(`{"data":{"id":77},"error":false}`))
		case "/api/v2/history/tasks":
			_, _ = w.Write([]byte(f.history.Load().(string)))
		case "/api/v2/history/goods/task":
			_, _ = w.Write([]byte(f.goods.Load().(string)))
		case "/api/v2/list/goods/filter":
			_, _ = w.Write([]byte(f.prices.Load().(string)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	key := testdb.EncryptionKey
	token, err := crypto.Encrypt(testAdvertisingToken(), key)
	require.NoError(t, err)
	_, err = f.pool.Exec(context.Background(), `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, f.cab, token)
	require.NoError(t, err)
	f.svc = NewRepricerService(f.q, wb.NewClient(&config.Config{WBAPIBaseURL: server.URL, WBAPIRateLimit: 10000}, zerolog.Nop()), key, zerolog.Nop())
	return f
}

func wbObservedPriceBody(nm, price int64, discount int, differentSecondSize bool) string {
	second := price
	if differentSecondSize {
		second++
	}
	sizes := []map[string]any{}
	for i, base := range []int64{price, second} {
		sizes = append(sizes, map[string]any{"sizeID": 201 + i, "price": base, "discountedPrice": float64(base*(100-int64(discount))) / 100, "clubDiscountedPrice": float64(base*(100-int64(discount))) / 100})
	}
	body, _ := json.Marshal(map[string]any{"error": false, "data": map[string]any{"listGoods": []any{map[string]any{"nmID": nm, "currencyIsoCode4217": "RUB", "discount": discount, "clubDiscount": 0, "editableSizePrice": false, "sizes": sizes}}}})
	return string(body)
}

func (f *wbPriceReadbackFixture) enqueue(t *testing.T, nm, price int64) sqlcgen.PriceChange {
	t.Helper()
	row, err := f.q.EnqueuePriceChange(context.Background(), sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(f.ws), SellerCabinetID: uuidToPgtype(f.cab), WbProductID: nm, OldPriceRub: 1000, NewPriceRub: price, OldDiscountPercent: 17, NewDiscountPercent: 17, Source: "manual"})
	require.NoError(t, err)
	return row
}

func (f *wbPriceReadbackFixture) status(t *testing.T, change sqlcgen.PriceChange) string {
	t.Helper()
	row, err := f.q.GetPriceChange(context.Background(), change.ID)
	require.NoError(t, err)
	return row.WbStatus
}

func (f *wbPriceReadbackFixture) poll(t *testing.T) int {
	t.Helper()
	n, err := f.svc.PollUploadTasks(context.Background(), f.ws)
	require.NoError(t, err)
	return n
}

func TestWBPriceTaskSuccessRequiresEveryLiveSizeAndExactDiscount(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	row := f.enqueue(t, 101, 901)
	_, err := f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
	require.NoError(t, err)
	for _, body := range []string{
		wbObservedPriceBody(101, 1000, 17, false),
		wbObservedPriceBody(101, 901, 18, false),
		wbObservedPriceBody(101, 901, 17, true),
		`{"error":false,"data":{"listGoods":[]}}`,
	} {
		f.prices.Store(body)
		require.Zero(t, f.poll(t), "native processing alone cannot prove current prices")
		require.Equal(t, "uploaded", f.status(t, row))
		active, err := f.q.HasInFlightPriceChange(context.Background(), uuidToPgtype(f.ws), 101)
		require.NoError(t, err)
		require.True(t, active)
	}
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	require.Equal(t, 1, f.poll(t))
	require.Equal(t, "applied", f.status(t, row))
	require.Zero(t, f.poll(t))
	require.Equal(t, int32(1), f.uploads.Load(), "verification is read-only at WB")
	var proof []byte
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT decision_context->'wb_verification' FROM price_changes WHERE id=$1`, row.ID).Scan(&proof))
	require.Contains(t, string(proof), `"price_cents": 90100`)
	require.Contains(t, string(proof), `"discounted_price_cents": 74783`)
}

func TestWBPricePartialUsesItemTwoAsSuccessAndThreeAsFailure(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	first, second := f.enqueue(t, 101, 901), f.enqueue(t, 102, 902)
	f.history.Store(`{"data":{"uploadID":77,"status":5},"error":false}`)
	var firstSource, secondSource struct {
		Data struct {
			Rows []json.RawMessage `json:"listGoods"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(wbObservedPriceBody(101, 1000, 17, false)), &firstSource))
	require.NoError(t, json.Unmarshal([]byte(wbObservedPriceBody(102, 1000, 17, false)), &secondSource))
	combined, _ := json.Marshal(map[string]any{"error": false, "data": map[string]any{"listGoods": append(firstSource.Data.Rows, secondSource.Data.Rows...)}})
	f.prices.Store(string(combined))
	_, err := f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
	require.NoError(t, err)
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	for _, goods := range []string{
		`[{"nmID":101,"price":901,"discount":17,"status":2}]`,
		`[{"nmID":101,"price":901,"discount":17,"status":2},{"nmID":102,"price":902,"discount":17,"status":99}]`,
		`[{"nmID":101,"price":901,"discount":17,"status":2},{"nmID":101,"price":901,"discount":17,"status":2}]`,
		`[{"nmID":101,"price":901,"discount":17,"status":2},{"nmID":999,"price":902,"discount":17,"status":3}]`,
		`[{"nmID":101,"price":901,"discount":17,"status":2,"errorText":"contradiction"},{"nmID":102,"price":902,"discount":17,"status":3}]`,
	} {
		f.goods.Store(`{"data":{"uploadID":77,"historyGoods":` + goods + `},"error":false}`)
		require.Zero(t, f.poll(t))
		require.Equal(t, "uploaded", f.status(t, first))
		require.Equal(t, "uploaded", f.status(t, second))
	}
	f.goods.Store(`{"data":{"uploadID":77,"historyGoods":[{"nmID":101,"price":901,"discount":17,"status":2},{"nmID":102,"price":902,"discount":17,"status":3,"errorText":"quarantine"}]},"error":false}`)
	require.Equal(t, 1, f.poll(t))
	require.Equal(t, "applied", f.status(t, first))
	require.Equal(t, "failed", f.status(t, second))
	require.Equal(t, int32(1), f.uploads.Load())
}

func TestWBPriceDuplicateOfOldAppliedTaskStillRequiresFreshReadback(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	old := f.enqueue(t, 101, 901)
	_, err := f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
	require.NoError(t, err)
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	require.Equal(t, 1, f.poll(t))
	f.prices.Store(wbObservedPriceBody(101, 1000, 17, false))
	row := f.enqueue(t, 101, 901)
	_, err = f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
	require.NoError(t, err)
	require.Equal(t, "applied", f.status(t, old))
	require.Equal(t, "uploaded", f.status(t, row), "a reused native ID is not a new price observation")
	require.Zero(t, f.poll(t))
	require.Equal(t, "uploaded", f.status(t, row))
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	require.Equal(t, 1, f.poll(t))
	require.Equal(t, "applied", f.status(t, row))
}

func TestWBPriceLateTaskRevisionCannotFinalizeNewlyLinkedCommand(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	f.enqueue(t, 101, 901)
	_, err := f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
	require.NoError(t, err)
	tasks, err := f.q.ListPendingPriceUploadTasks(context.Background(), uuidToPgtype(f.ws))
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	stale := tasks[0]
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	require.Equal(t, 1, f.poll(t))
	f.prices.Store(wbObservedPriceBody(101, 1000, 17, false))
	next := f.enqueue(t, 101, 901)
	_, err = f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
	require.NoError(t, err)
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	changed, err := f.svc.reconcileWBPriceTask(context.Background(), f.ws, testAdvertisingToken(), stale, 3)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "uploaded", f.status(t, next))
	changed, err = f.q.BumpPriceUploadTaskPoll(context.Background(), stale.ID, uuidToPgtype(f.ws), stale.PollCount, textToPgtype("late response"), stale.UpdatedAt)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 1, f.poll(t), "a current poll can complete the exact current revision")
}

func TestWBPriceRollbackMarksOriginalOnlyAfterReadback(t *testing.T) {
	f := newWBPriceReadbackFixture(t)
	ctx := context.Background()
	original, err := f.q.CreatePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(f.ws), SellerCabinetID: uuidToPgtype(f.cab), WbProductID: 101, OldPriceRub: 1100, NewPriceRub: 1000, OldDiscountPercent: 17, NewDiscountPercent: 17, Source: "manual", WbStatus: "applied"})
	require.NoError(t, err)
	child, err := f.q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: uuidToPgtype(f.ws), SellerCabinetID: uuidToPgtype(f.cab), WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 901, OldDiscountPercent: 17, NewDiscountPercent: 17, Source: "manual", RollbackOf: original.ID, DecisionContext: []byte(`null`)})
	require.NoError(t, err)
	_, err = f.svc.drainCabinetPriceChanges(ctx, f.ws, f.cab)
	require.NoError(t, err)
	require.Zero(t, f.poll(t))
	require.Equal(t, "applied", f.status(t, original))
	require.Equal(t, "uploaded", f.status(t, child))
	f.prices.Store(wbObservedPriceBody(101, 901, 17, false))
	require.Equal(t, 1, f.poll(t))
	require.Equal(t, "rolled_back", f.status(t, original))
	require.Equal(t, "applied", f.status(t, child))
	var proofExists bool
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT decision_context ? 'wb_verification' FROM price_changes WHERE id=$1`, child.ID).Scan(&proofExists))
	require.True(t, proofExists, "a legacy JSON null context must not discard the observation")
}

func TestWBPriceDefinitiveNativeRejectionReleasesHold(t *testing.T) {
	for _, native := range []string{"4", "6"} {
		t.Run(native, func(t *testing.T) {
			f := newWBPriceReadbackFixture(t)
			row := f.enqueue(t, 101, 901)
			_, err := f.svc.drainCabinetPriceChanges(context.Background(), f.ws, f.cab)
			require.NoError(t, err)
			f.history.Store(`{"data":{"uploadID":77,"status":` + native + `}}`)
			require.Equal(t, 1, f.poll(t))
			require.Equal(t, "failed", f.status(t, row))
			active, err := f.q.HasInFlightPriceChange(context.Background(), uuidToPgtype(f.ws), 101)
			require.NoError(t, err)
			require.False(t, active)
			require.Equal(t, int32(1), f.uploads.Load())
		})
	}
}
