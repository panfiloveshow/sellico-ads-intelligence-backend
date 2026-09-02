package main

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
)

func TestIsLegacyRecurringRetryOnlyMatchesOldPayloadlessRetries(t *testing.T) {
	cutoff := time.Now().Add(-time.Hour)
	failedAt := cutoff.Add(-time.Minute)
	require.True(t, isLegacyRecurringRetry(&asynq.TaskInfo{
		Type: worker.TaskSweepSyncWorkspace, MaxRetry: 25, Retried: 2, LastFailedAt: failedAt,
	}, cutoff))
	require.True(t, isLegacyRecurringRetry(&asynq.TaskInfo{
		Type: worker.TaskOzonCPOOrdersSync, MaxRetry: 25, Retried: 1, LastFailedAt: failedAt,
	}, cutoff))
	require.False(t, isLegacyRecurringRetry(&asynq.TaskInfo{
		Type:         worker.TaskOzonCPOOrdersSync,
		Payload:      []byte(`{"cabinet_id":"test"}`),
		MaxRetry:     25,
		Retried:      1,
		LastFailedAt: failedAt,
	}, cutoff))
	require.False(t, isLegacyRecurringRetry(&asynq.TaskInfo{
		Type: worker.TaskSweepSyncWorkspace, MaxRetry: 0, Retried: 1, LastFailedAt: failedAt,
	}, cutoff))
	require.False(t, isLegacyRecurringRetry(&asynq.TaskInfo{
		Type: worker.TaskSweepSyncWorkspace, MaxRetry: 25, Retried: 0, LastFailedAt: failedAt,
	}, cutoff))
	require.False(t, isLegacyRecurringRetry(&asynq.TaskInfo{
		Type: worker.TaskSyncWorkspace, MaxRetry: 25, Retried: 1, LastFailedAt: failedAt,
	}, cutoff))
	require.False(t, isLegacyRecurringRetry(&asynq.TaskInfo{
		Type: worker.TaskSweepSyncWorkspace, MaxRetry: 25, Retried: 1, LastFailedAt: cutoff.Add(time.Minute),
	}, cutoff))
	require.False(t, isLegacyRecurringRetry(nil, cutoff))
}
