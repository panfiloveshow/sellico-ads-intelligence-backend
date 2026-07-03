package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type UpsertWBNormQueryClusterParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	CampaignID      pgtype.UUID
	ProductID       pgtype.UUID
	WBCampaignID    int64
	WBProductID     int64
	NormQuery       string
	State           string
	CurrentBid      pgtype.Int8
	SyncedAt        pgtype.Timestamptz
}

func (q *Queries) UpsertWBNormQueryCluster(ctx context.Context, arg UpsertWBNormQueryClusterParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO wb_normquery_clusters (
  workspace_id, seller_cabinet_id, campaign_id, product_id, wb_campaign_id,
  wb_product_id, norm_query, state, current_bid, synced_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (seller_cabinet_id, wb_campaign_id, wb_product_id, norm_query) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  campaign_id = COALESCE(EXCLUDED.campaign_id, wb_normquery_clusters.campaign_id),
  product_id = COALESCE(EXCLUDED.product_id, wb_normquery_clusters.product_id),
  state = EXCLUDED.state,
  current_bid = COALESCE(EXCLUDED.current_bid, wb_normquery_clusters.current_bid),
  synced_at = EXCLUDED.synced_at,
  updated_at = now()`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.CampaignID, arg.ProductID, arg.WBCampaignID,
		arg.WBProductID, arg.NormQuery, arg.State, arg.CurrentBid, arg.SyncedAt)
	return err
}

type CreateWBBidActionParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	CampaignID      pgtype.UUID
	ProductID       pgtype.UUID
	WBCampaignID    int64
	WBProductID     int64
	NormQuery       pgtype.Text
	ActionType      string
	OldBid          pgtype.Int8
	NewBid          pgtype.Int8
	Reason          pgtype.Text
	Status          string
	WBResponse      []byte
	CreatedBy       pgtype.UUID
}

func (q *Queries) CreateWBBidAction(ctx context.Context, arg CreateWBBidActionParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO wb_bid_actions (
  workspace_id, seller_cabinet_id, campaign_id, product_id, wb_campaign_id,
  wb_product_id, norm_query, action_type, old_bid, new_bid, reason,
  status, wb_response, created_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.CampaignID, arg.ProductID, arg.WBCampaignID,
		arg.WBProductID, arg.NormQuery, arg.ActionType, arg.OldBid, arg.NewBid, arg.Reason,
		arg.Status, arg.WBResponse, arg.CreatedBy)
	return err
}

type UpsertSellerAdBalanceParams struct {
	SellerCabinetID pgtype.UUID
	Balance         int64
	Net             int64
	Bonus           int64
	CapturedAt      pgtype.Timestamptz
}

func (q *Queries) UpsertSellerAdBalance(ctx context.Context, arg UpsertSellerAdBalanceParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO seller_ad_balances (seller_cabinet_id, balance, net, bonus, captured_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (seller_cabinet_id, captured_at) DO UPDATE SET
  balance = EXCLUDED.balance,
  net = EXCLUDED.net,
  bonus = EXCLUDED.bonus`,
		arg.SellerCabinetID, arg.Balance, arg.Net, arg.Bonus, arg.CapturedAt)
	return err
}

type SellerAdBalance struct {
	ID              pgtype.UUID
	SellerCabinetID pgtype.UUID
	Balance         int64
	Net             int64
	Bonus           int64
	CapturedAt      pgtype.Timestamptz
	CreatedAt       pgtype.Timestamptz
}

func (q *Queries) GetLatestSellerAdBalance(ctx context.Context, sellerCabinetID pgtype.UUID) (SellerAdBalance, error) {
	row := q.db.QueryRow(ctx, `
SELECT id, seller_cabinet_id, balance, net, bonus, captured_at, created_at
FROM seller_ad_balances
WHERE seller_cabinet_id = $1
ORDER BY captured_at DESC
LIMIT 1`, sellerCabinetID)
	var item SellerAdBalance
	err := row.Scan(
		&item.ID,
		&item.SellerCabinetID,
		&item.Balance,
		&item.Net,
		&item.Bonus,
		&item.CapturedAt,
		&item.CreatedAt,
	)
	return item, err
}

type UpsertWBAdFinanceDocumentParams struct {
	SellerCabinetID pgtype.UUID
	ExternalID      string
	DocumentType    string
	WBCampaignID    int64
	Amount          int64
	DocumentDate    pgtype.Timestamptz
	Raw             []byte
}

func (q *Queries) UpsertWBAdFinanceDocument(ctx context.Context, arg UpsertWBAdFinanceDocumentParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO wb_ad_finance_documents (
  seller_cabinet_id, external_id, document_type, wb_campaign_id, amount, document_date, raw
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (seller_cabinet_id, document_type, external_id) DO UPDATE SET
  wb_campaign_id = EXCLUDED.wb_campaign_id,
  amount = EXCLUDED.amount,
  document_date = EXCLUDED.document_date,
  raw = EXCLUDED.raw,
  updated_at = now()`,
		arg.SellerCabinetID, arg.ExternalID, arg.DocumentType, arg.WBCampaignID, arg.Amount, arg.DocumentDate, arg.Raw)
	return err
}

type WBAdFinanceDocument struct {
	ID              pgtype.UUID
	SellerCabinetID pgtype.UUID
	ExternalID      string
	DocumentType    string
	WBCampaignID    int64
	Amount          int64
	DocumentDate    pgtype.Timestamptz
	Raw             []byte
	CreatedAt       pgtype.Timestamptz
	UpdatedAt       pgtype.Timestamptz
}

func (q *Queries) ListWBAdFinanceDocumentsByWorkspaceDateRange(ctx context.Context, workspaceID pgtype.UUID, dateFrom, dateTo pgtype.Timestamptz, limit, offset int32) ([]WBAdFinanceDocument, error) {
	rows, err := q.db.Query(ctx, `
SELECT d.id, d.seller_cabinet_id, d.external_id, d.document_type, d.wb_campaign_id,
  d.amount, d.document_date, d.raw, d.created_at, d.updated_at
FROM wb_ad_finance_documents d
JOIN seller_cabinets sc ON sc.id = d.seller_cabinet_id
WHERE sc.workspace_id = $1
  AND d.document_date IS NOT NULL
  AND d.document_date >= $2
  AND d.document_date < $3
ORDER BY d.document_date DESC, d.created_at DESC
LIMIT $4 OFFSET $5`, workspaceID, dateFrom, dateTo, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []WBAdFinanceDocument{}
	for rows.Next() {
		var item WBAdFinanceDocument
		if err := rows.Scan(
			&item.ID,
			&item.SellerCabinetID,
			&item.ExternalID,
			&item.DocumentType,
			&item.WBCampaignID,
			&item.Amount,
			&item.DocumentDate,
			&item.Raw,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type UpsertProductSalesFunnelPeriodParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	ProductID       pgtype.UUID
	WBProductID     int64
	DateFrom        pgtype.Date
	DateTo          pgtype.Date
	OpenCount       int64
	CartCount       int64
	OrderCount      int64
	CapturedAt      pgtype.Timestamptz
}

func (q *Queries) UpsertProductSalesFunnelPeriod(ctx context.Context, arg UpsertProductSalesFunnelPeriodParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO product_sales_funnel_periods (
  workspace_id, seller_cabinet_id, product_id, wb_product_id, date_from, date_to,
  open_count, cart_count, order_count, captured_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (seller_cabinet_id, wb_product_id, date_from, date_to) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  product_id = COALESCE(EXCLUDED.product_id, product_sales_funnel_periods.product_id),
  open_count = EXCLUDED.open_count,
  cart_count = EXCLUDED.cart_count,
  order_count = EXCLUDED.order_count,
  captured_at = EXCLUDED.captured_at,
  updated_at = now()`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.ProductID, arg.WBProductID,
		arg.DateFrom, arg.DateTo, arg.OpenCount, arg.CartCount, arg.OrderCount, arg.CapturedAt)
	return err
}

type ProductSalesFunnelPeriod struct {
	ID              pgtype.UUID
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	ProductID       pgtype.UUID
	WBProductID     int64
	DateFrom        pgtype.Date
	DateTo          pgtype.Date
	OpenCount       int64
	CartCount       int64
	OrderCount      int64
	Source          string
	CapturedAt      pgtype.Timestamptz
}

func (q *Queries) ListProductSalesFunnelPeriodsByWorkspaceDateRange(ctx context.Context, workspaceID pgtype.UUID, dateFrom, dateTo pgtype.Date, limit, offset int32) ([]ProductSalesFunnelPeriod, error) {
	rows, err := q.db.Query(ctx, `
SELECT id, workspace_id, seller_cabinet_id, product_id, wb_product_id, date_from, date_to,
  open_count, cart_count, order_count, source, captured_at
FROM product_sales_funnel_periods
WHERE workspace_id = $1
  AND date_to >= $2
  AND date_from <= $3
ORDER BY date_to DESC, captured_at DESC
LIMIT $4 OFFSET $5`, workspaceID, dateFrom, dateTo, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ProductSalesFunnelPeriod{}
	for rows.Next() {
		var item ProductSalesFunnelPeriod
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.SellerCabinetID,
			&item.ProductID,
			&item.WBProductID,
			&item.DateFrom,
			&item.DateTo,
			&item.OpenCount,
			&item.CartCount,
			&item.OrderCount,
			&item.Source,
			&item.CapturedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
