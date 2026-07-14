package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type GetCampaignByIDAndWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) GetCampaignByIDAndWorkspace(ctx context.Context, arg GetCampaignByIDAndWorkspaceParams) (Campaign, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, seller_cabinet_id, wb_campaign_id, name, status, campaign_type, bid_type, payment_type, daily_budget, placement_search, placement_recommendations, created_at, updated_at FROM campaigns WHERE id = $1 AND workspace_id = $2`, arg.ID, arg.WorkspaceID)
	var i Campaign
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.WbCampaignID, &i.Name, &i.Status, &i.CampaignType, &i.BidType, &i.PaymentType, &i.DailyBudget, &i.PlacementSearch, &i.PlacementRecommendations, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

type GetProductByIDAndWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) GetProductByIDAndWorkspace(ctx context.Context, arg GetProductByIDAndWorkspaceParams) (Product, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, seller_cabinet_id, wb_product_id, title, brand, category, image_url, price, created_at, updated_at, current_bid_search, current_bid_recommend, min_bid_search, min_bid_recommend, competitive_bid, rating, reviews_count, stock_total, content_hash, last_event_at FROM products WHERE id = $1 AND workspace_id = $2`, arg.ID, arg.WorkspaceID)
	var i Product
	err := row.Scan(
		&i.ID,
		&i.WorkspaceID,
		&i.SellerCabinetID,
		&i.WbProductID,
		&i.Title,
		&i.Brand,
		&i.Category,
		&i.ImageUrl,
		&i.Price,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.CurrentBidSearch,
		&i.CurrentBidRecommend,
		&i.MinBidSearch,
		&i.MinBidRecommend,
		&i.CompetitiveBid,
		&i.Rating,
		&i.ReviewsCount,
		&i.StockTotal,
		&i.ContentHash,
		&i.LastEventAt,
	)
	return i, err
}

type GetRecommendationByIDAndWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) GetRecommendationByIDAndWorkspace(ctx context.Context, arg GetRecommendationByIDAndWorkspaceParams) (Recommendation, error) {
	row := q.db.QueryRow(ctx, `SELECT id, workspace_id, campaign_id, phrase_id, product_id, title, description, type, severity, confidence, source_metrics, next_action, status, created_at, updated_at FROM recommendations WHERE id = $1 AND workspace_id = $2`, arg.ID, arg.WorkspaceID)
	var i Recommendation
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.CampaignID, &i.PhraseID, &i.ProductID, &i.Title, &i.Description, &i.Type, &i.Severity, &i.Confidence, &i.SourceMetrics, &i.NextAction, &i.Status, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

type UpdateRecommendationStatusInWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
	Status      string
}

func (q *Queries) UpdateRecommendationStatusInWorkspace(ctx context.Context, arg UpdateRecommendationStatusInWorkspaceParams) (Recommendation, error) {
	row := q.db.QueryRow(ctx, `UPDATE recommendations SET status = $3, updated_at = now() WHERE id = $1 AND workspace_id = $2 RETURNING id, workspace_id, campaign_id, phrase_id, product_id, title, description, type, severity, confidence, source_metrics, next_action, status, created_at, updated_at`, arg.ID, arg.WorkspaceID, arg.Status)
	var i Recommendation
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.CampaignID, &i.PhraseID, &i.ProductID, &i.Title, &i.Description, &i.Type, &i.Severity, &i.Confidence, &i.SourceMetrics, &i.NextAction, &i.Status, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// ClaimActiveRecommendationInWorkspace atomically grants one caller the right
// to execute an external recommendation action. Concurrent callers receive no
// row and therefore cannot send the same mutation to WB twice.
func (q *Queries) ClaimActiveRecommendationInWorkspace(ctx context.Context, id, workspaceID pgtype.UUID) (Recommendation, error) {
	row := q.db.QueryRow(ctx, `UPDATE recommendations
SET status = 'applying', updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'active'
RETURNING id, workspace_id, campaign_id, phrase_id, product_id, title, description, type, severity, confidence, source_metrics, next_action, status, created_at, updated_at`, id, workspaceID)
	var i Recommendation
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.CampaignID, &i.PhraseID, &i.ProductID, &i.Title, &i.Description, &i.Type, &i.Severity, &i.Confidence, &i.SourceMetrics, &i.NextAction, &i.Status, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// TransitionRecommendationStatusInWorkspace performs a compare-and-set state
// transition so completion/release cannot overwrite a newer operator action.
func (q *Queries) TransitionRecommendationStatusInWorkspace(ctx context.Context, id, workspaceID pgtype.UUID, fromStatus, toStatus string) (Recommendation, error) {
	row := q.db.QueryRow(ctx, `UPDATE recommendations
SET status = $4, updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = $3
RETURNING id, workspace_id, campaign_id, phrase_id, product_id, title, description, type, severity, confidence, source_metrics, next_action, status, created_at, updated_at`, id, workspaceID, fromStatus, toStatus)
	var i Recommendation
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.CampaignID, &i.PhraseID, &i.ProductID, &i.Title, &i.Description, &i.Type, &i.Severity, &i.Confidence, &i.SourceMetrics, &i.NextAction, &i.Status, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

// InvalidateActiveRecommendationsForCampaign closes recommendations whose
// suggested action is no longer valid after a campaign lifecycle transition.
func (q *Queries) InvalidateActiveRecommendationsForCampaign(ctx context.Context, workspaceID, campaignID pgtype.UUID) (int64, error) {
	tag, err := q.db.Exec(ctx, `UPDATE recommendations
SET status = 'invalidated', updated_at = now()
	WHERE workspace_id = $1
	  AND status = 'active'
	  AND (
		campaign_id = $2
		OR phrase_id IN (SELECT id FROM phrases WHERE campaign_id = $2)
	  )`, workspaceID, campaignID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type ListBidChangesByCampaignAndWorkspaceParams struct {
	CampaignID  pgtype.UUID
	WorkspaceID pgtype.UUID
	Limit       int32
	Offset      int32
}

func (q *Queries) ListBidChangesByCampaignAndWorkspace(ctx context.Context, arg ListBidChangesByCampaignAndWorkspaceParams) ([]BidChange, error) {
	rows, err := q.db.Query(ctx, `SELECT id, workspace_id, seller_cabinet_id, campaign_id, product_id, phrase_id, strategy_id, recommendation_id, placement, old_bid, new_bid, reason, source, acos, roas, wb_status, created_at FROM bid_changes WHERE campaign_id = $1 AND workspace_id = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`, arg.CampaignID, arg.WorkspaceID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []BidChange
	for rows.Next() {
		var i BidChange
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.SellerCabinetID, &i.CampaignID, &i.ProductID, &i.PhraseID, &i.StrategyID, &i.RecommendationID, &i.Placement, &i.OldBid, &i.NewBid, &i.Reason, &i.Source, &i.Acos, &i.Roas, &i.WbStatus, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

type DeleteCampaignPhraseInWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

func (q *Queries) DeleteMinusPhraseInWorkspace(ctx context.Context, arg DeleteCampaignPhraseInWorkspaceParams) error {
	_, err := q.db.Exec(ctx, `DELETE FROM campaign_minus_phrases mp USING campaigns c WHERE mp.id = $1 AND mp.campaign_id = c.id AND c.workspace_id = $2`, arg.ID, arg.WorkspaceID)
	return err
}

func (q *Queries) DeletePlusPhraseInWorkspace(ctx context.Context, arg DeleteCampaignPhraseInWorkspaceParams) error {
	_, err := q.db.Exec(ctx, `DELETE FROM campaign_plus_phrases pp USING campaigns c WHERE pp.id = $1 AND pp.campaign_id = c.id AND c.workspace_id = $2`, arg.ID, arg.WorkspaceID)
	return err
}
