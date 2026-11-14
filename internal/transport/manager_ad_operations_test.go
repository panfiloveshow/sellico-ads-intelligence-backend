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

type adOperationsHTTPStub struct {
	calls            int
	workspace, actor uuid.UUID
}

func (s *adOperationsHTTPStub) PrepareBudget(_ context.Context, _ domain.ManagerAccess, w, a uuid.UUID, token, integration string, campaign uuid.UUID, period string, target domain.ManagerAdBudgetTarget, caps *domain.ManagerAdBudgetConstraints) (*domain.ManagerAdOperation, error) {
	s.calls++
	s.workspace = w
	s.actor = a
	return &domain.ManagerAdOperation{ID: uuid.New(), Action: "set_budget", Status: "awaiting_confirmation"}, nil
}

func (s *adOperationsHTTPStub) PrepareBid(_ context.Context, _ domain.ManagerAccess, w, a uuid.UUID, token, integration string, campaign uuid.UUID, product, placement, bid string, limits domain.ManagerAdBidConstraints) (*domain.ManagerAdOperation, error) {
	s.calls++
	s.workspace = w
	s.actor = a
	return &domain.ManagerAdOperation{ID: uuid.New(), Action: "set_bid", Status: "awaiting_confirmation"}, nil
}
func (s *adOperationsHTTPStub) Prepare(_ context.Context, _ domain.ManagerAccess, w, a uuid.UUID, token, integration string, campaign uuid.UUID, action string) (*domain.ManagerAdOperation, error) {
	s.calls++
	s.workspace = w
	s.actor = a
	return &domain.ManagerAdOperation{ID: uuid.New(), Status: "awaiting_confirmation"}, nil
}
func (s *adOperationsHTTPStub) Approve(_ context.Context, _ domain.ManagerAccess, w, a, id uuid.UUID, token, fp string, checks map[string]bool) (*domain.ManagerAdOperation, error) {
	s.calls++
	return &domain.ManagerAdOperation{ID: id, Status: "verifying"}, nil
}
func (s *adOperationsHTTPStub) Cancel(_ context.Context, _ domain.ManagerAccess, w, a, id uuid.UUID, token, fp string) (*domain.ManagerAdOperation, error) {
	s.calls++
	return &domain.ManagerAdOperation{ID: id, Status: "cancelled"}, nil
}
func (s *adOperationsHTTPStub) Receipt(_ context.Context, _ domain.ManagerAccess, w, a, id uuid.UUID, token string) (*domain.ManagerAdOperation, error) {
	s.calls++
	return &domain.ManagerAdOperation{ID: id, Status: "unknown"}, nil
}

func TestManagerAdBudgetHTTPRequiresExactScopeExplicitModeAndStrictNominalCaps(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "marketing.manage", "integrations.view"}}
	svc := &adOperationsHTTPStub{}
	router := NewRouter(RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier, ManagerAdOperationsHandler: handler.NewManagerAdOperationsHandler(svc)})
	path := "/api/v1/manager/ads/budget-operations"
	request := func(body string, authorized bool) int {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if authorized {
			r.Header.Set("Authorization", "Bearer personal-token")
			r.Header.Set("X-Sellico-Workspace", "3")
			r.Header.Set("X-User-Id", "spoofed")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	good := `{"integration_id":"28","campaign_id":"019b1234-1111-7111-8111-111111111111","period":"daily","target":{"enabled":true,"amount_rub":"1200.01","carry_over_enabled":false},"constraints":{"max_budget_rub":"2000","max_absolute_change_rub":"300"}}`
	require.Equal(t, 200, request(good, true))
	require.Equal(t, auth.user, svc.actor)
	require.Equal(t, auth.workspace, svc.workspace)
	for _, bad := range []string{strings.Replace(good, `"enabled":true,`, "", 1), strings.Replace(good, `"1200.01"`, `1200.01`, 1), strings.Replace(good, `"daily"`, `"total"`, 1), strings.Replace(good, `"max_budget_rub"`, `"max_profit_rub"`, 1), strings.TrimSuffix(good, "}") + `,"actor_id":"spoofed"}`} {
		require.Equal(t, 400, request(bad, true))
	}
	off := `{"integration_id":"28","campaign_id":"019b1234-1111-7111-8111-111111111111","period":"weekly","target":{"enabled":false,"amount_rub":null,"carry_over_enabled":null},"constraints":null}`
	require.Equal(t, 200, request(off, true))
	require.Equal(t, 400, request(strings.Replace(off, `"amount_rub":null`, `"amount_rub":"0"`, 1), true))
	require.Equal(t, 401, request(good, false))
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	require.Equal(t, 403, request(good, true))
	require.Equal(t, 2, svc.calls)
}
func TestManagerAdOperationsHTTPUsesAuthenticatedScopeAndFreshManagePermission(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "marketing.manage", "integrations.view"}}
	svc := &adOperationsHTTPStub{}
	router := NewRouter(RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier, ManagerAdOperationsHandler: handler.NewManagerAdOperationsHandler(svc)})
	request := func(method, url, body string, authorized bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		if authorized {
			r.Header.Set("Authorization", "Bearer personal-token")
			r.Header.Set("X-Sellico-Workspace", "3")
			r.Header.Set("X-User-Id", "spoofed")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/manager/ads/operations"
	body := `{"integration_id":"28","campaign_id":"019b1234-1111-7111-8111-111111111111","action":"pause"}`
	require.Equal(t, 200, request("POST", path, body, true).Code)
	require.Equal(t, auth.user, svc.actor)
	require.Equal(t, auth.workspace, svc.workspace)
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	require.Equal(t, 403, request("POST", path, body, true).Code)
	verifier.permissions = append(verifier.permissions, "marketing.manage")
	for _, invalid := range []string{`{"integration_id":"28","campaign_id":"bad","action":"pause"}`, `{"integration_id":"28","campaign_id":"019b1234-1111-7111-8111-111111111111","action":"activate"}`, strings.TrimSuffix(body, "}") + `,"actor_id":"caller"}`} {
		require.Equal(t, 400, request("POST", path, invalid, true).Code)
	}
	require.Equal(t, 400, request("POST", path+"?workspace_id=4", body, true).Code)
	require.Equal(t, 1, svc.calls)
	bidBody := `{"integration_id":"28","campaign_id":"019b1234-1111-7111-8111-111111111111","product_id":"202","placement":"search","target_bid_rub":"12.35","constraints":{"max_bid_rub":"20","max_change_percent":"100"}}`
	require.Equal(t, 200, request("POST", "/api/v1/manager/ads/bid-operations", bidBody, true).Code)
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	require.Equal(t, 403, request("POST", "/api/v1/manager/ads/bid-operations", bidBody, true).Code)
	verifier.permissions = append(verifier.permissions, "marketing.manage")
	for _, bad := range []string{strings.Replace(bidBody, `"20"`, `20`, 1), strings.Replace(bidBody, `"search"`, `"combined"`, 1), strings.TrimSuffix(bidBody, "}") + `,"actor_id":"spoof"}`} {
		require.Equal(t, 400, request("POST", "/api/v1/manager/ads/bid-operations", bad, true).Code)
	}

	for _, kind := range []string{"resume", "handover", "launch"} {
		require.Equal(t, 200, request("POST", path, strings.Replace(body, `"pause"`, `"`+kind+`"`, 1), true).Code)
	}

	id := "019b1234-1111-7111-8111-111111111111"
	approve := `{"fingerprint":"` + strings.Repeat("a", 64) + `","confirmations":{"campaign_checked":true,"pause_impact_checked":true}}`
	require.Equal(t, 200, request("POST", path+"/"+id+"/approve", approve, true).Code)
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	require.Equal(t, 403, request("POST", path+"/"+id+"/approve", approve, true).Code)
	require.Equal(t, 200, request("GET", path+"/"+id, "", true).Code)
	require.Equal(t, 401, request("GET", path+"/"+id, "", false).Code)
}
