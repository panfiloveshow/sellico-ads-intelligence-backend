package transport

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/require"
)

type priceExecutionControlHTTPStub struct {
	calls            int
	actor, workspace uuid.UUID
}

func (s *priceExecutionControlHTTPStub) Context(context.Context, domain.ManagerAccess, uuid.UUID, string, string, string) (*domain.PriceExecutionControlContext, error) {
	s.calls++
	return &domain.PriceExecutionControlContext{Scope: "configured_price_account"}, nil
}
func (s *priceExecutionControlHTTPStub) Prepare(_ context.Context, _ domain.ManagerAccess, w, a uuid.UUID, _ string, _ string, _ string, _ string, _ string, _ string) (*domain.PriceExecutionControlOperation, error) {
	s.calls++
	s.actor = a
	s.workspace = w
	return &domain.PriceExecutionControlOperation{ID: uuid.New(), Status: "awaiting_confirmation"}, nil
}
func (s *priceExecutionControlHTTPStub) Approve(_ context.Context, _ domain.ManagerAccess, w, a, id uuid.UUID, _ string, _ string, _ map[string]bool) (*domain.PriceExecutionControlOperation, error) {
	s.calls++
	s.actor = a
	s.workspace = w
	return &domain.PriceExecutionControlOperation{ID: id, Status: "succeeded"}, nil
}
func (s *priceExecutionControlHTTPStub) Cancel(_ context.Context, _ domain.ManagerAccess, _ uuid.UUID, _ uuid.UUID, id uuid.UUID, _ string, _ string) (*domain.PriceExecutionControlOperation, error) {
	s.calls++
	return &domain.PriceExecutionControlOperation{ID: id, Status: "cancelled"}, nil
}
func (s *priceExecutionControlHTTPStub) Receipt(_ context.Context, _ domain.ManagerAccess, _ uuid.UUID, _ uuid.UUID, id uuid.UUID, _ string) (*domain.PriceExecutionControlOperation, error) {
	s.calls++
	return &domain.PriceExecutionControlOperation{ID: id, Status: "succeeded"}, nil
}

func TestPriceExecutionControlHTTPStrictScopeAndFreshCanonicalActor(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "marketing.manage", "integrations.view", "products.view", "unit_economics.view", "unit_economics.calculate"}}
	svc := &priceExecutionControlHTTPStub{}
	router := NewRouter(RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier, ManagerPriceExecutionControlHandler: handler.NewManagerPriceExecutionControlHandler(svc)})
	request := func(method, path, body string, authorized bool) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if authorized {
			req.Header.Set("Authorization", "Bearer personal-token")
			req.Header.Set("X-Sellico-Workspace", "3")
			req.Header.Set("X-User-Id", "spoofed-user")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}
	path := "/api/v1/manager/prices/execution-controls"
	good := `{"integration_id":"28","resource_id":"101","target_mode":"stopped","reason":"Личная проверка","context_fingerprint":"` + strings.Repeat("a", 64) + `"}`
	require.Equal(t, 200, request("POST", path, good, true))
	require.Equal(t, auth.user, svc.actor)
	require.Equal(t, auth.workspace, svc.workspace)
	before := svc.calls
	for _, bad := range []string{strings.TrimSuffix(good, "}") + `,"actor_id":"spoofed"}`, strings.TrimSuffix(good, "}") + `,"account_key":"other"}`, strings.Replace(good, `"stopped"`, `"legacy"`, 1), strings.Replace(good, `"28"`, `28`, 1), good + `{}`, `null`, `[]`} {
		require.Equal(t, 400, request("POST", path, bad, true))
	}
	require.Equal(t, before, svc.calls)
	require.Equal(t, 401, request("POST", path, good, false))
	verifier.permissions = []string{"marketing.view", "integrations.view", "products.view", "unit_economics.view"}
	require.Equal(t, 403, request("POST", path, good, true))
	require.Equal(t, before, svc.calls)
	require.Equal(t, 200, request("GET", path+"/context?integration_id=28&resource_id=101", "", true))
	require.Equal(t, 400, request("GET", path+"/context?integration_id=28&integration_id=29&resource_id=101", "", true))
}
func TestPriceExecutionControlHTTPMutationsAndReadbackHaveSeparateStrictBodies(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "marketing.manage", "integrations.view", "products.view", "unit_economics.view", "unit_economics.calculate"}}
	svc := &priceExecutionControlHTTPStub{}
	router := NewRouter(RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier, ManagerPriceExecutionControlHandler: handler.NewManagerPriceExecutionControlHandler(svc)})
	path := "/api/v1/manager/prices/execution-controls/60000000-0000-4000-8000-000000000002"
	request := func(method, url, body string) int {
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer personal-token")
		r.Header.Set("X-Sellico-Workspace", "3")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 403 {
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
		}
		return w.Code
	}
	fp := strings.Repeat("a", 64)
	require.Equal(t, 200, request("POST", path+"/approve", `{"fingerprint":"`+fp+`","confirmations":{"price_account_scope_checked":true,"price_service_effect_checked":true,"price_reopening_checked":true}}`))
	require.Equal(t, auth.user, svc.actor)
	require.Equal(t, 400, request("POST", path+"/approve", `{"fingerprint":"`+fp+`","account_key":"spoofed"}`))
	require.Equal(t, 400, request("POST", path+"/cancel", `{"fingerprint":"`+fp+`","confirmations":{}}`))
	verifier.permissions = []string{"marketing.view", "integrations.view", "products.view", "unit_economics.view"}
	require.Equal(t, 403, request("POST", path+"/approve", `{"fingerprint":"`+fp+`"}`))
	require.Equal(t, 200, request("GET", path, ""))
	require.Equal(t, 200, request("POST", path+"/cancel", `{"fingerprint":"`+fp+`"}`))
	require.Equal(t, 400, request("GET", path+"?actor_id=other", ""))
}
