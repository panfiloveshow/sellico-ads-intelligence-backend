package sqlcgen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveStrategyOwnershipQueriesSerializeAndTreatCampaignScopeAsWildcard(t *testing.T) {
	t.Parallel()
	require.Contains(t, strategyOwnershipAdvisoryLock, "pg_advisory_xact_lock")
	require.Contains(t, strategyOwnershipAdvisoryLock, "live-strategy-ownership")

	require.NotContains(t, liveStrategyOwnershipConflictForScope, "other.id <> target.id")
	require.Contains(t, liveStrategyOwnershipConflictForScope, "other.is_active = true")
	require.Contains(t, liveStrategyOwnershipConflictForScope, "automation_level")
	require.Contains(t, liveStrategyOwnershipConflictForScope, "$4::uuid IS NULL OR other_binding.product_id IS NULL")

	require.Contains(t, liveStrategyOwnershipConflictForBindings, "target_binding.campaign_id IS NOT NULL")
	require.Contains(t, liveStrategyOwnershipConflictForBindings, "other_binding.id <> target_binding.id")
	require.Contains(t, liveStrategyOwnershipConflictForBindings, "other.id = target.id")
	require.Contains(t, liveStrategyOwnershipConflictForBindings, "target_binding.product_id IS NULL")
	require.Contains(t, liveStrategyOwnershipConflictForBindings, "other_binding.product_id IS NULL")
	require.Contains(t, liveStrategyOwnershipConflictForBindings, "other_binding.product_id = target_binding.product_id")
}
