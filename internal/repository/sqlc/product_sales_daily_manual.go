package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type ProductSalesDaily struct {
	ID              pgtype.UUID
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	ProductID       pgtype.UUID
	WbProductID     int64
	Date            pgtype.Date
	Orders          int64
	CanceledOrders  int64
	Sales           int64
	Returns         int64
	OrderedRevenue  int64
	SoldRevenue     int64
	ReturnedRevenue int64
	Source          string
	CapturedAt      pgtype.Timestamptz
	CreatedAt       pgtype.Timestamptz
	UpdatedAt       pgtype.Timestamptz
}

type UpsertProductSalesDailyParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	ProductID       pgtype.UUID
	WbProductID     int64
	Date            pgtype.Date
	Orders          int64
	CanceledOrders  int64
	Sales           int64
	Returns         int64
	OrderedRevenue  int64
	SoldRevenue     int64
	ReturnedRevenue int64
	Source          string
	CapturedAt      pgtype.Timestamptz
}

const upsertProductSalesDaily = `
INSERT INTO product_sales_daily (
  workspace_id, seller_cabinet_id, product_id, wb_product_id, date,
  orders, canceled_orders, sales, returns, ordered_revenue, sold_revenue,
  returned_revenue, source, captured_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (seller_cabinet_id, wb_product_id, date) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  product_id = COALESCE(EXCLUDED.product_id, product_sales_daily.product_id),
  orders = EXCLUDED.orders,
  canceled_orders = EXCLUDED.canceled_orders,
  sales = EXCLUDED.sales,
  returns = EXCLUDED.returns,
  ordered_revenue = EXCLUDED.ordered_revenue,
  sold_revenue = EXCLUDED.sold_revenue,
  returned_revenue = EXCLUDED.returned_revenue,
  source = EXCLUDED.source,
  captured_at = EXCLUDED.captured_at,
  updated_at = now()
RETURNING id, workspace_id, seller_cabinet_id, product_id, wb_product_id, date,
  orders, canceled_orders, sales, returns, ordered_revenue, sold_revenue,
  returned_revenue, source, captured_at, created_at, updated_at
`

func (q *Queries) UpsertProductSalesDaily(ctx context.Context, arg UpsertProductSalesDailyParams) (ProductSalesDaily, error) {
	row := q.db.QueryRow(ctx, upsertProductSalesDaily,
		arg.WorkspaceID,
		arg.SellerCabinetID,
		arg.ProductID,
		arg.WbProductID,
		arg.Date,
		arg.Orders,
		arg.CanceledOrders,
		arg.Sales,
		arg.Returns,
		arg.OrderedRevenue,
		arg.SoldRevenue,
		arg.ReturnedRevenue,
		arg.Source,
		arg.CapturedAt,
	)
	var item ProductSalesDaily
	err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.SellerCabinetID,
		&item.ProductID,
		&item.WbProductID,
		&item.Date,
		&item.Orders,
		&item.CanceledOrders,
		&item.Sales,
		&item.Returns,
		&item.OrderedRevenue,
		&item.SoldRevenue,
		&item.ReturnedRevenue,
		&item.Source,
		&item.CapturedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

type ListProductSalesDailyByWorkspaceDateRangeParams struct {
	WorkspaceID pgtype.UUID
	DateFrom    pgtype.Date
	DateTo      pgtype.Date
}

const listProductSalesDailyByWorkspaceDateRange = `
SELECT id, workspace_id, seller_cabinet_id, product_id, wb_product_id, date,
  orders, canceled_orders, sales, returns, ordered_revenue, sold_revenue,
  returned_revenue, source, captured_at, created_at, updated_at
FROM product_sales_daily
WHERE workspace_id = $1
  AND date BETWEEN $2 AND $3
ORDER BY date DESC
`

func (q *Queries) ListProductSalesDailyByWorkspaceDateRange(ctx context.Context, arg ListProductSalesDailyByWorkspaceDateRangeParams) ([]ProductSalesDaily, error) {
	rows, err := q.db.Query(ctx, listProductSalesDailyByWorkspaceDateRange, arg.WorkspaceID, arg.DateFrom, arg.DateTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ProductSalesDaily
	for rows.Next() {
		var item ProductSalesDaily
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.SellerCabinetID,
			&item.ProductID,
			&item.WbProductID,
			&item.Date,
			&item.Orders,
			&item.CanceledOrders,
			&item.Sales,
			&item.Returns,
			&item.OrderedRevenue,
			&item.SoldRevenue,
			&item.ReturnedRevenue,
			&item.Source,
			&item.CapturedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
