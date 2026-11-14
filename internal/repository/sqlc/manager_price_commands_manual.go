package sqlcgen

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrManagerPriceCommandConflict = errors.New("manager price operation scope or immutable payload differs")
var ErrManagerPriceCommandHeld = errors.New("manager price operation could not acquire its physical article")

type ManagerPriceCommand struct {
	OperationID, WorkspaceID, SellerCabinetID, ActorID pgtype.UUID
	Marketplace, Fingerprint                           string
	Approval                                           []byte
	NativeChangeID                                     pgtype.UUID
	ExpiresAt                                          time.Time
}

func scanManagerPriceCommand(row pgx.Row) (ManagerPriceCommand, error) {
	var v ManagerPriceCommand
	err := row.Scan(&v.OperationID, &v.WorkspaceID, &v.SellerCabinetID, &v.ActorID, &v.Marketplace, &v.Fingerprint, &v.Approval, &v.NativeChangeID, &v.ExpiresAt)
	return v, err
}

const managerPriceColumns = `operation_id,workspace_id,seller_cabinet_id,actor_id,marketplace,fingerprint,approval,native_change_id,expires_at`

func (q *Queries) GetManagerPriceCommand(ctx context.Context, operationID, workspaceID, actorID pgtype.UUID) (ManagerPriceCommand, error) {
	return scanManagerPriceCommand(q.db.QueryRow(ctx, `SELECT `+managerPriceColumns+` FROM manager_price_commands WHERE operation_id=$1 AND workspace_id=$2 AND actor_id=$3`, operationID, workspaceID, actorID))
}

func (q *Queries) GetPendingWBPriceTaskForChange(ctx context.Context, workspaceID, changeID pgtype.UUID) (PriceUploadTask, error) {
	return scanPriceUploadTask(q.db.QueryRow(ctx, `SELECT t.id,t.workspace_id,t.seller_cabinet_id,t.wb_task_id,t.status,t.items_count,t.poll_count,t.last_polled_at,t.completed_at,t.error,t.created_at,t.updated_at
FROM price_upload_tasks t JOIN price_changes p ON p.upload_task_id=t.id
WHERE p.id=$2 AND p.workspace_id=$1 AND t.workspace_id=$1 AND t.seller_cabinet_id=p.seller_cabinet_id AND t.status IN ('uploaded','processing')`, workspaceID, changeID))
}

// Receipt recovery fails only an expired command with proof that transport
// never began. It never replays a command or releases an uncertain dispatch.
func (q *Queries) ExpireUnsentManagerPriceCommand(ctx context.Context, v ManagerPriceCommand) error {
	if v.Marketplace == "wb" {
		_, err := q.db.Exec(ctx, `UPDATE price_changes p SET wb_status='failed',error='manager approval expired before dispatch',updated_at=now()
WHERE p.id=$1 AND p.workspace_id=$2 AND p.seller_cabinet_id=$3 AND p.wb_status='submitting'
 AND p.upload_task_id IS NULL AND NOT(p.decision_context ? 'wb_dispatch')
 AND EXISTS(SELECT 1 FROM manager_price_commands m WHERE m.operation_id=$4 AND m.native_change_id=p.id
   AND m.workspace_id=$2 AND m.actor_id=$5 AND m.marketplace='wb' AND m.expires_at<=statement_timestamp())`,
			v.NativeChangeID, v.WorkspaceID, v.SellerCabinetID, v.OperationID, v.ActorID)
		return err
	}
	if v.Marketplace == "ozon" {
		_, err := q.db.Exec(ctx, `UPDATE ozon_price_changes p SET status='failed',error='manager approval expired before dispatch'
WHERE p.id=$1 AND p.seller_cabinet_id=$2 AND p.status='pending'
 AND NOT EXISTS(SELECT 1 FROM ozon_price_write_attempts a WHERE a.change_id=p.id)
 AND EXISTS(SELECT 1 FROM manager_price_commands m WHERE m.operation_id=$3 AND m.native_change_id=p.id
   AND m.workspace_id=$4 AND m.actor_id=$5 AND m.marketplace='ozon' AND m.expires_at<=statement_timestamp())`,
			v.NativeChangeID, v.SellerCabinetID, v.OperationID, v.WorkspaceID, v.ActorID)
		return err
	}
	return ErrManagerPriceCommandConflict
}

func (q *Queries) reserveManagerPriceCommand(ctx context.Context, v ManagerPriceCommand) (ManagerPriceCommand, bool, error) {
	tag, err := q.db.Exec(ctx, `INSERT INTO manager_price_commands(operation_id,workspace_id,seller_cabinet_id,actor_id,marketplace,fingerprint,approval,expires_at)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(operation_id) DO NOTHING`,
		v.OperationID, v.WorkspaceID, v.SellerCabinetID, v.ActorID, v.Marketplace, v.Fingerprint, v.Approval, v.ExpiresAt)
	if err != nil {
		return ManagerPriceCommand{}, false, err
	}
	stored, err := scanManagerPriceCommand(q.db.QueryRow(ctx, `SELECT `+managerPriceColumns+` FROM manager_price_commands WHERE operation_id=$1 FOR UPDATE`, v.OperationID))
	if err != nil {
		return stored, false, err
	}
	if stored.WorkspaceID != v.WorkspaceID || stored.ActorID != v.ActorID || stored.SellerCabinetID != v.SellerCabinetID || stored.Marketplace != v.Marketplace || stored.Fingerprint != v.Fingerprint {
		return stored, false, ErrManagerPriceCommandConflict
	}
	return stored, tag.RowsAffected() == 1, nil
}

// The registry and native intent commit together. A repeated operation never
// creates a second native intent or asks any dispatcher to replay it.
func (q *Queries) CreateManagerWBPriceCommand(ctx context.Context, v ManagerPriceCommand, intent CreatePriceChangeParams, batchID pgtype.UUID) (ManagerPriceCommand, []PriceChange, bool, error) {
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return v, nil, false, err
	}
	defer tx.Rollback(context.Background())
	qt := q.WithTx(tx)
	stored, created, err := qt.reserveManagerPriceCommand(ctx, v)
	if err != nil {
		return stored, nil, false, err
	}
	if !created {
		return stored, nil, false, tx.Commit(ctx)
	}
	row, err := qt.CreatePriceChange(ctx, intent)
	if err != nil {
		return stored, nil, false, err
	}
	// Claim only this intent, under the same physical hold used by all writers.
	claimed, err := qt.ClaimSelectedPendingPriceChanges(ctx, v.WorkspaceID, v.SellerCabinetID, batchID, []pgtype.UUID{row.ID})
	if err != nil {
		return stored, nil, false, err
	}
	if len(claimed) != 1 {
		return stored, nil, false, ErrManagerPriceCommandHeld
	}
	_, err = tx.Exec(ctx, `UPDATE manager_price_commands SET native_change_id=$2 WHERE operation_id=$1`, v.OperationID, row.ID)
	if err != nil {
		return stored, nil, false, err
	}
	stored.NativeChangeID = row.ID
	return stored, claimed, true, tx.Commit(ctx)
}

func (q *Queries) CreateManagerOzonPriceCommand(ctx context.Context, v ManagerPriceCommand, intent InsertOzonPriceChangeParams) (ManagerPriceCommand, bool, error) {
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return v, false, err
	}
	defer tx.Rollback(context.Background())
	qt := q.WithTx(tx)
	stored, created, err := qt.reserveManagerPriceCommand(ctx, v)
	if err != nil {
		return stored, false, err
	}
	if !created {
		return stored, false, tx.Commit(ctx)
	}
	row, err := qt.InsertOzonPriceChange(ctx, intent)
	if err != nil {
		return stored, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE manager_price_commands SET native_change_id=$2 WHERE operation_id=$1`, v.OperationID, row.ID)
	if err != nil {
		return stored, false, err
	}
	stored.NativeChangeID = row.ID
	return stored, true, tx.Commit(ctx)
}
