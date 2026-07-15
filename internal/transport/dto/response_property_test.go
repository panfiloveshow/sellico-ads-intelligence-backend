package dto

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 20: API формат — Response_Envelope и пагинация
// Проверяет: Требования 17.2, 17.3

// TestProperty_WriteJSON_AlwaysProducesValidEnvelope verifies Requirement 17.2:
// For any data written via WriteJSON, the HTTP response body always deserializes
// to a valid Response_Envelope with data set and no errors.
func TestProperty_WriteJSON_AlwaysProducesValidEnvelope(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dataType := rapid.IntRange(0, 2).Draw(t, "dataType")
		var data interface{}
		switch dataType {
		case 0:
			data = rapid.String().Draw(t, "strData")
		case 1:
			data = rapid.Int().Draw(t, "intData")
		case 2:
			data = map[string]interface{}{
				"key": rapid.String().Draw(t, "mapVal"),
			}
		}

		statusCodes := []int{http.StatusOK, http.StatusCreated}
		status := statusCodes[rapid.IntRange(0, len(statusCodes)-1).Draw(t, "statusIdx")]

		rec := httptest.NewRecorder()
		WriteJSON(rec, status, data)

		if rec.Code != status {
			t.Fatalf("expected status %d, got %d", status, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ct)
		}

		var resp envelope.Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Data == nil && data != nil {
			t.Fatal("response data must not be nil when input data is non-nil")
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("WriteJSON must produce no errors, got %d", len(resp.Errors))
		}
	})
}

// TestProperty_WriteJSONWithMeta_AlwaysProducesValidEnvelopeWithPagination verifies Requirement 17.2, 17.3:
// For any data written via WriteJSONWithMeta, the HTTP response body always
// deserializes to a valid Response_Envelope with correct meta pagination fields.
func TestProperty_WriteJSONWithMeta_AlwaysProducesValidEnvelopeWithPagination(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		data := rapid.SliceOfN(rapid.String(), 0, 20).Draw(t, "data")
		page := rapid.IntRange(1, 1000).Draw(t, "page")
		perPage := rapid.IntRange(1, 100).Draw(t, "perPage")
		total := int64(rapid.IntRange(0, 100000).Draw(t, "total"))

		meta := &envelope.Meta{
			Page:    page,
			PerPage: perPage,
			Total:   total,
		}

		rec := httptest.NewRecorder()
		WriteJSONWithMeta(rec, http.StatusOK, data, meta)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ct)
		}

		var resp envelope.Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Meta == nil {
			t.Fatal("response meta must not be nil")
		}
		if resp.Meta.Page != page {
			t.Fatalf("meta.Page: expected %d, got %d", page, resp.Meta.Page)
		}
		if resp.Meta.PerPage != perPage {
			t.Fatalf("meta.PerPage: expected %d, got %d", perPage, resp.Meta.PerPage)
		}
		if resp.Meta.Total != total {
			t.Fatalf("meta.Total: expected %d, got %d", total, resp.Meta.Total)
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("WriteJSONWithMeta must produce no errors, got %d", len(resp.Errors))
		}
	})
}

// TestProperty_WriteValidationError_AllFieldsHaveValidationCode verifies Requirement 17.2:
// For any map of field errors, WriteValidationError produces an HTTP 400 response
// where every error has code "VALIDATION_ERROR" and the field/message match the input.
func TestProperty_WriteValidationError_AllFieldsHaveValidationCode(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "numFields")
		fieldErrors := make(map[string]string, n)
		for i := 0; i < n; i++ {
			field := rapid.StringMatching(`[a-z_]{2,15}`).Draw(t, "field")
			msg := rapid.StringMatching(`.{1,50}`).Draw(t, "msg")
			fieldErrors[field] = msg
		}

		rec := httptest.NewRecorder()
		WriteValidationError(rec, fieldErrors)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}

		var resp envelope.Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Data != nil {
			t.Fatalf("validation error response must have nil data, got %v", resp.Data)
		}
		if len(resp.Errors) != len(fieldErrors) {
			t.Fatalf("expected %d errors, got %d", len(fieldErrors), len(resp.Errors))
		}

		seen := make(map[string]string, len(resp.Errors))
		for _, e := range resp.Errors {
			if e.Code != "VALIDATION_ERROR" {
				t.Fatalf("expected code VALIDATION_ERROR, got %q", e.Code)
			}
			seen[e.Field] = e.Message
		}

		for field, msg := range fieldErrors {
			gotMsg, ok := seen[field]
			if !ok {
				t.Fatalf("field %q missing from response errors", field)
			}
			if gotMsg != msg {
				t.Fatalf("field %q: expected message %q, got %q", field, msg, gotMsg)
			}
		}
	})
}

func TestAdsOverviewFromDomainMapsRecommendationTaskTotals(t *testing.T) {
	response := AdsOverviewFromDomain(domain.AdsOverview{
		Totals: domain.AdsOverviewTotals{
			ActiveRecommendations:  7,
			OverdueRecommendations: 2,
			DecisionQueueBuckets: map[string]int{
				"losses": 3,
			},
			TaskOwnerBuckets: map[string]int{
				domain.RecommendationTaskOwnerMarketer: 4,
			},
		},
	})

	if response.Totals.ActiveRecommendations != 7 {
		t.Fatalf("expected active recommendation total 7, got %d", response.Totals.ActiveRecommendations)
	}
	if response.Totals.OverdueRecommendations != 2 {
		t.Fatalf("expected overdue recommendation total 2, got %d", response.Totals.OverdueRecommendations)
	}
	if response.Totals.DecisionQueueBuckets["losses"] != 3 {
		t.Fatalf("expected decision queue buckets to be mapped, got %+v", response.Totals.DecisionQueueBuckets)
	}
	if response.Totals.TaskOwnerBuckets[domain.RecommendationTaskOwnerMarketer] != 4 {
		t.Fatalf("expected task owner buckets to be mapped, got %+v", response.Totals.TaskOwnerBuckets)
	}
}

func TestCampaignFromDomainPreservesNullableCanChangeNMs(t *testing.T) {
	allowed := true
	response := CampaignFromDomain(domain.Campaign{CanChangeNMs: &allowed})
	if response.CanChangeNMs == nil || !*response.CanChangeNMs {
		t.Fatalf("expected true WB restriction, got %v", response.CanChangeNMs)
	}

	unknown, err := json.Marshal(CampaignFromDomain(domain.Campaign{}))
	if err != nil {
		t.Fatalf("marshal campaign response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(unknown, &payload); err != nil {
		t.Fatalf("unmarshal campaign response: %v", err)
	}
	value, exists := payload["can_change_nms"]
	if !exists || value != nil {
		t.Fatalf("unknown WB restriction must be explicit null, got exists=%v value=%v", exists, value)
	}
}

func TestRecommendationTaskMetadataUsesCreatedAtAndActiveStatus(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	ageHours, dueAt, slaHours, overdue := recommendationTaskMetadata(domain.Recommendation{
		Status:    domain.RecommendationStatusActive,
		CreatedAt: now.Add(-72 * time.Hour),
	}, now)
	if ageHours != 72 || !overdue {
		t.Fatalf("expected 72h overdue active recommendation, got age=%d overdue=%v", ageHours, overdue)
	}
	if dueAt == nil || !dueAt.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("expected due_at from created_at + SLA, got %v", dueAt)
	}
	if slaHours != 48 {
		t.Fatalf("expected 48h task SLA, got %d", slaHours)
	}

	ageHours, dueAt, slaHours, overdue = recommendationTaskMetadata(domain.Recommendation{
		Status:    domain.RecommendationStatusCompleted,
		CreatedAt: now.Add(-96 * time.Hour),
	}, now)
	if ageHours != 96 || overdue {
		t.Fatalf("completed recommendation must not be overdue, got age=%d overdue=%v", ageHours, overdue)
	}
	if dueAt == nil || !dueAt.Equal(now.Add(-48*time.Hour)) || slaHours != 48 {
		t.Fatalf("expected completed task to keep factual SLA metadata, due_at=%v sla=%d", dueAt, slaHours)
	}
}

func TestRecommendationFromDomainExposesTaskMetadata(t *testing.T) {
	response := RecommendationFromDomain(domain.Recommendation{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Title:       "Проверить слабый кластер",
		Description: "Подтвержденная рекомендация",
		Type:        domain.RecommendationTypeHighSpendLowOrders,
		Severity:    domain.SeverityHigh,
		Status:      domain.RecommendationStatusActive,
		CreatedAt:   time.Now().Add(-72 * time.Hour),
		UpdatedAt:   time.Now().Add(-71 * time.Hour),
	})

	if !response.IsOverdue {
		t.Fatal("expected active recommendation older than threshold to be marked overdue")
	}
	if response.TaskCategory != domain.RecommendationTaskCategoryLosses {
		t.Fatalf("expected loss-control task category, got %q", response.TaskCategory)
	}
	if response.TaskOwnerRole != domain.RecommendationTaskOwnerMarketer {
		t.Fatalf("expected marketer task owner role, got %q", response.TaskOwnerRole)
	}
	if response.TaskSLAHours != 48 || response.TaskDueAt == nil {
		t.Fatalf("expected SLA metadata in recommendation response, sla=%d due_at=%v", response.TaskSLAHours, response.TaskDueAt)
	}
	if response.TaskAgeHours < 71 {
		t.Fatalf("expected task age from created_at, got %d", response.TaskAgeHours)
	}
}

func TestCampaignRecommendationsFromDomainExposesTaskMetadata(t *testing.T) {
	createdAt := time.Now().Add(-72 * time.Hour)

	response := campaignRecommendationsFromDomain([]domain.CampaignRecommendationSummary{
		{
			ID:         uuid.New(),
			Scope:      "campaign",
			Title:      "Снизить слабую ставку",
			Type:       domain.RecommendationTypeLowerBid,
			Severity:   domain.SeverityHigh,
			Confidence: 0.9,
			Status:     domain.RecommendationStatusActive,
			CreatedAt:  createdAt,
		},
	})

	if len(response) != 1 {
		t.Fatalf("expected one campaign recommendation, got %d", len(response))
	}
	if response[0].TaskCategory != domain.RecommendationTaskCategoryLosses || !response[0].IsOverdue {
		t.Fatalf("expected overdue loss-control task metadata, got %+v", response[0])
	}
	if response[0].TaskOwnerRole != domain.RecommendationTaskOwnerMarketer {
		t.Fatalf("expected campaign recommendation owner role, got %+v", response[0])
	}
	if response[0].TaskSLAHours != 48 || response[0].TaskDueAt == nil || response[0].TaskAgeHours < 71 {
		t.Fatalf("expected campaign recommendation SLA metadata, got %+v", response[0])
	}
}
