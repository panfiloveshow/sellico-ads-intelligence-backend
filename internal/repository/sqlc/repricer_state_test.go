package sqlcgen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnqueuePriceChangeSerializesAndPreservesSupersededAudit(t *testing.T) {
	t.Parallel()
	require.Contains(t, enqueuePriceChange, "pg_advisory_xact_lock")
	require.Contains(t, enqueuePriceChange, "UPDATE price_changes old_intent")
	require.Contains(t, enqueuePriceChange, "old_intent.wb_status = 'pending'")
	require.Contains(t, enqueuePriceChange, "RETURNING old_intent.id, old_intent.schedule_entry_id, old_intent.source")
	require.Contains(t, enqueuePriceChange, "INSERT INTO price_changes")
	require.Contains(t, enqueuePriceChange, "superseded_dependency")
	require.NotContains(t, enqueuePriceChange, "ON CONFLICT")
	require.NotContains(t, enqueuePriceChange, "schedule_entry_id = EXCLUDED")

	// Server-side type inference pins a parameter to the type of its FIRST use.
	// A bare $2::text/$5::text inside the advisory-lock key made every later
	// "seller_cabinet_id = $2" resolve as uuid = text (SQLSTATE 42883) and
	// broke all price-change enqueues in production. Keep the first use typed.
	require.Contains(t, enqueuePriceChange, "($2::uuid)::text")
	require.Contains(t, enqueuePriceChange, "($5::bigint)::text")
	require.NotContains(t, enqueuePriceChange, "hashtextextended($2::text")
}

func TestPendingClaimFreezesPayloadBeforeHTTP(t *testing.T) {
	t.Parallel()
	require.Contains(t, claimPendingPriceChanges, "FOR UPDATE SKIP LOCKED")
	require.Contains(t, claimPendingPriceChanges, "wb_status = 'submitting'")
	require.Contains(t, claimPendingPriceChanges, "active.wb_status IN ('submitting', 'submit_unknown', 'uploaded')")
	require.Contains(t, claimUnknownPriceChanges, "uncertain.wb_status = 'submit_unknown'")
	require.Contains(t, claimPendingPriceChanges, "submission_batch_id = $4")
	require.Contains(t, claimUnknownPriceChanges, "claimed.submission_batch_id = candidate_batch.submission_batch_id")
	require.Contains(t, claimPendingPriceChanges, "claimed.new_price_rub")
}

func TestPriceTaskChangesAndScheduleAreLinkedInOneStatement(t *testing.T) {
	t.Parallel()
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "WITH task AS")
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "UPDATE price_changes")
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "upload_task_id = (SELECT id FROM task)")
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "UPDATE price_schedule_entries schedule")
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "array_append")
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "wb_status = 'submitting'")
	require.NotContains(t, createPriceUploadTaskAndLinkChanges, "SET status = EXCLUDED.status")
}

func TestTerminalTaskUpdatesUseCAS(t *testing.T) {
	t.Parallel()
	require.Contains(t, finalizePriceUploadTask, "status IN ('uploaded', 'processing')")
	require.Contains(t, finalizePriceUploadTask, "workspace_id = $2")
	require.Contains(t, bumpPriceUploadTaskPoll, "poll_count = $3")
	require.Contains(t, updatePriceScheduleEntryStatus, "status NOT IN ('done', 'failed', 'canceled')")
}

func TestPartialTaskAssignsFailedGoodsWithoutAppliedIntermediateState(t *testing.T) {
	t.Parallel()
	require.Contains(t, finalizePartialPriceUploadTask, "WHEN wb_product_id = ANY($3::bigint[]) THEN 'failed'")
	require.Contains(t, finalizePartialPriceUploadTask, "ELSE 'applied'")
	require.Contains(t, finalizePartialPriceUploadTask, "status IN ('uploaded', 'processing', 'partial')")
}

func TestRecoveryIsWorkspaceScopedAndNeverResendsLinkedTask(t *testing.T) {
	t.Parallel()
	require.Contains(t, listClaimablePriceChangeCabinets, "pending.workspace_id = $1")
	require.Contains(t, listClaimablePriceChangeCabinets, "active.wb_status IN ('submitting', 'submit_unknown', 'uploaded')")
	require.Contains(t, claimUnknownPriceChanges, "submission_batch_id")
}

func TestSchedulesListNewestFirst(t *testing.T) {
	t.Parallel()
	require.Contains(t, listPriceScheduleEntriesByWorkspace, "ORDER BY scheduled_at DESC")
}

func TestAutoRevertPairAndRollbackCompletionAreAtomic(t *testing.T) {
	t.Parallel()
	require.Contains(t, createPriceSchedulePair, "WITH primary_entry AS")
	require.Contains(t, createPriceSchedulePair, "revert_entry AS")
	require.Contains(t, createPriceSchedulePair, "FROM primary_entry")
	require.Contains(t, finalizePriceUploadTask, "UPDATE price_changes original")
	require.Contains(t, finalizePartialPriceUploadTask, "child.wb_status = 'applied'")
}

func TestScheduleAggregationWaitsForAllTasksAndFailsOnAnyFailure(t *testing.T) {
	t.Parallel()
	require.Contains(t, aggregatePriceSchedules, "change.wb_status IN ('pending', 'submitting', 'submit_unknown', 'uploaded')")
	require.Contains(t, aggregatePriceSchedules, "task.status IN ('uploaded', 'processing')")
	require.Contains(t, aggregatePriceSchedules, "task.status IN ('partial', 'failed')")
	require.Contains(t, aggregatePriceSchedules, "WHEN decisions.all_terminal AND decisions.has_failure THEN 'failed'")
	require.Contains(t, aggregatePriceSchedules, "WHEN decisions.all_terminal THEN 'done'")
}
