package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type UpdateSERPItemPromoStatusParams struct {
	ID         pgtype.UUID
	IsPromoted bool
	PromoType  string
}

func (q *Queries) UpdateSERPItemPromoStatus(ctx context.Context, arg UpdateSERPItemPromoStatusParams) error {
	_, err := q.db.Exec(ctx,
		`UPDATE serp_result_items SET is_promoted = $2, promo_type = $3 WHERE id = $1`,
		arg.ID, arg.IsPromoted, arg.PromoType)
	return err
}
