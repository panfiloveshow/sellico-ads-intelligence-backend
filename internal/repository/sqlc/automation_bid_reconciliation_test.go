package sqlcgen

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnresolvedBidActionGuardIsScopeExactAndFailClosed(t *testing.T) {
	t.Parallel()
	require.Contains(t, hasUnresolvedAutomationBidAction, "product_id IS NULL OR $2::uuid IS NULL OR product_id = $2")
	require.Contains(t, hasUnresolvedAutomationBidAction, "placement = $3 OR placement IS NULL")
	require.Contains(t, hasUnresolvedAutomationBidAction, "norm_query IS NULL OR $4::text IS NULL OR norm_query = $4")
	require.Contains(t, hasUnresolvedAutomationBidAction, "status IN ('pending', 'unknown')")
	require.Contains(t, claimAutomationBidAction, "pg_advisory_xact_lock")
	require.Contains(t, claimAutomationBidAction, "unresolved.product_id IS NULL OR $6::uuid IS NULL OR unresolved.product_id = $6")
	require.Contains(t, claimAutomationBidAction, "unresolved.norm_query IS NULL OR $19::text IS NULL OR unresolved.norm_query = $19")
	require.Contains(t, claimAutomationBidAction, "unresolved.status IN ('pending', 'unknown')")
	require.Contains(t, claimAutomationBidAction, ":workspace-daily-bid-actions")
	require.Contains(t, claimAutomationBidAction, "counted.status IN ('pending', 'unknown', 'applied')")
	require.Contains(t, claimAutomationBidAction, "w.settings #>> '{automation,max_bid_changes_per_day}'")
	require.Contains(t, claimAutomationBidAction, "LEAST(configured_cap, observed_cap)")
	require.Contains(t, claimAutomationBidAction, "daily_action_count >= effective_cap")
	require.Contains(t, claimAutomationBidAction, "workspace max_bid_changes_per_day reached")
	require.Contains(t, claimAutomationBidAction, "ON CONFLICT DO NOTHING")
	require.Contains(t, claimAutomationBidAction, "RETURNING id, status")

	migration, err := os.ReadFile("../../../migrations/000045_automation_bid_reconciliation.up.sql")
	require.NoError(t, err)
	require.Contains(t, string(migration), "idx_wb_bid_actions_unresolved_automation")
	require.Contains(t, string(migration), "status IN ('pending', 'unknown')")
}

func TestPreWriteGuardUsesCurrentLoweredWorkspaceCap(t *testing.T) {
	t.Parallel()
	require.Contains(t, automationBidActionPreWriteGuard, ":workspace-daily-bid-actions")
	require.Contains(t, automationBidActionPreWriteGuard, "w.settings #>> '{automation,max_bid_changes_per_day}'")
	require.Contains(t, automationBidActionPreWriteGuard, "LEAST(")
	require.Contains(t, automationBidActionPreWriteGuard, "daily_action_count > effective_cap")
	require.Contains(t, automationBidActionPreWriteGuard, "was lowered below claimed actions")
	require.Contains(t, updateWorkspaceSettings, ":workspace-daily-bid-actions")

	queries := &Queries{}
	_, _, err := queries.BeginAutomationBidWriteLease(context.Background(), AutomationBidActionPreWriteGuardParams{})
	require.ErrorContains(t, err, "does not support automation bid write transactions")
}

func TestReconciliationUpsertsCanonicalUserBidAudit(t *testing.T) {
	t.Parallel()
	require.Contains(t, upsertReconciledAutomationBidChange, "automation_action_id")
	require.Contains(t, upsertReconciledAutomationBidChange, "ON CONFLICT (automation_action_id)")
	require.Contains(t, upsertReconciledAutomationBidChange, "DO UPDATE SET wb_status")
	require.Contains(t, upsertReconciledAutomationBidChange, "action.status IN ('pending', 'unknown')")
	require.Contains(t, upsertReconciledAutomationBidChange, "action.phrase_id")
}

func TestPhraseBidObservationTimeOnlyAdvancesWithRealBid(t *testing.T) {
	t.Parallel()
	require.Contains(t, upsertPhrase, "WHEN EXCLUDED.current_bid IS NOT NULL THEN now()")
	require.Contains(t, upsertPhrase, "ELSE phrases.current_bid_observed_at")
	require.Contains(t, createPhrase, "CASE WHEN $9::bigint IS NULL THEN NULL ELSE now() END")
}
