package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/require"
)

type managerRouterContext struct {
	calls       int
	bidCalls    int
	budgetCalls int
}

func (s *managerRouterContext) CampaignBudgetContext(_ context.Context, identity domain.ManagerAccess, _ uuid.UUID, token, integration string, id uuid.UUID, period string) (*domain.ManagerAdCampaignBudgetContext, error) {
	s.budgetCalls++
	return &domain.ManagerAdCampaignBudgetContext{WorkspaceID: identity.ExternalWorkspaceID, CampaignID: id, IntegrationID: integration, Partial: true, ExecutionPolicy: "not_configured", AvailableActions: []string{}}, nil
}

func (s *managerRouterContext) ProductBidContext(_ context.Context, identity domain.ManagerAccess, _ uuid.UUID, token, integration string, id uuid.UUID, product, placement string) (*domain.ManagerAdProductBidContext, error) {
	s.bidCalls++
	return &domain.ManagerAdProductBidContext{CampaignID: id, IntegrationID: integration}, nil
}
func (s *managerRouterContext) CampaignContext(_ context.Context, identity domain.ManagerAccess, _ uuid.UUID, token, integration string, id uuid.UUID) (*domain.ManagerAdContext, error) {
	s.calls++
	return &domain.ManagerAdContext{WorkspaceID: identity.ExternalWorkspaceID, IntegrationID: integration, CampaignID: id, Partial: true, ExecutionPolicy: "not_configured", AvailableActions: []string{}}, nil
}

func TestManagerAdContextRouteRequiresFreshScopedAccessAndOneExplicitTarget(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "integrations.view"}}
	svc := &managerRouterContext{}
	router := NewRouter(RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier, ManagerAdContextHandler: handler.NewManagerAdContextHandler(svc)})
	url := "/api/v1/manager/ads/campaigns/019b1234-1111-7111-8111-111111111111/context?integration_id=28"
	request := func(method, url string, authorized bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, nil)
		if authorized {
			r.Header.Set("Authorization", "Bearer personal-token")
			r.Header.Set("X-Sellico-Workspace", "3")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, 200, request("GET", url, true).Code)
	require.Equal(t, 1, svc.calls)
	bidURL := strings.Replace(url, "/context?", "/bid-context?", 1) + "&product_id=202&placement=search"
	require.Equal(t, 200, request("GET", bidURL, true).Code)
	require.Equal(t, 1, svc.bidCalls)
	require.Equal(t, 400, request("GET", bidURL+"&product_id=303", true).Code)
	require.Equal(t, 400, request("GET", bidURL+"&target_bid_rub=12", true).Code)
	require.Equal(t, 400, request("GET", strings.Replace(bidURL, "&product_id=202", "", 1), true).Code)
	require.Equal(t, 401, request("GET", bidURL, false).Code)
	budgetURL := strings.Replace(url, "/context?", "/budget-context?", 1) + "&period=daily"
	require.Equal(t, 200, request("GET", budgetURL, true).Code)
	require.Equal(t, 1, svc.budgetCalls)
	for _, suffix := range []string{"&period=weekly", "&enabled=false", "&target_budget_rub=100", "&actor_id=someone"} {
		require.Equal(t, 400, request("GET", budgetURL+suffix, true).Code)
	}
	require.Equal(t, 400, request("GET", strings.Replace(budgetURL, "&period=daily", "", 1), true).Code)
	require.Equal(t, http.StatusMethodNotAllowed, request("POST", budgetURL, true).Code)
	require.Equal(t, 401, request("GET", budgetURL, false).Code)

	verifier.permissions = []string{}
	require.Equal(t, 403, request("GET", budgetURL, true).Code)
	require.Equal(t, 403, request("GET", bidURL, true).Code)
	require.Equal(t, 403, request("GET", url, true).Code)
	require.Equal(t, 1, svc.calls)
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	for _, suffix := range []string{"&integration_id=29", "&date_from=2026-11-01", "&endpoint=https://invalid.test"} {
		require.Equal(t, 400, request("GET", url+suffix, true).Code)
	}
	require.Equal(t, 400, request("GET", "/api/v1/manager/ads/campaigns/all/context?integration_id=28", true).Code)
	require.Equal(t, http.StatusMethodNotAllowed, request("POST", url, true).Code)
	require.Equal(t, 401, request("GET", url, false).Code)
	require.Equal(t, 1, svc.calls)
}
