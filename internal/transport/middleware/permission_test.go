package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
	"github.com/stretchr/testify/assert"
)

// fakeChecker records the last call and returns canned results.
type fakeChecker struct {
	allowed  bool
	err      error
	lastSlug string
}

func (f *fakeChecker) HasPermission(_ context.Context, _, _, _, permission string) (bool, error) {
	f.lastSlug = permission
	return f.allowed, f.err
}

// withPrincipalAndWorkspace injects a shared-auth principal and workspace ref.
func withPrincipalAndWorkspace(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), PrincipalKey, AuthPrincipal{
		ExternalUserID: "42",
		Token:          "user-token",
	})
	ctx = context.WithValue(ctx, WorkspaceRefKey, "7")
	return r.WithContext(ctx)
}

func TestRequirePermission_Allowed(t *testing.T) {
	checker := &fakeChecker{allowed: true}
	h, called := rbacDummyHandler()
	rec := httptest.NewRecorder()
	req := withPrincipalAndWorkspace(httptest.NewRequest(http.MethodPost, "/x", nil))

	RequirePermission(checker, "marketing.manage")(h).ServeHTTP(rec, req)

	assert.True(t, *called)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "marketing.manage", checker.lastSlug)
}

func TestRequirePermission_Denied(t *testing.T) {
	checker := &fakeChecker{allowed: false}
	h, called := rbacDummyHandler()
	rec := httptest.NewRecorder()
	req := withPrincipalAndWorkspace(httptest.NewRequest(http.MethodPost, "/x", nil))

	RequirePermission(checker, "marketing.manage")(h).ServeHTTP(rec, req)

	assert.False(t, *called)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "marketing.manage")
}

func TestRequirePermission_CheckerError_503(t *testing.T) {
	checker := &fakeChecker{err: errors.New("crm down")}
	h, called := rbacDummyHandler()
	rec := httptest.NewRecorder()
	req := withPrincipalAndWorkspace(httptest.NewRequest(http.MethodGet, "/x", nil))

	RequirePermission(checker, "marketing.view")(h).ServeHTTP(rec, req)

	assert.False(t, *called)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestRequirePermission_NilChecker_FallsBackToRoles(t *testing.T) {
	h, called := rbacDummyHandler()

	// viewer может читать
	rec := httptest.NewRecorder()
	req := withRole(httptest.NewRequest(http.MethodGet, "/x", nil), domain.RoleViewer)
	RequirePermission(nil, "marketing.view")(h).ServeHTTP(rec, req)
	assert.True(t, *called)
	assert.Equal(t, http.StatusOK, rec.Code)

	// viewer не может писать
	h2, called2 := rbacDummyHandler()
	rec = httptest.NewRecorder()
	req = withRole(httptest.NewRequest(http.MethodPost, "/x", nil), domain.RoleViewer)
	RequirePermission(nil, "marketing.manage")(h2).ServeHTTP(rec, req)
	assert.False(t, *called2)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequirePermission_ExtensionToken_FallsBackToRoles(t *testing.T) {
	checker := &fakeChecker{allowed: false} // CRM сказал бы "нет" — но у extension свой путь
	h, called := rbacDummyHandler()
	rec := httptest.NewRecorder()

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	ctx := context.WithValue(req.Context(), TokenClaimsKey, &jwt.TokenClaims{TokenType: "extension"})
	ctx = context.WithValue(ctx, MemberRoleKey, domain.RoleManager)
	req = req.WithContext(ctx)

	RequirePermission(checker, "marketing.manage")(h).ServeHTTP(rec, req)

	assert.True(t, *called)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, checker.lastSlug, "CRM не должен вызываться для локальных JWT")
}
