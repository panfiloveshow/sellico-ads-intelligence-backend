package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestSourceEvidenceLabelsAndBidMismatch(t *testing.T) {
	phraseID := uuid.New()
	apiBid := int64(320)
	liveBid := int64(360)
	now := time.Now().UTC()
	evidence := &workspaceExtensionEvidence{
		bids: []domain.ExtensionBidSnapshot{
			{
				ID:         uuid.New(),
				PhraseID:   &phraseID,
				VisibleBid: &liveBid,
				Source:     domain.SourceExtension,
				Confidence: 0.9,
				CapturedAt: now,
			},
		},
	}
	evidence.buildIndexes()

	result := evidence.phraseEvidenceIndexedWithBid(phraseID, &apiBid)

	require.Equal(t, "mixed", result.Source)
	require.Equal(t, "API WB + кабинет WB", result.SourceLabel)
	require.Equal(t, []string{"official_wb_api", "wb_cabinet_evidence"}, result.SourcePriority)
	require.True(t, result.ConfirmedInCabinet)
	require.Contains(t, evidenceIssueTypes(result.Issues), "api_extension_bid_mismatch")
}

func TestBackendOnlyEvidenceLabel(t *testing.T) {
	result := backendOnlyEvidence(domain.SourceAPI, 0.75)

	require.Equal(t, domain.SourceAPI, result.Source)
	require.Equal(t, "API WB", result.SourceLabel)
	require.Equal(t, []string{"official_wb_api"}, result.SourcePriority)
	require.False(t, result.ConfirmedInCabinet)
}

func TestBidMismatchCountUsesRealPhraseAndLiveBidEvidence(t *testing.T) {
	phraseID := uuid.New()
	campaignID := uuid.New()
	apiBid := int64(320)
	liveBid := int64(360)
	evidence := &workspaceExtensionEvidence{
		bids: []domain.ExtensionBidSnapshot{
			{PhraseID: &phraseID, VisibleBid: &liveBid, CapturedAt: time.Now().UTC(), Confidence: 0.9},
		},
	}
	evidence.buildIndexes()

	count := evidence.bidMismatchCount([]domain.Phrase{
		{ID: phraseID, CampaignID: campaignID, CurrentBid: &apiBid},
	}, map[uuid.UUID]struct{}{campaignID: struct{}{}})

	require.Equal(t, 1, count)
}

func evidenceIssueTypes(items []domain.SourceEvidenceIssue) []string {
	result := make([]string, len(items))
	for i, item := range items {
		result[i] = item.Type
	}
	return result
}
