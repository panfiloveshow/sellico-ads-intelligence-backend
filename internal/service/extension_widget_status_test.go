package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

func TestBuildExtensionWidgetDataStatus_EmptyCampaignExplainsNextStep(t *testing.T) {
	status := buildExtensionWidgetDataStatus("campaign", nil, nil, nil)

	if status.FreshnessState != "empty" {
		t.Fatalf("expected empty freshness, got %q", status.FreshnessState)
	}
	if status.ConfirmedInCabinet {
		t.Fatal("empty status must not be confirmed in cabinet")
	}
	if status.EvidenceCounts.BidSnapshots != 0 || status.EvidenceCounts.PositionSnapshots != 0 || status.EvidenceCounts.UISignals != 0 {
		t.Fatalf("expected zero evidence counts, got %+v", status.EvidenceCounts)
	}
	if len(status.Issues) == 0 {
		t.Fatal("expected an actionable issue for empty data")
	}
	if !hasWidgetAction(status.NextActions, "refresh") {
		t.Fatalf("expected refresh action, got %+v", status.NextActions)
	}
	if !containsIssue(status.Issues, "extension_capture") || !containsIssue(status.Issues, "bid_visibility") {
		t.Fatalf("expected capture and bid visibility issues, got %+v", status.Issues)
	}
}

func TestBuildExtensionWidgetDataStatus_UsesRealEvidenceCounts(t *testing.T) {
	now := time.Now()
	message := "WB returned 429 rate limit while loading bids"
	status := buildExtensionWidgetDataStatus(
		"search",
		&domain.ExtensionBidSnapshot{
			CapturedAt: now,
			Confidence: 0.8,
		},
		[]domain.ExtensionPositionSnapshot{
			{CapturedAt: now.Add(-time.Minute), Confidence: 0.7},
			{CapturedAt: now.Add(-2 * time.Minute), Confidence: 0.6},
		},
		[]domain.ExtensionUISignal{
			{
				Severity:   "high",
				Title:      "WB ограничил запросы",
				Message:    &message,
				Confidence: 0.9,
				CapturedAt: now,
			},
		},
	)

	if status.FreshnessState != "fresh" {
		t.Fatalf("expected fresh status, got %q", status.FreshnessState)
	}
	if !status.ConfirmedInCabinet {
		t.Fatal("status with live evidence must be confirmed in cabinet")
	}
	if status.EvidenceCounts.BidSnapshots != 1 || status.EvidenceCounts.PositionSnapshots != 2 || status.EvidenceCounts.UISignals != 1 {
		t.Fatalf("unexpected evidence counts: %+v", status.EvidenceCounts)
	}
	if !containsIssue(status.Issues, "wb_page_signal") {
		t.Fatalf("expected ui signal issue, got %+v", status.Issues)
	}
}

func TestBuildExtensionWidgetDataStatus_StaleEvidenceRequestsRefresh(t *testing.T) {
	old := time.Now().Add(-25 * time.Hour)
	status := buildExtensionWidgetDataStatus(
		"product",
		nil,
		[]domain.ExtensionPositionSnapshot{{CapturedAt: old, Confidence: 0.7}},
		nil,
	)

	if status.FreshnessState != "stale" {
		t.Fatalf("expected stale status, got %q", status.FreshnessState)
	}
	if !containsIssueMessage(status.Issues, "старше суток") {
		t.Fatalf("expected stale warning, got %+v", status.Issues)
	}
	if !hasWidgetAction(status.NextActions, "refresh") {
		t.Fatalf("expected refresh action, got %+v", status.NextActions)
	}
}

func TestBuildCampaignWidgetPrimaryInsight_SpendWithoutOrdersLeads(t *testing.T) {
	status := buildExtensionWidgetDataStatus("campaign", nil, nil, nil)
	insight := buildCampaignWidgetPrimaryInsight([]domain.CampaignStat{
		{
			Spend:       2800,
			Impressions: 18000,
			Clicks:      94,
		},
	}, nil, nil, status)

	if insight.Severity != domain.SeverityHigh {
		t.Fatalf("expected high severity, got %+v", insight)
	}
	if insight.Source != domain.SourceAPI {
		t.Fatalf("expected api source, got %q", insight.Source)
	}
	if !strings.Contains(insight.Message, "WB API") {
		t.Fatalf("expected WB API evidence message, got %q", insight.Message)
	}
	if insight.NextAction == nil || insight.NextAction.ActionPath != "open-sellico-ads" {
		t.Fatalf("expected Sellico action, got %+v", insight.NextAction)
	}
}

func TestBuildSearchWidgetPrimaryInsight_RecommendationWinsOverLiveEvidence(t *testing.T) {
	recID := uuid.New()
	nextAction := "Снизить ставку по слабому кластеру"
	status := buildExtensionWidgetDataStatus("search", &domain.ExtensionBidSnapshot{CapturedAt: time.Now(), Confidence: 0.8}, nil, nil)
	insight := buildSearchWidgetPrimaryInsight("термоэтикетки", nil, nil, &domain.ExtensionBidSnapshot{
		CapturedAt: time.Now(),
		Confidence: 0.8,
	}, nil, []domain.Recommendation{
		{
			ID:          recID,
			Title:       "Слабый кластер тратит бюджет",
			Type:        domain.RecommendationTypeLowerBid,
			Severity:    domain.SeverityHigh,
			NextAction:  &nextAction,
			CreatedAt:   time.Now(),
			WorkspaceID: uuid.New(),
		},
	}, status)

	if insight.Title != "Слабый кластер тратит бюджет" {
		t.Fatalf("expected recommendation title, got %+v", insight)
	}
	if insight.Source != domain.SourceDerived {
		t.Fatalf("expected derived recommendation source, got %q", insight.Source)
	}
	if !hasEvidence(insight.Evidence, recID.String()) {
		t.Fatalf("expected recommendation id evidence, got %+v", insight.Evidence)
	}
}

func TestBuildProductWidgetPrimaryInsight_EmptyStateIsTruthful(t *testing.T) {
	status := buildExtensionWidgetDataStatus("product", nil, nil, nil)
	insight := buildProductWidgetPrimaryInsight(domain.Product{ID: uuid.New(), Title: "Товар"}, nil, nil, nil, status)

	if insight.Source != domain.SourceExtension {
		t.Fatalf("expected extension source, got %+v", insight)
	}
	if !strings.Contains(insight.Message, "live-сигналы") && !strings.Contains(insight.Message, "live-сигналов") {
		t.Fatalf("expected missing live evidence message, got %q", insight.Message)
	}
	if insight.NextAction == nil || insight.NextAction.ActionPath != "refresh" {
		t.Fatalf("expected refresh action, got %+v", insight.NextAction)
	}
}

func hasWidgetAction(actions []ExtensionWidgetAction, id string) bool {
	for _, action := range actions {
		if action.ID == id {
			return true
		}
	}
	return false
}

func hasEvidence(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func containsIssue(issues []ExtensionWidgetIssue, stage string) bool {
	for _, issue := range issues {
		if issue.Stage == stage {
			return true
		}
	}
	return false
}

func containsIssueMessage(issues []ExtensionWidgetIssue, text string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Message, text) {
			return true
		}
	}
	return false
}
