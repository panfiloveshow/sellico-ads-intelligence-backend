package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type ProductEconomics struct {
	ID                  pgtype.UUID
	WorkspaceID         pgtype.UUID
	WbProductID         int64
	CostPrice           pgtype.Int8
	LogisticsCost       pgtype.Int8
	OtherCosts          pgtype.Int8
	TaxRatePercent      pgtype.Float8
	CommissionPercent   pgtype.Float8
	TargetMarginPercent pgtype.Float8
	MaxAllowedDrr       pgtype.Float8
	Source              string
	EffectiveAt         pgtype.Date
	UpdatedBy           pgtype.UUID
	CreatedAt           pgtype.Timestamptz
	UpdatedAt           pgtype.Timestamptz
}

type UpsertProductEconomicsParams struct {
	WorkspaceID         pgtype.UUID
	WbProductID         int64
	CostPrice           pgtype.Int8
	LogisticsCost       pgtype.Int8
	OtherCosts          pgtype.Int8
	TaxRatePercent      pgtype.Float8
	CommissionPercent   pgtype.Float8
	TargetMarginPercent pgtype.Float8
	MaxAllowedDrr       pgtype.Float8
	Source              string
	EffectiveAt         pgtype.Date
	UpdatedBy           pgtype.UUID
}

func (q *Queries) UpsertProductEconomics(ctx context.Context, arg UpsertProductEconomicsParams) (ProductEconomics, error) {
	row := q.db.QueryRow(ctx, `
INSERT INTO product_economics (
  workspace_id, wb_product_id, cost_price, logistics_cost, other_costs,
  tax_rate_percent, commission_percent, target_margin_percent, max_allowed_drr,
  source, effective_at, updated_by
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (workspace_id, wb_product_id) DO UPDATE SET
  cost_price = COALESCE(EXCLUDED.cost_price, product_economics.cost_price),
  logistics_cost = COALESCE(EXCLUDED.logistics_cost, product_economics.logistics_cost),
  other_costs = COALESCE(EXCLUDED.other_costs, product_economics.other_costs),
  tax_rate_percent = COALESCE(EXCLUDED.tax_rate_percent, product_economics.tax_rate_percent),
  commission_percent = COALESCE(EXCLUDED.commission_percent, product_economics.commission_percent),
  target_margin_percent = COALESCE(EXCLUDED.target_margin_percent, product_economics.target_margin_percent),
  max_allowed_drr = COALESCE(EXCLUDED.max_allowed_drr, product_economics.max_allowed_drr),
  source = EXCLUDED.source,
  effective_at = COALESCE(EXCLUDED.effective_at, product_economics.effective_at),
  updated_by = EXCLUDED.updated_by,
  updated_at = now()
RETURNING id, workspace_id, wb_product_id, cost_price, logistics_cost, other_costs,
  tax_rate_percent, commission_percent, target_margin_percent, max_allowed_drr,
  source, effective_at, updated_by, created_at, updated_at`,
		arg.WorkspaceID, arg.WbProductID, arg.CostPrice, arg.LogisticsCost, arg.OtherCosts,
		arg.TaxRatePercent, arg.CommissionPercent, arg.TargetMarginPercent, arg.MaxAllowedDrr,
		arg.Source, arg.EffectiveAt, arg.UpdatedBy,
	)
	return scanProductEconomics(row)
}

func (q *Queries) ListProductEconomicsByWorkspace(ctx context.Context, workspaceID pgtype.UUID, limit, offset int32) ([]ProductEconomics, error) {
	rows, err := q.db.Query(ctx, `
SELECT id, workspace_id, wb_product_id, cost_price, logistics_cost, other_costs,
  tax_rate_percent, commission_percent, target_margin_percent, max_allowed_drr,
  source, effective_at, updated_by, created_at, updated_at
FROM product_economics
WHERE workspace_id = $1
ORDER BY updated_at DESC
LIMIT $2 OFFSET $3`, workspaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ProductEconomics{}
	for rows.Next() {
		item, scanErr := scanProductEconomics(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type productEconomicsScanner interface {
	Scan(dest ...any) error
}

func scanProductEconomics(row productEconomicsScanner) (ProductEconomics, error) {
	var item ProductEconomics
	err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.WbProductID,
		&item.CostPrice,
		&item.LogisticsCost,
		&item.OtherCosts,
		&item.TaxRatePercent,
		&item.CommissionPercent,
		&item.TargetMarginPercent,
		&item.MaxAllowedDrr,
		&item.Source,
		&item.EffectiveAt,
		&item.UpdatedBy,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	return item, err
}
