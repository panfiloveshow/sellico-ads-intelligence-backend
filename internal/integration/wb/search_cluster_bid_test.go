package wb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAggregatedToClusterStatsCarriesRealCurrentBid(t *testing.T) {
	rows := aggregatedToClusterStats(44, "2026-07-14", map[string]*aggregatedNormQuery{
		"10:running shoes": {
			keyword: "running shoes",
			nmID:    10,
			bid:     650,
			daily: map[string]*aggregatedNormQueryDay{
				"2026-07-14": {date: "2026-07-14", views: 5, clicks: 1},
			},
		},
	})

	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].CurrentBid)
	assert.EqualValues(t, 650, *rows[0].CurrentBid)
}

func TestAggregatedToClusterStatsDoesNotInventMissingBid(t *testing.T) {
	rows := aggregatedToClusterStats(44, "2026-07-14", map[string]*aggregatedNormQuery{
		"10:running shoes": {
			keyword: "running shoes",
			nmID:    10,
			daily: map[string]*aggregatedNormQueryDay{
				"2026-07-14": {date: "2026-07-14"},
			},
		},
	})

	require.Len(t, rows, 1)
	assert.Nil(t, rows[0].CurrentBid)
}
