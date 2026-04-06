package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocsSpec(t *testing.T) {
	h := NewDocsHandler([]byte("openapi: 3.0.3\ninfo:\n  title: test\n"))
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	h.Spec(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/yaml")
	assert.Contains(t, rec.Body.String(), "openapi: 3.0.3")
}

func TestDocsSpec_Unavailable(t *testing.T) {
	h := NewDocsHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	h.Spec(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeEnvelope(t, rec)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "DOCS_UNAVAILABLE", resp.Errors[0].Code)
}

func TestDocsIndex(t *testing.T) {
	h := NewDocsHandler([]byte("openapi: 3.0.3\n"))
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	assert.True(t, strings.Contains(body, "Sellico Ads Intelligence API"))
	assert.True(t, strings.Contains(body, "/openapi.yaml"))
}
