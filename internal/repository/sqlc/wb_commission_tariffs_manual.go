package sqlcgen

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type WBCommissionTariff struct {
	ID                  pgtype.UUID
	WorkspaceID         pgtype.UUID
	SellerCabinetID     pgtype.UUID
	ParentID            int64
	ParentName          string
	SubjectID           int64
	SubjectName         string
	KGVPBooking         pgtype.Float8
	KGVPPickup          pgtype.Float8
	KGVPSupplier        pgtype.Float8
	KGVPSupplierExpress pgtype.Float8
	KGVPMarketplace     pgtype.Float8
	PaidStorageKGVP     pgtype.Float8
	Source              string
	CapturedAt          pgtype.Timestamptz
	CreatedAt           pgtype.Timestamptz
	UpdatedAt           pgtype.Timestamptz
}

type UpsertWBCommissionTariffParams struct {
	WorkspaceID         pgtype.UUID
	SellerCabinetID     pgtype.UUID
	ParentID            int64
	ParentName          string
	SubjectID           int64
	SubjectName         string
	KGVPBooking         pgtype.Float8
	KGVPPickup          pgtype.Float8
	KGVPSupplier        pgtype.Float8
	KGVPSupplierExpress pgtype.Float8
	KGVPMarketplace     pgtype.Float8
	PaidStorageKGVP     pgtype.Float8
	Source              string
	CapturedAt          pgtype.Timestamptz
}

func (q *Queries) UpsertWBCommissionTariff(ctx context.Context, arg UpsertWBCommissionTariffParams) error {
	_, err := q.db.Exec(ctx, `
INSERT INTO wb_commission_tariffs (
  workspace_id, seller_cabinet_id, parent_id, parent_name, subject_id, subject_name,
  kgvp_booking, kgvp_pickup, kgvp_supplier, kgvp_supplier_express, kgvp_marketplace,
  paid_storage_kgvp, source, captured_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (seller_cabinet_id, subject_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  parent_id = EXCLUDED.parent_id,
  parent_name = EXCLUDED.parent_name,
  subject_name = EXCLUDED.subject_name,
  kgvp_booking = EXCLUDED.kgvp_booking,
  kgvp_pickup = EXCLUDED.kgvp_pickup,
  kgvp_supplier = EXCLUDED.kgvp_supplier,
  kgvp_supplier_express = EXCLUDED.kgvp_supplier_express,
  kgvp_marketplace = EXCLUDED.kgvp_marketplace,
  paid_storage_kgvp = EXCLUDED.paid_storage_kgvp,
  source = EXCLUDED.source,
  captured_at = EXCLUDED.captured_at,
  updated_at = now()`,
		arg.WorkspaceID, arg.SellerCabinetID, arg.ParentID, arg.ParentName, arg.SubjectID, arg.SubjectName,
		arg.KGVPBooking, arg.KGVPPickup, arg.KGVPSupplier, arg.KGVPSupplierExpress, arg.KGVPMarketplace,
		arg.PaidStorageKGVP, arg.Source, arg.CapturedAt)
	return err
}

func (q *Queries) ListWBCommissionTariffsByWorkspace(ctx context.Context, workspaceID pgtype.UUID, limit, offset int32) ([]WBCommissionTariff, error) {
	rows, err := q.db.Query(ctx, `
SELECT id, workspace_id, seller_cabinet_id, parent_id, parent_name, subject_id, subject_name,
  kgvp_booking, kgvp_pickup, kgvp_supplier, kgvp_supplier_express, kgvp_marketplace,
  paid_storage_kgvp, source, captured_at, created_at, updated_at
FROM wb_commission_tariffs
WHERE workspace_id = $1
ORDER BY captured_at DESC
LIMIT $2 OFFSET $3`, workspaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []WBCommissionTariff{}
	for rows.Next() {
		var item WBCommissionTariff
		if err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.SellerCabinetID,
			&item.ParentID,
			&item.ParentName,
			&item.SubjectID,
			&item.SubjectName,
			&item.KGVPBooking,
			&item.KGVPPickup,
			&item.KGVPSupplier,
			&item.KGVPSupplierExpress,
			&item.KGVPMarketplace,
			&item.PaidStorageKGVP,
			&item.Source,
			&item.CapturedAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
