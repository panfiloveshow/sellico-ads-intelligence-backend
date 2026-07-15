package sqlcgen

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const campaignProductMembershipAdvisoryLock = `SELECT pg_advisory_xact_lock(
	hashtextextended($1::text || ':campaign-product-membership', 0)
)`

type campaignProductTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// BeginCampaignProductMembershipTx serializes and atomically applies the
// complete WB membership snapshot for one campaign.
func (q *Queries) BeginCampaignProductMembershipTx(ctx context.Context, campaignID pgtype.UUID) (*Queries, pgx.Tx, error) {
	beginner, ok := q.db.(campaignProductTxBeginner)
	if !ok {
		return nil, nil, fmt.Errorf("database does not support campaign product transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, campaignProductMembershipAdvisoryLock, campaignID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return q.WithTx(tx), tx, nil
}

type UpsertCampaignProductParams struct {
	CampaignID         pgtype.UUID
	ProductID          pgtype.UUID
	WorkspaceID        pgtype.UUID
	SellerCabinetID    pgtype.UUID
	WbCampaignID       int64
	WbProductID        int64
	SubjectName        pgtype.Text
	BidSearch          pgtype.Int8
	BidRecommendations pgtype.Int8
}

type CampaignProduct struct {
	CampaignID         pgtype.UUID
	ProductID          pgtype.UUID
	WorkspaceID        pgtype.UUID
	SellerCabinetID    pgtype.UUID
	WbCampaignID       int64
	WbProductID        int64
	SubjectName        pgtype.Text
	BidSearch          pgtype.Int8
	BidRecommendations pgtype.Int8
	CreatedAt          pgtype.Timestamptz
	UpdatedAt          pgtype.Timestamptz
}

const upsertCampaignProduct = `
INSERT INTO campaign_products (campaign_id, product_id, workspace_id, seller_cabinet_id, wb_campaign_id, wb_product_id, subject_name, bid_search, bid_recommendations)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (campaign_id, product_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  seller_cabinet_id = EXCLUDED.seller_cabinet_id,
  wb_campaign_id = EXCLUDED.wb_campaign_id,
  wb_product_id = EXCLUDED.wb_product_id,
  subject_name = COALESCE(EXCLUDED.subject_name, campaign_products.subject_name),
  bid_search = EXCLUDED.bid_search,
  bid_recommendations = EXCLUDED.bid_recommendations,
  updated_at = now()
RETURNING campaign_id, product_id, workspace_id, seller_cabinet_id, wb_campaign_id, wb_product_id, subject_name, bid_search, bid_recommendations, created_at, updated_at
`

func (q *Queries) UpsertCampaignProduct(ctx context.Context, arg UpsertCampaignProductParams) (CampaignProduct, error) {
	row := q.db.QueryRow(ctx, upsertCampaignProduct,
		arg.CampaignID,
		arg.ProductID,
		arg.WorkspaceID,
		arg.SellerCabinetID,
		arg.WbCampaignID,
		arg.WbProductID,
		arg.SubjectName,
		arg.BidSearch,
		arg.BidRecommendations,
	)
	var item CampaignProduct
	err := row.Scan(
		&item.CampaignID,
		&item.ProductID,
		&item.WorkspaceID,
		&item.SellerCabinetID,
		&item.WbCampaignID,
		&item.WbProductID,
		&item.SubjectName,
		&item.BidSearch,
		&item.BidRecommendations,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

type DeleteStaleCampaignProductsParams struct {
	CampaignID         pgtype.UUID
	WorkspaceID        pgtype.UUID
	SellerCabinetID    pgtype.UUID
	WBCampaignID       int64
	CurrentNMIDs       []int64
	MembershipComplete bool
}

const deleteStaleCampaignProducts = `
DELETE FROM campaign_products
WHERE campaign_id = $1
  AND workspace_id = $2
  AND seller_cabinet_id = $3
  AND wb_campaign_id = $4
  AND NOT (wb_product_id = ANY($5::bigint[]))
`

// DeleteStaleCampaignProducts removes links absent from a complete real WB
// membership snapshot. The caller must not invoke it for partial snapshots.
func (q *Queries) DeleteStaleCampaignProducts(ctx context.Context, arg DeleteStaleCampaignProductsParams) (int64, error) {
	if !arg.MembershipComplete {
		return 0, errors.New("campaign product membership snapshot is not complete")
	}
	command, err := q.db.Exec(ctx, deleteStaleCampaignProducts,
		arg.CampaignID, arg.WorkspaceID, arg.SellerCabinetID, arg.WBCampaignID, arg.CurrentNMIDs)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

const listCampaignProductsByWorkspace = `
SELECT campaign_id, product_id, workspace_id, seller_cabinet_id, wb_campaign_id, wb_product_id, subject_name, bid_search, bid_recommendations, created_at, updated_at
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
			&item.SubjectName,
			&item.BidSearch,
			&item.BidRecommendations,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const listCampaignProductsByCampaign = `
SELECT campaign_id, product_id, workspace_id, seller_cabinet_id, wb_campaign_id, wb_product_id, subject_name, bid_search, bid_recommendations, created_at, updated_at
FROM campaign_products
WHERE campaign_id = $1
`

func (q *Queries) ListCampaignProductsByCampaign(ctx context.Context, campaignID pgtype.UUID) ([]CampaignProduct, error) {
	rows, err := q.db.Query(ctx, listCampaignProductsByCampaign, campaignID)
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
			&item.SubjectName,
			&item.BidSearch,
			&item.BidRecommendations,
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
	Shks        pgtype.Int8
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
	Shks        pgtype.Int8
}

const upsertProductStat = `
INSERT INTO product_stats (product_id, campaign_id, date, impressions, clicks, spend, orders, revenue, atbs, canceled, shks)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (product_id, campaign_id, date) DO UPDATE SET
  impressions = EXCLUDED.impressions,
  clicks = EXCLUDED.clicks,
  spend = EXCLUDED.spend,
  orders = EXCLUDED.orders,
  revenue = EXCLUDED.revenue,
  atbs = EXCLUDED.atbs,
  canceled = EXCLUDED.canceled,
  shks = EXCLUDED.shks,
  updated_at = now()
RETURNING id, product_id, campaign_id, date, impressions, clicks, spend, orders, revenue, atbs, canceled, shks, created_at, updated_at
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
		arg.Shks,
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
		&item.Shks,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}

const listProductStatsByWorkspaceDateRange = `
SELECT ps.id, ps.product_id, ps.campaign_id, ps.date, ps.impressions, ps.clicks, ps.spend, ps.orders, ps.revenue, ps.atbs, ps.canceled, ps.shks, ps.created_at, ps.updated_at
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
			&item.Shks,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const getProductStatsByProductCampaignDateRange = `
SELECT ps.id, ps.product_id, ps.campaign_id, ps.date, ps.impressions, ps.clicks, ps.spend, ps.orders, ps.revenue, ps.atbs, ps.canceled, ps.shks, ps.created_at, ps.updated_at
FROM product_stats ps
WHERE ps.product_id = $1
  AND ps.campaign_id = $2
  AND ps.date BETWEEN $3 AND $4
ORDER BY ps.date
`

type GetProductStatsByProductCampaignDateRangeParams struct {
	ProductID  pgtype.UUID
	CampaignID pgtype.UUID
	DateFrom   pgtype.Date
	DateTo     pgtype.Date
}

// GetProductStatsByProductCampaignDateRange returns only the real attributed
// statistics for one product inside one campaign. Bid automation uses this for
// product-level bindings so another SKU's performance cannot affect its bid.
func (q *Queries) GetProductStatsByProductCampaignDateRange(ctx context.Context, arg GetProductStatsByProductCampaignDateRangeParams) ([]ProductStat, error) {
	rows, err := q.db.Query(ctx, getProductStatsByProductCampaignDateRange, arg.ProductID, arg.CampaignID, arg.DateFrom, arg.DateTo)
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
			&item.Shks,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
