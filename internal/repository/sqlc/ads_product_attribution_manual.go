package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type UpsertCampaignProductParams struct {
	CampaignID      pgtype.UUID
	ProductID       pgtype.UUID
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	WbCampaignID    int64
	WbProductID     int64
}

type CampaignProduct struct {
	CampaignID      pgtype.UUID
	ProductID       pgtype.UUID
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	WbCampaignID    int64
	WbProductID     int64
	CreatedAt       pgtype.Timestamptz
	UpdatedAt       pgtype.Timestamptz
}

const upsertCampaignProduct = `
INSERT INTO campaign_products (campaign_id, product_id, workspace_id, seller_cabinet_id, wb_campaign_id, wb_product_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (campaign_id, product_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  seller_cabinet_id = EXCLUDED.seller_cabinet_id,
  wb_campaign_id = EXCLUDED.wb_campaign_id,
  wb_product_id = EXCLUDED.wb_product_id,
  updated_at = now()
RETURNING campaign_id, product_id, workspace_id, seller_cabinet_id, wb_campaign_id, wb_product_id, created_at, updated_at
`

func (q *Queries) UpsertCampaignProduct(ctx context.Context, arg UpsertCampaignProductParams) (CampaignProduct, error) {
	row := q.db.QueryRow(ctx, upsertCampaignProduct,
		arg.CampaignID,
		arg.ProductID,
		arg.WorkspaceID,
		arg.SellerCabinetID,
		arg.WbCampaignID,
		arg.WbProductID,
	)
	var item CampaignProduct
	err := row.Scan(
		&item.CampaignID,
		&item.ProductID,
		&item.WorkspaceID,
		&item.SellerCabinetID,
		&item.WbCampaignID,
		&item.WbProductID,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

const listCampaignProductsByWorkspace = `
SELECT campaign_id, product_id, workspace_id, seller_cabinet_id, wb_campaign_id, wb_product_id, created_at, updated_at
FROM campaign_products
WHERE workspace_id = $1
`

func (q *Queries) ListCampaignProductsByWorkspace(ctx context.Context, workspaceID pgtype.UUID) ([]CampaignProduct, error) {
	rows, err := q.db.Query(ctx, listCampaignProductsByWorkspace, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CampaignProduct
	for rows.Next() {
		var item CampaignProduct
		if err := rows.Scan(
			&item.CampaignID,
			&item.ProductID,
			&item.WorkspaceID,
			&item.SellerCabinetID,
			&item.WbCampaignID,
			&item.WbProductID,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type ProductStat struct {
	ID          pgtype.UUID
	ProductID   pgtype.UUID
	CampaignID  pgtype.UUID
	Date        pgtype.Date
	Impressions int64
	Clicks      int64
	Spend       int64
	Orders      pgtype.Int8
	Revenue     pgtype.Int8
	Atbs        pgtype.Int8
	Canceled    pgtype.Int8
	CreatedAt   pgtype.Timestamptz
	UpdatedAt   pgtype.Timestamptz
}

type UpsertProductStatParams struct {
	ProductID   pgtype.UUID
	CampaignID  pgtype.UUID
	Date        pgtype.Date
	Impressions int64
	Clicks      int64
	Spend       int64
	Orders      pgtype.Int8
	Revenue     pgtype.Int8
	Atbs        pgtype.Int8
	Canceled    pgtype.Int8
}

const upsertProductStat = `
INSERT INTO product_stats (product_id, campaign_id, date, impressions, clicks, spend, orders, revenue, atbs, canceled)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (product_id, campaign_id, date) DO UPDATE SET
  impressions = EXCLUDED.impressions,
  clicks = EXCLUDED.clicks,
  spend = EXCLUDED.spend,
  orders = EXCLUDED.orders,
  revenue = EXCLUDED.revenue,
  atbs = EXCLUDED.atbs,
  canceled = EXCLUDED.canceled,
  updated_at = now()
RETURNING id, product_id, campaign_id, date, impressions, clicks, spend, orders, revenue, atbs, canceled, created_at, updated_at
`

func (q *Queries) UpsertProductStat(ctx context.Context, arg UpsertProductStatParams) (ProductStat, error) {
	row := q.db.QueryRow(ctx, upsertProductStat,
		arg.ProductID,
		arg.CampaignID,
		arg.Date,
		arg.Impressions,
		arg.Clicks,
		arg.Spend,
		arg.Orders,
		arg.Revenue,
		arg.Atbs,
		arg.Canceled,
	)
	var item ProductStat
	err := row.Scan(
		&item.ID,
		&item.ProductID,
		&item.CampaignID,
		&item.Date,
		&item.Impressions,
		&item.Clicks,
		&item.Spend,
		&item.Orders,
		&item.Revenue,
		&item.Atbs,
		&item.Canceled,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

const listProductStatsByWorkspaceDateRange = `
SELECT ps.id, ps.product_id, ps.campaign_id, ps.date, ps.impressions, ps.clicks, ps.spend, ps.orders, ps.revenue, ps.atbs, ps.canceled, ps.created_at, ps.updated_at
FROM product_stats ps
JOIN products p ON p.id = ps.product_id
WHERE p.workspace_id = $1
  AND ps.date BETWEEN $2 AND $3
ORDER BY ps.date DESC
`

type ListProductStatsByWorkspaceDateRangeParams struct {
	WorkspaceID pgtype.UUID
	DateFrom    pgtype.Date
	DateTo      pgtype.Date
}

func (q *Queries) ListProductStatsByWorkspaceDateRange(ctx context.Context, arg ListProductStatsByWorkspaceDateRangeParams) ([]ProductStat, error) {
	rows, err := q.db.Query(ctx, listProductStatsByWorkspaceDateRange, arg.WorkspaceID, arg.DateFrom, arg.DateTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ProductStat
	for rows.Next() {
		var item ProductStat
		if err := rows.Scan(
			&item.ID,
			&item.ProductID,
			&item.CampaignID,
			&item.Date,
			&item.Impressions,
			&item.Clicks,
			&item.Spend,
			&item.Orders,
			&item.Revenue,
			&item.Atbs,
			&item.Canceled,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
