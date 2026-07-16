package sqlcgen

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type StrategyBindingRolloutRow struct {
	BindingID, WorkspaceID, StrategyID pgtype.UUID
	DesiredMode, State, HoldReason     string
	LastBlockCode, LastBlockDetail     string
	ValidationStartedAt, LiveEnabledAt pgtype.Timestamptz
	UpdatedAt                          pgtype.Timestamptz
}

const rolloutColumns = `binding_id, workspace_id, strategy_id, desired_mode, state, COALESCE(hold_reason,''),
 COALESCE(last_block_code,''), COALESCE(last_block_detail,''), validation_started_at, live_enabled_at, updated_at`

func scanStrategyBindingRollout(row pgx.Row) (StrategyBindingRolloutRow, error) {
	var item StrategyBindingRolloutRow
	err := row.Scan(&item.BindingID, &item.WorkspaceID, &item.StrategyID, &item.DesiredMode, &item.State, &item.HoldReason,
		&item.LastBlockCode, &item.LastBlockDetail,
		&item.ValidationStartedAt, &item.LiveEnabledAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) EnsureStrategyBindingRollout(ctx context.Context, workspaceID, strategyID, bindingID pgtype.UUID) (StrategyBindingRolloutRow, error) {
	return scanStrategyBindingRollout(q.db.QueryRow(ctx, `
INSERT INTO strategy_binding_rollouts (binding_id, workspace_id, strategy_id, desired_mode)
SELECT b.id, s.workspace_id, s.id,
 CASE WHEN s.is_active AND COALESCE(NULLIF((s.params->>'automation_level')::int,0),1)>=3 THEN 'live' ELSE 'shadow' END
FROM strategy_bindings b JOIN strategies s ON s.id=b.strategy_id
WHERE b.id=$3 AND s.id=$2 AND s.workspace_id=$1
ON CONFLICT (binding_id) DO UPDATE SET updated_at=strategy_binding_rollouts.updated_at
RETURNING `+rolloutColumns, workspaceID, strategyID, bindingID))
}

func (q *Queries) GetStrategyBindingRollout(ctx context.Context, workspaceID, strategyID, bindingID pgtype.UUID) (StrategyBindingRolloutRow, error) {
	return scanStrategyBindingRollout(q.db.QueryRow(ctx, `SELECT `+rolloutColumns+`
FROM strategy_binding_rollouts WHERE workspace_id=$1 AND strategy_id=$2 AND binding_id=$3`, workspaceID, strategyID, bindingID))
}

func (q *Queries) ListStrategyBindingRollouts(ctx context.Context, workspaceID, strategyID pgtype.UUID) ([]StrategyBindingRolloutRow, error) {
	rows, err := q.db.Query(ctx, `SELECT `+rolloutColumns+` FROM strategy_binding_rollouts
WHERE workspace_id=$1 AND strategy_id=$2 ORDER BY created_at, binding_id`, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]StrategyBindingRolloutRow, 0)
	for rows.Next() {
		item, scanErr := scanStrategyBindingRollout(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type UpdateStrategyBindingRolloutParams struct {
	WorkspaceID, StrategyID, BindingID, UpdatedBy pgtype.UUID
	DesiredMode, State, HoldReason                string
}

func (q *Queries) UpdateStrategyBindingRollout(ctx context.Context, arg UpdateStrategyBindingRolloutParams) (StrategyBindingRolloutRow, error) {
	return scanStrategyBindingRollout(q.db.QueryRow(ctx, `UPDATE strategy_binding_rollouts SET
 desired_mode=$4, state=$5, hold_reason=NULLIF($6,''), updated_by=$7,
	 last_block_code=NULL, last_block_detail=NULL,
 live_enabled_at=CASE WHEN $5='live' THEN COALESCE(live_enabled_at,now()) ELSE live_enabled_at END,
 validation_started_at=CASE WHEN $5='shadow_validating' AND state<>$5 THEN now() ELSE validation_started_at END,
	 updated_at=now()
WHERE workspace_id=$1 AND strategy_id=$2 AND binding_id=$3 RETURNING `+rolloutColumns,
		arg.WorkspaceID, arg.StrategyID, arg.BindingID, arg.DesiredMode, arg.State, arg.HoldReason, arg.UpdatedBy))
}

type SetStrategyBindingRolloutSystemStateParams struct {
	BindingID                     pgtype.UUID
	State, BlockCode, BlockDetail string
}

func (q *Queries) SetStrategyBindingRolloutSystemState(ctx context.Context, arg SetStrategyBindingRolloutSystemStateParams) error {
	_, err := q.db.Exec(ctx, `UPDATE strategy_binding_rollouts SET state=$2,
 last_block_code=NULLIF($3,''), last_block_detail=NULLIF($4,''),
 live_enabled_at=CASE WHEN $2='live' THEN COALESCE(live_enabled_at,now()) ELSE live_enabled_at END,
 validation_started_at=CASE WHEN $2='shadow_validating' AND state<>$2 THEN now() ELSE validation_started_at END,
 updated_at=now() WHERE binding_id=$1 AND state<>'manual_hold'`, arg.BindingID, arg.State, arg.BlockCode, arg.BlockDetail)
	return err
}

type StrategyEvaluationRunRow struct {
	ID, WorkspaceID, SellerCabinetID, StrategyID, StrategyBindingID pgtype.UUID
	CampaignID, ProductID                                           pgtype.UUID
	AutomationLevel                                                 int32
	RolloutState                                                    string
	ApplyRequested                                                  bool
	Outcome, ReasonCode, ReasonDetail                               string
	ProposedActions, AppliedActions                                 int32
	StartedAt, FinishedAt                                           pgtype.Timestamptz
}

const evaluationRunColumns = `id, workspace_id, seller_cabinet_id, strategy_id, strategy_binding_id,
 campaign_id, product_id, automation_level, rollout_state, apply_requested, outcome, reason_code,
 COALESCE(reason_detail,''), proposed_actions, applied_actions, started_at, finished_at`

func scanStrategyEvaluationRun(row pgx.Row) (StrategyEvaluationRunRow, error) {
	var item StrategyEvaluationRunRow
	err := row.Scan(&item.ID, &item.WorkspaceID, &item.SellerCabinetID, &item.StrategyID, &item.StrategyBindingID,
		&item.CampaignID, &item.ProductID, &item.AutomationLevel, &item.RolloutState, &item.ApplyRequested,
		&item.Outcome, &item.ReasonCode, &item.ReasonDetail, &item.ProposedActions, &item.AppliedActions,
		&item.StartedAt, &item.FinishedAt)
	return item, err
}

type CreateStrategyEvaluationRunParams struct {
	WorkspaceID, SellerCabinetID, StrategyID, StrategyBindingID pgtype.UUID
	CampaignID, ProductID                                       pgtype.UUID
	AutomationLevel                                             int32
	RolloutState                                                string
	ApplyRequested                                              bool
}

func (q *Queries) CreateStrategyEvaluationRun(ctx context.Context, arg CreateStrategyEvaluationRunParams) (StrategyEvaluationRunRow, error) {
	return scanStrategyEvaluationRun(q.db.QueryRow(ctx, `INSERT INTO strategy_evaluation_runs
(workspace_id,seller_cabinet_id,strategy_id,strategy_binding_id,campaign_id,product_id,automation_level,rollout_state,apply_requested)
SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9
WHERE EXISTS (SELECT 1 FROM strategy_binding_rollouts r WHERE r.binding_id=$4 AND r.workspace_id=$1 AND r.strategy_id=$3)
RETURNING `+evaluationRunColumns, arg.WorkspaceID, arg.SellerCabinetID, arg.StrategyID, arg.StrategyBindingID,
		arg.CampaignID, arg.ProductID, arg.AutomationLevel, arg.RolloutState, arg.ApplyRequested))
}

type CompleteStrategyEvaluationRunParams struct {
	ID                                pgtype.UUID
	Outcome, ReasonCode, ReasonDetail string
	ProposedActions, AppliedActions   int32
}

func (q *Queries) CompleteStrategyEvaluationRun(ctx context.Context, arg CompleteStrategyEvaluationRunParams) error {
	_, err := q.db.Exec(ctx, `UPDATE strategy_evaluation_runs SET outcome=$2, reason_code=$3,
 reason_detail=NULLIF($4,''), proposed_actions=$5, applied_actions=$6, finished_at=now(), updated_at=now()
WHERE id=$1 AND outcome='evaluating'`, arg.ID, arg.Outcome, arg.ReasonCode, arg.ReasonDetail, arg.ProposedActions, arg.AppliedActions)
	return err
}

type StrategyEvaluationFactRow struct {
	ID, RunID                     pgtype.UUID
	Code, Status, Outcome, Source string
	Value                         []byte
	ObservedAt                    pgtype.Timestamptz
}

type CreateStrategyEvaluationFactParams struct {
	RunID                         pgtype.UUID
	Code, Status, Outcome, Source string
	Value                         []byte
}

func scanStrategyEvaluationFact(row pgx.Row) (StrategyEvaluationFactRow, error) {
	var item StrategyEvaluationFactRow
	err := row.Scan(&item.ID, &item.RunID, &item.Code, &item.Status, &item.Outcome, &item.Source, &item.Value, &item.ObservedAt)
	return item, err
}

func (q *Queries) CreateStrategyEvaluationFact(ctx context.Context, arg CreateStrategyEvaluationFactParams) (StrategyEvaluationFactRow, error) {
	return scanStrategyEvaluationFact(q.db.QueryRow(ctx, `INSERT INTO strategy_evaluation_facts
(run_id,code,status,outcome,source,value) VALUES($1,$2,$3,$4,$5,$6)
RETURNING id,run_id,code,status,outcome,source,value,observed_at`, arg.RunID, arg.Code, arg.Status, arg.Outcome, arg.Source, arg.Value))
}

func (q *Queries) ListStrategyEvaluationFacts(ctx context.Context, runID pgtype.UUID) ([]StrategyEvaluationFactRow, error) {
	rows, err := q.db.Query(ctx, `SELECT id,run_id,code,status,outcome,source,value,observed_at
FROM strategy_evaluation_facts WHERE run_id=$1 ORDER BY observed_at,id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]StrategyEvaluationFactRow, 0)
	for rows.Next() {
		item, scanErr := scanStrategyEvaluationFact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (q *Queries) ListStrategyEvaluationRuns(ctx context.Context, workspaceID, strategyID pgtype.UUID, limit, offset int32) ([]StrategyEvaluationRunRow, error) {
	rows, err := q.db.Query(ctx, `SELECT `+evaluationRunColumns+` FROM strategy_evaluation_runs
WHERE workspace_id=$1 AND strategy_id=$2 ORDER BY started_at DESC,id DESC LIMIT $3 OFFSET $4`, workspaceID, strategyID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]StrategyEvaluationRunRow, 0)
	for rows.Next() {
		item, scanErr := scanStrategyEvaluationRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (q *Queries) GetStrategyEvaluationRun(ctx context.Context, workspaceID, strategyID, runID pgtype.UUID) (StrategyEvaluationRunRow, error) {
	item, err := scanStrategyEvaluationRun(q.db.QueryRow(ctx, `SELECT `+evaluationRunColumns+` FROM strategy_evaluation_runs
WHERE workspace_id=$1 AND strategy_id=$2 AND id=$3`, workspaceID, strategyID, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return item, pgx.ErrNoRows
	}
	return item, err
}
