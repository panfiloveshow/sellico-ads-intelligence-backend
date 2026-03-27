package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthLive(t *testing.T) {
	h := NewHealthHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()

	h.Live(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeEnvelope(t, rec)
	assert.Empty(t, resp.Errors)
}

func TestHealthReady_Unavailable(t *testing.T) {
	h := NewHealthHandler(func(_ context.Context) error {
		return errors.New("database not ready")
	})
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()

	h.Ready(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "SERVICE_UNAVAILABLE", resp.Errors[0].Code)
}
