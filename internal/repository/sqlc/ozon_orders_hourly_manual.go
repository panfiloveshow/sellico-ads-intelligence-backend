package sqlcgen

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// ozon_orders_hourly (7×24 MSK heatmap from Seller API postings)
// ---------------------------------------------------------------------------

// OzonOrdersHourlyRow is one aggregated (sku, dow, hour) bucket for the full
// cabinet rewrite. dow: 0=Пн .. 6=Вс (ISO − 1), MSK. sku is the SALES sku.
type OzonOrdersHourlyRow struct {
	Sku      int64
	Dow      int16
	Hour     int16
	Orders   int32
	Quantity int32
}

type ozonOrdersHourlyTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

const replaceOzonOrdersHourlyInsert = `
INSERT INTO ozon_orders_hourly (seller_cabinet_id, sku, dow, hour, orders, quantity)
SELECT $1, u.sku, u.dow, u.hour, u.orders, u.quantity
FROM unnest($2::bigint[], $3::smallint[], $4::smallint[], $5::int[], $6::int[])
    AS u(sku, dow, hour, orders, quantity)
`

const lockOzonOrdersHourlyCabinet = `
SELECT pg_advisory_xact_lock(
    hashtextextended(($1::uuid)::text || ':ozon-orders-hourly-replace', 0)
)
`

// ReplaceOzonOrdersHourly fully rewrites one cabinet's heatmap in a single
// transaction (delete + bulk insert): the postings sync recomputes the whole
// 28-day window every run, so partial upserts would leave stale buckets from
// orders that aged out of the window.
func (q *Queries) ReplaceOzonOrdersHourly(ctx context.Context, sellerCabinetID pgtype.UUID, rows []OzonOrdersHourlyRow) error {
	beginner, ok := q.db.(ozonOrdersHourlyTxBeginner)
	if !ok {
		return fmt.Errorf("database does not support ozon orders hourly transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Startup recovery and scheduled sync may overlap for the same cabinet.
	// Serialize the complete delete+insert rewrite per cabinet so two valid
	// snapshots cannot race into the table's unique constraint.
	if _, err := tx.Exec(ctx, lockOzonOrdersHourlyCabinet, sellerCabinetID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM ozon_orders_hourly WHERE seller_cabinet_id = $1`, sellerCabinetID); err != nil {
		return err
	}
	if len(rows) > 0 {
		skus := make([]int64, len(rows))
		dows := make([]int16, len(rows))
		hours := make([]int16, len(rows))
		orders := make([]int32, len(rows))
		quantities := make([]int32, len(rows))
		for i, row := range rows {
			skus[i] = row.Sku
			dows[i] = row.Dow
			hours[i] = row.Hour
			orders[i] = row.Orders
			quantities[i] = row.Quantity
		}
		if _, err := tx.Exec(ctx, replaceOzonOrdersHourlyInsert,
			sellerCabinetID, skus, dows, hours, orders, quantities); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// OzonOrdersHeatmapCell is one (dow, hour) bucket aggregated for the heatmap
// endpoint. Dow is 0=Пн .. 6=Вс.
type OzonOrdersHeatmapCell struct {
	Dow      int16
	Hour     int16
	Orders   int64
	Quantity int64
}

const ozonOrdersHeatmap = `
SELECT dow, hour, COALESCE(SUM(orders), 0)::bigint, COALESCE(SUM(quantity), 0)::bigint
FROM ozon_orders_hourly
WHERE seller_cabinet_id = $1
  AND ($2::bigint = 0 OR sku = $2)
GROUP BY 1, 2
`

// OzonOrdersHeatmap returns the aggregated 7×24 buckets of a cabinet
// (optionally one sales SKU; sku=0 aggregates the whole cabinet).
func (q *Queries) OzonOrdersHeatmap(ctx context.Context, sellerCabinetID pgtype.UUID, sku int64) ([]OzonOrdersHeatmapCell, error) {
	rows, err := q.db.Query(ctx, ozonOrdersHeatmap, sellerCabinetID, sku)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OzonOrdersHeatmapCell
	for rows.Next() {
		var c OzonOrdersHeatmapCell
		if err := rows.Scan(&c.Dow, &c.Hour, &c.Orders, &c.Quantity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OzonSlotIntensity is one SKU's demand intensity for a specific (dow, hour)
// slot plus its total orders over the stored window (the fallback threshold).
type OzonSlotIntensity struct {
	Intensity   float64
	TotalOrders int64
}

const ozonSlotIntensities = `
SELECT sku,
    COALESCE(MAX(orders) FILTER (WHERE dow = $2 AND hour = $3), 0)::float8
      / NULLIF(MAX(orders)::float8, 0) AS intensity,
    COALESCE(SUM(orders), 0)::bigint AS total_orders
FROM ozon_orders_hourly
WHERE seller_cabinet_id = $1
GROUP BY sku
`

// OzonSlotIntensities returns each sales SKU's demand intensity (0..1,
// slot orders / SKU max-slot orders) for the given dow (0=Пн..6=Вс) and hour,
// keyed by sales SKU.
func (q *Queries) OzonSlotIntensities(ctx context.Context, sellerCabinetID pgtype.UUID, dow, hour int16) (map[int64]OzonSlotIntensity, error) {
	rows, err := q.db.Query(ctx, ozonSlotIntensities, sellerCabinetID, dow, hour)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]OzonSlotIntensity{}
	for rows.Next() {
		var sku int64
		var intensity pgtype.Float8
		var total int64
		if err := rows.Scan(&sku, &intensity, &total); err != nil {
			return nil, err
		}
		if intensity.Valid {
			out[sku] = OzonSlotIntensity{Intensity: intensity.Float64, TotalOrders: total}
		}
	}
	return out, rows.Err()
}

const ozonCabinetSlotIntensity = `
WITH agg AS (
    SELECT dow, hour, SUM(orders) AS v
    FROM ozon_orders_hourly
    WHERE seller_cabinet_id = $1
    GROUP BY 1, 2
)
SELECT COALESCE(MAX(v) FILTER (WHERE dow = $2 AND hour = $3), 0)::float8
      / NULLIF(MAX(v)::float8, 0)
FROM agg
`

// OzonCabinetSlotIntensity returns the cabinet-wide demand intensity for one
// (dow, hour) slot — the fallback when a SKU has too few orders of its own.
// ok=false means the cabinet has no heatmap data at all.
func (q *Queries) OzonCabinetSlotIntensity(ctx context.Context, sellerCabinetID pgtype.UUID, dow, hour int16) (float64, bool, error) {
	var intensity pgtype.Float8
	err := q.db.QueryRow(ctx, ozonCabinetSlotIntensity, sellerCabinetID, dow, hour).Scan(&intensity)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !intensity.Valid {
		return 0, false, nil
	}
	return intensity.Float64, true, nil
}
