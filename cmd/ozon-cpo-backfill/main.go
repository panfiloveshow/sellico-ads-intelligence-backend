// Command ozon-cpo-backfill imports a historical Ozon all-SKU-promo orders
// report. It discards rows outside the requested report-date window and
// requires the remaining expense to equal the campaign's daily spend.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

func main() {
	cabinetArg := flag.String("cabinet", "", "seller cabinet UUID")
	campaignID := flag.Int64("campaign", 0, "all-SKU-promo campaign ID")
	fromArg := flag.String("from", "", "first report date, YYYY-MM-DD")
	toArg := flag.String("to", "", "last report date, YYYY-MM-DD")
	apply := flag.Bool("apply", false, "replace rows in the requested window; default is dry-run")
	flag.Parse()
	if err := run(*cabinetArg, *campaignID, *fromArg, *toArg, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cabinetArg string, campaignID int64, fromArg, toArg string, apply bool) error {
	cabinetID, err := uuid.Parse(cabinetArg)
	if err != nil {
		return fmt.Errorf("invalid --cabinet: %w", err)
	}
	if campaignID <= 0 {
		return fmt.Errorf("--campaign must be positive")
	}
	from, err := time.Parse("2006-01-02", fromArg)
	if err != nil {
		return fmt.Errorf("invalid --from: %w", err)
	}
	to, err := time.Parse("2006-01-02", toArg)
	if err != nil {
		return fmt.Errorf("invalid --to: %w", err)
	}
	if to.Before(from) || to.Sub(from) > 31*24*time.Hour {
		return fmt.Errorf("date window must be 1–32 days")
	}
	url := os.Getenv("DATABASE_URL")
	key := os.Getenv("ENCRYPTION_KEY")
	if url == "" || len(key) != 32 {
		return fmt.Errorf("DATABASE_URL and a 32-byte ENCRYPTION_KEY are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 17*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	var encrypted, marketplace string
	if err := pool.QueryRow(ctx, `SELECT encrypted_credentials, marketplace FROM seller_cabinets WHERE id=$1 AND deleted_at IS NULL`, cabinetID).Scan(&encrypted, &marketplace); err != nil {
		return fmt.Errorf("load cabinet: %w", err)
	}
	if marketplace != "ozon" {
		return fmt.Errorf("cabinet is not Ozon")
	}
	var kind string
	var campaignSpend float64
	if err := pool.QueryRow(ctx, `SELECT c.adv_object_type, COALESCE(SUM(s.spend_rub),0)::float8 FROM ozon_campaigns c LEFT JOIN ozon_campaign_stats s ON s.campaign_id=c.id AND s.date BETWEEN $3 AND $4 WHERE c.seller_cabinet_id=$1 AND c.ozon_campaign_id=$2 GROUP BY c.id`, cabinetID, campaignID, from, to).Scan(&kind, &campaignSpend); err != nil {
		return fmt.Errorf("load campaign totals: %w", err)
	}
	if kind != "ALL_SKU_PROMO" {
		return fmt.Errorf("campaign is not ALL_SKU_PROMO (got %s)", kind)
	}
	if campaignSpend <= 0 {
		return fmt.Errorf("no positive campaign spend for requested period")
	}
	plain, err := crypto.Decrypt(encrypted, []byte(key))
	if err != nil {
		return fmt.Errorf("decrypt cabinet credentials: %w", err)
	}
	var credentials domain.OzonCredentials
	if err := json.Unmarshal([]byte(plain), &credentials); err != nil {
		return fmt.Errorf("decode credentials: %w", err)
	}
	if !credentials.HasPerformanceAPI() {
		return fmt.Errorf("cabinet has no Performance API credentials")
	}
	perfURL := os.Getenv("OZON_PERF_API_BASE_URL")
	if perfURL == "" {
		perfURL = "https://api-performance.ozon.ru"
	}
	client := ozon.NewPerfClient(&config.Config{OzonPerfAPIBaseURL: perfURL}, zerolog.Nop())
	rows, err := client.GetAllSKUPromoOrders(ctx, ozon.Credentials{PerfClientID: credentials.PerfClientID, PerfClientSecret: credentials.PerfClientSecret}, from, to.Add(24*time.Hour-time.Second))
	if err != nil {
		return fmt.Errorf("get Ozon report: %w", err)
	}
	selected := make([]ozon.CPOOrderRow, 0, len(rows))
	seen := map[string]bool{}
	var reportCents int64
	outside := 0
	for _, row := range rows {
		date := row.Date.UTC().Truncate(24 * time.Hour)
		if date.Before(from) || date.After(to) {
			outside++
			continue
		}
		if row.OrderID == "" || row.SKU <= 0 || row.SpendRub < 0 {
			return fmt.Errorf("report contains invalid order, SKU or expense")
		}
		key := fmt.Sprintf("%s:%d", row.OrderID, row.SKU)
		if seen[key] {
			return fmt.Errorf("report contains duplicate order/SKU %s", key)
		}
		seen[key] = true
		reportCents += int64(math.Round(row.SpendRub * 100))
		selected = append(selected, row)
	}
	if len(selected) == 0 {
		return fmt.Errorf("Ozon report has no rows in requested dates; history left intact")
	}
	campaignCents := int64(math.Round(campaignSpend * 100))
	fmt.Printf("campaign=%d dates=%s..%s orders=%d skipped_outside=%d order_spend=%.2f campaign_spend=%.2f difference=%.2f apply=%t\n", campaignID, fromArg, toArg, len(selected), outside, float64(reportCents)/100, float64(campaignCents)/100, float64(reportCents-campaignCents)/100, apply)
	if reportCents != campaignCents {
		return fmt.Errorf("order report does not reconcile to campaign spend; history left intact")
	}
	if !apply {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ozon_cpo_orders WHERE seller_cabinet_id=$1 AND date BETWEEN $2 AND $3`, cabinetID, from, to); err != nil {
		return fmt.Errorf("replace old window: %w", err)
	}
	for _, row := range selected {
		if _, err := tx.Exec(ctx, `INSERT INTO ozon_cpo_orders (seller_cabinet_id, date, order_id, order_number, sku, adv_sku, vendor_code, name, quantity, price_rub, sale_price_rub, bid_pct, bid_rub, spend_rub) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (seller_cabinet_id, order_id, sku) DO UPDATE SET date=EXCLUDED.date, order_number=EXCLUDED.order_number, adv_sku=EXCLUDED.adv_sku, vendor_code=EXCLUDED.vendor_code, name=EXCLUDED.name, quantity=EXCLUDED.quantity, price_rub=EXCLUDED.price_rub, sale_price_rub=EXCLUDED.sale_price_rub, bid_pct=EXCLUDED.bid_pct, bid_rub=EXCLUDED.bid_rub, spend_rub=EXCLUDED.spend_rub, updated_at=now()`, cabinetID, row.Date, row.OrderID, row.OrderNumber, row.SKU, row.AdvSKU, row.VendorCode, row.Name, row.Quantity, row.PriceRub, row.SalePriceRub, row.BidPct, row.BidRub, row.SpendRub); err != nil {
			return fmt.Errorf("insert order %s: %w", row.OrderID, err)
		}
	}
	return tx.Commit(ctx)
}
