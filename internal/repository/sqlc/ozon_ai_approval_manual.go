package sqlcgen

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ozonAILockPoolSlots struct {
	slots chan struct{}
	users int
}

var ozonAILockPools = struct {
	sync.Mutex
	entries map[*pgxpool.Pool]*ozonAILockPoolSlots
}{entries: make(map[*pgxpool.Pool]*ozonAILockPoolSlots)}

// Reserve at least one pool connection for durable callback writes even when
// different cabinets execute concurrently. Entries disappear after their last
// caller, so short-lived pools are not retained by this package.
func reserveOzonAILockSlot(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	ozonAILockPools.Lock()
	entry := ozonAILockPools.entries[pool]
	if entry == nil {
		entry = &ozonAILockPoolSlots{slots: make(chan struct{}, int(pool.Config().MaxConns)-1)}
		ozonAILockPools.entries[pool] = entry
	}
	entry.users++
	ozonAILockPools.Unlock()
	forget := func() {
		ozonAILockPools.Lock()
		defer ozonAILockPools.Unlock()
		entry.users--
		if entry.users == 0 {
			delete(ozonAILockPools.entries, pool)
		}
	}
	select {
	case entry.slots <- struct{}{}:
		return func() { <-entry.slots; forget() }, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// WithOzonAIExecutionLock serializes AI execution within one cabinet across
// workers and approval requests. The transaction holds only the advisory lock:
// callback writes use the original connection pool and commit independently.
// Otherwise an external success followed by rollback could resurrect a proposal
// and execute it a second time. Callers must use atomic decision transitions.
func (q *Queries) WithOzonAIExecutionLock(ctx context.Context, cabinetID pgtype.UUID, fn func(*Queries) error) error {
	pool, ok := q.db.(*pgxpool.Pool)
	if !ok || pool.Config().MaxConns < 2 {
		return fmt.Errorf("AI execution requires a connection pool with at least two connections for durable writes")
	}
	releaseSlot, err := reserveOzonAILockSlot(ctx, pool)
	if err != nil {
		return err
	}
	defer releaseSlot()
	var tx pgx.Tx
	for {
		var err error
		tx, err = pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin AI execution lock: %w", err)
		}
		var acquired bool
		err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('ozon-ai:' || $1::uuid::text, 0))`, cabinetID).Scan(&acquired)
		if err == nil && acquired {
			break
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rollbackErr := tx.Rollback(cleanupCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("acquire AI execution lock: %w", err)
		}
		if rollbackErr != nil {
			return fmt.Errorf("release AI execution lock attempt: %w", rollbackErr)
		}
		// A blocking advisory lock would let queued reviewers occupy every
		// pool connection while the executor needs another for durable writes.
		// Release the connection before waiting for the next attempt.
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// pgx closes the connection when rollback fails, releasing the lock.
		_ = tx.Rollback(cleanupCtx)
	}()
	return fn(q)
}

// A proposal belongs to the strategy that produced its run. Replacing an
// active strategy must not silently grant its authority to an older proposal.
func (q *Queries) GetAIDecisionOriginStrategy(ctx context.Context, decisionID, workspaceID pgtype.UUID) (pgtype.UUID, error) {
	var strategyID pgtype.UUID
	err := q.db.QueryRow(ctx, `SELECT r.strategy_id FROM ai_decisions d
	 JOIN ai_runs r ON r.id=d.run_id AND r.workspace_id=d.workspace_id
	 AND r.seller_cabinet_id=d.seller_cabinet_id
	 WHERE d.id=$1 AND d.workspace_id=$2`, decisionID, workspaceID).Scan(&strategyID)
	return strategyID, err
}

type TransitionAIDecisionParams struct {
	ID             pgtype.UUID
	WorkspaceID    pgtype.UUID
	ExpectedStatus string
	Status         string
	Error          pgtype.Text
	AppliedBy      pgtype.UUID
	// Proposal, when supplied, persists the actual value after fresh guardrails.
	Proposal []byte
}

// TransitionAIDecision claims or finalizes exactly the expected lifecycle state.
// A false result is a concurrent/stale request, never permission to call Ozon.
func (q *Queries) TransitionAIDecision(ctx context.Context, p TransitionAIDecisionParams) (bool, error) {
	tag, err := q.db.Exec(ctx, `UPDATE ai_decisions SET
	 status=$4, error=$5, applied_by=COALESCE($6,applied_by),
	 proposal=COALESCE($7::jsonb,proposal),
	 applied_at=CASE WHEN $4 IN ('applied','auto_applied') THEN now() ELSE applied_at END
	 WHERE id=$1 AND workspace_id=$2 AND status=$3`,
		p.ID, p.WorkspaceID, p.ExpectedStatus, p.Status, p.Error, p.AppliedBy, p.Proposal)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
