package service

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeriveBidSnapshotsFromNetworkCapture_UsesExplicitBidCandidates(t *testing.T) {
	query := "термоэтикетки"
	region := "Москва"
	capturedAt := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{
		"url": "https://cmp.wildberries.ru/api/v1/advert/preset-bids?nm_id=439046552",
		"wb_product_id": 439046552,
		"page_subtype": "promotion",
		"bid_candidates": [
			{
				"normQuery": "термоэтикетки",
				"nmId": 439046552,
				"bid": "1 100 ₽",
				"recommendedBid": 1250,
				"cpmMin": 850
			}
		]
	}`)

	got := deriveBidSnapshotsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "query",
		EndpointKey: "wb.bid.recommendations",
		Query:       &query,
		Region:      &region,
		Payload:     payload,
		CapturedAt:  &capturedAt,
	}, payload)

	if len(got) != 1 {
		t.Fatalf("expected 1 derived bid snapshot, got %d", len(got))
	}
	assertStringPtr(t, got[0].Query, "термоэтикетки")
	assertStringPtr(t, got[0].Region, "Москва")
	assertInt64Ptr(t, got[0].VisibleBid, 1100)
	assertInt64Ptr(t, got[0].RecommendedBid, 1250)
	assertInt64Ptr(t, got[0].CPMMin, 850)
	if got[0].CapturedAt == nil || !got[0].CapturedAt.Equal(capturedAt) {
		t.Fatalf("expected captured_at to be preserved")
	}

	var metadata map[string]any
	if err := json.Unmarshal(got[0].Metadata, &metadata); err != nil {
		t.Fatalf("invalid metadata: %v", err)
	}
	if metadata["derived_from_network_capture"] != true {
		t.Fatalf("expected derived_from_network_capture metadata, got %#v", metadata)
	}
	if metadata["wb_product_id"].(float64) != 439046552 {
		t.Fatalf("expected wb_product_id metadata, got %#v", metadata)
	}
}

func TestDeriveBidSnapshotsFromNetworkCapture_WalksNestedResponse(t *testing.T) {
	payload := json.RawMessage(`{
		"endpoint_key": "wb.bid.recommendations",
		"payload": {
			"response": {
				"data": {
					"items": [
						{
							"keyword": "самоклеящиеся этикетки",
							"advertId": 35905770,
							"nm_id": 439046552,
							"competitive_bid": 900,
							"leadershipBid": 1300
						}
					]
				}
			}
		}
	}`)

	got := deriveBidSnapshotsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "query",
		EndpointKey: "wb.bid.recommendations",
		Payload:     payload,
	}, payload)

	if len(got) != 1 {
		t.Fatalf("expected 1 derived bid snapshot, got %d", len(got))
	}
	assertStringPtr(t, got[0].Query, "самоклеящиеся этикетки")
	assertInt64Ptr(t, got[0].CompetitiveBid, 900)
	assertInt64Ptr(t, got[0].LeadershipBid, 1300)

	var metadata map[string]any
	if err := json.Unmarshal(got[0].Metadata, &metadata); err != nil {
		t.Fatalf("invalid metadata: %v", err)
	}
	if metadata["wb_campaign_id"].(float64) != 35905770 {
		t.Fatalf("expected wb_campaign_id metadata, got %#v", metadata)
	}
}

func TestDeriveBidSnapshotsFromNetworkCapture_IgnoresUnsupportedOrMetriclessPayloads(t *testing.T) {
	payload := json.RawMessage(`{"response":{"items":[{"keyword":"рюкзак","views":10}]}}`)

	if got := deriveBidSnapshotsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "query",
		EndpointKey: "wb.query.stats",
		Payload:     payload,
	}, payload); len(got) != 0 {
		t.Fatalf("expected unsupported endpoint to be ignored, got %d items", len(got))
	}

	if got := deriveBidSnapshotsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "query",
		EndpointKey: "wb.bid.recommendations",
		Payload:     payload,
	}, payload); len(got) != 0 {
		t.Fatalf("expected metricless payload to be ignored, got %d items", len(got))
	}
}

func TestDerivePositionSnapshotsFromNetworkCapture_WalksNestedResponse(t *testing.T) {
	query := "самоклеящиеся этикетки"
	region := "Москва"
	payload := json.RawMessage(`{
		"wb_product_id": 439046552,
		"payload": {
			"response": {
				"items": [
					{
						"query": "самоклеящиеся этикетки",
						"region": "Москва",
						"nmId": 439046552,
						"position": 7,
						"page": 1,
						"placement": "search"
					}
				]
			}
		}
	}`)

	got := derivePositionSnapshotsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "search",
		EndpointKey: "wb.serp.snapshot",
		Query:       &query,
		Region:      &region,
		Payload:     payload,
	}, payload)

	if len(got) != 1 {
		t.Fatalf("expected 1 derived position snapshot, got %d", len(got))
	}
	if got[0].Query != "самоклеящиеся этикетки" || got[0].Region != "Москва" {
		t.Fatalf("unexpected query/region: %#v", got[0])
	}
	if got[0].VisiblePosition != 7 {
		t.Fatalf("expected position 7, got %d", got[0].VisiblePosition)
	}
	if got[0].VisiblePage == nil || *got[0].VisiblePage != 1 {
		t.Fatalf("expected page 1, got %#v", got[0].VisiblePage)
	}
	assertStringPtr(t, got[0].PageSubtype, "search")
	var metadata map[string]any
	if err := json.Unmarshal(got[0].Metadata, &metadata); err != nil {
		t.Fatalf("invalid metadata: %v", err)
	}
	if metadata["derived_kind"] != "position" {
		t.Fatalf("expected position metadata, got %#v", metadata)
	}
}

func TestDerivePositionSnapshotsFromNetworkCapture_RequiresRealContext(t *testing.T) {
	payload := json.RawMessage(`{"response":{"items":[{"query":"рюкзак","position":3}]}}`)

	got := derivePositionSnapshotsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "search",
		EndpointKey: "wb.serp.snapshot",
		Payload:     payload,
	}, payload)

	if len(got) != 0 {
		t.Fatalf("expected incomplete position payload to be ignored, got %d", len(got))
	}
}

func TestDeriveUISignalsFromNetworkCapture_CreatesAPIErrorSignal(t *testing.T) {
	payload := json.RawMessage(`{
		"status": 429,
		"response": {
			"message": "rate limit exceeded"
		},
		"wb_campaign_id": 35905770
	}`)

	got := deriveUISignalsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "campaign",
		EndpointKey: "wb.query.stats",
		Payload:     payload,
	}, payload)

	if len(got) == 0 {
		t.Fatalf("expected API error signal")
	}
	if got[0].SignalType != "wb_api_error" || got[0].Severity != "high" {
		t.Fatalf("unexpected signal classification: %#v", got[0])
	}
	if got[0].Title != "Ошибка WB API 429" {
		t.Fatalf("unexpected title: %q", got[0].Title)
	}
	assertStringPtr(t, got[0].Message, "rate limit exceeded")
}

func TestDeriveUISignalsFromNetworkCapture_IgnoresNoiseStatus(t *testing.T) {
	payload := json.RawMessage(`{"status":200,"response":{"status":"ok","message":"success"}}`)

	got := deriveUISignalsFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "campaign",
		EndpointKey: "wb.query.stats",
		Payload:     payload,
	}, payload)

	if len(got) != 0 {
		t.Fatalf("expected success noise to be ignored, got %d", len(got))
	}
}

func TestDeriveCampaignBudgetFromNetworkCapture_UsesRealWBBudgetResponse(t *testing.T) {
	payload := json.RawMessage(`{
		"url": "https://cmp.wildberries.ru/adv/v1/budget?id=184010773",
		"status": 200,
		"wb_campaign_id": 184010773,
		"response": {
			"cash": 120.25,
			"netting": 30,
			"total": 150.25
		}
	}`)

	got := deriveCampaignBudgetFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "campaign",
		EndpointKey: "wb.budget",
		Payload:     payload,
	}, payload)

	if got == nil {
		t.Fatalf("expected budget snapshot")
	}
	if got.cash != 12025 || got.netting != 3000 || got.total != 15025 {
		t.Fatalf("unexpected budget values: %#v", got)
	}
}

func TestDeriveCampaignBudgetFromNetworkCapture_IgnoresErrorsAndIncompletePayloads(t *testing.T) {
	errorPayload := json.RawMessage(`{"status": 404, "response": {"cash": 1, "netting": 2, "total": 3}}`)
	if got := deriveCampaignBudgetFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "campaign",
		EndpointKey: "wb.budget",
		Payload:     errorPayload,
	}, errorPayload); got != nil {
		t.Fatalf("expected error budget response to be ignored")
	}

	incompletePayload := json.RawMessage(`{"status": 200, "response": {"total": 3}}`)
	if got := deriveCampaignBudgetFromNetworkCapture(CreateExtensionNetworkCaptureInput{
		PageType:    "campaign",
		EndpointKey: "wb.budget",
		Payload:     incompletePayload,
	}, incompletePayload); got != nil {
		t.Fatalf("expected incomplete budget response to be ignored")
	}
}

func assertStringPtr(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("expected %q, got %#v", want, got)
	}
}

func assertInt64Ptr(t *testing.T, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("expected %d, got %#v", want, got)
	}
}
