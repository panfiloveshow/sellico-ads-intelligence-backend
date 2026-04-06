package worker

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
)

func TestNewRuntime_BuildsWithoutStarting(t *testing.T) {
	cfg := &config.Config{
		RedisURL:               "redis://localhost:6379/0",
		SyncInterval:           "@every 1h",
		RecommendationInterval: "@every 2h",
		BidAutomationInterval:  "@every 15m",
	}

	runtime, err := NewRuntime(cfg, nil, nil, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	require.NoError(t, err)
	require.NotNil(t, runtime)
}
