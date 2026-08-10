package testdb

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

// EncryptionKey is the fixed 32-byte AES key used by every fixture and by the
// services under test (mirrors ENCRYPTION_KEY in production config).
var EncryptionKey = []byte("test-encryption-key-32-bytes-ok!")

var fixtureSeq atomic.Int64

// NextSeq returns a process-unique counter for building distinct fixture
// values (slugs, client ids) without collisions across parallel tests.
func NextSeq() int64 { return fixtureSeq.Add(1) }

// OzonCredentials returns a full credential set (Seller + Performance) with
// process-unique client ids so every test gets its own token cache and rate
// limiter buckets inside the shared HTTP clients.
func OzonCredentials() domain.OzonCredentials {
	n := NextSeq()
	return domain.OzonCredentials{
		ClientID:         fmt.Sprintf("seller-%d", n),
		APIKey:           fmt.Sprintf("api-key-%d", n),
		PerfClientID:     fmt.Sprintf("perf-%d", n),
		PerfClientSecret: fmt.Sprintf("perf-secret-%d", n),
	}
}

// OzonCredentialsSellerOnly returns Seller-API-only credentials (prices-only
// cabinets: no Performance pair).
func OzonCredentialsSellerOnly() domain.OzonCredentials {
	creds := OzonCredentials()
	creds.PerfClientID = ""
	creds.PerfClientSecret = ""
	return creds
}

// Workspace inserts a workspace and returns its id.
func Workspace(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO workspaces (name, slug) VALUES ($1, $2) RETURNING id`,
		"Test Workspace", fmt.Sprintf("test-ws-%d", NextSeq()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("fixture workspace: %v", err)
	}
	return id
}

// OzonCabinet inserts an active Ozon seller cabinet with the given credential
// set encrypted under EncryptionKey and returns its id.
func OzonCabinet(t *testing.T, pool *pgxpool.Pool, workspaceID uuid.UUID, creds domain.OzonCredentials) uuid.UUID {
	t.Helper()
	payload, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("fixture ozon cabinet: marshal creds: %v", err)
	}
	blob, err := crypto.Encrypt(string(payload), EncryptionKey)
	if err != nil {
		t.Fatalf("fixture ozon cabinet: encrypt creds: %v", err)
	}
	var id uuid.UUID
	err = pool.QueryRow(context.Background(),
		`INSERT INTO seller_cabinets (workspace_id, name, encrypted_token, marketplace, encrypted_credentials)
		 VALUES ($1, $2, '', 'ozon', $3) RETURNING id`,
		workspaceID, fmt.Sprintf("Ozon Cabinet %d", NextSeq()), blob,
	).Scan(&id)
	if err != nil {
		t.Fatalf("fixture ozon cabinet: %v", err)
	}
	return id
}

// WBCabinet inserts a Wildberries cabinet (for marketplace-mismatch tests).
func WBCabinet(t *testing.T, pool *pgxpool.Pool, workspaceID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO seller_cabinets (workspace_id, name, encrypted_token, marketplace)
		 VALUES ($1, $2, 'wb-token', 'wb') RETURNING id`,
		workspaceID, fmt.Sprintf("WB Cabinet %d", NextSeq()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("fixture wb cabinet: %v", err)
	}
	return id
}

// OzonCampaign inserts an ozon_campaigns mirror row and returns its id.
func OzonCampaign(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, ozonCampaignID int64, title, state string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO ozon_campaigns (seller_cabinet_id, ozon_campaign_id, title, adv_object_type, state)
		 VALUES ($1, $2, $3, 'SKU', $4) RETURNING id`,
		cabinetID, ozonCampaignID, title, state,
	).Scan(&id)
	if err != nil {
		t.Fatalf("fixture ozon campaign: %v", err)
	}
	return id
}

// OzonCampaignTyped inserts an ozon_campaigns mirror row with an explicit
// adv_object_type (e.g. ALL_SKU_PROMO / SEARCH_PROMO for CPO tests) and returns
// its id.
func OzonCampaignTyped(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, ozonCampaignID int64, title, advObjectType, state string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO ozon_campaigns (seller_cabinet_id, ozon_campaign_id, title, adv_object_type, state)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		cabinetID, ozonCampaignID, title, advObjectType, state,
	).Scan(&id)
	if err != nil {
		t.Fatalf("fixture ozon campaign typed: %v", err)
	}
	return id
}

// OzonCampaignProduct inserts a per-SKU bid mirror row.
func OzonCampaignProduct(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID, sku int64, bidRub float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ozon_campaign_products (campaign_id, sku, bid_rub, is_active)
		 VALUES ($1, $2, $3, TRUE)`,
		campaignID, sku, bidRub,
	)
	if err != nil {
		t.Fatalf("fixture ozon campaign product: %v", err)
	}
}

// OzonCampaignStat inserts one daily stats row.
func OzonCampaignStat(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID, date time.Time, views, clicks int64, spendRub float64, orders int64, revenueRub float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ozon_campaign_stats (campaign_id, date, views, clicks, spend_rub, orders, revenue_rub)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		campaignID, date, views, clicks, spendRub, orders, revenueRub,
	)
	if err != nil {
		t.Fatalf("fixture ozon campaign stat: %v", err)
	}
}

// OzonProduct inserts an ozon_products catalog mapping row (product_id ↔ sales
// SKU + name/offer_id).
func OzonProduct(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, productID, sku int64, offerID, name string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ozon_products (seller_cabinet_id, product_id, sku, offer_id, name)
		 VALUES ($1, $2, $3, $4, $5)`,
		cabinetID, productID, sku, offerID, name,
	)
	if err != nil {
		t.Fatalf("fixture ozon product: %v", err)
	}
}

// OzonPriceRow is the shape for OzonProductPrice fixtures. Zero-valued money
// fields are stored as NULL so tests can exercise fallback paths.
type OzonPriceRow struct {
	SKU              int64
	OfferID          string
	Name             string
	PriceRub         float64
	NetPriceRub      float64
	CommissionFBOPct float64
	CommissionFBSPct float64
	AcquiringPct     float64
}

// OzonProductPrice inserts an ozon_product_prices mirror row and returns its id.
func OzonProductPrice(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, row OzonPriceRow) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO ozon_product_prices (
		    seller_cabinet_id, sku, offer_id, name, price_rub, net_price_rub,
		    commission_fbo_pct, commission_fbs_pct, acquiring_pct, synced_at
		 ) VALUES ($1, $2, $3, $4, NULLIF($5, 0::numeric), NULLIF($6, 0::numeric), $7, $8, $9, now())
		 RETURNING id`,
		cabinetID, row.SKU, row.OfferID, row.Name, row.PriceRub, row.NetPriceRub,
		row.CommissionFBOPct, row.CommissionFBSPct, row.AcquiringPct,
	).Scan(&id)
	if err != nil {
		t.Fatalf("fixture ozon product price: %v", err)
	}
	return id
}

// OzonProductEconomic inserts a Sellico unit-economics mirror row (cost
// fallback source for the informational floor).
func OzonProductEconomic(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, offerID string, costPriceRub, otherCostsRub float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ozon_product_economics (seller_cabinet_id, offer_id, cost_price_rub, other_costs_rub)
		 VALUES ($1, $2, $3, $4)`,
		cabinetID, offerID, costPriceRub, otherCostsRub,
	)
	if err != nil {
		t.Fatalf("fixture ozon product economic: %v", err)
	}
}

// OzonCPOProduct inserts a CPO (search promo) mirror row.
func OzonCPOProduct(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, sku int64, enabled bool, bidRub float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ozon_cpo_products (seller_cabinet_id, sku, enabled, bid, bid_kind)
		 VALUES ($1, $2, $3, NULLIF($4, 0::numeric), CASE WHEN $4 > 0 THEN 'fixed_rub' END)`,
		cabinetID, sku, enabled, bidRub,
	)
	if err != nil {
		t.Fatalf("fixture ozon cpo product: %v", err)
	}
}

// OzonCPOOrder inserts one promoted-order mirror row (ozon_cpo_orders).
func OzonCPOOrder(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, date time.Time, orderID string, sku int64, quantity int32, salePriceRub, bidPct, spendRub float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ozon_cpo_orders (seller_cabinet_id, date, order_id, sku, quantity, sale_price_rub, bid_pct, spend_rub)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		cabinetID, date, orderID, sku, quantity, salePriceRub, bidPct, spendRub,
	)
	if err != nil {
		t.Fatalf("fixture ozon cpo order: %v", err)
	}
}

// OzonSearchQuery inserts one search-query stats row. avgPosition may be nil.
func OzonSearchQuery(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, sku int64, query string, date time.Time, views, clicks, orders int64, spendRub float64, avgPosition *float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO ozon_search_queries (seller_cabinet_id, sku, query, date, views, clicks, orders, spend_rub, avg_position)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		cabinetID, sku, query, date, views, clicks, orders, spendRub, avgPosition,
	)
	if err != nil {
		t.Fatalf("fixture ozon search query: %v", err)
	}
}
