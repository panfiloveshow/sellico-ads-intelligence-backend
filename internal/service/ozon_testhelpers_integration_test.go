package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
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
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
)

var ozonSchemaOnce sync.Once

// ozonEnsureSchema applies every *.up.sql migration once per process, tolerating
// already-applied statements. The shared sellico_test database may be on an
// older schema version; this brings it up to date (e.g. the ozon_* tables and
// the seller_cabinets.marketplace column) without depending on the migration
// tracking that testutil.NewTestDB assumes (which fails re-running the
// non-idempotent 000001 base migration against a pre-provisioned DB).
func ozonEnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	// Targeted, idempotent convergence for columns added AFTER the shared DB was
	// first provisioned. Unlike re-running whole migration files (which reverts
	// converged objects — an older idempotent file can DROP+re-ADD a constraint
	// to a stale list), an "ADD COLUMN IF NOT EXISTS" cannot revert anything, so
	// it is safe to run every time. Best-effort: on a truly fresh DB the table
	// does not exist yet and this errors harmlessly before the full run below.
	_, _ = pool.Exec(ctx, `ALTER TABLE ozon_cpo_products
        ADD COLUMN IF NOT EXISTS offer_id TEXT,
        ADD COLUMN IF NOT EXISTS name TEXT,
        ADD COLUMN IF NOT EXISTS price_rub NUMERIC(12,2),
        ADD COLUMN IF NOT EXISTS bid_price_rub NUMERIC(12,2),
        ADD COLUMN IF NOT EXISTS image_url TEXT,
        ADD COLUMN IF NOT EXISTS visibility_index TEXT`)

	// If the ozon schema is already present the DB is converged — do nothing.
	// Re-running the whole-file migrations here would roll back partially-
	// applicable files (they mix already-existing objects with constraint
	// updates) and silently revert converged schema, so skip entirely.
	var present *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.ozon_price_schedule_entries')::text`).Scan(&present); err == nil && present != nil {
		return nil
	}
	dir := ozonFindMigrationsDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			if ok, _ := filepath.Match("*.up.sql", e.Name()); ok {
				files = append(files, e.Name())
			}
		}
	}
	sort.Strings(files)
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		// Whole-file, best-effort convergence. Each migration file is atomic:
		// files already applied to the shared DB error wholesale ("already
		// exists") and are skipped; the not-yet-applied ozon_* files execute
		// cleanly. A genuinely missing object surfaces later as a clear
		// "relation/column does not exist" failure in a seeding helper.
		_, _ = pool.Exec(ctx, string(body))
	}
	return nil
}

func ozonFindMigrationsDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(filename)
	for i := 0; i < 12; i++ {
		candidate := filepath.Join(dir, "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// newOzonTestDB connects to the shared local Postgres and returns a clean
// (truncated) *testutil.TestDB. Unlike testutil.NewTestDB it does NOT re-run
// the non-idempotent migrations: the sellico_test database is provisioned once
// with the full schema, so this harness only truncates between tests. This
// keeps the suite green when run repeatedly in one process (multiple NewTestDB
// calls against an already-migrated DB would fail on CREATE TABLE).
func newOzonTestDB(t *testing.T) (*testutil.TestDB, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://sellico:sellico@localhost:5432/sellico_test?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test database: %v (is PostgreSQL running?)", err)
	}
	var schemaErr error
	ozonSchemaOnce.Do(func() { schemaErr = ozonEnsureSchema(ctx, pool) })
	if schemaErr != nil {
		pool.Close()
		t.Fatalf("ensure schema: %v", schemaErr)
	}
	if err := ozonTruncateAll(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("truncate tables: %v", err)
	}
	db := &testutil.TestDB{Pool: pool, Queries: sqlcgen.New(pool)}
	cleanup := func() {
		_ = ozonTruncateAll(ctx, pool)
		pool.Close()
	}
	return db, cleanup
}

func ozonTruncateAll(ctx context.Context, pool *pgxpool.Pool) error {
	const q = `
		DO $$
		DECLARE r RECORD;
		BEGIN
			FOR r IN (
				SELECT tablename FROM pg_tables
				WHERE schemaname = 'public'
				  AND tablename NOT LIKE 'schema_migrations%'
			) LOOP
				EXECUTE 'TRUNCATE TABLE public.' || quote_ident(r.tablename) || ' CASCADE';
			END LOOP;
		END $$;`
	_, err := pool.Exec(ctx, q)
	return err
}

// This file provides the shared DB seeding harness and behaviour-equivalent
// fake Ozon clients used by the ozon_*_integration_test.go suite. All tests in
// this suite hit a real local Postgres (testutil.NewTestDB) and stay in
// package service (white-box) so they can seed encrypted credentials with
// encryptOzonCredentials and drive the audited write paths through narrow
// client interfaces.

// ozonTestKey is a 32-byte AES-256 key for encryptOzonCredentials.
func ozonTestKey() []byte { return []byte("0123456789abcdef0123456789abcdef") }

// fullOzonCreds has both Seller and Performance API pairs.
func fullOzonCreds() domain.OzonCredentials {
	return domain.OzonCredentials{
		ClientID:         "seller-client",
		APIKey:           "seller-key",
		PerfClientID:     "perf-client",
		PerfClientSecret: "perf-secret",
	}
}

// pricesOnlyOzonCreds has only the Seller API pair (prices-only cabinet).
func pricesOnlyOzonCreds() domain.OzonCredentials {
	return domain.OzonCredentials{ClientID: "seller-client", APIKey: "seller-key"}
}

// ozonFixture bundles the primary IDs a seeded cabinet exposes.
type ozonFixture struct {
	db          *testutil.TestDB
	workspaceID uuid.UUID
	cabinetID   uuid.UUID
}

// newOzonWorkspace creates a workspace and returns its id.
func newOzonWorkspace(t *testing.T, db *testutil.TestDB, slug string) uuid.UUID {
	t.Helper()
	ws, err := db.Queries.CreateWorkspace(context.Background(), sqlcgen.CreateWorkspaceParams{Name: "WS " + slug, Slug: slug})
	require.NoError(t, err)
	return uuid.UUID(ws.ID.Bytes)
}

// seedOzonCabinet inserts an Ozon seller cabinet with encrypted credentials and
// returns its id. status defaults to 'active'.
func seedOzonCabinet(t *testing.T, db *testutil.TestDB, workspaceID uuid.UUID, creds domain.OzonCredentials) uuid.UUID {
	t.Helper()
	enc, err := encryptOzonCredentials(creds, ozonTestKey())
	require.NoError(t, err)
	var id uuid.UUID
	err = db.Pool.QueryRow(context.Background(), `
		INSERT INTO seller_cabinets (workspace_id, name, encrypted_token, marketplace, encrypted_credentials, status)
		VALUES ($1, $2, $3, 'ozon', $4, 'active')
		RETURNING id`,
		uuidToPgtype(workspaceID), "Ozon Cabinet", "legacy-token", enc,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// seedWBCabinet inserts a non-Ozon (WB) cabinet to exercise marketplace gates.
func seedWBCabinet(t *testing.T, db *testutil.TestDB, workspaceID uuid.UUID) uuid.UUID {
	t.Helper()
	cab, err := db.Queries.CreateSellerCabinet(context.Background(), sqlcgen.CreateSellerCabinetParams{
		WorkspaceID: uuidToPgtype(workspaceID), Name: "WB Cabinet", EncryptedToken: "wb-token",
	})
	require.NoError(t, err)
	return uuid.UUID(cab.ID.Bytes)
}

// newOzonFixture is the common setup: one workspace + one full-creds Ozon cabinet.
func newOzonFixture(t *testing.T, slug string) (*ozonFixture, func()) {
	t.Helper()
	db, cleanup := newOzonTestDB(t)
	ws := newOzonWorkspace(t, db, slug)
	cab := seedOzonCabinet(t, db, ws, fullOzonCreds())
	return &ozonFixture{db: db, workspaceID: ws, cabinetID: cab}, cleanup
}

func ozonTestLogger() zerolog.Logger { return zerolog.Nop() }

// --- seeding helpers on top of sqlc ---

func seedOzonCampaign(t *testing.T, db *testutil.TestDB, cabinetID uuid.UUID, ozonID int64, state string, dailyBudget *int64, weeklyBudget *int64) sqlcgen.OzonCampaign {
	t.Helper()
	row, err := db.Queries.UpsertOzonCampaign(context.Background(), sqlcgen.UpsertOzonCampaignParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		OzonCampaignID:  ozonID,
		Title:           textToPgtype("Campaign"),
		AdvObjectType:   textToPgtype("SKU"),
		State:           textToPgtype(state),
		DailyBudgetRub:  int64PtrToPgInt8(dailyBudget),
		WeeklyBudgetRub: int64PtrToPgInt8(weeklyBudget),
	})
	require.NoError(t, err)
	return row
}

func seedOzonCampaignProduct(t *testing.T, db *testutil.TestDB, campaignID pgtype.UUID, sku int64, bid float64) {
	t.Helper()
	err := db.Queries.UpsertOzonCampaignProduct(context.Background(), sqlcgen.UpsertOzonCampaignProductParams{
		CampaignID: campaignID,
		Sku:        sku,
		BidRub:     floatToPgNumeric(bid),
		IsActive:   true,
	})
	require.NoError(t, err)
}

func seedOzonCampaignStat(t *testing.T, db *testutil.TestDB, campaignID pgtype.UUID, date time.Time, views, clicks, orders int64, spend, revenue float64) {
	t.Helper()
	err := db.Queries.UpsertOzonCampaignStat(context.Background(), sqlcgen.UpsertOzonCampaignStatParams{
		CampaignID: campaignID,
		Date:       pgtype.Date{Time: date, Valid: true},
		Views:      views,
		Clicks:     clicks,
		SpendRub:   floatToPgNumeric(spend),
		Orders:     orders,
		RevenueRub: floatToPgNumeric(revenue),
	})
	require.NoError(t, err)
}

// seedOzonPrice inserts a price mirror row. When any of the economics fields are
// non-zero they populate net_price/commission/acquiring so computeOzonFloor can
// produce a floor.
type ozonPriceSeed struct {
	SKU              int64
	OfferID          string
	Name             string
	PriceRub         float64
	NetPriceRub      float64
	CommissionFBOPct float64
	AcquiringPct     float64
	OzonIndexMinRub  float64
	ExternalIndexMin float64
}

func seedOzonPrice(t *testing.T, db *testutil.TestDB, cabinetID uuid.UUID, p ozonPriceSeed) {
	t.Helper()
	err := db.Queries.UpsertOzonProductPrice(context.Background(), sqlcgen.UpsertOzonProductPriceParams{
		SellerCabinetID:          uuidToPgtype(cabinetID),
		Sku:                      p.SKU,
		OfferID:                  textToPgtype(p.OfferID),
		Name:                     textToPgtype(p.Name),
		PriceRub:                 floatToPgNumeric(p.PriceRub),
		NetPriceRub:              floatToPgNumeric(p.NetPriceRub),
		CommissionFboPct:         floatToPgNumeric(p.CommissionFBOPct),
		AcquiringPct:             floatToPgNumeric(p.AcquiringPct),
		OzonIndexMinPriceRub:     floatToPgNumeric(p.OzonIndexMinRub),
		ExternalIndexMinPriceRub: floatToPgNumeric(p.ExternalIndexMin),
	})
	require.NoError(t, err)
}

func seedOzonProduct(t *testing.T, db *testutil.TestDB, cabinetID uuid.UUID, productID, sku int64, offerID, name string) {
	t.Helper()
	err := db.Queries.UpsertOzonProduct(context.Background(), sqlcgen.UpsertOzonProductParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		ProductID:       productID,
		Sku:             pgtype.Int8{Int64: sku, Valid: sku != 0},
		OfferID:         textToPgtype(offerID),
		Name:            textToPgtype(name),
	})
	require.NoError(t, err)
}

func seedOzonEconomics(t *testing.T, db *testutil.TestDB, cabinetID uuid.UUID, offerID string, sku int64, cost, other float64) {
	t.Helper()
	err := db.Queries.UpsertOzonProductEconomics(context.Background(), sqlcgen.UpsertOzonProductEconomicsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		OfferID:         offerID,
		Sku:             pgtype.Int8{Int64: sku, Valid: sku != 0},
		CostPriceRub:    floatToPgNumeric(cost),
		OtherCostsRub:   floatToPgNumeric(other),
		Source:          textToPgtype("sellico"),
	})
	require.NoError(t, err)
}

func seedOzonStrategy(t *testing.T, db *testutil.TestDB, workspaceID, cabinetID uuid.UUID, sType string, params domain.StrategyParams, active bool) uuid.UUID {
	t.Helper()
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	row, err := db.Queries.CreateStrategy(context.Background(), sqlcgen.CreateStrategyParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(cabinetID),
		Name:            sType,
		Type:            sType,
		Params:          raw,
		IsActive:        active,
	})
	require.NoError(t, err)
	return uuid.UUID(row.ID.Bytes)
}

// ozonSearchQueryRow builds an UpsertOzonSearchQuery param set.
func ozonSearchQueryRow(cabinetID uuid.UUID, sku int64, query string, date time.Time, views, clicks, orders int64, spend float64) sqlcgen.UpsertOzonSearchQueryParams {
	return sqlcgen.UpsertOzonSearchQueryParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Sku:             sku,
		Query:           query,
		Date:            pgtype.Date{Time: date, Valid: true},
		Views:           views,
		Clicks:          clicks,
		Orders:          orders,
		SpendRub:        floatToPgNumeric(spend),
	}
}

// setCabinetPaused sets repricer_paused_until on a cabinet.
func setCabinetPaused(t *testing.T, db *testutil.TestDB, cabinetID uuid.UUID, until time.Time) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(),
		`UPDATE seller_cabinets SET repricer_paused_until = $2 WHERE id = $1`,
		uuidToPgtype(cabinetID), pgtype.Timestamptz{Time: until, Valid: true})
	require.NoError(t, err)
}

// --- fake Ozon clients ---

// fakePriceWriter is a behaviour-equivalent stand-in for the Seller-API
// UpdatePrices call. By default every item is reported Updated=true.
type fakePriceWriter struct {
	err        error          // transport-level failure (no per-item results)
	rejectSKUs map[int64]bool // SKUs to report Updated=false
	calls      [][]ozon.PriceUpdate
}

func (f *fakePriceWriter) UpdatePrices(_ context.Context, _ ozon.Credentials, items []ozon.PriceUpdate) ([]ozon.PriceUpdateResult, error) {
	f.calls = append(f.calls, items)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]ozon.PriceUpdateResult, 0, len(items))
	for _, it := range items {
		res := ozon.PriceUpdateResult{ProductID: it.ProductID, Updated: true}
		if f.rejectSKUs[it.ProductID] {
			res.Updated = false
			res.Errors = []string{"rejected by ozon"}
		}
		out = append(out, res)
	}
	return out, nil
}

// newRepricer builds an OzonRepricerService wired to a fake price writer.
func newRepricer(db *testutil.TestDB, writer ozonPriceWriter) *OzonRepricerService {
	return &OzonRepricerService{
		queries:       db.Queries,
		sellerClient:  writer,
		encryptionKey: ozonTestKey(),
		logger:        ozonTestLogger(),
	}
}

// fakePerfClient is a behaviour-equivalent stand-in for the Performance-API
// surface used by OzonCampaignActionsService.
type fakePerfClient struct {
	writeErr       error
	competitive    []ozon.CompetitiveBid
	competitiveErr error
	cpoProducts    []ozon.CPOProduct
	cpoListErr     error
	bidLimits      json.RawMessage
	bidLimitsErr   error
	minBids        map[int64]float64
	minBidsErr     error
	cpoMinBids     map[int64]float64

	cpoRate    int
	cpoRateErr error

	activateCalls    []int64
	deactivateCalls  []int64
	budgetCalls      []int64
	bidCalls         int
	enableCalls      int
	disableCalls     int
	cpoBidCalls      int
	cpoRateCalls     []int
	cpoActivateCalls int
	cpoDeactivate    int
}

func (f *fakePerfClient) ActivateCampaign(_ context.Context, _ ozon.Credentials, id int64) error {
	f.activateCalls = append(f.activateCalls, id)
	return f.writeErr
}
func (f *fakePerfClient) DeactivateCampaign(_ context.Context, _ ozon.Credentials, id int64) error {
	f.deactivateCalls = append(f.deactivateCalls, id)
	return f.writeErr
}
func (f *fakePerfClient) UpdateCampaign(_ context.Context, _ ozon.Credentials, id int64, _ ozon.CampaignPatch) error {
	f.budgetCalls = append(f.budgetCalls, id)
	return f.writeErr
}
func (f *fakePerfClient) SetCampaignProductBids(_ context.Context, _ ozon.Credentials, _ int64, _ []ozon.ProductBid) error {
	f.bidCalls++
	return f.writeErr
}
func (f *fakePerfClient) GetCompetitiveBids(_ context.Context, _ ozon.Credentials, _ int64, _ []int64) ([]ozon.CompetitiveBid, error) {
	return f.competitive, f.competitiveErr
}
func (f *fakePerfClient) ListSearchPromoProducts(_ context.Context, _ ozon.Credentials) ([]ozon.CPOProduct, error) {
	return f.cpoProducts, f.cpoListErr
}
func (f *fakePerfClient) EnableSearchPromo(_ context.Context, _ ozon.Credentials, _ []int64) error {
	f.enableCalls++
	return f.writeErr
}
func (f *fakePerfClient) DisableSearchPromo(_ context.Context, _ ozon.Credentials, _ []int64) error {
	f.disableCalls++
	return f.writeErr
}
func (f *fakePerfClient) SetSearchPromoBids(_ context.Context, _ ozon.Credentials, _ []ozon.CPOBid) error {
	f.cpoBidCalls++
	return f.writeErr
}
func (f *fakePerfClient) GetAllSKUPromoRate(_ context.Context, _ ozon.Credentials) (int, error) {
	f.cpoActivateCalls++
	return f.cpoRate, f.cpoRateErr
}
func (f *fakePerfClient) SetAllSKUPromoRate(_ context.Context, _ ozon.Credentials, ratePct int) error {
	f.cpoRateCalls = append(f.cpoRateCalls, ratePct)
	return f.writeErr
}
func (f *fakePerfClient) DeactivateAllSKUPromo(_ context.Context, _ ozon.Credentials) error {
	f.cpoDeactivate++
	return f.writeErr
}
func (f *fakePerfClient) GetBidLimits(_ context.Context, _ ozon.Credentials) (json.RawMessage, error) {
	return f.bidLimits, f.bidLimitsErr
}

// GetMinSKUBids lets fakePerfClient double as ozonStrategyPerfClient. minBids
// maps SKU -> minimum CPC bid (rubles); absent SKUs return no row (min 0).
func (f *fakePerfClient) GetMinSKUBids(_ context.Context, _ ozon.Credentials, skus []int64, _ string) ([]ozon.MinSKUBid, error) {
	if f.minBidsErr != nil {
		return nil, f.minBidsErr
	}
	out := make([]ozon.MinSKUBid, 0, len(skus))
	for _, sku := range skus {
		if b, ok := f.minBids[sku]; ok {
			out = append(out, ozon.MinSKUBid{SKU: sku, BidRub: b})
		}
	}
	return out, nil
}

// GetCPOMinBids lets fakePerfClient double as the AI manager's perf client.
func (f *fakePerfClient) GetCPOMinBids(_ context.Context, _ ozon.Credentials, skus []int64) ([]ozon.MinSKUBid, error) {
	out := make([]ozon.MinSKUBid, 0, len(skus))
	for _, sku := range skus {
		if b, ok := f.cpoMinBids[sku]; ok {
			out = append(out, ozon.MinSKUBid{SKU: sku, BidRub: b})
		}
	}
	return out, nil
}

// newStrategyService builds an OzonStrategyService wired to a fake perf client
// and a real BidEngine.
func newStrategyService(db *testutil.TestDB, perf ozonStrategyPerfClient) *OzonStrategyService {
	return &OzonStrategyService{
		queries:       db.Queries,
		perfClient:    perf,
		engine:        NewBidEngine(ozonTestLogger()),
		encryptionKey: ozonTestKey(),
		logger:        ozonTestLogger(),
	}
}

// newCampaignActions builds an OzonCampaignActionsService wired to a fake perf client.
func newCampaignActions(db *testutil.TestDB, perf ozonCampaignPerfClient) *OzonCampaignActionsService {
	return &OzonCampaignActionsService{
		queries:       db.Queries,
		perfClient:    perf,
		encryptionKey: ozonTestKey(),
		logger:        ozonTestLogger(),
		limitsCache:   make(map[uuid.UUID]ozonBidLimitsCacheEntry),
	}
}

// newSyncService builds an OzonSyncService (nil clients — only client-free
// paths are exercised).
func newSyncService(db *testutil.TestDB) *OzonSyncService {
	return &OzonSyncService{
		queries:       db.Queries,
		encryptionKey: ozonTestKey(),
		logger:        ozonTestLogger(),
	}
}
