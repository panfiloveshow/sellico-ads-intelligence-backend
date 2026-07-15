package sqlcgen

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSettingsMergeUsesSameLockBeforeReadAndUpdate(t *testing.T) {
	t.Parallel()
	require.Contains(t, workspaceSettingsUpdateAdvisoryLock, ":workspace-daily-bid-actions")
	require.Contains(t, updateWorkspaceSettings, ":workspace-daily-bid-actions")
	require.Contains(t, updateWorkspaceSettings, "pg_advisory_xact_lock")

	queries := &Queries{}
	_, _, err := queries.BeginWorkspaceSettingsUpdateTx(context.Background(), pgtype.UUID{})
	require.ErrorContains(t, err, "does not support workspace settings transactions")
}
