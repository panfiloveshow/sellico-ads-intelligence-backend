package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestExtensionBidMismatchGuardrailReasonBlocksOnLatestLiveMismatch(t *testing.T) {
	oldBid := int64(300)
	liveBid := int64(340)
	now := time.Now().UTC()

	reason := extensionBidMismatchGuardrailReason(300, []domain.ExtensionBidSnapshot{
		{
			VisibleBid: &liveBid,
			CapturedAt: now,
		},
		{
			VisibleBid: &oldBid,
			CapturedAt: now.Add(-time.Hour),
		},
	})

	require.Contains(t, reason, "live cabinet bid 340")
	require.Contains(t, reason, "synced WB API bid 300")
}

func TestExtensionBidMismatchGuardrailReasonAllowsMatchingOrMissingEvidence(t *testing.T) {
	currentBid := int64(300)
	now := time.Now().UTC()

	require.Empty(t, extensionBidMismatchGuardrailReason(300, []domain.ExtensionBidSnapshot{
		{
			VisibleBid: &currentBid,
			CapturedAt: now,
		},
	}))
	require.Empty(t, extensionBidMismatchGuardrailReason(300, nil))
	require.Empty(t, extensionBidMismatchGuardrailReason(300, []domain.ExtensionBidSnapshot{
		{
			CapturedAt: now,
		},
	}))
}
