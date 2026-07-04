package sqlcgen

import (
	"context"

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
SET status = EXCLUDED.status, items_count = EXCLUDED.items_count, updated_at = now()
RETURNING id, workspace_id, seller_cabinet_id, wb_task_id, status, items_count, poll_count,
    last_polled_at, completed_at, error, created_at, updated_at
`

func (q *Queries) CreatePriceUploadTask(ctx context.Context, arg CreatePriceUploadTaskParams) (PriceUploadTask, error) {
	row := q.db.QueryRow(ctx, createPriceUploadTask, arg.WorkspaceID, arg.SellerCabinetID, arg.WbTaskID, arg.Status, arg.ItemsCount)
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
