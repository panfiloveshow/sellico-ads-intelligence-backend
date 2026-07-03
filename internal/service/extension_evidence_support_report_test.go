package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestBuildExtensionEvidenceSupportReport_MissingEvidence(t *testing.T) {
	now := time.Now().UTC()
	report := buildExtensionEvidenceSupportReport(ExtensionEvidenceDebug{
		WorkspaceID: uuid.New(),
		Scope:       "campaign",
		GeneratedAt: now,
	})

	require.Equal(t, "Нет данных из кабинета WB", report.Summary.SourceLabel)
	require.Equal(t, "missing", report.Summary.Readiness)
	require.Equal(t, 0, report.Summary.CapturedSignals)
	require.Equal(t, 6, report.Summary.MissingSignals)
	require.Equal(t, "empty", report.Summary.FreshnessState)
	require.Len(t, report.Sections, 6)
	require.Len(t, report.Checklist, 6)
	require.False(t, report.Checklist[0].Done)
}

func TestBuildExtensionEvidenceSupportReport_PartialRealEvidence(t *testing.T) {
	now := time.Now().UTC()
	campaignID := uuid.New()
	report := buildExtensionEvidenceSupportReport(ExtensionEvidenceDebug{
		WorkspaceID:      uuid.New(),
		Scope:            "campaign",
		CampaignID:       &campaignID,
		GeneratedAt:      now,
		LatestCapturedAt: &now,
		Counts: ExtensionEvidenceDebugCounts{
			PageContexts:    1,
			NetworkCaptures: 1,
			DOMRowSnapshots: 1,
			BidSnapshots:    1,
		},
		DataStatus: ExtensionWidgetDataStatus{
			FreshnessState:     "fresh",
			Coverage:           "partial",
			ConfirmedInCabinet: true,
		},
		PageContexts: []domain.ExtensionPageContext{{
			CapturedAt: now,
		}},
		NetworkCaptures: []domain.ExtensionNetworkCapture{{
			CapturedAt: now,
		}},
		DOMRowSnapshots: []domain.ExtensionDOMRowSnapshot{{
			CapturedAt: now,
		}},
		BidSnapshots: []domain.ExtensionBidSnapshot{{
			CapturedAt: now,
		}},
	})

	require.Equal(t, "Данные кабинета WB", report.Summary.SourceLabel)
	require.Equal(t, "partial", report.Summary.Readiness)
	require.Equal(t, 4, report.Summary.CapturedSignals)
	require.Equal(t, 2, report.Summary.MissingSignals)
	require.True(t, report.Summary.ConfirmedInCabinet)
	require.Equal(t, "fresh", report.Summary.FreshnessState)
	require.Equal(t, "partial", report.Summary.Coverage)
	require.True(t, report.Checklist[0].Done)
	require.True(t, report.Checklist[1].Done)
	require.True(t, report.Checklist[2].Done)
	require.True(t, report.Checklist[3].Done)
	require.False(t, report.Checklist[4].Done)
}
