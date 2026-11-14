package sellico

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagerIntegrationRequiresFreshExactScopedMetadata(t *testing.T) {
	for _, test := range []struct {
		name    string
		changes map[string]any
		valid   bool
	}{
		{"valid", nil, true}, {"foreign workspace", map[string]any{"work_space_id": 4}, false}, {"different id", map[string]any{"id": 29}, false},
		{"disabled", map[string]any{"status": "inactive"}, false}, {"unknown status", map[string]any{"status": nil}, false},
		{"unsupported marketplace", map[string]any{"type": "Uzum"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "GET", r.Method)
				assert.Equal(t, "/workspaces/3/integrations/28", r.URL.Path)
				assert.Equal(t, "Bearer personal-token", r.Header.Get("Authorization"))
				payload := map[string]any{"id": 28, "work_space_id": 3, "status": "active", "type": "WildBerries", "name": "Test store", "api_key": "never-return-this-key"}
				for k, v := range test.changes {
					payload[k] = v
				}
				json.NewEncoder(w).Encode(payload)
			}))
			defer server.Close()
			client := NewClient(server.URL, time.Second)
			integration, err := client.VerifyManagerIntegration(context.Background(), "personal-token", "3", "28")
			if test.valid {
				require.NoError(t, err)
				assert.Equal(t, "wb", integration.Marketplace)
				raw, err := json.Marshal(integration)
				require.NoError(t, err)
				assert.NotContains(t, string(raw), "never-return-this-key")
			} else {
				require.Error(t, err)
				assert.Nil(t, integration)
			}
			assert.Equal(t, 1, calls)
		})
	}
}

func TestManagerIntegrationRejectsInvalidIDsAndRedirects(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "https://invalid.test/collect", 302)
	}))
	defer server.Close()
	client := NewClient(server.URL, time.Second)
	_, err := client.VerifyManagerIntegration(context.Background(), "personal-token", "3", "28/other")
	require.Error(t, err)
	assert.Zero(t, calls)
	_, err = client.VerifyManagerIntegration(context.Background(), "personal-token", "3", "28")
	require.Error(t, err)
	assert.Equal(t, 1, calls)
}
