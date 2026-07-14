package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

func (q *Queries) CountProductCatalog(ctx context.Context, workspaceID, sellerCabinetID pgtype.UUID) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM products
		WHERE workspace_id = $1 AND ($2::uuid IS NULL OR seller_cabinet_id = $2)`, workspaceID, sellerCabinetID).Scan(&count)
	return count, err
}

func (q *Queries) CountPriceChanges(ctx context.Context, arg ListPriceChangesParams) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM price_changes
		WHERE workspace_id = $1
		  AND ($2::uuid IS NULL OR seller_cabinet_id = $2)
		  AND ($3::bigint IS NULL OR wb_product_id = $3)
		  AND ($4::text IS NULL OR source = $4)
		  AND ($5::text IS NULL OR wb_status = $5)`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.WbProductID, arg.Source, arg.WbStatus).Scan(&count)
	return count, err
}

func (q *Queries) CountPriceUploadTasks(ctx context.Context, workspaceID, sellerCabinetID pgtype.UUID) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM price_upload_tasks
		WHERE workspace_id = $1 AND ($2::uuid IS NULL OR seller_cabinet_id = $2)`, workspaceID, sellerCabinetID).Scan(&count)
	return count, err
}

func (q *Queries) CountPriceSchedules(ctx context.Context, workspaceID, sellerCabinetID pgtype.UUID, status pgtype.Text) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx, `SELECT COUNT(*) FROM price_schedule_entries
		WHERE workspace_id = $1
		  AND ($2::uuid IS NULL OR seller_cabinet_id = $2)
		  AND ($3::text IS NULL OR status = $3)`, workspaceID, sellerCabinetID, status).Scan(&count)
	return count, err
}
