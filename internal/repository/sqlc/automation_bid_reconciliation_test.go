package sqlcgen

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnresolvedBidActionGuardIsScopeExactAndFailClosed(t *testing.T) {
	t.Parallel()
	require.Contains(t, hasUnresolvedAutomationBidAction, "product_id IS NULL OR $2::uuid IS NULL OR product_id = $2")
	require.Contains(t, hasUnresolvedAutomationBidAction, "placement = $3 OR placement IS NULL")
	require.Contains(t, hasUnresolvedAutomationBidAction, "status IN ('pending', 'unknown')")
	require.Contains(t, claimAutomationBidAction, "pg_advisory_xact_lock")
	require.Contains(t, claimAutomationBidAction, "unresolved.product_id IS NULL OR $6::uuid IS NULL OR unresolved.product_id = $6")
	require.Contains(t, claimAutomationBidAction, "unresolved.status IN ('pending', 'unknown')")
	require.Contains(t, claimAutomationBidAction, "ON CONFLICT DO NOTHING")
	require.Contains(t, claimAutomationBidAction, "RETURNING id")

	migration, err := os.ReadFile("../../../migrations/000045_automation_bid_reconciliation.up.sql")
	require.NoError(t, err)
	require.Contains(t, string(migration), "idx_wb_bid_actions_unresolved_automation")
	require.Contains(t, string(migration), "status IN ('pending', 'unknown')")
}

func TestReconciliationUpsertsCanonicalUserBidAudit(t *testing.T) {
	t.Parallel()
	require.Contains(t, upsertReconciledAutomationBidChange, "automation_action_id")
	require.Contains(t, upsertReconciledAutomationBidChange, "ON CONFLICT (automation_action_id)")
	require.Contains(t, upsertReconciledAutomationBidChange, "DO UPDATE SET wb_status")
	require.Contains(t, upsertReconciledAutomationBidChange, "action.status IN ('pending', 'unknown')")
}
