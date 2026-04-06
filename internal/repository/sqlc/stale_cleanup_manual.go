package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// MarkStaleCampaigns sets status='deleted' on campaigns in a cabinet that are NOT in the given wb_campaign_id list.
// Returns the number of rows affected.
func (q *Queries) MarkStaleCampaigns(ctx context.Context, sellerCabinetID pgtype.UUID, activeWBIDs []int64) (int64, error) {
	if len(activeWBIDs) == 0 {
		return 0, nil
	}
	tag, err := q.db.Exec(ctx,
		`UPDATE campaigns SET status = 'deleted', updated_at = NOW()
		 WHERE seller_cabinet_id = $1
		   AND status != 'deleted'
		   AND wb_campaign_id != ALL($2)`,
		sellerCabinetID, activeWBIDs,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkStaleProducts sets deleted_at for products in a cabinet that are NOT in the given wb_product_id list.
// Returns the number of rows affected.
func (q *Queries) MarkStaleProducts(ctx context.Context, sellerCabinetID pgtype.UUID, activeWBIDs []int64) (int64, error) {
	if len(activeWBIDs) == 0 {
		return 0, nil
	}
	tag, err := q.db.Exec(ctx,
		`UPDATE products SET updated_at = NOW()
		 WHERE seller_cabinet_id = $1
		   AND wb_product_id != ALL($2)
		   AND updated_at > NOW() - INTERVAL '7 days'`,
		sellerCabinetID, activeWBIDs,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CloseOrphanedRecommendations closes active recommendations for campaigns that no longer exist.
func (q *Queries) CloseOrphanedRecommendations(ctx context.Context, workspaceID pgtype.UUID) (int64, error) {
	tag, err := q.db.Exec(ctx,
		`UPDATE recommendations SET status = 'closed', updated_at = NOW()
		 WHERE workspace_id = $1
		   AND status = 'active'
		   AND campaign_id IS NOT NULL
		   AND campaign_id NOT IN (
		       SELECT id FROM campaigns WHERE workspace_id = $1 AND status != 'deleted'
		   )`,
		workspaceID,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
