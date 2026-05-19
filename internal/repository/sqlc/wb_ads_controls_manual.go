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

type UpsertProductSalesFunnelPeriodParams struct {
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	ProductID       pgtype.UUID
	WBProductID     int64
	DateFrom        pgtype.Date
	DateTo          pgtype.Date
	CartCount       int64
	CapturedAt      pgtype.Timestamptz
}

func (q *Queries) UpsertProductSalesFunnelPeriod(ctx context.Context, arg UpsertProductSalesFunnelPeriodParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO product_sales_funnel_periods (
  workspace_id, seller_cabinet_id, product_id, wb_product_id, date_from, date_to, cart_count, captured_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (seller_cabinet_id, wb_product_id, date_from, date_to) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  product_id = COALESCE(EXCLUDED.product_id, product_sales_funnel_periods.product_id),
  cart_count = EXCLUDED.cart_count,
  captured_at = EXCLUDED.captured_at,
  updated_at = now()`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.ProductID, arg.WBProductID,
		arg.DateFrom, arg.DateTo, arg.CartCount, arg.CapturedAt)
	return err
}
