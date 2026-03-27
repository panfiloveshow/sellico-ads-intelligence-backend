package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

// withRole injects a member role into the request context.
func withRole(r *http.Request, role string) *http.Request {
	ctx := context.WithValue(r.Context(), MemberRoleKey, role)
	return r.WithContext(ctx)
}

// rbacDummyHandler returns a handler that records whether it was called.
func rbacDummyHandler() (http.Handler, *bool) {
	called := false
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	return h, &called
}

// assertForbiddenResponse checks that the response is a 403 with the expected message.
func assertForbiddenResponse(t *testing.T, rec *httptest.ResponseRecorder, message string) {
	t.Helper()
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp envelope.Response
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Errors)
	assert.Equal(t, "FORBIDDEN", resp.Errors[0].Code)
	assert.Equal(t, message, resp.Errors[0].Message)
}

// --- RequireRole tests ---

func TestRequireRole_AllowedRole(t *testing.T) {
	handler, called := rbacDummyHandler()
	mw := RequireRole(domain.RoleOwner, domain.RoleManager)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req = withRole(req, domain.RoleOwner)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *called)
}

func TestRequireRole_DeniedRole(t *testing.T) {
	handler, called := rbacDummyHandler()
	mw := RequireRole(domain.RoleOwner, domain.RoleManager)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req = withRole(req, domain.RoleViewer)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.False(t, *called)
	assertForbiddenResponse(t, rec, "insufficient permissions")
}

func TestRequireRole_NoRoleInContext(t *testing.T) {
	handler, called := rbacDummyHandler()
	mw := RequireRole(domain.RoleOwner)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.False(t, *called)
	assertForbiddenResponse(t, rec, "role not found in context")
}

func TestRequireRole_AllRoles(t *testing.T) {
	allRoles := []string{domain.RoleOwner, domain.RoleManager, domain.RoleAnalyst, domain.RoleViewer}

	tests := []struct {
		name         string
		allowedRoles []string
		userRole     string
		expectAllow  bool
	}{
		{"owner allowed for owner+manager", []string{domain.RoleOwner, domain.RoleManager}, domain.RoleOwner, true},
		{"manager allowed for owner+manager", []string{domain.RoleOwner, domain.RoleManager}, domain.RoleManager, true},
		{"analyst denied for owner+manager", []string{domain.RoleOwner, domain.RoleManager}, domain.RoleAnalyst, false},
		{"viewer denied for owner+manager", []string{domain.RoleOwner, domain.RoleManager}, domain.RoleViewer, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, called := rbacDummyHandler()
			mw := RequireRole(tt.allowedRoles...)(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req = withRole(req, tt.userRole)
			rec := httptest.NewRecorder()

			mw.ServeHTTP(rec, req)

			if tt.expectAllow {
				assert.Equal(t, http.StatusOK, rec.Code)
				assert.True(t, *called)
			} else {
				assert.False(t, *called)
				assertForbiddenResponse(t, rec, "insufficient permissions")
			}
		})
	}

	// All roles allowed
	for _, role := range allRoles {
		t.Run("all_roles_allows_"+role, func(t *testing.T) {
			handler, called := rbacDummyHandler()
			mw := RequireRole(allRoles...)(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req = withRole(req, role)
			rec := httptest.NewRecorder()

			mw.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.True(t, *called)
		})
	}
}

func TestRequireRole_SingleRole(t *testing.T) {
	handler, called := rbacDummyHandler()
	mw := RequireRole(domain.RoleOwner)(handler)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req = withRole(req, domain.RoleOwner)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *called)
}

// --- RequireWriteAccess tests ---

func TestRequireWriteAccess_ReadMethodsAllowAllRoles(t *testing.T) {
	readMethods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	allRoles := []string{domain.RoleOwner, domain.RoleManager, domain.RoleAnalyst, domain.RoleViewer}

	for _, method := range readMethods {
		for _, role := range allRoles {
			t.Run(method+"_"+role, func(t *testing.T) {
				handler, called := rbacDummyHandler()
				mw := RequireWriteAccess()(handler)

				req := httptest.NewRequest(method, "/test", nil)
				req = withRole(req, role)
				rec := httptest.NewRecorder()

				mw.ServeHTTP(rec, req)

				assert.Equal(t, http.StatusOK, rec.Code)
				assert.True(t, *called)
			})
		}
	}
}

func TestRequireWriteAccess_WriteMethodsDenyViewer(t *testing.T) {
	writeMethods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

	for _, method := range writeMethods {
		t.Run(method+"_viewer_denied", func(t *testing.T) {
			handler, called := rbacDummyHandler()
			mw := RequireWriteAccess()(handler)

			req := httptest.NewRequest(method, "/test", nil)
			req = withRole(req, domain.RoleViewer)
			rec := httptest.NewRecorder()

			mw.ServeHTTP(rec, req)

			assert.False(t, *called)
			assertForbiddenResponse(t, rec, "insufficient permissions")
		})
	}
}

func TestRequireWriteAccess_WriteMethodsAllowNonViewer(t *testing.T) {
	writeMethods := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	nonViewerRoles := []string{domain.RoleOwner, domain.RoleManager, domain.RoleAnalyst}

	for _, method := range writeMethods {
		for _, role := range nonViewerRoles {
			t.Run(method+"_"+role+"_allowed", func(t *testing.T) {
				handler, called := rbacDummyHandler()
				mw := RequireWriteAccess()(handler)

				req := httptest.NewRequest(method, "/test", nil)
				req = withRole(req, role)
				rec := httptest.NewRecorder()

				mw.ServeHTTP(rec, req)

				assert.Equal(t, http.StatusOK, rec.Code)
				assert.True(t, *called)
			})
		}
	}
}

func TestRequireWriteAccess_NoRoleInContext(t *testing.T) {
	handler, called := rbacDummyHandler()
	mw := RequireWriteAccess()(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.False(t, *called)
	assertForbiddenResponse(t, rec, "role not found in context")
}

func TestRequireWriteAccess_NoRoleInContext_WriteMethod(t *testing.T) {
	handler, called := rbacDummyHandler()
	mw := RequireWriteAccess()(handler)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.False(t, *called)
	assertForbiddenResponse(t, rec, "role not found in context")
}
