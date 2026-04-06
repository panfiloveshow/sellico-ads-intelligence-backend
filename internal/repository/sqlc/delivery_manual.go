package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type CreateDeliveryDataParams struct {
	WorkspaceID  pgtype.UUID
	ProductID    pgtype.UUID
	Region       string
	Warehouse    pgtype.Text
	DeliveryDays int32
	DeliveryCost int64
	InStock      bool
}

func (q *Queries) CreateDeliveryData(ctx context.Context, arg CreateDeliveryDataParams) error {
	_, err := q.db.Exec(ctx,
		`INSERT INTO delivery_data (workspace_id, product_id, region, warehouse, delivery_days, delivery_cost, in_stock)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		arg.WorkspaceID, arg.ProductID, arg.Region, arg.Warehouse, arg.DeliveryDays, arg.DeliveryCost, arg.InStock)
	return err
}

type DeliveryDataRow struct {
	ID           pgtype.UUID        `json:"id"`
	WorkspaceID  pgtype.UUID        `json:"workspace_id"`
	ProductID    pgtype.UUID        `json:"product_id"`
	Region       string             `json:"region"`
	Warehouse    pgtype.Text        `json:"warehouse"`
	DeliveryDays int32              `json:"delivery_days"`
	DeliveryCost int64              `json:"delivery_cost"`
	InStock      bool               `json:"in_stock"`
	CapturedAt   pgtype.Timestamptz `json:"captured_at"`
}

func (q *Queries) GetLatestDeliveryData(ctx context.Context, productID pgtype.UUID) (DeliveryDataRow, error) {
	row := q.db.QueryRow(ctx,
		`SELECT id, workspace_id, product_id, region, warehouse, delivery_days, delivery_cost, in_stock, captured_at
		FROM delivery_data WHERE product_id = $1 ORDER BY captured_at DESC LIMIT 1`, productID)
	var i DeliveryDataRow
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.Region, &i.Warehouse, &i.DeliveryDays, &i.DeliveryCost, &i.InStock, &i.CapturedAt)
	return i, err
}

func (q *Queries) ListDeliveryByProduct(ctx context.Context, productID pgtype.UUID, limit int32) ([]DeliveryDataRow, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, workspace_id, product_id, region, warehouse, delivery_days, delivery_cost, in_stock, captured_at
		FROM delivery_data WHERE product_id = $1 ORDER BY captured_at DESC LIMIT $2`, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []DeliveryDataRow
	for rows.Next() {
		var i DeliveryDataRow
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.ProductID, &i.Region, &i.Warehouse, &i.DeliveryDays, &i.DeliveryCost, &i.InStock, &i.CapturedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (q *Queries) UpdateProductStock(ctx context.Context, productID pgtype.UUID, stockTotal int32) error {
	_, err := q.db.Exec(ctx, `UPDATE products SET stock_total = $2, updated_at = now() WHERE id = $1`, productID, stockTotal)
	return err
}
