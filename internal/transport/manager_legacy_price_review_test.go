package transport

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/require"
)

type legacyPriceReviewHTTPStub struct {
	calls            int
	actor, workspace uuid.UUID
	command          uuid.UUID
	cursor           string
	checks           map[string]bool
	err              error
}

func (s *legacyPriceReviewHTTPStub) Context(_ context.Context, _ domain.ManagerAccess, _ uuid.UUID, _, _, _, cursor string) (*domain.LegacyPriceJournalContext, error) {
	s.calls++
	s.cursor = cursor
	return &domain.LegacyPriceJournalContext{Version: "legacy_price_journal_v1", Scope: "configured_price_account_legacy_review", Items: []domain.LegacyPriceJournalItem{}}, s.err
}
func (s *legacyPriceReviewHTTPStub) Prepare(_ context.Context, _ domain.ManagerAccess, w, a uuid.UUID, _, _, _ string, command uuid.UUID, _, _, _ string) (*domain.LegacyPriceReviewOperation, error) {
	s.calls++
	s.actor, s.workspace, s.command = a, w, command
	return &domain.LegacyPriceReviewOperation{ID: uuid.New(), Status: "awaiting_confirmation"}, s.err
}
func (s *legacyPriceReviewHTTPStub) Approve(_ context.Context, _ domain.ManagerAccess, w, a, id uuid.UUID, _, _ string, checks map[string]bool) (*domain.LegacyPriceReviewOperation, error) {
	s.calls++
	s.actor, s.workspace, s.checks = a, w, checks
	if s.err != nil {
		return nil, s.err
	}
	return &domain.LegacyPriceReviewOperation{ID: id, Status: "resolved_manually"}, nil
}
func (s *legacyPriceReviewHTTPStub) Cancel(_ context.Context, _ domain.ManagerAccess, _, _, id uuid.UUID, _, _ string) (*domain.LegacyPriceReviewOperation, error) {
	s.calls++
	return &domain.LegacyPriceReviewOperation{ID: id, Status: "cancelled"}, s.err
}
func (s *legacyPriceReviewHTTPStub) Receipt(_ context.Context, _ domain.ManagerAccess, _, _, id uuid.UUID, _ string) (*domain.LegacyPriceReviewOperation, error) {
	s.calls++
	return &domain.LegacyPriceReviewOperation{ID: id, Status: "resolved_manually"}, s.err
}

func legacyPriceReviewRouter(svc *legacyPriceReviewHTTPStub) (managerRouterAuth, *managerRouterVerifier, func(method, path, body string, authorized bool) *httptest.ResponseRecorder) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "marketing.manage", "integrations.view", "products.view", "unit_economics.view", "unit_economics.calculate"}}
	deps := RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier}
	if svc != nil {
		deps.ManagerLegacyPriceReviewHandler = handler.NewManagerLegacyPriceReviewHandler(svc)
	}
	router := NewRouter(deps)
	return auth, verifier, func(method, path, body string, authorized bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if authorized {
			req.Header.Set("Authorization", "Bearer personal-token")
			req.Header.Set("X-Sellico-Workspace", "3")
			req.Header.Set("X-User-Id", "spoofed-user")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
}

func TestLegacyPriceReviewHTTPStrictJournalScopeAndFreshCanonicalActor(t *testing.T) {
	svc := &legacyPriceReviewHTTPStub{}
	auth, verifier, request := legacyPriceReviewRouter(svc)
	command := "60000000-0000-4000-8000-000000000009"
	path := "/api/v1/manager/prices/legacy-reviews"
	good := `{"integration_id":"28","resource_id":"101","command_id":"` + command + `","context_fingerprint":"` + strings.Repeat("a", 64) + `","reason":"Личная проверка","support_reference":"Обращение в поддержку №TEST-0001"}`
	response := request("POST", path, good, true)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	require.Equal(t, auth.user, svc.actor)
	require.Equal(t, auth.workspace, svc.workspace)
	require.Equal(t, command, svc.command.String())
	before := svc.calls
	for _, bad := range []string{
		strings.TrimSuffix(good, "}") + `,"actor_id":"spoofed"}`,
		strings.TrimSuffix(good, "}") + `,"account_key":"other"}`,
		strings.TrimSuffix(good, "}") + `,"native_outcome_verified":true}`,
		strings.Replace(good, command, "not-a-uuid", 1),
		strings.Replace(good, command, uuid.Nil.String(), 1),
		strings.Replace(good, `"28"`, `28`, 1),
		strings.Replace(good, strings.Repeat("a", 64), strings.Repeat("A", 64), 1),
		good + `{}`, `null`, `[]`, ``,
	} {
		require.Equal(t, 400, request("POST", path, bad, true).Code, bad)
	}
	require.Equal(t, 400, request("POST", path+"?integration_id=28", good, true).Code)
	require.Equal(t, before, svc.calls)
	require.Equal(t, 401, request("POST", path, good, false).Code)

	journal := "/api/v1/manager/prices/legacy-journal/context"
	require.Equal(t, 200, request("GET", journal+"?integration_id=28&resource_id=101", "", true).Code)
	require.Empty(t, svc.cursor)
	require.Equal(t, 200, request("GET", journal+"?integration_id=28&resource_id=101&cursor=abc_DEF-123", "", true).Code)
	require.Equal(t, "abc_DEF-123", svc.cursor)
	before = svc.calls
	for _, bad := range []string{
		"?integration_id=28", "?resource_id=101", "?integration_id=28&integration_id=29&resource_id=101",
		"?integration_id=28&resource_id=101&cursor=", "?integration_id=28&resource_id=101&cursor=" + strings.Repeat("a", 513),
		"?integration_id=28&resource_id=101&account_key=other", "?integration_id=0&resource_id=101", "?integration_id=28&resource_id=-1",
		"?integration_id=28&resource_id=101&cursor=a&cursor=b",
	} {
		require.Equal(t, 400, request("GET", journal+bad, "", true).Code, bad)
	}
	require.Equal(t, before, svc.calls)

	// Reading stays available without the write permissions; preparing does not.
	verifier.permissions = []string{"marketing.view", "integrations.view", "products.view", "unit_economics.view"}
	require.Equal(t, 200, request("GET", journal+"?integration_id=28&resource_id=101", "", true).Code)
	require.Equal(t, 403, request("POST", path, good, true).Code)
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	require.Equal(t, 403, request("GET", journal+"?integration_id=28&resource_id=101", "", true).Code)
}

func TestLegacyPriceReviewHTTPDecisionsHaveSeparateStrictBodiesAndConflictStatus(t *testing.T) {
	svc := &legacyPriceReviewHTTPStub{}
	auth, verifier, request := legacyPriceReviewRouter(svc)
	path := "/api/v1/manager/prices/legacy-reviews/60000000-0000-4000-8000-000000000002"
	fp := strings.Repeat("a", 64)
	checks := `{"legacy_history_and_support_checked":true,"legacy_no_native_work_in_progress":true,"legacy_late_effect_risk_accepted":true,"legacy_new_approvals_only":true}`
	approved := request("POST", path+"/approve", `{"fingerprint":"`+fp+`","confirmations":`+checks+`}`, true)
	require.Equal(t, 200, approved.Code)
	require.Contains(t, approved.Header().Get("Cache-Control"), "no-store")
	require.Contains(t, approved.Body.String(), `"native_outcome_verified":false`)
	require.Contains(t, approved.Body.String(), `"native_prices_changed":false`)
	require.Equal(t, auth.user, svc.actor)
	require.Len(t, svc.checks, 4)
	before := svc.calls
	for _, bad := range []string{
		`{"fingerprint":"` + fp + `","account_key":"spoofed"}`,
		`{"fingerprint":"` + fp + `","confirmations":` + checks + `,"native_outcome_verified":true}`,
		`{"fingerprint":"short","confirmations":` + checks + `}`,
		`{"fingerprint":"` + fp + `","confirmations":{"legacy_history_and_support_checked":"yes"}}`,
		`{"fingerprint":"` + fp + `","confirmations":` + checks + `}{}`,
	} {
		require.Equal(t, 400, request("POST", path+"/approve", bad, true).Code, bad)
	}
	require.Equal(t, 400, request("POST", path+"/cancel", `{"fingerprint":"`+fp+`","confirmations":{}}`, true).Code)
	require.Equal(t, 400, request("GET", path+"?actor_id=other", "", true).Code)
	require.Equal(t, 400, request("GET", "/api/v1/manager/prices/legacy-reviews/not-a-uuid", "", true).Code)
	require.Equal(t, 400, request("POST", "/api/v1/manager/prices/legacy-reviews/"+uuid.Nil.String()+"/approve", `{"fingerprint":"`+fp+`","confirmations":`+checks+`}`, true).Code)
	require.Equal(t, before, svc.calls)

	for _, changed := range []error{sqlcgen.ErrLegacyPriceReviewChanged, sqlcgen.ErrPriceExecutionAccountHeld, sqlcgen.ErrPriceExecutionControlChanged, sqlcgen.ErrLegacyPriceActiveWrite, sqlcgen.ErrLegacyPriceBindingUnknown} {
		svc.err = changed
		conflict := request("POST", path+"/approve", `{"fingerprint":"`+fp+`","confirmations":`+checks+`}`, true)
		require.Equal(t, 409, conflict.Code, changed.Error())
		require.Contains(t, conflict.Body.String(), "REVIEW_CHANGED")
		require.NotContains(t, conflict.Body.String(), changed.Error(), "storage errors are not echoed")
	}
	svc.err = nil

	verifier.permissions = []string{"marketing.view", "integrations.view", "products.view", "unit_economics.view"}
	require.Equal(t, 403, request("POST", path+"/approve", `{"fingerprint":"`+fp+`","confirmations":`+checks+`}`, true).Code)
	require.Equal(t, 200, request("GET", path, "", true).Code)
	require.Equal(t, 200, request("POST", path+"/cancel", `{"fingerprint":"`+fp+`"}`, true).Code)
	require.Equal(t, 401, request("GET", path, "", false).Code)
}

func TestLegacyPriceReviewRoutesStayClosedWithoutTheirService(t *testing.T) {
	_, _, request := legacyPriceReviewRouter(nil)
	id := "60000000-0000-4000-8000-000000000002"
	for route, method := range map[string]string{
		"/api/v1/manager/prices/legacy-journal/context?integration_id=28&resource_id=101": "GET",
		"/api/v1/manager/prices/legacy-reviews/" + id:                                     "GET",
		"/api/v1/manager/prices/legacy-reviews":                                           "POST",
		"/api/v1/manager/prices/legacy-reviews/" + id + "/approve":                        "POST",
		"/api/v1/manager/prices/legacy-reviews/" + id + "/cancel":                         "POST",
	} {
		require.Equal(t, 501, request(method, route, `{}`, true).Code, route)
	}
}
