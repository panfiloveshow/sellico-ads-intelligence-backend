package dto

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
