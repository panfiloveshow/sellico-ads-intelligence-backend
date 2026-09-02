package sqlcgen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaceOzonOrdersHourlyUsesNamespacedCabinetLock(t *testing.T) {
	require.Contains(t, lockOzonOrdersHourlyCabinet, "pg_advisory_xact_lock")
	require.Contains(t, lockOzonOrdersHourlyCabinet, "($1::uuid)::text")
	require.Contains(t, lockOzonOrdersHourlyCabinet, ":ozon-orders-hourly-replace")
}
