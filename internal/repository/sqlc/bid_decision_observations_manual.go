package sqlcgen

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type BidDecisionObservation struct {
	ID                pgtype.UUID
	ObservationKey    string
	WorkspaceID       pgtype.UUID
	SellerCabinetID   pgtype.UUID
	StrategyID        pgtype.UUID
	StrategyBindingID pgtype.UUID
	CampaignID        pgtype.UUID
	ProductID         pgtype.UUID
	PhraseID          pgtype.UUID
	WBCampaignID      int64
	WBProductID       int64
	NormQuery         pgtype.Text
	Placement         string
	OldBid            int32
	ProposedBid       int32
	Reason            string
	Metrics           []byte
	AutomationLevel   int32
	BidObservedAt     pgtype.Timestamptz
	FirstSeenAt       pgtype.Timestamptz
	LastSeenAt        pgtype.Timestamptz
}

type UpsertBidDecisionObservationParams struct {
	ObservationKey    string
	WorkspaceID       pgtype.UUID
	SellerCabinetID   pgtype.UUID
	StrategyID        pgtype.UUID
	StrategyBindingID pgtype.UUID
	CampaignID        pgtype.UUID
	ProductID         pgtype.UUID
	PhraseID          pgtype.UUID
	WBCampaignID      int64
	WBProductID       int64
	NormQuery         pgtype.Text
	Placement         string
	OldBid            int32
	ProposedBid       int32
	Reason            string
	Metrics           []byte
	AutomationLevel   int32
	BidObservedAt     time.Time
}

// UpsertBidDecisionObservation records what an analytics/semi-auto strategy
// would have done. It never updates bid_changes and never calls WB.
func (q *Queries) UpsertBidDecisionObservation(ctx context.Context, arg UpsertBidDecisionObservationParams) error {
	_, err := q.db.Exec(ctx, `
		INSERT INTO bid_decision_observations (
			observation_key, workspace_id, seller_cabinet_id, strategy_id,
			strategy_binding_id, campaign_id, product_id, phrase_id, wb_campaign_id,
			wb_product_id, norm_query, placement, old_bid, proposed_bid, reason, metrics,
			automation_level, bid_observed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13, $14, $15, $16, $17, $18
		)
		ON CONFLICT (observation_key) DO UPDATE SET
			reason = EXCLUDED.reason,
			metrics = EXCLUDED.metrics,
			automation_level = EXCLUDED.automation_level,
			last_seen_at = now()
	`, arg.ObservationKey, arg.WorkspaceID, arg.SellerCabinetID, arg.StrategyID,
		arg.StrategyBindingID, arg.CampaignID, arg.ProductID, arg.PhraseID, arg.WBCampaignID,
		arg.WBProductID, arg.NormQuery, arg.Placement, arg.OldBid, arg.ProposedBid, arg.Reason,
		arg.Metrics, arg.AutomationLevel, arg.BidObservedAt)
	return err
}

type ListBidDecisionObservationsByStrategyParams struct {
	WorkspaceID pgtype.UUID
	StrategyID  pgtype.UUID
	Limit       int32
	Offset      int32
}

func (q *Queries) ListBidDecisionObservationsByStrategy(ctx context.Context, arg ListBidDecisionObservationsByStrategyParams) ([]BidDecisionObservation, error) {
	rows, err := q.db.Query(ctx, `
		SELECT id, observation_key, workspace_id, seller_cabinet_id, strategy_id,
		       strategy_binding_id, campaign_id, product_id, phrase_id, wb_campaign_id,
		       wb_product_id, norm_query, placement, old_bid, proposed_bid, reason, metrics,
		       automation_level, bid_observed_at, first_seen_at, last_seen_at
		FROM bid_decision_observations
		WHERE workspace_id = $1 AND strategy_id = $2
		ORDER BY last_seen_at DESC
		LIMIT $3 OFFSET $4
	`, arg.WorkspaceID, arg.StrategyID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]BidDecisionObservation, 0)
	for rows.Next() {
		var item BidDecisionObservation
		if err := rows.Scan(
			&item.ID, &item.ObservationKey, &item.WorkspaceID, &item.SellerCabinetID,
			&item.StrategyID, &item.StrategyBindingID, &item.CampaignID, &item.ProductID, &item.PhraseID,
			&item.WBCampaignID, &item.WBProductID, &item.NormQuery, &item.Placement, &item.OldBid,
			&item.ProposedBid, &item.Reason, &item.Metrics, &item.AutomationLevel,
			&item.BidObservedAt, &item.FirstSeenAt, &item.LastSeenAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
