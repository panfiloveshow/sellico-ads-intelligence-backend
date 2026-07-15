package sqlcgen

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const strategyOwnershipAdvisoryLock = `SELECT pg_advisory_xact_lock(
	hashtextextended($1::text || ':live-strategy-ownership', 0)
)`

const liveStrategyOwnershipConflictForScope = `SELECT EXISTS (
	SELECT 1
	FROM strategies target
	JOIN strategies other
	  ON other.workspace_id = target.workspace_id
	 AND other.seller_cabinet_id = target.seller_cabinet_id
	JOIN strategy_bindings other_binding ON other_binding.strategy_id = other.id
	WHERE target.id = $2
	  AND target.workspace_id = $1
	  AND other.is_active = true
	  AND COALESCE(NULLIF((other.params->>'automation_level')::int, 0), 1) >= 3
	  AND other_binding.campaign_id = $3
	  AND ($4::uuid IS NULL OR other_binding.product_id IS NULL OR other_binding.product_id = $4)
)`

const liveStrategyOwnershipConflictForBindings = `SELECT EXISTS (
	SELECT 1
	FROM strategies target
	JOIN strategy_bindings target_binding ON target_binding.strategy_id = target.id
	JOIN strategies other
	  ON other.workspace_id = target.workspace_id
	 AND other.seller_cabinet_id = target.seller_cabinet_id
	JOIN strategy_bindings other_binding
	  ON other_binding.strategy_id = other.id
	 AND other_binding.campaign_id = target_binding.campaign_id
	WHERE target.id = $2
	  AND target.workspace_id = $1
	  AND target_binding.campaign_id IS NOT NULL
	  AND other_binding.id <> target_binding.id
	  AND (
		other.id = target.id
		OR (
			other.is_active = true
			AND COALESCE(NULLIF((other.params->>'automation_level')::int, 0), 1) >= 3
		)
	  )
	  AND (
		target_binding.product_id IS NULL
		OR other_binding.product_id IS NULL
		OR other_binding.product_id = target_binding.product_id
	  )
)`

type strategyOwnershipTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// BeginStrategyOwnershipTx serializes live strategy ownership mutations for a
// workspace. The lock is acquired in its own statement, so later READ COMMITTED
// statements see changes committed by a previous lock holder before checking.
func (q *Queries) BeginStrategyOwnershipTx(ctx context.Context, workspaceID pgtype.UUID) (*Queries, pgx.Tx, error) {
	beginner, ok := q.db.(strategyOwnershipTxBeginner)
	if !ok {
		return nil, nil, fmt.Errorf("database does not support strategy ownership transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, strategyOwnershipAdvisoryLock, workspaceID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return q.WithTx(tx), tx, nil
}

type HasLiveStrategyOwnershipConflictForScopeParams struct {
	WorkspaceID pgtype.UUID
	StrategyID  pgtype.UUID
	CampaignID  pgtype.UUID
	ProductID   pgtype.UUID
}

func (q *Queries) HasLiveStrategyOwnershipConflictForScope(ctx context.Context, arg HasLiveStrategyOwnershipConflictForScopeParams) (bool, error) {
	var conflict bool
	err := q.db.QueryRow(ctx, liveStrategyOwnershipConflictForScope,
		arg.WorkspaceID, arg.StrategyID, arg.CampaignID, arg.ProductID).Scan(&conflict)
	return conflict, err
}

type HasLiveStrategyOwnershipConflictForBindingsParams struct {
	WorkspaceID pgtype.UUID
	StrategyID  pgtype.UUID
}

func (q *Queries) HasLiveStrategyOwnershipConflictForBindings(ctx context.Context, arg HasLiveStrategyOwnershipConflictForBindingsParams) (bool, error) {
	var conflict bool
	err := q.db.QueryRow(ctx, liveStrategyOwnershipConflictForBindings,
		arg.WorkspaceID, arg.StrategyID).Scan(&conflict)
	return conflict, err
}
