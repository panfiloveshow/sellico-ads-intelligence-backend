package transport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubMembershipChecker always returns a member with the given role.
type stubMembershipChecker struct {
	role string
}

func (s *stubMembershipChecker) GetWorkspaceMember(_ context.Context, _, _ uuid.UUID) (*domain.WorkspaceMember, error) {
	return &domain.WorkspaceMember{
		ID:   uuid.New(),
		Role: s.role,
	}, nil
}

func newTestRouter() chi.Router {
	return NewRouter(RouterDeps{
		JWTSecret:         "test-secret-key-for-testing-only",
		MembershipChecker: &stubMembershipChecker{role: domain.RoleOwner},
	})
}

func TestNewRouter_DoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		r := newTestRouter()
		require.NotNil(t, r)
	})
}

func TestNewRouter_RoutesExist(t *testing.T) {
	r := newTestRouter()

	// Collect all registered routes via chi.Walk.
	type route struct {
		method  string
		pattern string
	}
	var routes []route
	err := chi.Walk(r, func(method, rt string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, route{method: method, pattern: rt})
		return nil
	})
	require.NoError(t, err)

	// Build a lookup set for quick assertions.
	set := make(map[string]bool, len(routes))
	for _, rt := range routes {
		key := fmt.Sprintf("%s %s", rt.method, rt.pattern)
		set[key] = true
	}

	// Public routes
	assert.True(t, set["GET /health/live"], "missing GET /health/live")
	assert.True(t, set["GET /health/ready"], "missing GET /health/ready")

	// Auth routes
	assert.True(t, set["POST /api/v1/auth/register"], "missing POST /api/v1/auth/register")
	assert.True(t, set["POST /api/v1/auth/login"], "missing POST /api/v1/auth/login")
	assert.True(t, set["POST /api/v1/auth/refresh"], "missing POST /api/v1/auth/refresh")
	assert.True(t, set["POST /api/v1/auth/logout"], "missing POST /api/v1/auth/logout")

	// Workspace routes
	assert.True(t, set["POST /api/v1/workspaces"], "missing POST /api/v1/workspaces")
	assert.True(t, set["GET /api/v1/workspaces"], "missing GET /api/v1/workspaces")
	assert.True(t, set["GET /api/v1/workspaces/{workspaceId}/"], "missing GET /api/v1/workspaces/{workspaceId}/")

	// Workspace members
	assert.True(t, set["POST /api/v1/workspaces/{workspaceId}/members/"], "missing POST members")
	assert.True(t, set["PATCH /api/v1/workspaces/{workspaceId}/members/{memberId}"], "missing PATCH members")
	assert.True(t, set["DELETE /api/v1/workspaces/{workspaceId}/members/{memberId}"], "missing DELETE members")

	// Seller cabinets
	assert.True(t, set["POST /api/v1/seller-cabinets/"], "missing POST /api/v1/seller-cabinets/")
	assert.True(t, set["GET /api/v1/seller-cabinets/"], "missing GET /api/v1/seller-cabinets/")
	assert.True(t, set["GET /api/v1/seller-cabinets/{id}"], "missing GET /api/v1/seller-cabinets/{id}")
	assert.True(t, set["DELETE /api/v1/seller-cabinets/{id}"], "missing DELETE /api/v1/seller-cabinets/{id}")

	// Campaigns
	assert.True(t, set["GET /api/v1/campaigns/"], "missing GET /api/v1/campaigns/")
	assert.True(t, set["GET /api/v1/campaigns/{id}"], "missing GET /api/v1/campaigns/{id}")
	assert.True(t, set["GET /api/v1/campaigns/{id}/stats"], "missing GET /api/v1/campaigns/{id}/stats")
	assert.True(t, set["GET /api/v1/campaigns/{id}/phrases"], "missing GET /api/v1/campaigns/{id}/phrases")

	// Phrases
	assert.True(t, set["GET /api/v1/phrases/{id}"], "missing GET /api/v1/phrases/{id}")
	assert.True(t, set["GET /api/v1/phrases/{id}/stats"], "missing GET /api/v1/phrases/{id}/stats")
	assert.True(t, set["GET /api/v1/phrases/{id}/bids"], "missing GET /api/v1/phrases/{id}/bids")

	// Products
	assert.True(t, set["GET /api/v1/products/"], "missing GET /api/v1/products/")
	assert.True(t, set["GET /api/v1/products/{id}"], "missing GET /api/v1/products/{id}")
	assert.True(t, set["GET /api/v1/products/{id}/positions"], "missing GET /api/v1/products/{id}/positions")

	// Positions
	assert.True(t, set["GET /api/v1/positions/"], "missing GET /api/v1/positions/")
	assert.True(t, set["GET /api/v1/positions/aggregate"], "missing GET /api/v1/positions/aggregate")

	// SERP
	assert.True(t, set["GET /api/v1/serp-snapshots/"], "missing GET /api/v1/serp-snapshots/")
	assert.True(t, set["GET /api/v1/serp-snapshots/{id}"], "missing GET /api/v1/serp-snapshots/{id}")

	// Recommendations
	assert.True(t, set["GET /api/v1/recommendations/"], "missing GET /api/v1/recommendations/")
	assert.True(t, set["PATCH /api/v1/recommendations/{id}"], "missing PATCH /api/v1/recommendations/{id}")

	// Exports
	assert.True(t, set["POST /api/v1/exports/"], "missing POST /api/v1/exports/")
	assert.True(t, set["GET /api/v1/exports/{id}"], "missing GET /api/v1/exports/{id}")
	assert.True(t, set["GET /api/v1/exports/{id}/download"], "missing GET /api/v1/exports/{id}/download")

	// Audit logs
	assert.True(t, set["GET /api/v1/audit-logs"], "missing GET /api/v1/audit-logs")

	// Extension
	assert.True(t, set["POST /api/v1/extension/sessions"], "missing POST /api/v1/extension/sessions")
	assert.True(t, set["POST /api/v1/extension/context"], "missing POST /api/v1/extension/context")
	assert.True(t, set["GET /api/v1/extension/version"], "missing GET /api/v1/extension/version")
}

func TestPublicRoutes_NoAuth(t *testing.T) {
	r := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/health/live"},
		{"GET", "/health/ready"},
		{"POST", "/api/v1/auth/register"},
		{"POST", "/api/v1/auth/login"},
		{"POST", "/api/v1/auth/refresh"},
		{"POST", "/api/v1/auth/logout"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
			req, err := http.NewRequest(tc.method, ts.URL+tc.path, nil)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// Public routes should return 501 (placeholder), NOT 401.
			assert.Equal(t, http.StatusNotImplemented, resp.StatusCode,
				"public route should not require auth")
		})
	}
}

func TestProtectedRoutes_Return401WithoutAuth(t *testing.T) {
	r := newTestRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	tests := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/workspaces"},
		{"GET", "/api/v1/workspaces"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
			req, err := http.NewRequest(tc.method, ts.URL+tc.path, nil)
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"protected route should return 401 without auth")
		})
	}
}
