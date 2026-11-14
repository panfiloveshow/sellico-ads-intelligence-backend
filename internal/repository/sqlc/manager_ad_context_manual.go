package sqlcgen

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func (q *Queries) ManagerAdsCampaignByID(ctx context.Context, cabinet domain.ManagerAdsCabinet, id uuid.UUID) (*domain.ManagerAdsCampaign, error) {
	var row *domain.ManagerAdsCampaign = new(domain.ManagerAdsCampaign)
	var err error
	switch cabinet.Marketplace {
	case "wb":
		err = q.db.QueryRow(ctx, `SELECT id,wb_campaign_id::text FROM campaigns WHERE id=$1 AND seller_cabinet_id=$2 AND workspace_id=$3`, id, cabinet.ID, cabinet.WorkspaceID).Scan(&row.ID, &row.ExternalID)
	case "ozon":
		err = q.db.QueryRow(ctx, `SELECT id,ozon_campaign_id::text FROM ozon_campaigns WHERE id=$1 AND seller_cabinet_id=$2`, id, cabinet.ID).Scan(&row.ID, &row.ExternalID)
	default:
		return nil, errors.New("unsupported manager marketplace")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}
