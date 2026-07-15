package sqlcgen

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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

type ClaimAutomationBidActionParams struct {
	AutomationKey             string
	AutomationObservationKey  string
	WorkspaceID               pgtype.UUID
	SellerCabinetID           pgtype.UUID
	CampaignID                pgtype.UUID
	ProductID                 pgtype.UUID
	WBCampaignID              int64
	WBProductID               int64
	OldBid                    int64
	NewBid                    int64
	Reason                    string
	Placement                 string
	BidObservedAt             pgtype.Timestamptz
	StrategyID                pgtype.UUID
	DayStart                  pgtype.Timestamptz
	DayEnd                    pgtype.Timestamptz
	MaxWorkspaceChangesPerDay int32
	PhraseID                  pgtype.UUID
	NormQuery                 pgtype.Text
}

const claimAutomationBidAction = `WITH workspace_lock AS MATERIALIZED (
	SELECT pg_advisory_xact_lock(hashtextextended($3::text || ':workspace-daily-bid-actions', 0))
), scope_lock AS MATERIALIZED (
	-- Serialize all scopes for a campaign placement. This deliberately keeps a
	-- product-wide action (NULL norm_query) from racing a cluster action whose
	-- unresolved-scope predicate treats the product-wide row as a wildcard.
	SELECT pg_advisory_xact_lock(hashtextextended($5::text || ':' || $12, 0))
	FROM workspace_lock
), workspace_state AS MATERIALIZED (
	SELECT
		COALESCE((w.settings #>> '{automation,enabled}')::boolean, false) AS automation_enabled,
		COALESCE((w.settings #>> '{automation,manual_hold}')::boolean, false) AS manual_hold,
		COALESCE(NULLIF(w.settings #>> '{automation,max_bid_changes_per_day}', '')::integer, 0) AS configured_cap,
		$17::integer AS observed_cap
	FROM workspaces w
	CROSS JOIN scope_lock
	WHERE w.id = $3 AND w.deleted_at IS NULL
), claim_state AS MATERIALIZED (
	SELECT
		EXISTS (
			SELECT 1
			FROM wb_bid_actions unresolved
			WHERE unresolved.campaign_id = $5
			  AND (unresolved.placement = $12 OR unresolved.placement IS NULL)
			  AND (unresolved.product_id IS NULL OR $6::uuid IS NULL OR unresolved.product_id = $6)
			  AND (unresolved.norm_query IS NULL OR $19::text IS NULL OR unresolved.norm_query = $19)
			  AND unresolved.action_type = 'strategy_bid'
			  AND unresolved.status IN ('pending', 'unknown')
		) AS unresolved,
		(
			SELECT count(*)
			FROM wb_bid_actions counted
			WHERE counted.workspace_id = $3
			  AND counted.action_type = 'strategy_bid'
			  AND counted.status IN ('pending', 'unknown', 'applied')
			  AND counted.created_at >= $15
			  AND counted.created_at < $16
		) AS daily_action_count,
		automation_enabled,
		manual_hold,
		configured_cap,
		LEAST(configured_cap, observed_cap) AS effective_cap
	FROM workspace_state
)
INSERT INTO wb_bid_actions (
		automation_key, automation_observation_key, workspace_id, seller_cabinet_id, campaign_id, product_id,
		wb_campaign_id, wb_product_id, action_type, old_bid, new_bid, reason, placement, bid_observed_at, strategy_id,
		phrase_id, norm_query, status, wb_response
	)
	SELECT $1,$2,$3,$4,$5,$6,$7,$8,'strategy_bid',$9,$10,$11,$12,$13,$14,$18,$19,
		CASE WHEN NOT automation_enabled OR manual_hold OR effective_cap <= 0 OR daily_action_count >= effective_cap
			THEN 'blocked' ELSE 'pending' END,
		CASE WHEN NOT automation_enabled OR manual_hold OR effective_cap <= 0 OR daily_action_count >= effective_cap
		THEN jsonb_build_object(
			'block_reason', CASE
				WHEN NOT automation_enabled THEN 'workspace automation is not enabled'
				WHEN manual_hold THEN 'workspace automation manual hold is active'
				WHEN effective_cap <= 0 THEN 'workspace max_bid_changes_per_day is unavailable'
				ELSE 'workspace max_bid_changes_per_day reached'
			END,
			'max_bid_changes_per_day', configured_cap,
			'effective_max_bid_changes_per_day', effective_cap,
			'day_start', $15::timestamptz
		) ELSE NULL END
	FROM claim_state
	WHERE NOT unresolved
	ON CONFLICT DO NOTHING
	RETURNING id, status`

type AutomationBidActionClaim struct {
	ID     pgtype.UUID
	Status string
}

type HasUnresolvedAutomationBidActionParams struct {
	CampaignID pgtype.UUID
	ProductID  pgtype.UUID
	Placement  string
	NormQuery  pgtype.Text
}

const hasUnresolvedAutomationBidAction = `SELECT EXISTS (
	SELECT 1
	FROM wb_bid_actions
	WHERE campaign_id = $1
	  AND (product_id IS NULL OR $2::uuid IS NULL OR product_id = $2)
	  AND (placement = $3 OR placement IS NULL)
	  AND (norm_query IS NULL OR $4::text IS NULL OR norm_query = $4)
	  AND action_type = 'strategy_bid'
	  AND status IN ('pending', 'unknown')
)`

func (q *Queries) HasUnresolvedAutomationBidAction(ctx context.Context, arg HasUnresolvedAutomationBidActionParams) (bool, error) {
	var unresolved bool
	err := q.db.QueryRow(ctx, hasUnresolvedAutomationBidAction, arg.CampaignID, arg.ProductID, arg.Placement, arg.NormQuery).Scan(&unresolved)
	return unresolved, err
}

func (q *Queries) ClaimAutomationBidAction(ctx context.Context, arg ClaimAutomationBidActionParams) (AutomationBidActionClaim, bool, error) {
	var claim AutomationBidActionClaim
	err := q.db.QueryRow(ctx, claimAutomationBidAction,
		arg.AutomationKey, arg.AutomationObservationKey, arg.WorkspaceID, arg.SellerCabinetID, arg.CampaignID, arg.ProductID,
		arg.WBCampaignID, arg.WBProductID, arg.OldBid, arg.NewBid, arg.Reason, arg.Placement, arg.BidObservedAt, arg.StrategyID,
		arg.DayStart, arg.DayEnd, arg.MaxWorkspaceChangesPerDay, arg.PhraseID, arg.NormQuery).Scan(&claim.ID, &claim.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return AutomationBidActionClaim{}, false, nil
	}
	if err != nil {
		return AutomationBidActionClaim{}, false, err
	}
	return claim, true, nil
}

type AutomationBidActionPreWriteGuardParams struct {
	ActionID                  pgtype.UUID
	WorkspaceID               pgtype.UUID
	DayStart                  pgtype.Timestamptz
	DayEnd                    pgtype.Timestamptz
	MaxWorkspaceChangesPerDay int32
}

const automationBidActionPreWriteGuard = `WITH workspace_lock AS MATERIALIZED (
	SELECT pg_advisory_xact_lock(hashtextextended($2::text || ':workspace-daily-bid-actions', 0))
), state AS MATERIALIZED (
	SELECT
		EXISTS (
			SELECT 1 FROM wb_bid_actions action
			WHERE action.id = $1
			  AND action.workspace_id = $2
			  AND action.action_type = 'strategy_bid'
			  AND action.status = 'pending'
		) AS action_pending,
		COALESCE((w.settings #>> '{automation,enabled}')::boolean, false) AS automation_enabled,
		COALESCE((w.settings #>> '{automation,manual_hold}')::boolean, false) AS manual_hold,
		COALESCE(NULLIF(w.settings #>> '{automation,hold_reason}', ''), 'manual hold is active') AS hold_reason,
		COALESCE(NULLIF(w.settings #>> '{automation,max_bid_changes_per_day}', '')::integer, 0) AS configured_cap,
		LEAST(
			COALESCE(NULLIF(w.settings #>> '{automation,max_bid_changes_per_day}', '')::integer, 0),
			$5::integer
		) AS effective_cap,
		(
			SELECT count(*)
			FROM wb_bid_actions counted
			WHERE counted.workspace_id = $2
			  AND counted.action_type = 'strategy_bid'
			  AND counted.status IN ('pending', 'unknown', 'applied')
			  AND counted.created_at >= $3
			  AND counted.created_at < $4
		) AS daily_action_count
	FROM workspaces w
	CROSS JOIN workspace_lock
	WHERE w.id = $2 AND w.deleted_at IS NULL
)
SELECT CASE
	WHEN NOT action_pending THEN 'automation action claim is no longer pending'
	WHEN NOT automation_enabled THEN 'workspace automation is not enabled'
	WHEN manual_hold THEN hold_reason
	WHEN effective_cap <= 0 THEN 'workspace max_bid_changes_per_day is unavailable'
	WHEN daily_action_count > effective_cap THEN 'workspace max_bid_changes_per_day was lowered below claimed actions'
	ELSE ''
END
FROM state`

// AutomationBidActionPreWriteGuard serializes the final cap check with both
// claims and settings updates. The claimed action is included in the count, so
// count == cap is allowed while count > a newly lowered cap fails closed.
func (q *Queries) AutomationBidActionPreWriteGuard(ctx context.Context, arg AutomationBidActionPreWriteGuardParams) (string, error) {
	var reason string
	err := q.db.QueryRow(ctx, automationBidActionPreWriteGuard,
		arg.ActionID, arg.WorkspaceID, arg.DayStart, arg.DayEnd, arg.MaxWorkspaceChangesPerDay,
	).Scan(&reason)
	return reason, err
}

type automationBidWriteTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// BeginAutomationBidWriteLease keeps the workspace advisory lock until the
// caller finishes the external WB write. A concurrent cap/settings update is
// therefore ordered either before this final check or after the write, never in
// the gap between them.
func (q *Queries) BeginAutomationBidWriteLease(ctx context.Context, arg AutomationBidActionPreWriteGuardParams) (pgx.Tx, string, error) {
	beginner, ok := q.db.(automationBidWriteTxBeginner)
	if !ok {
		return nil, "", fmt.Errorf("database does not support automation bid write transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	reason, err := q.WithTx(tx).AutomationBidActionPreWriteGuard(ctx, arg)
	if err != nil {
		_ = tx.Rollback(context.Background())
		return nil, "", err
	}
	if reason != "" {
		_ = tx.Rollback(context.Background())
		return nil, reason, nil
	}
	return tx, "", nil
}

type AutomationBidActionForReconciliation struct {
	ID              pgtype.UUID
	AutomationKey   string
	WorkspaceID     pgtype.UUID
	SellerCabinetID pgtype.UUID
	CampaignID      pgtype.UUID
	ProductID       pgtype.UUID
	PhraseID        pgtype.UUID
	WBCampaignID    int64
	WBProductID     int64
	OldBid          int64
	NewBid          int64
	Placement       pgtype.Text
	NormQuery       pgtype.Text
	BidObservedAt   pgtype.Timestamptz
	Status          string
	CreatedAt       pgtype.Timestamptz
}

type ListStaleAutomationBidActionsParams struct {
	OlderThan pgtype.Timestamptz
	Limit     int32
}

func (q *Queries) ListStaleAutomationBidActions(ctx context.Context, arg ListStaleAutomationBidActionsParams) ([]AutomationBidActionForReconciliation, error) {
	rows, err := q.db.Query(ctx, `SELECT id, automation_key, workspace_id, seller_cabinet_id,
		campaign_id, product_id, phrase_id, wb_campaign_id, wb_product_id, old_bid, new_bid,
		placement, norm_query, bid_observed_at, status, created_at
	FROM wb_bid_actions
	WHERE action_type = 'strategy_bid'
	  AND status IN ('pending', 'unknown')
	  AND created_at <= $1
	ORDER BY created_at ASC
	LIMIT $2`, arg.OlderThan, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]AutomationBidActionForReconciliation, 0)
	for rows.Next() {
		var item AutomationBidActionForReconciliation
		if err := rows.Scan(
			&item.ID, &item.AutomationKey, &item.WorkspaceID, &item.SellerCabinetID,
			&item.CampaignID, &item.ProductID, &item.PhraseID, &item.WBCampaignID, &item.WBProductID,
			&item.OldBid, &item.NewBid, &item.Placement, &item.NormQuery, &item.BidObservedAt,
			&item.Status, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

type ReconcileAutomationBidActionParams struct {
	ID           pgtype.UUID
	Status       string
	WBResponse   []byte
	ReconciledAt pgtype.Timestamptz
}

type UpsertReconciledAutomationBidChangeParams struct {
	AutomationActionID pgtype.UUID
	Status             string
	Placement          string
}

const upsertReconciledAutomationBidChange = `INSERT INTO bid_changes (
	workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id,
	placement, old_bid, new_bid, reason, source, wb_status, automation_action_id
)
SELECT action.workspace_id, action.seller_cabinet_id, action.campaign_id,
	action.product_id, action.phrase_id, action.strategy_id, COALESCE(NULLIF(action.placement, ''), $3),
	action.old_bid::int, action.new_bid::int, COALESCE(action.reason, 'Automated bid reconciliation'),
	'strategy', $2, action.id
FROM wb_bid_actions action
WHERE action.id = $1
  AND action.action_type = 'strategy_bid'
  AND action.status IN ('pending', 'unknown')
  AND action.campaign_id IS NOT NULL
ON CONFLICT (automation_action_id) WHERE automation_action_id IS NOT NULL
DO UPDATE SET wb_status = EXCLUDED.wb_status`

// UpsertReconciledAutomationBidChange is the canonical user-facing audit
// bridge. It creates the history row after a crash-before-audit and updates the
// same row when the initial uncertain attempt was already recorded.
func (q *Queries) UpsertReconciledAutomationBidChange(ctx context.Context, arg UpsertReconciledAutomationBidChangeParams) (bool, error) {
	command, err := q.db.Exec(ctx, upsertReconciledAutomationBidChange,
		arg.AutomationActionID, arg.Status, arg.Placement)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (q *Queries) ReconcileAutomationBidAction(ctx context.Context, arg ReconcileAutomationBidActionParams) (bool, error) {
	command, err := q.db.Exec(ctx, `UPDATE wb_bid_actions
	SET status = $2, wb_response = $3, reconciled_at = $4, updated_at = now()
	WHERE id = $1 AND status IN ('pending', 'unknown')`,
		arg.ID, arg.Status, arg.WBResponse, arg.ReconciledAt)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

type CompleteAutomationBidActionParams struct {
	AutomationKey string
	Status        string
	WBResponse    []byte
}

func (q *Queries) CompleteAutomationBidAction(ctx context.Context, arg CompleteAutomationBidActionParams) error {
	_, err := q.db.Exec(ctx, `UPDATE wb_bid_actions
		SET status = $2, wb_response = $3, updated_at = now()
		WHERE automation_key = $1 AND status = 'pending'`, arg.AutomationKey, arg.Status, arg.WBResponse)
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

type CampaignDailyLimit struct {
	ID               pgtype.UUID
	WorkspaceID      pgtype.UUID
	SellerCabinetID  pgtype.UUID
	CampaignID       pgtype.UUID
	DailyLimit       int64
	Enabled          bool
	PauseWhenReached bool
	ResumeNextDay    bool
	LastCheckedAt    pgtype.Timestamptz
	LastActionAt     pgtype.Timestamptz
	CreatedAt        pgtype.Timestamptz
	UpdatedAt        pgtype.Timestamptz
}

func (q *Queries) GetCampaignDailyLimit(ctx context.Context, campaignID pgtype.UUID) (CampaignDailyLimit, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, seller_cabinet_id, campaign_id, daily_limit,
		enabled, pause_when_reached, resume_next_day, last_checked_at, last_action_at, created_at, updated_at
		FROM campaign_daily_limits WHERE campaign_id = $1`, campaignID)
	var item CampaignDailyLimit
	err := row.Scan(&item.ID, &item.WorkspaceID, &item.SellerCabinetID, &item.CampaignID, &item.DailyLimit,
		&item.Enabled, &item.PauseWhenReached, &item.ResumeNextDay, &item.LastCheckedAt, &item.LastActionAt,
		&item.CreatedAt, &item.UpdatedAt)
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
