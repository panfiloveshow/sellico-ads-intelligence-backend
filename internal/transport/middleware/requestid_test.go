package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestID_GeneratesNewID(t *testing.T) {
	var gotID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	RequestID(handler).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, gotID)
	// Response header should match context value.
	assert.Equal(t, gotID, rec.Header().Get("X-Request-ID"))
}

func TestRequestID_UsesExistingHeader(t *testing.T) {
	existingID := "my-custom-request-id"
	var gotID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", existingID)
	rec := httptest.NewRecorder()

	RequestID(handler).ServeHTTP(rec, req)

	assert.Equal(t, existingID, gotID)
	assert.Equal(t, existingID, rec.Header().Get("X-Request-ID"))
}

func TestRequestID_UniquePerRequest(t *testing.T) {
	ids := make([]string, 0, 3)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, RequestIDFromContext(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	mw := RequestID(handler)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
	}

	require.Len(t, ids, 3)
	assert.NotEqual(t, ids[0], ids[1])
	assert.NotEqual(t, ids[1], ids[2])
	assert.NotEqual(t, ids[0], ids[2])
}

func TestRequestIDFromContext_NotPresent(t *testing.T) {
	ctx := context.Background()
	id := RequestIDFromContext(ctx)
	assert.Empty(t, id)
}
