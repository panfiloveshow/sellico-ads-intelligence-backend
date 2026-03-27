package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 27: Ошибки — HTTP 500 без внутренних деталей
// Проверяет: Требования 20.5, 19.5

// TestProperty_Recovery_AlwaysReturns500OnPanic verifies Requirement 20.5:
// For any panic value (string, error, int, struct), Recovery MUST return HTTP 500
// with Content-Type application/json and a valid Response_Envelope containing
// exactly the generic INTERNAL_ERROR code and message.
func TestProperty_Recovery_AlwaysReturns500OnPanic(t *testing.T) {
	panicValues := []func(*rapid.T) interface{}{
		func(t *rapid.T) interface{} { return rapid.String().Draw(t, "panicStr") },
		func(t *rapid.T) interface{} { return rapid.Int().Draw(t, "panicInt") },
		func(t *rapid.T) interface{} { return fmt.Errorf("err: %s", rapid.String().Draw(t, "errMsg")) },
		func(t *rapid.T) interface{} {
			return struct{ Msg string }{Msg: rapid.String().Draw(t, "structMsg")}
		},
	}

	rapid.Check(t, func(t *rapid.T) {
		genIdx := rapid.IntRange(0, len(panicValues)-1).Draw(t, "genIdx")
		panicVal := panicValues[genIdx](t)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(panicVal)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		Recovery(handler).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d for panic(%v)", rec.Code, panicVal)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %q", ct)
		}

		var resp envelope.Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp.Errors) != 1 {
			t.Fatalf("expected exactly 1 error, got %d", len(resp.Errors))
		}
		if resp.Errors[0].Code != apperror.ErrInternal.Code {
			t.Fatalf("expected code %q, got %q", apperror.ErrInternal.Code, resp.Errors[0].Code)
		}
		if resp.Errors[0].Message != apperror.ErrInternal.Message {
			t.Fatalf("expected message %q, got %q", apperror.ErrInternal.Message, resp.Errors[0].Message)
		}
	})
}

// TestProperty_Recovery_NeverLeaksPanicValue verifies Requirements 20.5, 19.5:
// For any arbitrary panic string, the HTTP response body MUST NOT contain
// the panic value. Internal details (stack traces, secrets, error messages)
// must never be exposed to the client.
func TestProperty_Recovery_NeverLeaksPanicValue(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a non-empty secret string that could represent sensitive data.
		secret := rapid.StringMatching(`[a-zA-Z0-9_]{4,64}`).Draw(t, "secret")

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("sensitive: " + secret)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		Recovery(handler).ServeHTTP(rec, req)

		body := rec.Body.String()
		if strings.Contains(body, secret) {
			t.Fatalf("response body leaked panic secret %q:\n%s", secret, body)
		}
	})
}

// TestProperty_Recovery_NoPanicPassesThrough verifies that Recovery does not
// interfere with normal handler execution. For any HTTP status code written
// by the handler, the response MUST pass through unchanged.
func TestProperty_Recovery_NoPanicPassesThrough(t *testing.T) {
	statusCodes := []int{
		http.StatusOK, http.StatusCreated, http.StatusNoContent,
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusConflict,
	}

	rapid.Check(t, func(t *rapid.T) {
		code := statusCodes[rapid.IntRange(0, len(statusCodes)-1).Draw(t, "statusIdx")]
		bodyContent := rapid.String().Draw(t, "body")

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(code)
			w.Write([]byte(bodyContent))
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		Recovery(handler).ServeHTTP(rec, req)

		if !handlerCalled {
			t.Fatal("handler must be called when no panic occurs")
		}
		if rec.Code != code {
			t.Fatalf("expected status %d, got %d", code, rec.Code)
		}
		if rec.Body.String() != bodyContent {
			t.Fatalf("body mismatch: expected %q, got %q", bodyContent, rec.Body.String())
		}
	})
}

// TestProperty_Recovery_PanicWithRequestIDNeverLeaks verifies Requirements 20.5, 19.5:
// Even when a request_id is present in context, the panic value and stack trace
// MUST NOT appear in the response body.
func TestProperty_Recovery_PanicWithRequestIDNeverLeaks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		secret := rapid.StringMatching(`[a-zA-Z0-9]{8,32}`).Draw(t, "secret")
		reqID := rapid.StringMatching(`[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}`).Draw(t, "reqID")

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(secret)
		})

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), RequestIDKey, reqID)
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()

		Recovery(handler).ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}

		body := rec.Body.String()
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked panic secret %q with request_id %q", secret, reqID)
		}
		// Stack trace keywords must not appear in response.
		if strings.Contains(body, "goroutine") {
			t.Fatal("response body contains stack trace")
		}
		if strings.Contains(body, ".go:") {
			t.Fatal("response body contains file references from stack trace")
		}
	})
}
