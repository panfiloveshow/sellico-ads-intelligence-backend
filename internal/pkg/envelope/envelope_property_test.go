package envelope

import (
	"encoding/json"
	"testing"

	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 20: API формат — Response_Envelope и пагинация
// Проверяет: Требования 17.2, 17.3

// TestProperty_OK_AlwaysProducesDataAndNoErrors verifies Requirement 17.2:
// For any data payload, OK() always produces a Response with data set and no errors.
func TestProperty_OK_AlwaysProducesDataAndNoErrors(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate arbitrary data payloads.
		dataType := rapid.IntRange(0, 3).Draw(t, "dataType")
		var data interface{}
		switch dataType {
		case 0:
			data = rapid.String().Draw(t, "strData")
		case 1:
			data = rapid.Int().Draw(t, "intData")
		case 2:
			data = rapid.SliceOfN(rapid.String(), 0, 20).Draw(t, "sliceData")
		case 3:
			data = map[string]interface{}{
				"key": rapid.String().Draw(t, "mapVal"),
			}
		}

		resp := OK(data, nil)

		if resp.Data == nil && data != nil {
			t.Fatalf("OK() with non-nil data must set Data, got nil")
		}
		if len(resp.Errors) != 0 {
			t.Fatalf("OK() must produce no errors, got %d", len(resp.Errors))
		}
	})
}

// TestProperty_Err_AlwaysProducesErrorsAndNilData verifies Requirement 17.2:
// For any errors, Err() always produces a Response with errors set and nil data.
func TestProperty_Err_AlwaysProducesErrorsAndNilData(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(t, "numErrors")
		errs := make([]Error, n)
		for i := 0; i < n; i++ {
			errs[i] = Error{
				Code:    rapid.StringMatching(`[A-Z_]{3,20}`).Draw(t, "code"),
				Message: rapid.String().Draw(t, "message"),
			}
		}

		resp := Err(errs...)

		if resp.Data != nil {
			t.Fatalf("Err() must produce nil data, got %v", resp.Data)
		}
		if len(resp.Errors) != n {
			t.Fatalf("Err() must produce %d errors, got %d", n, len(resp.Errors))
		}
	})
}

// TestProperty_ValidationErr_AllErrorsHaveValidationCode verifies Requirement 17.2:
// For any map of field errors, ValidationErr produces a Response where every error
// has code "VALIDATION_ERROR" and the field/message match the input.
func TestProperty_ValidationErr_AllErrorsHaveValidationCode(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "numFields")
		fieldErrors := make(map[string]string, n)
		for i := 0; i < n; i++ {
			field := rapid.StringMatching(`[a-z_]{2,15}`).Draw(t, "field")
			msg := rapid.StringMatching(`.{1,50}`).Draw(t, "msg")
			fieldErrors[field] = msg
		}

		resp := ValidationErr(fieldErrors)

		if resp.Data != nil {
			t.Fatalf("ValidationErr must produce nil data, got %v", resp.Data)
		}
		if len(resp.Errors) != len(fieldErrors) {
			t.Fatalf("expected %d errors, got %d", len(fieldErrors), len(resp.Errors))
		}

		// Build a lookup from the response errors.
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

// TestProperty_Response_JSONRoundTrip verifies Requirement 17.2:
// For any Response produced by OK(), the JSON serialization/deserialization
// round-trip preserves the envelope structure (data present, meta present if set,
// errors omitted when empty).
func TestProperty_Response_JSONRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hasMeta := rapid.Bool().Draw(t, "hasMeta")

		data := rapid.String().Draw(t, "data")
		var meta *Meta
		if hasMeta {
			meta = &Meta{
				Page:    rapid.IntRange(1, 1000).Draw(t, "page"),
				PerPage: rapid.IntRange(1, 100).Draw(t, "perPage"),
				Total:   int64(rapid.IntRange(0, 100000).Draw(t, "total")),
			}
		}

		resp := OK(data, meta)

		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var decoded Response
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		// Data must be present.
		if decoded.Data == nil {
			t.Fatal("decoded Data must not be nil")
		}

		// Meta must match.
		if hasMeta {
			if decoded.Meta == nil {
				t.Fatal("decoded Meta must not be nil when set")
			}
			if decoded.Meta.Page != meta.Page {
				t.Fatalf("meta.Page: expected %d, got %d", meta.Page, decoded.Meta.Page)
			}
			if decoded.Meta.PerPage != meta.PerPage {
				t.Fatalf("meta.PerPage: expected %d, got %d", meta.PerPage, decoded.Meta.PerPage)
			}
			if decoded.Meta.Total != meta.Total {
				t.Fatalf("meta.Total: expected %d, got %d", meta.Total, decoded.Meta.Total)
			}
		} else {
			if decoded.Meta != nil {
				t.Fatal("decoded Meta must be nil when not set")
			}
		}

		// Errors must be empty.
		if len(decoded.Errors) != 0 {
			t.Fatalf("decoded Errors must be empty for OK response, got %d", len(decoded.Errors))
		}
	})
}
