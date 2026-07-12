package sqlcgen

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// product_prices
// ---------------------------------------------------------------------------

type ProductPrice struct {
	ID                  pgtype.UUID        `json:"id"`
	WorkspaceID         pgtype.UUID        `json:"workspace_id"`
	SellerCabinetID     pgtype.UUID        `json:"seller_cabinet_id"`
	WbProductID         int64              `json:"wb_product_id"`
	PriceRub            int64              `json:"price_rub"`
	DiscountPercent     int32              `json:"discount_percent"`
	ClubDiscountPercent int32              `json:"club_discount_percent"`
	DiscountedPriceRub  pgtype.Int8        `json:"discounted_price_rub"`
	EditableSizePrice   bool               `json:"editable_size_price"`
	SyncedAt            pgtype.Timestamptz `json:"synced_at"`
	UpdatedAt           pgtype.Timestamptz `json:"updated_at"`
}

type UpsertProductPriceParams struct {
	WorkspaceID         pgtype.UUID
	SellerCabinetID     pgtype.UUID
	WbProductID         int64
	PriceRub            int64
	DiscountPercent     int32
	ClubDiscountPercent int32
	DiscountedPriceRub  pgtype.Int8
	EditableSizePrice   bool
}

const upsertProductPrice = `
INSERT INTO product_prices (workspace_id, seller_cabinet_id, wb_product_id, price_rub,
    discount_percent, club_discount_percent, discounted_price_rub, editable_size_price, synced_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (workspace_id, wb_product_id) DO UPDATE
SET seller_cabinet_id = EXCLUDED.seller_cabinet_id,
    price_rub = EXCLUDED.price_rub,
    discount_percent = EXCLUDED.discount_percent,
    club_discount_percent = EXCLUDED.club_discount_percent,
    discounted_price_rub = EXCLUDED.discounted_price_rub,
    editable_size_price = EXCLUDED.editable_size_price,
    synced_at = now(),
    updated_at = now()
RETURNING id, workspace_id, seller_cabinet_id, wb_product_id, price_rub, discount_percent,
    club_discount_percent, discounted_price_rub, editable_size_price, synced_at, updated_at
`

func (q *Queries) UpsertProductPrice(ctx context.Context, arg UpsertProductPriceParams) (ProductPrice, error) {
	row := q.db.QueryRow(ctx, upsertProductPrice,
		arg.WorkspaceID, arg.SellerCabinetID, arg.WbProductID, arg.PriceRub,
		arg.DiscountPercent, arg.ClubDiscountPercent, arg.DiscountedPriceRub, arg.EditableSizePrice)
	var i ProductPrice
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbProductID, &i.PriceRub,
		&i.DiscountPercent, &i.ClubDiscountPercent, &i.DiscountedPriceRub, &i.EditableSizePrice,
		&i.SyncedAt, &i.UpdatedAt)
	return i, err
}

const listProductPricesByWorkspace = `
SELECT id, workspace_id, seller_cabinet_id, wb_product_id, price_rub, discount_percent,
    club_discount_percent, discounted_price_rub, editable_size_price, synced_at, updated_at
FROM product_prices
WHERE workspace_id = $1
ORDER BY wb_product_id
LIMIT $2 OFFSET $3
`

func (q *Queries) ListProductPricesByWorkspace(ctx context.Context, workspaceID pgtype.UUID, limit, offset int32) ([]ProductPrice, error) {
	rows, err := q.db.Query(ctx, listProductPricesByWorkspace, workspaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ProductPrice
	for rows.Next() {
		var i ProductPrice
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbProductID, &i.PriceRub,
			&i.DiscountPercent, &i.ClubDiscountPercent, &i.DiscountedPriceRub, &i.EditableSizePrice,
			&i.SyncedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const listProductPricesByCabinet = `
SELECT id, workspace_id, seller_cabinet_id, wb_product_id, price_rub, discount_percent,
    club_discount_percent, discounted_price_rub, editable_size_price, synced_at, updated_at
FROM product_prices
WHERE seller_cabinet_id = $1
ORDER BY wb_product_id
`

func (q *Queries) ListProductPricesByCabinet(ctx context.Context, sellerCabinetID pgtype.UUID) ([]ProductPrice, error) {
	rows, err := q.db.Query(ctx, listProductPricesByCabinet, sellerCabinetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ProductPrice
	for rows.Next() {
		var i ProductPrice
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbProductID, &i.PriceRub,
			&i.DiscountPercent, &i.ClubDiscountPercent, &i.DiscountedPriceRub, &i.EditableSizePrice,
			&i.SyncedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ---------------------------------------------------------------------------
// catalog (products LEFT JOIN product_prices)
// ---------------------------------------------------------------------------

type CatalogRow struct {
	WbProductID         int64
	Title               string
	Brand               pgtype.Text
	ImageUrl            pgtype.Text
	StockTotal          pgtype.Int4
	PriceRub            pgtype.Int8
	DiscountPercent     pgtype.Int4
	ClubDiscountPercent pgtype.Int4
	DiscountedPriceRub  pgtype.Int8
	EditableSizePrice   pgtype.Bool
	SyncedAt            pgtype.Timestamptz
}

const listCatalogWithPrices = `
SELECT p.wb_product_id, p.title, p.brand, p.image_url, p.stock_total,
    pp.price_rub, pp.discount_percent, pp.club_discount_percent,
    pp.discounted_price_rub, pp.editable_size_price, pp.synced_at
FROM products p
LEFT JOIN product_prices pp
    ON pp.workspace_id = p.workspace_id AND pp.wb_product_id = p.wb_product_id
WHERE p.workspace_id = $1
  AND ($2::uuid IS NULL OR p.seller_cabinet_id = $2)
ORDER BY (pp.price_rub IS NULL), p.wb_product_id
LIMIT $3 OFFSET $4
`

func (q *Queries) ListCatalogWithPrices(ctx context.Context, workspaceID, sellerCabinetID pgtype.UUID, limit, offset int32) ([]CatalogRow, error) {
	rows, err := q.db.Query(ctx, listCatalogWithPrices, workspaceID, sellerCabinetID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CatalogRow
	for rows.Next() {
		var i CatalogRow
		if err := rows.Scan(&i.WbProductID, &i.Title, &i.Brand, &i.ImageUrl, &i.StockTotal,
			&i.PriceRub, &i.DiscountPercent, &i.ClubDiscountPercent, &i.DiscountedPriceRub,
			&i.EditableSizePrice, &i.SyncedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ---------------------------------------------------------------------------
// product stock (real FBW stock from WB Statistics)
// ---------------------------------------------------------------------------

const setProductStock = `
UPDATE products SET stock_total = $4, updated_at = now()
WHERE workspace_id = $1 AND seller_cabinet_id = $2 AND wb_product_id = $3
`

func (q *Queries) SetProductStock(ctx context.Context, workspaceID, sellerCabinetID pgtype.UUID, wbProductID int64, stock int32) error {
	_, err := q.db.Exec(ctx, setProductStock, workspaceID, sellerCabinetID, wbProductID, stock)
	return err
}

// ---------------------------------------------------------------------------
// hourly orders (heatmap source)
// ---------------------------------------------------------------------------

const upsertProductOrdersHourly = `
INSERT INTO product_orders_hourly (workspace_id, seller_cabinet_id, wb_product_id, date, hour, orders, units, revenue_kopecks)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (seller_cabinet_id, wb_product_id, date, hour) DO UPDATE SET
    orders = EXCLUDED.orders,
    units = EXCLUDED.units,
    revenue_kopecks = EXCLUDED.revenue_kopecks,
    updated_at = now()
`

type UpsertProductOrdersHourlyParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	WbProductID     int64
	Date            pgtype.Date
	Hour            int16
	Orders          int32
	Units           int32
	RevenueKopecks  int64
}

func (q *Queries) UpsertProductOrdersHourly(ctx context.Context, p UpsertProductOrdersHourlyParams) error {
	_, err := q.db.Exec(ctx, upsertProductOrdersHourly,
		p.WorkspaceID, p.SellerCabinetID, p.WbProductID, p.Date, p.Hour, p.Orders, p.Units, p.RevenueKopecks)
	return err
}

const deleteOldProductOrdersHourly = `
DELETE FROM product_orders_hourly WHERE seller_cabinet_id = $1 AND date < now() - interval '60 days'
`

func (q *Queries) DeleteOldProductOrdersHourly(ctx context.Context, sellerCabinetID pgtype.UUID) error {
	_, err := q.db.Exec(ctx, deleteOldProductOrdersHourly, sellerCabinetID)
	return err
}

// CompetitorMedianByProduct returns the median competitor price (rubles) per our
// product_id, for the price_competitor_follow strategy.
func (q *Queries) CompetitorMedianByProduct(ctx context.Context, workspaceID pgtype.UUID) (map[string]int64, error) {
	rows, err := q.db.Query(ctx, `
SELECT product_id, percentile_cont(0.5) WITHIN GROUP (ORDER BY competitor_price)
FROM competitors
WHERE workspace_id = $1 AND product_id IS NOT NULL AND competitor_price > 0
GROUP BY product_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var pid pgtype.UUID
		var median float64
		if err := rows.Scan(&pid, &median); err != nil {
			return nil, err
		}
		out[uuid.UUID(pid.Bytes).String()] = int64(median)
	}
	return out, rows.Err()
}

const productSlotIntensity = `
WITH agg AS (
    SELECT wb_product_id, EXTRACT(ISODOW FROM date)::int AS dow, hour, SUM(units) AS v
    FROM product_orders_hourly
    WHERE seller_cabinet_id = $1 AND date >= (now() at time zone 'UTC')::date - $4::int
    GROUP BY 1, 2, 3
)
SELECT wb_product_id,
    COALESCE(MAX(v) FILTER (WHERE dow = $2 AND hour = $3), 0)::float8
      / NULLIF(MAX(v)::float8, 0) AS intensity
FROM agg
GROUP BY wb_product_id
`

// ProductSlotIntensities returns each product's demand intensity for the given
// ISO day-of-week (1..7) and hour (0..23) over the last lookbackDays.
func (q *Queries) ProductSlotIntensities(ctx context.Context, sellerCabinetID pgtype.UUID, dow, hour, lookbackDays int) (map[int64]float64, error) {
	rows, err := q.db.Query(ctx, productSlotIntensity, sellerCabinetID, dow, hour, lookbackDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var nm int64
		var intensity pgtype.Float8
		if err := rows.Scan(&nm, &intensity); err != nil {
			return nil, err
		}
		if intensity.Valid {
			out[nm] = intensity.Float64
		}
	}
	return out, rows.Err()
}

// OrdersHeatmapCell is one aggregated (ISO day-of-week, hour) bucket.
type OrdersHeatmapCell struct {
	DayOfWeek int16 // 1=Mon .. 7=Sun
	Hour      int16 // 0..23
	Orders    int64
	Units     int64
	RevenueK  int64
}

const ordersHeatmap = `
SELECT EXTRACT(ISODOW FROM date)::smallint AS dow, hour,
    COALESCE(SUM(orders),0), COALESCE(SUM(units),0), COALESCE(SUM(revenue_kopecks),0)
FROM product_orders_hourly
WHERE workspace_id = $1 AND seller_cabinet_id = $2
  AND ($3::bigint = 0 OR wb_product_id = $3)
  AND date >= $4 AND date <= $5
GROUP BY 1, 2
`

func (q *Queries) OrdersHeatmap(ctx context.Context, workspaceID, sellerCabinetID pgtype.UUID, wbProductID int64, from, to pgtype.Date) ([]OrdersHeatmapCell, error) {
	rows, err := q.db.Query(ctx, ordersHeatmap, workspaceID, sellerCabinetID, wbProductID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrdersHeatmapCell
	for rows.Next() {
		var c OrdersHeatmapCell
		if err := rows.Scan(&c.DayOfWeek, &c.Hour, &c.Orders, &c.Units, &c.RevenueK); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// repricer pause (freeze switch)
// ---------------------------------------------------------------------------

const setCabinetRepricerPause = `
UPDATE seller_cabinets SET repricer_paused_until = $2, updated_at = now()
WHERE id = $1 AND workspace_id = $3
`

func (q *Queries) SetCabinetRepricerPause(ctx context.Context, cabinetID pgtype.UUID, until pgtype.Timestamptz, workspaceID pgtype.UUID) error {
	_, err := q.db.Exec(ctx, setCabinetRepricerPause, cabinetID, until, workspaceID)
	return err
}

// PausedCabinets returns the set of a workspace's cabinets whose repricer is
// currently frozen (paused_until in the future).
func (q *Queries) PausedCabinets(ctx context.Context, workspaceID pgtype.UUID) (map[string]time.Time, error) {
	rows, err := q.db.Query(ctx, `SELECT id, repricer_paused_until FROM seller_cabinets
		WHERE workspace_id = $1 AND repricer_paused_until > now()`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id pgtype.UUID
		var until pgtype.Timestamptz
		if err := rows.Scan(&id, &until); err != nil {
			return nil, err
		}
		out[uuid.UUID(id.Bytes).String()] = until.Time
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// repricer health summary
// ---------------------------------------------------------------------------

type RepricerHealthRow struct {
	Products         int64
	WithPrice        int64
	LastSyncAt       pgtype.Timestamptz
	ActiveStrategies int64
	ChangesApplied   int64 // last 24h
	Recommendations  int64 // last 24h, pending review
	Failed           int64 // last 24h
	PausedUntil      pgtype.Timestamptz
}

const repricerHealth = `
SELECT
  (SELECT count(*) FROM products p WHERE p.seller_cabinet_id = $2) AS products,
  (SELECT count(*) FROM product_prices pp WHERE pp.seller_cabinet_id = $2) AS with_price,
  (SELECT max(pp.synced_at) FROM product_prices pp WHERE pp.seller_cabinet_id = $2) AS last_sync,
  (SELECT count(*) FROM strategies s WHERE s.seller_cabinet_id = $2 AND s.is_active AND s.type LIKE 'price_%') AS active_strategies,
  (SELECT count(*) FROM price_changes c WHERE c.seller_cabinet_id = $2 AND c.wb_status = 'applied' AND c.created_at > now() - interval '24 hours') AS applied,
  (SELECT count(*) FROM price_changes c WHERE c.seller_cabinet_id = $2 AND c.wb_status = 'recommended' AND c.created_at > now() - interval '24 hours') AS recommended,
  (SELECT count(*) FROM price_changes c WHERE c.seller_cabinet_id = $2 AND c.wb_status = 'failed' AND c.created_at > now() - interval '24 hours') AS failed,
  (SELECT repricer_paused_until FROM seller_cabinets sc WHERE sc.id = $2 AND sc.workspace_id = $1) AS paused_until
`

// RepricerDigestCounts sums the workspace's repricer activity over the last 24h.
func (q *Queries) RepricerDigestCounts(ctx context.Context, workspaceID pgtype.UUID) (applied, recommended, failed int64, err error) {
	err = q.db.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE wb_status = 'applied'),
  count(*) FILTER (WHERE wb_status = 'recommended'),
  count(*) FILTER (WHERE wb_status = 'failed')
FROM price_changes
WHERE workspace_id = $1 AND created_at > now() - interval '24 hours'`, workspaceID).Scan(&applied, &recommended, &failed)
	return
}

func (q *Queries) RepricerHealth(ctx context.Context, workspaceID, cabinetID pgtype.UUID) (RepricerHealthRow, error) {
	var r RepricerHealthRow
	err := q.db.QueryRow(ctx, repricerHealth, workspaceID, cabinetID).Scan(
		&r.Products, &r.WithPrice, &r.LastSyncAt, &r.ActiveStrategies,
		&r.ChangesApplied, &r.Recommendations, &r.Failed, &r.PausedUntil)
	return r, err
}

// ---------------------------------------------------------------------------
// cabinet prices-scope status (for the frontend)
// ---------------------------------------------------------------------------

type CabinetScopeRow struct {
	ID                   pgtype.UUID
	Name                 string
	PricesScopeStatus    string
	PricesScopeCheckedAt pgtype.Timestamptz
}

const listCabinetPricesScope = `
SELECT id, name, prices_scope_status, prices_scope_checked_at
FROM seller_cabinets
WHERE workspace_id = $1 AND deleted_at IS NULL
ORDER BY name
`

func (q *Queries) ListCabinetPricesScope(ctx context.Context, workspaceID pgtype.UUID) ([]CabinetScopeRow, error) {
	rows, err := q.db.Query(ctx, listCabinetPricesScope, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []CabinetScopeRow
	for rows.Next() {
		var i CabinetScopeRow
		if err := rows.Scan(&i.ID, &i.Name, &i.PricesScopeStatus, &i.PricesScopeCheckedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ---------------------------------------------------------------------------
// price_upload_tasks
// ---------------------------------------------------------------------------

type PriceUploadTask struct {
	ID              pgtype.UUID        `json:"id"`
	WorkspaceID     pgtype.UUID        `json:"workspace_id"`
	SellerCabinetID pgtype.UUID        `json:"seller_cabinet_id"`
	WbTaskID        int64              `json:"wb_task_id"`
	Status          string             `json:"status"`
	ItemsCount      int32              `json:"items_count"`
	PollCount       int32              `json:"poll_count"`
	LastPolledAt    pgtype.Timestamptz `json:"last_polled_at"`
	CompletedAt     pgtype.Timestamptz `json:"completed_at"`
	Error           pgtype.Text        `json:"error"`
	CreatedAt       pgtype.Timestamptz `json:"created_at"`
	UpdatedAt       pgtype.Timestamptz `json:"updated_at"`
}

type CreatePriceUploadTaskParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	WbTaskID        int64
	Status          string
	ItemsCount      int32
}

const createPriceUploadTask = `
INSERT INTO price_upload_tasks (workspace_id, seller_cabinet_id, wb_task_id, status, items_count)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (seller_cabinet_id, wb_task_id) DO UPDATE
SET items_count = GREATEST(price_upload_tasks.items_count, EXCLUDED.items_count), updated_at = now()
RETURNING id, workspace_id, seller_cabinet_id, wb_task_id, status, items_count, poll_count,
    last_polled_at, completed_at, error, created_at, updated_at
`

func (q *Queries) CreatePriceUploadTask(ctx context.Context, arg CreatePriceUploadTaskParams) (PriceUploadTask, error) {
	row := q.db.QueryRow(ctx, createPriceUploadTask, arg.WorkspaceID, arg.SellerCabinetID, arg.WbTaskID, arg.Status, arg.ItemsCount)
	return scanPriceUploadTask(row)
}

// CreatePriceUploadTaskAndLinkChanges records the WB task and links all locally
// reserved changes in one SQL statement. On a duplicate WB task, its terminal
// status is preserved instead of being reset to uploaded.
const createPriceUploadTaskAndLinkChanges = `
WITH task AS (
  INSERT INTO price_upload_tasks (workspace_id, seller_cabinet_id, wb_task_id, status, items_count)
  VALUES ($1, $2, $3, $4, $5)
  ON CONFLICT (seller_cabinet_id, wb_task_id) DO UPDATE
  SET items_count = GREATEST(price_upload_tasks.items_count, EXCLUDED.items_count),
      updated_at = now()
  RETURNING id, workspace_id, seller_cabinet_id, wb_task_id, status, items_count, poll_count,
      last_polled_at, completed_at, error, created_at, updated_at
), linked AS (
  UPDATE price_changes
  SET upload_task_id = (SELECT id FROM task),
      wb_status = CASE (SELECT status FROM task)
        WHEN 'applied' THEN 'applied'
        WHEN 'failed' THEN 'failed'
        ELSE 'uploaded'
      END,
      updated_at = now()
  WHERE id = ANY($6::uuid[]) AND wb_status = 'pending'
  RETURNING id
)
SELECT id, workspace_id, seller_cabinet_id, wb_task_id, status, items_count, poll_count,
    last_polled_at, completed_at, error, created_at, updated_at
FROM task
`

func (q *Queries) CreatePriceUploadTaskAndLinkChanges(ctx context.Context, arg CreatePriceUploadTaskParams, changeIDs []pgtype.UUID) (PriceUploadTask, error) {
	row := q.db.QueryRow(ctx, createPriceUploadTaskAndLinkChanges,
		arg.WorkspaceID, arg.SellerCabinetID, arg.WbTaskID, arg.Status, arg.ItemsCount, changeIDs)
	return scanPriceUploadTask(row)
}

type UpdatePriceUploadTaskParams struct {
	ID          pgtype.UUID
	Status      string
	PollCount   int32
	CompletedAt pgtype.Timestamptz
	Error       pgtype.Text
}

const updatePriceUploadTask = `
UPDATE price_upload_tasks
SET status = $2, poll_count = $3, completed_at = $4, error = $5,
    last_polled_at = now(), updated_at = now()
WHERE id = $1
RETURNING id, workspace_id, seller_cabinet_id, wb_task_id, status, items_count, poll_count,
    last_polled_at, completed_at, error, created_at, updated_at
`

func (q *Queries) UpdatePriceUploadTask(ctx context.Context, arg UpdatePriceUploadTaskParams) (PriceUploadTask, error) {
	row := q.db.QueryRow(ctx, updatePriceUploadTask, arg.ID, arg.Status, arg.PollCount, arg.CompletedAt, arg.Error)
	return scanPriceUploadTask(row)
}

const listPendingPriceUploadTasks = `
SELECT id, workspace_id, seller_cabinet_id, wb_task_id, status, items_count, poll_count,
    last_polled_at, completed_at, error, created_at, updated_at
FROM price_upload_tasks
WHERE workspace_id = $1 AND status IN ('uploaded', 'processing')
ORDER BY created_at
`

func (q *Queries) ListPendingPriceUploadTasks(ctx context.Context, workspaceID pgtype.UUID) ([]PriceUploadTask, error) {
	rows, err := q.db.Query(ctx, listPendingPriceUploadTasks, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PriceUploadTask
	for rows.Next() {
		i, err := scanPriceUploadTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const listPriceUploadTasksByWorkspace = `
SELECT id, workspace_id, seller_cabinet_id, wb_task_id, status, items_count, poll_count,
    last_polled_at, completed_at, error, created_at, updated_at
FROM price_upload_tasks
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3
`

func (q *Queries) ListPriceUploadTasksByWorkspace(ctx context.Context, workspaceID pgtype.UUID, limit, offset int32) ([]PriceUploadTask, error) {
	rows, err := q.db.Query(ctx, listPriceUploadTasksByWorkspace, workspaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PriceUploadTask
	for rows.Next() {
		i, err := scanPriceUploadTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func scanPriceUploadTask(row rowScanner) (PriceUploadTask, error) {
	var i PriceUploadTask
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbTaskID, &i.Status, &i.ItemsCount,
		&i.PollCount, &i.LastPolledAt, &i.CompletedAt, &i.Error, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// ---------------------------------------------------------------------------
// price_changes
// ---------------------------------------------------------------------------

type PriceChange struct {
	ID                 pgtype.UUID        `json:"id"`
	WorkspaceID        pgtype.UUID        `json:"workspace_id"`
	SellerCabinetID    pgtype.UUID        `json:"seller_cabinet_id"`
	StrategyID         pgtype.UUID        `json:"strategy_id"`
	ScheduleEntryID    pgtype.UUID        `json:"schedule_entry_id"`
	UploadTaskID       pgtype.UUID        `json:"upload_task_id"`
	WbProductID        int64              `json:"wb_product_id"`
	OldPriceRub        int64              `json:"old_price_rub"`
	NewPriceRub        int64              `json:"new_price_rub"`
	OldDiscountPercent int32              `json:"old_discount_percent"`
	NewDiscountPercent int32              `json:"new_discount_percent"`
	MinPriceRub        pgtype.Int8        `json:"min_price_rub"`
	Reason             string             `json:"reason"`
	Source             string             `json:"source"`
	WbStatus           string             `json:"wb_status"`
	Error              pgtype.Text        `json:"error"`
	CanRollback        bool               `json:"can_rollback"`
	RollbackOf         pgtype.UUID        `json:"rollback_of"`
	DecisionContext    []byte             `json:"decision_context"`
	CreatedBy          pgtype.UUID        `json:"created_by"`
	CreatedAt          pgtype.Timestamptz `json:"created_at"`
	UpdatedAt          pgtype.Timestamptz `json:"updated_at"`
}

type CreatePriceChangeParams struct {
	WorkspaceID        pgtype.UUID
	SellerCabinetID    pgtype.UUID
	StrategyID         pgtype.UUID
	ScheduleEntryID    pgtype.UUID
	UploadTaskID       pgtype.UUID
	WbProductID        int64
	OldPriceRub        int64
	NewPriceRub        int64
	OldDiscountPercent int32
	NewDiscountPercent int32
	MinPriceRub        pgtype.Int8
	Reason             string
	Source             string
	WbStatus           string
	CanRollback        bool
	RollbackOf         pgtype.UUID
	DecisionContext    []byte
	CreatedBy          pgtype.UUID
}

const priceChangeColumns = `id, workspace_id, seller_cabinet_id, strategy_id, schedule_entry_id,
    upload_task_id, wb_product_id, old_price_rub, new_price_rub, old_discount_percent,
    new_discount_percent, min_price_rub, reason, source, wb_status, error, can_rollback,
    rollback_of, decision_context, created_by, created_at, updated_at`

const createPriceChange = `
INSERT INTO price_changes (workspace_id, seller_cabinet_id, strategy_id, schedule_entry_id,
    upload_task_id, wb_product_id, old_price_rub, new_price_rub, old_discount_percent,
    new_discount_percent, min_price_rub, reason, source, wb_status, can_rollback, rollback_of,
    decision_context, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
ON CONFLICT (seller_cabinet_id, wb_product_id) WHERE wb_status IN ('pending', 'uploaded')
DO NOTHING
RETURNING ` + priceChangeColumns

func (q *Queries) CreatePriceChange(ctx context.Context, arg CreatePriceChangeParams) (PriceChange, error) {
	row := q.db.QueryRow(ctx, createPriceChange,
		arg.WorkspaceID, arg.SellerCabinetID, arg.StrategyID, arg.ScheduleEntryID, arg.UploadTaskID,
		arg.WbProductID, arg.OldPriceRub, arg.NewPriceRub, arg.OldDiscountPercent, arg.NewDiscountPercent,
		arg.MinPriceRub, arg.Reason, arg.Source, arg.WbStatus, arg.CanRollback, arg.RollbackOf,
		arg.DecisionContext, arg.CreatedBy)
	return scanPriceChange(row)
}

type UpdatePriceChangeStatusParams struct {
	ID           pgtype.UUID
	WbStatus     string
	Error        pgtype.Text
	UploadTaskID pgtype.UUID
}

const updatePriceChangeStatus = `
UPDATE price_changes
SET wb_status = $2, error = $3,
    upload_task_id = COALESCE($4, upload_task_id),
    updated_at = now()
WHERE id = $1
RETURNING ` + priceChangeColumns

func (q *Queries) UpdatePriceChangeStatus(ctx context.Context, arg UpdatePriceChangeStatusParams) (PriceChange, error) {
	row := q.db.QueryRow(ctx, updatePriceChangeStatus, arg.ID, arg.WbStatus, arg.Error, arg.UploadTaskID)
	return scanPriceChange(row)
}

// UpdatePriceChangesStatusByTask flips every change of an upload task to a new status.
const updatePriceChangesStatusByTask = `
UPDATE price_changes
SET wb_status = $2, updated_at = now()
WHERE upload_task_id = $1
`

func (q *Queries) UpdatePriceChangesStatusByTask(ctx context.Context, uploadTaskID pgtype.UUID, status string) error {
	_, err := q.db.Exec(ctx, updatePriceChangesStatusByTask, uploadTaskID, status)
	return err
}

// UpdatePriceChangeStatusByTaskAndNmID records a per-good result for partial WB tasks.
func (q *Queries) UpdatePriceChangeStatusByTaskAndNmID(ctx context.Context, uploadTaskID pgtype.UUID, wbProductID int64, status string, errorText pgtype.Text) error {
	_, err := q.db.Exec(ctx, `UPDATE price_changes
		SET wb_status = $3, error = $4, updated_at = now()
		WHERE upload_task_id = $1 AND wb_product_id = $2`, uploadTaskID, wbProductID, status, errorText)
	return err
}

// RecoverStaleRepricerState makes abandoned local reservations retryable and
// releases schedule claims left behind by a crashed worker.
func (q *Queries) RecoverStaleRepricerState(ctx context.Context, staleBefore pgtype.Timestamptz) (pendingFailed, schedulesReleased int64, err error) {
	tag, err := q.db.Exec(ctx, `UPDATE price_changes
		SET wb_status = 'failed', error = 'stale_pending_recovered', updated_at = now()
		WHERE wb_status = 'pending' AND upload_task_id IS NULL AND updated_at < $1`, staleBefore)
	if err != nil {
		return 0, 0, err
	}
	pendingFailed = tag.RowsAffected()
	tag, err = q.db.Exec(ctx, `UPDATE price_schedule_entries
		SET status = 'planned', error = 'stale_executing_recovered', updated_at = now()
		WHERE status = 'executing' AND updated_at < $1`, staleBefore)
	if err != nil {
		return pendingFailed, 0, err
	}
	return pendingFailed, tag.RowsAffected(), nil
}

func (q *Queries) GetProductPriceForRollback(ctx context.Context, workspaceID, sellerCabinetID pgtype.UUID, wbProductID int64) (ProductPrice, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, seller_cabinet_id, wb_product_id, price_rub,
		discount_percent, club_discount_percent, discounted_price_rub, editable_size_price, synced_at, updated_at
		FROM product_prices
		WHERE workspace_id = $1 AND seller_cabinet_id = $2 AND wb_product_id = $3`, workspaceID, sellerCabinetID, wbProductID)
	var i ProductPrice
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbProductID, &i.PriceRub,
		&i.DiscountPercent, &i.ClubDiscountPercent, &i.DiscountedPriceRub, &i.EditableSizePrice,
		&i.SyncedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) HasNewerActivePriceChange(ctx context.Context, workspaceID pgtype.UUID, wbProductID int64, originalID pgtype.UUID) (bool, error) {
	var exists bool
	err := q.db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM price_changes newer
		JOIN price_changes original ON original.id = $3
		WHERE newer.workspace_id = $1
		  AND newer.wb_product_id = $2
		  AND newer.id <> original.id
		  AND newer.created_at > original.created_at
		  AND newer.wb_status IN ('pending', 'uploaded', 'applied')
	)`, workspaceID, wbProductID, originalID).Scan(&exists)
	return exists, err
}

const getPriceChange = `SELECT ` + priceChangeColumns + ` FROM price_changes WHERE id = $1`

func (q *Queries) GetPriceChange(ctx context.Context, id pgtype.UUID) (PriceChange, error) {
	row := q.db.QueryRow(ctx, getPriceChange, id)
	return scanPriceChange(row)
}

// ListPriceChanges filters by workspace and optional product/source/status/date window.
// Nil/zero optional filters are ignored.
type ListPriceChangesParams struct {
	WorkspaceID pgtype.UUID
	WbProductID pgtype.Int8
	Source      pgtype.Text
	WbStatus    pgtype.Text
	CreatedFrom pgtype.Timestamptz
	CreatedTo   pgtype.Timestamptz
	Limit       int32
	Offset      int32
}

const listPriceChanges = `
SELECT ` + priceChangeColumns + `
FROM price_changes
WHERE workspace_id = $1
  AND ($2::bigint IS NULL OR wb_product_id = $2)
  AND ($3::text IS NULL OR source = $3)
  AND ($4::text IS NULL OR wb_status = $4)
  AND ($5::timestamptz IS NULL OR created_at >= $5)
  AND ($6::timestamptz IS NULL OR created_at <= $6)
ORDER BY created_at DESC
LIMIT $7 OFFSET $8
`

func (q *Queries) ListPriceChanges(ctx context.Context, arg ListPriceChangesParams) ([]PriceChange, error) {
	rows, err := q.db.Query(ctx, listPriceChanges, arg.WorkspaceID, arg.WbProductID, arg.Source,
		arg.WbStatus, arg.CreatedFrom, arg.CreatedTo, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PriceChange
	for rows.Next() {
		i, err := scanPriceChange(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// CountRecentPriceChangesByProduct counts applied/pending changes for a product since a cutoff.
const countRecentPriceChangesByProduct = `
SELECT count(*) FROM price_changes
WHERE workspace_id = $1 AND wb_product_id = $2 AND created_at >= $3
  AND wb_status IN ('recommended', 'pending', 'uploaded', 'applied')
`

func (q *Queries) CountRecentPriceChangesByProduct(ctx context.Context, workspaceID pgtype.UUID, wbProductID int64, since pgtype.Timestamptz) (int64, error) {
	var n int64
	err := q.db.QueryRow(ctx, countRecentPriceChangesByProduct, workspaceID, wbProductID, since).Scan(&n)
	return n, err
}

// HasInFlightPriceChange reports whether a product has a pending/uploaded change.
const hasInFlightPriceChange = `
SELECT EXISTS (
  SELECT 1 FROM price_changes
  WHERE workspace_id = $1 AND wb_product_id = $2 AND wb_status IN ('pending', 'uploaded')
)
`

func (q *Queries) HasInFlightPriceChange(ctx context.Context, workspaceID pgtype.UUID, wbProductID int64) (bool, error) {
	var exists bool
	err := q.db.QueryRow(ctx, hasInFlightPriceChange, workspaceID, wbProductID).Scan(&exists)
	return exists, err
}

// HasRollbackChild reports whether a change already has a rollback pointing at it.
const hasRollbackChild = `SELECT EXISTS (SELECT 1 FROM price_changes WHERE rollback_of = $1)`

func (q *Queries) HasRollbackChild(ctx context.Context, changeID pgtype.UUID) (bool, error) {
	var exists bool
	err := q.db.QueryRow(ctx, hasRollbackChild, changeID).Scan(&exists)
	return exists, err
}

func scanPriceChange(row rowScanner) (PriceChange, error) {
	var i PriceChange
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.StrategyID, &i.ScheduleEntryID,
		&i.UploadTaskID, &i.WbProductID, &i.OldPriceRub, &i.NewPriceRub, &i.OldDiscountPercent,
		&i.NewDiscountPercent, &i.MinPriceRub, &i.Reason, &i.Source, &i.WbStatus, &i.Error,
		&i.CanRollback, &i.RollbackOf, &i.DecisionContext, &i.CreatedBy, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// ---------------------------------------------------------------------------
// price_quarantine_goods
// ---------------------------------------------------------------------------

type PriceQuarantineGood struct {
	ID              pgtype.UUID        `json:"id"`
	WorkspaceID     pgtype.UUID        `json:"workspace_id"`
	SellerCabinetID pgtype.UUID        `json:"seller_cabinet_id"`
	WbProductID     int64              `json:"wb_product_id"`
	OldPriceRub     pgtype.Int8        `json:"old_price_rub"`
	NewPriceRub     pgtype.Int8        `json:"new_price_rub"`
	DetectedAt      pgtype.Timestamptz `json:"detected_at"`
	ResolvedAt      pgtype.Timestamptz `json:"resolved_at"`
	Notified        bool               `json:"notified"`
}

type UpsertQuarantineGoodParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	WbProductID     int64
	OldPriceRub     pgtype.Int8
	NewPriceRub     pgtype.Int8
}

// Upsert keyed on the newest unresolved row for a product; inserts a fresh detection
// only when there is no active (unresolved) row.
const upsertQuarantineGood = `
INSERT INTO price_quarantine_goods (workspace_id, seller_cabinet_id, wb_product_id, old_price_rub, new_price_rub)
SELECT $1, $2, $3, $4, $5
WHERE NOT EXISTS (
  SELECT 1 FROM price_quarantine_goods
  WHERE workspace_id = $1 AND wb_product_id = $3 AND resolved_at IS NULL
)
RETURNING id, workspace_id, seller_cabinet_id, wb_product_id, old_price_rub, new_price_rub,
    detected_at, resolved_at, notified
`

// Returns the inserted row, or ErrNoRows when the product was already active in quarantine.
func (q *Queries) UpsertQuarantineGood(ctx context.Context, arg UpsertQuarantineGoodParams) (PriceQuarantineGood, error) {
	row := q.db.QueryRow(ctx, upsertQuarantineGood, arg.WorkspaceID, arg.SellerCabinetID, arg.WbProductID, arg.OldPriceRub, arg.NewPriceRub)
	var i PriceQuarantineGood
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbProductID, &i.OldPriceRub,
		&i.NewPriceRub, &i.DetectedAt, &i.ResolvedAt, &i.Notified)
	return i, err
}

const listActiveQuarantineGoods = `
SELECT id, workspace_id, seller_cabinet_id, wb_product_id, old_price_rub, new_price_rub,
    detected_at, resolved_at, notified
FROM price_quarantine_goods
WHERE workspace_id = $1 AND resolved_at IS NULL
ORDER BY detected_at DESC
`

func (q *Queries) ListActiveQuarantineGoods(ctx context.Context, workspaceID pgtype.UUID) ([]PriceQuarantineGood, error) {
	rows, err := q.db.Query(ctx, listActiveQuarantineGoods, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PriceQuarantineGood
	for rows.Next() {
		var i PriceQuarantineGood
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbProductID, &i.OldPriceRub,
			&i.NewPriceRub, &i.DetectedAt, &i.ResolvedAt, &i.Notified); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

// ResolveQuarantineGoodsExcept marks active quarantine rows resolved for a cabinet
// except the given still-present nmIDs.
const resolveQuarantineGoodsExcept = `
UPDATE price_quarantine_goods
SET resolved_at = now()
WHERE seller_cabinet_id = $1 AND resolved_at IS NULL AND NOT (wb_product_id = ANY($2))
`

func (q *Queries) ResolveQuarantineGoodsExcept(ctx context.Context, sellerCabinetID pgtype.UUID, stillPresent []int64) error {
	_, err := q.db.Exec(ctx, resolveQuarantineGoodsExcept, sellerCabinetID, stillPresent)
	return err
}

const markQuarantineNotified = `UPDATE price_quarantine_goods SET notified = true WHERE id = $1`

func (q *Queries) MarkQuarantineNotified(ctx context.Context, id pgtype.UUID) error {
	_, err := q.db.Exec(ctx, markQuarantineNotified, id)
	return err
}

// ---------------------------------------------------------------------------
// seller_cabinets.prices_scope_status
// ---------------------------------------------------------------------------

const setCabinetPricesScopeStatus = `
UPDATE seller_cabinets
SET prices_scope_status = $2, prices_scope_checked_at = now()
WHERE id = $1
`

func (q *Queries) SetCabinetPricesScopeStatus(ctx context.Context, cabinetID pgtype.UUID, status string) error {
	_, err := q.db.Exec(ctx, setCabinetPricesScopeStatus, cabinetID, status)
	return err
}

// ---------------------------------------------------------------------------
// price_schedule_entries
// ---------------------------------------------------------------------------

type PriceScheduleEntry struct {
	ID               pgtype.UUID        `json:"id"`
	WorkspaceID      pgtype.UUID        `json:"workspace_id"`
	SellerCabinetID  pgtype.UUID        `json:"seller_cabinet_id"`
	ScopeType        string             `json:"scope_type"`
	ProductIds       []int64            `json:"product_ids"`
	AdjustmentType   string             `json:"adjustment_type"`
	AdjustmentValue  float64            `json:"adjustment_value"`
	Direction        pgtype.Text        `json:"direction"`
	ScheduledAt      pgtype.Timestamptz `json:"scheduled_at"`
	RevertAt         pgtype.Timestamptz `json:"revert_at"`
	RevertToPrevious bool               `json:"revert_to_previous"`
	RevertOf         pgtype.UUID        `json:"revert_of"`
	Status           string             `json:"status"`
	ExecutedTaskIds  []pgtype.UUID      `json:"executed_task_ids"`
	Error            pgtype.Text        `json:"error"`
	Comment          pgtype.Text        `json:"comment"`
	CreatedBy        pgtype.UUID        `json:"created_by"`
	CreatedAt        pgtype.Timestamptz `json:"created_at"`
	UpdatedAt        pgtype.Timestamptz `json:"updated_at"`
}

type CreatePriceScheduleEntryParams struct {
	WorkspaceID      pgtype.UUID
	SellerCabinetID  pgtype.UUID
	ScopeType        string
	ProductIds       []int64
	AdjustmentType   string
	AdjustmentValue  float64
	Direction        pgtype.Text
	ScheduledAt      pgtype.Timestamptz
	RevertAt         pgtype.Timestamptz
	RevertToPrevious bool
	RevertOf         pgtype.UUID
	Comment          pgtype.Text
	CreatedBy        pgtype.UUID
}

const priceScheduleColumns = `id, workspace_id, seller_cabinet_id, scope_type, product_ids,
    adjustment_type, adjustment_value, direction, scheduled_at, revert_at, revert_to_previous,
    revert_of, status, executed_task_ids, error, comment, created_by, created_at, updated_at`

const createPriceScheduleEntry = `
INSERT INTO price_schedule_entries (workspace_id, seller_cabinet_id, scope_type, product_ids,
    adjustment_type, adjustment_value, direction, scheduled_at, revert_at, revert_to_previous,
    revert_of, comment, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING ` + priceScheduleColumns

func (q *Queries) CreatePriceScheduleEntry(ctx context.Context, arg CreatePriceScheduleEntryParams) (PriceScheduleEntry, error) {
	row := q.db.QueryRow(ctx, createPriceScheduleEntry,
		arg.WorkspaceID, arg.SellerCabinetID, arg.ScopeType, arg.ProductIds, arg.AdjustmentType,
		arg.AdjustmentValue, arg.Direction, arg.ScheduledAt, arg.RevertAt, arg.RevertToPrevious,
		arg.RevertOf, arg.Comment, arg.CreatedBy)
	return scanPriceScheduleEntry(row)
}

const listDuePriceScheduleEntries = `
SELECT ` + priceScheduleColumns + `
FROM price_schedule_entries
WHERE status = 'planned' AND scheduled_at <= $1
ORDER BY scheduled_at
LIMIT $2
`

func (q *Queries) ListDuePriceScheduleEntries(ctx context.Context, now pgtype.Timestamptz, limit int32) ([]PriceScheduleEntry, error) {
	rows, err := q.db.Query(ctx, listDuePriceScheduleEntries, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PriceScheduleEntry
	for rows.Next() {
		i, err := scanPriceScheduleEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const listPriceScheduleEntriesByWorkspace = `
SELECT ` + priceScheduleColumns + `
FROM price_schedule_entries
WHERE workspace_id = $1
  AND ($2::text IS NULL OR status = $2)
ORDER BY scheduled_at
LIMIT $3 OFFSET $4
`

func (q *Queries) ListPriceScheduleEntriesByWorkspace(ctx context.Context, workspaceID pgtype.UUID, status pgtype.Text, limit, offset int32) ([]PriceScheduleEntry, error) {
	rows, err := q.db.Query(ctx, listPriceScheduleEntriesByWorkspace, workspaceID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []PriceScheduleEntry
	for rows.Next() {
		i, err := scanPriceScheduleEntry(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const getPriceScheduleEntry = `SELECT ` + priceScheduleColumns + ` FROM price_schedule_entries WHERE id = $1`

func (q *Queries) GetPriceScheduleEntry(ctx context.Context, id pgtype.UUID) (PriceScheduleEntry, error) {
	row := q.db.QueryRow(ctx, getPriceScheduleEntry, id)
	return scanPriceScheduleEntry(row)
}

type UpdatePriceScheduleEntryStatusParams struct {
	ID              pgtype.UUID
	Status          string
	ExecutedTaskIds []pgtype.UUID
	Error           pgtype.Text
}

const updatePriceScheduleEntryStatus = `
UPDATE price_schedule_entries
SET status = $2,
    executed_task_ids = COALESCE($3, executed_task_ids),
    error = $4,
    updated_at = now()
WHERE id = $1
RETURNING ` + priceScheduleColumns

func (q *Queries) UpdatePriceScheduleEntryStatus(ctx context.Context, arg UpdatePriceScheduleEntryStatusParams) (PriceScheduleEntry, error) {
	row := q.db.QueryRow(ctx, updatePriceScheduleEntryStatus, arg.ID, arg.Status, arg.ExecutedTaskIds, arg.Error)
	return scanPriceScheduleEntry(row)
}

// ClaimDuePriceScheduleEntry atomically flips one planned+due entry to 'executing'
// so concurrent sweeps don't double-execute it. Returns ErrNoRows when none is claimable.
const claimDuePriceScheduleEntry = `
UPDATE price_schedule_entries
SET status = 'executing', updated_at = now()
WHERE id = (
  SELECT id FROM price_schedule_entries
  WHERE status = 'planned' AND scheduled_at <= $1
  ORDER BY scheduled_at
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
RETURNING ` + priceScheduleColumns

func (q *Queries) ClaimDuePriceScheduleEntry(ctx context.Context, now pgtype.Timestamptz) (PriceScheduleEntry, error) {
	row := q.db.QueryRow(ctx, claimDuePriceScheduleEntry, now)
	return scanPriceScheduleEntry(row)
}

func scanPriceScheduleEntry(row rowScanner) (PriceScheduleEntry, error) {
	var i PriceScheduleEntry
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.ScopeType, &i.ProductIds,
		&i.AdjustmentType, &i.AdjustmentValue, &i.Direction, &i.ScheduledAt, &i.RevertAt,
		&i.RevertToPrevious, &i.RevertOf, &i.Status, &i.ExecutedTaskIds, &i.Error, &i.Comment,
		&i.CreatedBy, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}
