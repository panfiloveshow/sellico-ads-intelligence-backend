package sqlcgen

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
)

type WBPriceDispatchEvidence struct {
	ChangeID    string          `json:"change_id"`
	NmID        int64           `json:"nm_id"`
	OldPriceRub int64           `json:"old_price_rub"`
	OldDiscount int32           `json:"old_discount"`
	NewPriceRub int64           `json:"new_price_rub"`
	NewDiscount int32           `json:"new_discount"`
	Snapshot    json.RawMessage `json:"snapshot"`
}

// The committed marker is one-use. All commands must still belong to the
// same physical claim and match the immutable payload observed under its hold.
const beginWBPriceDispatch = `
WITH supplied AS MATERIALIZED (
 SELECT * FROM jsonb_to_recordset($4::jsonb) AS v(change_id uuid,nm_id bigint,
   old_price_rub bigint,old_discount integer,new_price_rub bigint,new_discount integer,snapshot jsonb)
), claimed AS MATERIALIZED (
 SELECT id,wb_product_id,old_price_rub,old_discount_percent,new_price_rub,new_discount_percent
 FROM price_changes WHERE id IN (SELECT change_id FROM supplied)
   AND workspace_id=$1 AND seller_cabinet_id=$2 AND submission_batch_id=$3
   AND wb_status='submitting' AND upload_task_id IS NULL
   AND decision_context->>'wb_write_protocol'='observed_v1'
   AND NOT (decision_context ? 'wb_dispatch') AND updated_at>now()-interval '30 minutes'
   AND (NOT (decision_context ? 'manager_approval') OR
     COALESCE((decision_context#>>'{manager_approval,expires_at}')::timestamptz > statement_timestamp(),false))
 FOR UPDATE
), ready AS (
 SELECT 1 WHERE (SELECT count(*) FROM claimed)=(SELECT count(*) FROM supplied)
   AND EXISTS(SELECT 1 FROM supplied)
   AND (SELECT count(DISTINCT change_id) FROM supplied)=(SELECT count(*) FROM supplied)
   AND NOT EXISTS (
     SELECT 1 FROM supplied v LEFT JOIN claimed c ON c.id=v.change_id
     WHERE c.id IS NULL OR c.wb_product_id<>v.nm_id
       OR c.old_price_rub<>v.old_price_rub OR c.old_discount_percent<>v.old_discount
       OR c.new_price_rub<>v.new_price_rub OR c.new_discount_percent<>v.new_discount
       OR jsonb_typeof(v.snapshot) IS DISTINCT FROM 'object'
   )
), dispatched AS (
 UPDATE price_changes c SET updated_at=now(),decision_context=c.decision_context || jsonb_build_object(
   'wb_preflight',v.snapshot,'wb_preflight_observed_at',now(),
   'wb_dispatch',jsonb_build_object('started_at',now(),'batch_id',c.submission_batch_id,'account_key',$5::text)
 ) FROM supplied v
 WHERE c.id=v.change_id AND c.id IN (SELECT id FROM claimed) AND EXISTS(SELECT 1 FROM ready)
 RETURNING c.id
)
SELECT EXISTS(SELECT 1 FROM ready) AND (SELECT count(*) FROM dispatched)=(SELECT count(*) FROM supplied)
`

func (q *Queries) BeginWBPriceDispatch(ctx context.Context, workspaceID, cabinetID, batchID pgtype.UUID, evidence []WBPriceDispatchEvidence, account string) (bool, error) {
	if len(evidence) == 0 || len(evidence) > 1000 {
		return false, ErrPriceExecutionControlChanged
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return false, err
	}
	tx, err := q.ozonPriceTx(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	if err = acquirePriceExecutionAccount(ctx, tx, account); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT p.decision_context FROM price_changes p
 JOIN jsonb_to_recordset($4::jsonb) AS v(change_id uuid) ON p.id=v.change_id
 WHERE p.workspace_id=$1 AND p.seller_cabinet_id=$2 AND p.submission_batch_id=$3 FOR UPDATE OF p`, workspaceID, cabinetID, batchID, payload)
	if err != nil {
		return false, err
	}
	var decisions [][]byte
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return false, err
		}
		decisions = append(decisions, raw)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(decisions) != len(evidence) {
		return false, ErrPriceExecutionControlChanged
	}
	if err = q.WithTx(tx).checkPriceExecutionDecisions(ctx, account, "wb", decisions); err != nil {
		return false, err
	}
	var changed bool
	err = tx.QueryRow(ctx, beginWBPriceDispatch, workspaceID, cabinetID, batchID, payload, account).Scan(&changed)
	if err != nil || !changed {
		return changed, err
	}
	return true, tx.Commit(ctx)
}

func (q *Queries) FailUnsentWBPriceChanges(ctx context.Context, workspaceID, batchID pgtype.UUID, ids []pgtype.UUID, reason string) error {
	_, err := q.db.Exec(ctx, `UPDATE price_changes SET wb_status='failed',error=$4,updated_at=now()
WHERE workspace_id=$1 AND submission_batch_id=$2 AND id=ANY($3::uuid[]) AND wb_status='submitting'
  AND upload_task_id IS NULL AND decision_context->>'wb_write_protocol'='observed_v1'
  AND NOT (decision_context ? 'wb_dispatch')`, workspaceID, batchID, ids, reason)
	return err
}

// ListWBPriceTaskChanges includes historical links so a repeated native task
// can be scoped without treating its old applied rows as a new observation.
func (q *Queries) ListWBPriceTaskChanges(ctx context.Context, taskID, workspaceID pgtype.UUID) ([]PriceChange, error) {
	rows, err := q.db.Query(ctx, `SELECT `+priceChangeColumns+` FROM price_changes
WHERE upload_task_id=$1 AND workspace_id=$2 AND seller_cabinet_id=(
  SELECT seller_cabinet_id FROM price_upload_tasks WHERE id=$1 AND workspace_id=$2
) ORDER BY created_at,id`, taskID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changes []PriceChange
	for rows.Next() {
		row, err := scanPriceChange(rows)
		if err != nil {
			return nil, err
		}
		changes = append(changes, row)
	}
	return changes, rows.Err()
}

type WBPriceVerification struct {
	ChangeID string          `json:"change_id"`
	NmID     int64           `json:"nm_id"`
	PriceRub int64           `json:"price_rub"`
	Discount int32           `json:"discount"`
	Status   string          `json:"status"`
	Reason   string          `json:"reason"`
	Proof    json.RawMessage `json:"proof"`
}

// One transaction compares the exact task revision and complete active set.
// A late poll cannot finalize a newly linked command or overwrite its proof.
const finalizeObservedWBPriceTask = `
WITH task_scope AS MATERIALIZED (
 SELECT id FROM price_upload_tasks
 WHERE id=$1 AND workspace_id=$2 AND updated_at=$3
   AND status IN ('uploaded','processing')
 FOR UPDATE
), active AS MATERIALIZED (
 SELECT id,wb_product_id,new_price_rub,new_discount_percent
 FROM price_changes
 WHERE upload_task_id IN (SELECT id FROM task_scope) AND workspace_id=$2
   AND wb_status IN ('submitting','uploaded')
 FOR UPDATE
), supplied AS MATERIALIZED (
 SELECT * FROM jsonb_to_recordset($4::jsonb) AS v(
   change_id uuid,nm_id bigint,price_rub bigint,discount integer,status text,reason text,proof jsonb
 )
), ready AS (
 SELECT 1 WHERE EXISTS(SELECT 1 FROM active)
   AND (SELECT count(*) FROM active)=(SELECT count(*) FROM supplied)
   AND (SELECT count(DISTINCT change_id) FROM supplied)=(SELECT count(*) FROM supplied)
   AND $5::text IN ('applied','partial','failed')
   AND NOT EXISTS (
     SELECT 1 FROM supplied v LEFT JOIN active a ON a.id=v.change_id
     WHERE a.id IS NULL OR a.wb_product_id<>v.nm_id OR a.new_price_rub<>v.price_rub
       OR a.new_discount_percent<>v.discount OR v.status NOT IN ('applied','failed')
       OR jsonb_typeof(v.proof) IS DISTINCT FROM 'object'
   )
), finalized AS (
 UPDATE price_changes c
 SET wb_status=v.status,error=NULLIF(v.reason,''),updated_at=now(),
     decision_context=jsonb_set(COALESCE(NULLIF(c.decision_context,'null'::jsonb),'{}'::jsonb),'{wb_verification}',v.proof,true)
 FROM supplied v
 WHERE c.id=v.change_id AND c.id IN (SELECT id FROM active) AND EXISTS(SELECT 1 FROM ready)
 RETURNING c.rollback_of,c.wb_status
), rolled_back AS (
 UPDATE price_changes original SET wb_status='rolled_back',updated_at=now()
 FROM finalized child
 WHERE original.id=child.rollback_of AND child.wb_status='applied'
 RETURNING original.id
), terminal_task AS (
 UPDATE price_upload_tasks SET status=$5,completed_at=now(),last_polled_at=now(),updated_at=now(),
   error=CASE $5 WHEN 'partial' THEN 'partial errors' WHEN 'failed' THEN 'rejected by WB' ELSE NULL END
 WHERE id IN (SELECT id FROM task_scope) AND EXISTS(SELECT 1 FROM ready)
   AND (SELECT count(*) FROM finalized)=(SELECT count(*) FROM active)
 RETURNING id
)
SELECT EXISTS(SELECT 1 FROM terminal_task)
`

func (q *Queries) FinalizeObservedWBPriceTask(ctx context.Context, task PriceUploadTask, workspaceID pgtype.UUID, status string, evidence []WBPriceVerification) (bool, error) {
	if len(evidence) == 0 {
		return false, errors.New("WB price evidence is empty")
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		return false, err
	}
	var changed bool
	err = q.db.QueryRow(ctx, finalizeObservedWBPriceTask, task.ID, workspaceID, task.UpdatedAt, payload, status).Scan(&changed)
	return changed, err
}
