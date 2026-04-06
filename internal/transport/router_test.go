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
	assert.True(t, set["GET /openapi.yaml"], "missing GET /openapi.yaml")
	assert.True(t, set["GET /docs"], "missing GET /docs")
	assert.True(t, set["GET /health/live"], "missing GET /health/live")
	assert.True(t, set["GET /health/ready"], "missing GET /health/ready")

	// Auth routes
	assert.True(t, set["POST /api/v1/auth/register"], "missing POST /api/v1/auth/register")
	assert.True(t, set["POST /api/v1/auth/login"], "missing POST /api/v1/auth/login")
	assert.True(t, set["POST /api/v1/auth/refresh"], "missing POST /api/v1/auth/refresh")
	assert.True(t, set["POST /api/v1/auth/logout"], "missing POST /api/v1/auth/logout")
	assert.True(t, set["GET /api/v1/auth/me"], "missing GET /api/v1/auth/me")

	// Workspace routes
	assert.True(t, set["POST /api/v1/workspaces"], "missing POST /api/v1/workspaces")
	assert.True(t, set["GET /api/v1/workspaces"], "missing GET /api/v1/workspaces")
	assert.True(t, set["GET /api/v1/workspaces/{workspaceId}/"], "missing GET /api/v1/workspaces/{workspaceId}/")

	// Workspace members
	assert.True(t, set["GET /api/v1/workspaces/{workspaceId}/members/"], "missing GET members")
	assert.True(t, set["POST /api/v1/workspaces/{workspaceId}/members/"], "missing POST members")
	assert.True(t, set["PATCH /api/v1/workspaces/{workspaceId}/members/{memberId}"], "missing PATCH members")
	assert.True(t, set["DELETE /api/v1/workspaces/{workspaceId}/members/{memberId}"], "missing DELETE members")

	// Seller cabinets
	assert.True(t, set["POST /api/v1/seller-cabinets/"], "missing POST /api/v1/seller-cabinets/")
	assert.True(t, set["GET /api/v1/seller-cabinets/"], "missing GET /api/v1/seller-cabinets/")
	assert.True(t, set["GET /api/v1/seller-cabinets/{id}"], "missing GET /api/v1/seller-cabinets/{id}")
	assert.True(t, set["GET /api/v1/seller-cabinets/{id}/campaigns"], "missing GET /api/v1/seller-cabinets/{id}/campaigns")
	assert.True(t, set["GET /api/v1/seller-cabinets/{id}/products"], "missing GET /api/v1/seller-cabinets/{id}/products")
	assert.True(t, set["POST /api/v1/seller-cabinets/{id}/sync"], "missing POST /api/v1/seller-cabinets/{id}/sync")
	assert.True(t, set["DELETE /api/v1/seller-cabinets/{id}"], "missing DELETE /api/v1/seller-cabinets/{id}")
	assert.True(t, set["POST /api/v1/cabinets/"], "missing POST /api/v1/cabinets/")
	assert.True(t, set["GET /api/v1/cabinets/"], "missing GET /api/v1/cabinets/")
	assert.True(t, set["GET /api/v1/cabinets/{id}"], "missing GET /api/v1/cabinets/{id}")
	assert.True(t, set["GET /api/v1/cabinets/{id}/campaigns"], "missing GET /api/v1/cabinets/{id}/campaigns")
	assert.True(t, set["GET /api/v1/cabinets/{id}/products"], "missing GET /api/v1/cabinets/{id}/products")
	assert.True(t, set["POST /api/v1/cabinets/{id}/sync"], "missing POST /api/v1/cabinets/{id}/sync")
	assert.True(t, set["DELETE /api/v1/cabinets/{id}"], "missing DELETE /api/v1/cabinets/{id}")

	// Campaigns
	assert.True(t, set["GET /api/v1/campaigns/"], "missing GET /api/v1/campaigns/")
	assert.True(t, set["GET /api/v1/campaigns/{id}"], "missing GET /api/v1/campaigns/{id}")
	assert.True(t, set["GET /api/v1/campaigns/{id}/stats"], "missing GET /api/v1/campaigns/{id}/stats")
	assert.True(t, set["GET /api/v1/campaigns/{id}/daily-stats"], "missing GET /api/v1/campaigns/{id}/daily-stats")
	assert.True(t, set["GET /api/v1/campaigns/{id}/phrases"], "missing GET /api/v1/campaigns/{id}/phrases")
	assert.True(t, set["GET /api/v1/campaigns/{id}/recommendations"], "missing GET /api/v1/campaigns/{id}/recommendations")

	// Phrases
	assert.True(t, set["GET /api/v1/phrases/"], "missing GET /api/v1/phrases/")
	assert.True(t, set["GET /api/v1/phrases/{id}"], "missing GET /api/v1/phrases/{id}")
	assert.True(t, set["GET /api/v1/phrases/{id}/stats"], "missing GET /api/v1/phrases/{id}/stats")
	assert.True(t, set["GET /api/v1/phrases/{id}/daily-stats"], "missing GET /api/v1/phrases/{id}/daily-stats")
	assert.True(t, set["GET /api/v1/phrases/{id}/bids"], "missing GET /api/v1/phrases/{id}/bids")
	assert.True(t, set["GET /api/v1/phrases/{id}/recommendations"], "missing GET /api/v1/phrases/{id}/recommendations")

	// Bids
	assert.True(t, set["POST /api/v1/bids/history"], "missing POST /api/v1/bids/history")
	assert.True(t, set["GET /api/v1/bids/history"], "missing GET /api/v1/bids/history")
	assert.True(t, set["GET /api/v1/bids/estimates"], "missing GET /api/v1/bids/estimates")

	// Products
	assert.True(t, set["GET /api/v1/products/"], "missing GET /api/v1/products/")
	assert.True(t, set["GET /api/v1/products/{id}"], "missing GET /api/v1/products/{id}")
	assert.True(t, set["GET /api/v1/products/{id}/positions"], "missing GET /api/v1/products/{id}/positions")
	assert.True(t, set["GET /api/v1/products/{id}/recommendations"], "missing GET /api/v1/products/{id}/recommendations")

	// Positions
	assert.True(t, set["POST /api/v1/positions/targets"], "missing POST /api/v1/positions/targets")
	assert.True(t, set["GET /api/v1/positions/targets"], "missing GET /api/v1/positions/targets")
	assert.True(t, set["POST /api/v1/positions/"], "missing POST /api/v1/positions/")
	assert.True(t, set["GET /api/v1/positions/"], "missing GET /api/v1/positions/")
	assert.True(t, set["GET /api/v1/positions/history"], "missing GET /api/v1/positions/history")
	assert.True(t, set["GET /api/v1/positions/aggregate"], "missing GET /api/v1/positions/aggregate")

	// SERP
	assert.True(t, set["POST /api/v1/serp-snapshots/"], "missing POST /api/v1/serp-snapshots/")
	assert.True(t, set["GET /api/v1/serp-snapshots/"], "missing GET /api/v1/serp-snapshots/")
	assert.True(t, set["GET /api/v1/serp-snapshots/{id}"], "missing GET /api/v1/serp-snapshots/{id}")
	assert.True(t, set["GET /api/v1/serp/history"], "missing GET /api/v1/serp/history")
	assert.True(t, set["GET /api/v1/serp/{id}"], "missing GET /api/v1/serp/{id}")

	// Recommendations
	assert.True(t, set["GET /api/v1/recommendations/"], "missing GET /api/v1/recommendations/")
	assert.True(t, set["POST /api/v1/recommendations/generate"], "missing POST /api/v1/recommendations/generate")
	assert.True(t, set["PATCH /api/v1/recommendations/{id}"], "missing PATCH /api/v1/recommendations/{id}")
	assert.True(t, set["POST /api/v1/recommendations/{id}/resolve"], "missing POST /api/v1/recommendations/{id}/resolve")
	assert.True(t, set["POST /api/v1/recommendations/{id}/dismiss"], "missing POST /api/v1/recommendations/{id}/dismiss")

	// Exports
	assert.True(t, set["GET /api/v1/exports/"], "missing GET /api/v1/exports/")
	assert.True(t, set["POST /api/v1/exports/"], "missing POST /api/v1/exports/")
	assert.True(t, set["GET /api/v1/exports/{id}"], "missing GET /api/v1/exports/{id}")
	assert.True(t, set["GET /api/v1/exports/{id}/download"], "missing GET /api/v1/exports/{id}/download")

	// Audit logs
	assert.True(t, set["GET /api/v1/audit-logs"], "missing GET /api/v1/audit-logs")

	// Job runs
	assert.True(t, set["GET /api/v1/job-runs/"], "missing GET /api/v1/job-runs/")
	assert.True(t, set["GET /api/v1/job-runs/{id}"], "missing GET /api/v1/job-runs/{id}")
	assert.True(t, set["POST /api/v1/job-runs/{id}/retry"], "missing POST /api/v1/job-runs/{id}/retry")

	// Extension
	assert.True(t, set["POST /api/v1/extension/sessions"], "missing POST /api/v1/extension/sessions")
	assert.True(t, set["POST /api/v1/extension/session/start"], "missing POST /api/v1/extension/session/start")
	assert.True(t, set["POST /api/v1/extension/context"], "missing POST /api/v1/extension/context")
	assert.True(t, set["POST /api/v1/extension/page-context"], "missing POST /api/v1/extension/page-context")
	assert.True(t, set["POST /api/v1/extension/bid-snapshots"], "missing POST /api/v1/extension/bid-snapshots")
	assert.True(t, set["POST /api/v1/extension/position-snapshots"], "missing POST /api/v1/extension/position-snapshots")
	assert.True(t, set["POST /api/v1/extension/ui-signals"], "missing POST /api/v1/extension/ui-signals")
	assert.True(t, set["POST /api/v1/extension/network-captures/batch"], "missing POST /api/v1/extension/network-captures/batch")
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
		{"GET", "/openapi.yaml"},
		{"GET", "/docs"},
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
		{"GET", "/api/v1/auth/me"},
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
