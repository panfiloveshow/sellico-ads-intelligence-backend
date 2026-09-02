package sqlcgen

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrequencyHistoryDeduplicatesStableHourlyValues(t *testing.T) {
	source := readPackageSource(t, "semantics_manual.go")
	query := sourceBetween(t, source, "func (q *Queries) CreateFrequencyHistory", "type FrequencyHistoryRow")
	normalized := strings.ToLower(query)

	require.Contains(t, normalized, "pg_advisory_xact_lock")
	require.Contains(t, normalized, ":keyword-frequency-history")
	require.Contains(t, normalized, "order by h.checked_at desc")
	require.Contains(t, normalized, "latest.frequency = $2")
	require.Contains(t, normalized, "interval '1 hour'")
}

func TestSEOAnalysisReusesUnchangedLatestResult(t *testing.T) {
	source := readPackageSource(t, "semantics_manual.go")
	query := sourceBetween(t, source, "func (q *Queries) CreateSEOAnalysis", "func (q *Queries) GetLatestSEOAnalysis")
	normalized := strings.ToLower(query)

	require.Contains(t, normalized, "pg_advisory_xact_lock")
	require.Contains(t, normalized, ":seo-analysis")
	require.Contains(t, normalized, "where not exists")
	require.Contains(t, normalized, "title_issues = $7::jsonb")
	require.Contains(t, normalized, "recommendations = $10::jsonb")
	require.Contains(t, normalized, "select * from latest")
}

func TestLatestStockEvidenceUsesPerProductIndexLookup(t *testing.T) {
	source := readPackageSource(t, "product_events_manual.go")
	query := sourceBetween(t, source, "func (q *Queries) ListLatestProductStockEvidenceByWorkspace", "type CreateProductSnapshotParams")
	normalized := strings.ToLower(query)

	require.Contains(t, normalized, "join lateral")
	require.Contains(t, normalized, "snapshot.product_id = p.id")
	require.Contains(t, normalized, "order by snapshot.captured_at desc")
	require.Contains(t, normalized, "limit 1")
	require.NotContains(t, normalized, "distinct on (ps.product_id)")
}

func readPackageSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	require.NoError(t, err)
	return string(data)
}

func sourceBetween(t *testing.T, source, start, end string) string {
	t.Helper()
	startAt := strings.Index(source, start)
	require.NotEqual(t, -1, startAt, "start marker missing")
	endAt := strings.Index(source[startAt:], end)
	require.NotEqual(t, -1, endAt, "end marker missing")
	return source[startAt : startAt+endAt]
}
