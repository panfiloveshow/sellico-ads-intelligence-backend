package transport

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type managerRouterAuth struct{ user, workspace uuid.UUID }

func (a managerRouterAuth) Authenticate(_ context.Context, token string) (*domain.AuthPrincipal, error) {
	if token != "personal-token" {
		return nil, errors.New("invalid token")
	}
	return &domain.AuthPrincipal{UserID: a.user, ExternalUserID: "42", Token: token}, nil
}
func (a managerRouterAuth) ResolveWorkspaceAccess(_ context.Context, _ domain.AuthPrincipal, raw string) (*domain.WorkspaceAccess, error) {
	if raw != "3" {
		return nil, errors.New("foreign workspace")
	}
	return &domain.WorkspaceAccess{WorkspaceID: a.workspace, Role: domain.RoleOwner}, nil
}

type managerRouterVerifier struct {
	permissions []string
	calls       int
}

func (v *managerRouterVerifier) VerifyManagerAccess(_ context.Context, token, workspace string) (*domain.ManagerAccess, error) {
	v.calls++
	return &domain.ManagerAccess{ExternalUserID: "42", ExternalWorkspaceID: workspace, Permissions: v.permissions}, nil
}

type managerRouterReport struct{ calls int }

func (s *managerRouterReport) Campaigns(_ context.Context, identity domain.ManagerAccess, _ uuid.UUID, token, id string, from, to time.Time, page, limit int, compare bool) (*domain.ManagerAdsReport, error) {
	s.calls++
	return &domain.ManagerAdsReport{WorkspaceID: identity.ExternalWorkspaceID, IntegrationID: id, Period: domain.ManagerAdsPeriod{DateFrom: from.Format(time.DateOnly), DateTo: to.Format(time.DateOnly)}, Items: []domain.ManagerAdsCampaign{}, Page: page, Limit: limit}, nil
}

func TestManagerAdsRouteUsesFreshPermissionsAfterAuthentication(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "integrations.view"}}
	report := &managerRouterReport{}
	router := NewRouter(RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier, ManagerAdsHandler: handler.NewManagerAdsHandler(report)})
	request := func(method, url string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, nil)
		r.Header.Set("Authorization", "Bearer personal-token")
		r.Header.Set("X-Sellico-Workspace", "3")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	url := "/api/v1/manager/ads/campaigns?integration_id=28&date_from=2026-10-01&date_to=2026-10-02"
	require.Equal(t, 200, request(http.MethodGet, url).Code)
	assert.Equal(t, 1, report.calls)
	verifier.permissions = []string{}
	assert.Equal(t, 403, request(http.MethodGet, url).Code)
	assert.Equal(t, 1, report.calls)
	assert.Equal(t, 2, verifier.calls)
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	for _, query := range []string{"&integration_id=29", "&date_from=2026-02-30", "&unknown=1", "&limit=101", "&compare=yes"} {
		assert.Equal(t, 400, request(http.MethodGet, url+query).Code)
	}
	assert.Equal(t, 1, report.calls)
	assert.Equal(t, 405, request(http.MethodPost, url).Code)
	r := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(t, 401, w.Code)
}
