// Command ozon-campaign-sku-backfill imports historical Ozon Performance
// per-SKU campaign reports, including campaigns that are no longer running.
// It is read-only by default; --apply replaces only the requested campaign
// and date window after checking its total against the daily campaign stats.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
)

type skuDay struct {
	campaign int64
	sku      int64
	date     time.Time
	spend    int64 // kopecks
	views    int64
	clicks   int64
	orders   int64
	revenue  int64 // kopecks
}

func main() {
	cabinetArg := flag.String("cabinet", "", "seller cabinet UUID")
	campaignArg := flag.String("campaigns", "", "comma-separated Ozon campaign IDs")
	fromArg := flag.String("from", "", "first day, YYYY-MM-DD")
	toArg := flag.String("to", "", "last day, YYYY-MM-DD")
	apply := flag.Bool("apply", false, "replace rows in the requested window; default is dry-run")
	flag.Parse()
	if err := run(*cabinetArg, *campaignArg, *fromArg, *toArg, *apply); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cabinetArg, campaignArg, fromArg, toArg string, apply bool) error {
	cabinetID, err := uuid.Parse(cabinetArg)
	if err != nil {
		return fmt.Errorf("invalid --cabinet: %w", err)
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
	var campaignIDs []int64
	seen := map[int64]bool{}
	for _, raw := range strings.Split(campaignArg, ",") {
		id, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if parseErr != nil || id <= 0 {
			return fmt.Errorf("invalid campaign ID %q", raw)
		}
		if !seen[id] {
			campaignIDs = append(campaignIDs, id)
			seen[id] = true
		}
	}
	if len(campaignIDs) == 0 || len(campaignIDs) > 20 {
		return fmt.Errorf("specify 1–20 campaigns")
	}
	url := os.Getenv("DATABASE_URL")
	key := os.Getenv("ENCRYPTION_KEY")
	if url == "" || len(key) != 32 {
		return fmt.Errorf("DATABASE_URL and a 32-byte ENCRYPTION_KEY are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(campaignIDs))*17*time.Minute)
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
	apiCreds := ozon.Credentials{PerfClientID: credentials.PerfClientID, PerfClientSecret: credentials.PerfClientSecret}
	for _, campaignID := range campaignIDs {
		if err := backfillCampaign(ctx, pool, client, apiCreds, cabinetID, campaignID, from, to, apply); err != nil {
			return fmt.Errorf("campaign %d: %w", campaignID, err)
		}
	}
	return nil
}

func backfillCampaign(ctx context.Context, pool *pgxpool.Pool, client *ozon.PerfClient, creds ozon.Credentials, cabinetID uuid.UUID, campaignID int64, from, to time.Time, apply bool) error {
	var kind string
	var campaignTotal float64
	if err := pool.QueryRow(ctx, `SELECT c.adv_object_type, COALESCE(SUM(s.spend_rub),0)::float8 FROM ozon_campaigns c LEFT JOIN ozon_campaign_stats s ON s.campaign_id=c.id AND s.date BETWEEN $3 AND $4 WHERE c.seller_cabinet_id=$1 AND c.ozon_campaign_id=$2 GROUP BY c.id`, cabinetID, campaignID, from, to).Scan(&kind, &campaignTotal); err != nil {
		return fmt.Errorf("load campaign totals: %w", err)
	}
	if kind != "SKU" {
		return fmt.Errorf("only SKU campaigns support this report (got %s)", kind)
	}
	if campaignTotal <= 0 {
		return fmt.Errorf("no positive daily campaign spend for requested period")
	}
	rows, err := client.GetCampaignObjectsReport(ctx, creds, []int64{campaignID}, from, to.Add(24*time.Hour-time.Second))
	if err != nil {
		return fmt.Errorf("get Ozon report: %w", err)
	}
	grouped := map[string]*skuDay{}
	var reportCents int64
	for _, row := range rows {
		date := row.Date.UTC().Truncate(24 * time.Hour)
		if row.CampaignID != campaignID || row.SKU <= 0 || date.Before(from) || date.After(to) || row.SpendRub < 0 {
			return fmt.Errorf("report contains an invalid campaign, SKU, date or expense")
		}
		key := fmt.Sprintf("%d:%s", row.SKU, date.Format("2006-01-02"))
		item := grouped[key]
		if item == nil {
			item = &skuDay{campaign: campaignID, sku: row.SKU, date: date}
			grouped[key] = item
		}
		cents := int64(math.Round(row.SpendRub * 100))
		item.spend += cents
		item.views += row.Views
		item.clicks += row.Clicks
		item.orders += row.Orders
		item.revenue += int64(math.Round(row.RevenueRub * 100))
		reportCents += cents
	}
	if len(grouped) == 0 {
		return fmt.Errorf("Ozon report is empty; existing history left intact")
	}
	campaignCents := int64(math.Round(campaignTotal * 100))
	if reportCents > campaignCents+1 {
		return fmt.Errorf("SKU report %.2f exceeds campaign spend %.2f", float64(reportCents)/100, float64(campaignCents)/100)
	}
	fmt.Printf("campaign=%d days=%s..%s rows=%d sku_spend=%.2f campaign_spend=%.2f residual=%.2f apply=%t\n", campaignID, from.Format("2006-01-02"), to.Format("2006-01-02"), len(grouped), float64(reportCents)/100, float64(campaignCents)/100, float64(campaignCents-reportCents)/100, apply)
	if !apply {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM ozon_campaign_sku_stats WHERE seller_cabinet_id=$1 AND ozon_campaign_id=$2 AND date BETWEEN $3 AND $4`, cabinetID, campaignID, from, to); err != nil {
		return fmt.Errorf("replace old window: %w", err)
	}
	for _, item := range grouped {
		if _, err := tx.Exec(ctx, `INSERT INTO ozon_campaign_sku_stats (seller_cabinet_id, ozon_campaign_id, sku, date, views, clicks, spend_rub, orders, revenue_rub) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, cabinetID, item.campaign, item.sku, item.date, item.views, item.clicks, float64(item.spend)/100, item.orders, float64(item.revenue)/100); err != nil {
			return fmt.Errorf("insert SKU %d: %w", item.sku, err)
		}
	}
	return tx.Commit(ctx)
}
