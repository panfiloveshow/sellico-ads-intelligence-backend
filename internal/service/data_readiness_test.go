package service

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestCampaignBidSnapshotFromLinksRequiresCompleteConsistentSnapshot(t *testing.T) {
	oldest := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	newest := oldest.Add(time.Hour)
	links := []sqlcgen.CampaignProduct{
		{BidSearch: pgtype.Int8{Int64: 710, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: newest, Valid: true}},
		{BidSearch: pgtype.Int8{Int64: 710, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: oldest, Valid: true}},
	}

	snapshot, ok := campaignBidSnapshotFromLinks(links, "search")

	require.True(t, ok)
	assert.Equal(t, 710, snapshot.Bid)
	assert.Equal(t, 2, snapshot.ProductCount)
	assert.Equal(t, oldest, snapshot.CapturedAt, "aggregate freshness must be bounded by the oldest SKU")
	combined, ok := campaignBidSnapshotFromLinks(links, "combined")
	require.True(t, ok)
	assert.Equal(t, 710, combined.Bid, "unified bid snapshots use the persisted product search/combined bid")

	missing := append([]sqlcgen.CampaignProduct(nil), links...)
	missing[1].BidSearch = pgtype.Int8{}
	_, ok = campaignBidSnapshotFromLinks(missing, "search")
	assert.False(t, ok, "one missing product bid must fail closed")

	mixed := append([]sqlcgen.CampaignProduct(nil), links...)
	mixed[1].BidSearch = pgtype.Int8{Int64: 720, Valid: true}
	_, ok = campaignBidSnapshotFromLinks(mixed, "search")
	assert.False(t, ok, "mixed product bids are unsafe for a campaign-wide action")

	staleUnknown := append([]sqlcgen.CampaignProduct(nil), links...)
	staleUnknown[1].UpdatedAt = pgtype.Timestamptz{}
	_, ok = campaignBidSnapshotFromLinks(staleUnknown, "search")
	assert.False(t, ok, "unknown freshness must fail closed")
}

func TestWBAdvertStatsDateRangeIncludesCurrentMSKDay(t *testing.T) {
	from, to := wbAdvertStatsDateRange(time.Date(2026, 7, 14, 21, 30, 0, 0, time.UTC))

	assert.Equal(t, "2026-06-15", from)
	assert.Equal(t, "2026-07-15", to)
}

func TestWBRateLimitWindowFromErrorUsesRetryAfter(t *testing.T) {
	before := time.Now().UTC()
	err := &wb.APIError{StatusCode: 429, RetryAfter: 73 * time.Second, Message: "rate limited"}
	next, seconds := wbRateLimitWindowFromError(wbEndpointFullstats, err)

	assert.Equal(t, 73, seconds)
	assert.WithinDuration(t, before.Add(73*time.Second), next, time.Second)
}

func TestCabinetSyncReadinessStatus(t *testing.T) {
	assert.Equal(t, "ready", cabinetSyncReadinessStatus(SyncSummary{Campaigns: 1}, nil))
	assert.Equal(t, "partial", cabinetSyncReadinessStatus(SyncSummary{Campaigns: 1, Issues: []SyncIssue{{Stage: "finance"}}}, errors.New("partial")))
	assert.Equal(t, "failed", cabinetSyncReadinessStatus(SyncSummary{Issues: []SyncIssue{{Stage: "campaigns"}}}, errors.New("failed")))
}

func TestSellerCabinetAutomationReadinessBlockReason(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	state := sqlcgen.SellerCabinetSyncState{
		Status:          "ready",
		CompletedAt:     pgtype.Timestamptz{Time: now.Add(-10 * time.Minute), Valid: true},
		DataThroughDate: pgtype.Date{Time: time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC), Valid: true},
	}
	assert.Empty(t, SellerCabinetAutomationReadinessBlockReason(state, now, time.Hour))

	state.Status = "partial"
	assert.Contains(t, SellerCabinetAutomationReadinessBlockReason(state, now, time.Hour), "partial")
	state.Status = "ready"
	state.CompletedAt.Time = now.Add(-2 * time.Hour)
	assert.Contains(t, SellerCabinetAutomationReadinessBlockReason(state, now, time.Hour), "stale")
	state.CompletedAt.Time = now
	state.DataThroughDate.Time = time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	assert.Contains(t, SellerCabinetAutomationReadinessBlockReason(state, now, time.Hour), "current day")
}

func TestSyncJobRunQueueStateFinalizesDuplicate(t *testing.T) {
	status, duplicate := syncJobRunQueueState("already_queued")
	assert.Equal(t, "partial", status)
	assert.True(t, duplicate)

	status, duplicate = syncJobRunQueueState("enqueued")
	assert.Equal(t, "pending", status)
	assert.False(t, duplicate)
}
