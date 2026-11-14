package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
	"github.com/stretchr/testify/assert"
)

type managerVerifierStub struct {
	access *domain.ManagerAccess
	err    error
	calls  int
}

func (v *managerVerifierStub) VerifyManagerAccess(_ context.Context, token, workspace string) (*domain.ManagerAccess, error) {
	v.calls++
	if token != "personal-token" || workspace != "3" {
		return nil, errors.New("untrusted identity was used")
	}
	return v.access, v.err
}

func managerRequest() *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/manager/prepare?user_id=999", nil)
	request.Header.Set("X-Sellico-Workspace", "3")
	request.Header.Set("X-Account-Id", "3")
	request.Header.Set("X-User-Id", "999")
	user := uuid.New()
	ctx := context.WithValue(request.Context(), PrincipalKey, AuthPrincipal{UserID: user, ExternalUserID: "42", Token: "personal-token"})
	ctx = context.WithValue(ctx, UserIDKey, user)
	ctx = context.WithValue(ctx, WorkspaceRefKey, "3")
	ctx = context.WithValue(ctx, WorkspaceIDKey, uuid.New())
	ctx = context.WithValue(ctx, MemberRoleKey, domain.RoleOwner)
	return request.WithContext(ctx)
}

func TestManagerMiddlewareRequiresFreshMatchingPermissions(t *testing.T) {
	verifier := &managerVerifierStub{access: &domain.ManagerAccess{ExternalUserID: "42", ExternalWorkspaceID: "3", Permissions: []string{"marketing.view", "marketing.manage"}}}
	called := 0
	handler := RequireManagerPermissions(verifier, "marketing.view", "marketing.manage")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		identity, ok := ManagerAccessFromContext(r.Context())
		assert.True(t, ok)
		assert.Equal(t, "42", identity.ExternalUserID)
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, managerRequest())
	assert.Equal(t, http.StatusNoContent, response.Code)
	verifier.access.Permissions = []string{"marketing.view"}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, managerRequest())
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Equal(t, 2, verifier.calls)
	assert.Equal(t, 1, called)
}

func TestManagerMiddlewareRejectsLocalRoleFallbackAndConflictingScopes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*http.Request) *http.Request
		status int
	}{
		{"conflicting workspace", func(r *http.Request) *http.Request { r.Header.Set("X-Account-Id", "4"); return r }, 400},
		{"missing canonical workspace", func(r *http.Request) *http.Request { r.Header.Del("X-Sellico-Workspace"); return r }, 403},
		{"local jwt", func(r *http.Request) *http.Request {
			return r.WithContext(context.WithValue(r.Context(), TokenClaimsKey, &jwt.TokenClaims{TokenType: "access"}))
		}, 401},
		{"missing principal", func(r *http.Request) *http.Request { return r.WithContext(context.Background()) }, 401},
		{"different local user", func(r *http.Request) *http.Request {
			return r.WithContext(context.WithValue(r.Context(), UserIDKey, uuid.New()))
		}, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &managerVerifierStub{}
			handler, called := rbacDummyHandler()
			response := httptest.NewRecorder()
			RequireManagerPermissions(verifier, "marketing.manage")(handler).ServeHTTP(response, tc.mutate(managerRequest()))
			assert.Equal(t, tc.status, response.Code)
			assert.False(t, *called)
			assert.Zero(t, verifier.calls)
		})
	}
}

func TestManagerMiddlewareFailsClosedOnUnverifiedCRMResponse(t *testing.T) {
	for _, tc := range []struct {
		name string
		stub *managerVerifierStub
		want int
	}{
		{"unconfigured", nil, 503},
		{"unavailable", &managerVerifierStub{err: errors.New("PRIVATE_UPSTREAM_BODY")}, 503},
		{"revoked", &managerVerifierStub{err: sellico.ErrUnauthorized}, 401},
		{"denied", &managerVerifierStub{err: sellico.ErrManagerScopeDenied}, 403},
		{"another user", &managerVerifierStub{access: &domain.ManagerAccess{ExternalUserID: "99", ExternalWorkspaceID: "3", Permissions: []string{"marketing.manage"}}}, 503},
		{"another workspace", &managerVerifierStub{access: &domain.ManagerAccess{ExternalUserID: "42", ExternalWorkspaceID: "4", Permissions: []string{"marketing.manage"}}}, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var verifier ManagerAccessVerifier
			if tc.stub != nil {
				verifier = tc.stub
			}
			handler, called := rbacDummyHandler()
			response := httptest.NewRecorder()
			RequireManagerPermissions(verifier, "marketing.manage")(handler).ServeHTTP(response, managerRequest())
			assert.Equal(t, tc.want, response.Code)
			assert.False(t, *called)
			assert.NotContains(t, response.Body.String(), "PRIVATE_UPSTREAM")
		})
	}
}
