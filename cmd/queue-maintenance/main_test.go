package main

import (
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
)

func TestIsStaleRecurringSweepOnlyMatchesPayloadlessSchedulerTriggers(t *testing.T) {
	require.True(t, isStaleRecurringSweep(&asynq.TaskInfo{Type: worker.TaskSweepSyncWorkspace}))
	require.True(t, isStaleRecurringSweep(&asynq.TaskInfo{Type: worker.TaskOzonCPOOrdersSync}))
	require.False(t, isStaleRecurringSweep(&asynq.TaskInfo{
		Type:    worker.TaskOzonCPOOrdersSync,
		Payload: []byte(`{"cabinet_id":"test"}`),
	}))
	require.False(t, isStaleRecurringSweep(&asynq.TaskInfo{Type: worker.TaskSyncWorkspace}))
	require.False(t, isStaleRecurringSweep(nil))
}
