package worker

import (
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestScheduledSweepOptionsDisableRetries(t *testing.T) {
	opts := scheduledSweepOptions(QueueWBSync)
	byType := make(map[asynq.OptionType]asynq.Option, len(opts))
	for _, opt := range opts {
		byType[opt.Type()] = opt
	}

	require.Equal(t, QueueWBSync, byType[asynq.QueueOpt].Value())
	require.Equal(t, 0, byType[asynq.MaxRetryOpt].Value())
	_, hasTimeoutOverride := byType[asynq.TimeoutOpt]
	require.False(t, hasTimeoutOverride)
}
