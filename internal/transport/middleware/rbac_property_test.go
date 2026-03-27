package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 5: RBAC — роли ограничивают доступ к операциям
// Проверяет: Требования 3.2, 3.3, 3.5

var (
	propertyAllRoles    = []string{domain.RoleOwner, domain.RoleManager, domain.RoleAnalyst, domain.RoleViewer}
	propertyReadMethods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	writeMethods        = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
)

// TestProperty_RBAC_ViewerBlockedOnWrite verifies Requirement 3.2:
// For any write HTTP method, a user with role "viewer" MUST be denied (HTTP 403)
// and the downstream handler MUST NOT be called.
func TestProperty_RBAC_ViewerBlockedOnWrite(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		method := writeMethods[rapid.IntRange(0, len(writeMethods)-1).Draw(t, "method")]

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		mw := RequireWriteAccess()(handler)

		req := httptest.NewRequest(method, "/test", nil)
		req = withRole(req, domain.RoleViewer)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("viewer + %s: expected 403, got %d", method, rec.Code)
		}
		if handlerCalled {
			t.Fatalf("viewer + %s: handler must NOT be called", method)
		}
	})
}

// TestProperty_RBAC_NonViewerAllowedOnWrite verifies Requirement 3.2 (inverse):
// For any write HTTP method and any non-viewer role, the request MUST be allowed.
func TestProperty_RBAC_NonViewerAllowedOnWrite(t *testing.T) {
	nonViewerRoles := []string{domain.RoleOwner, domain.RoleManager, domain.RoleAnalyst}

	rapid.Check(t, func(t *rapid.T) {
		method := writeMethods[rapid.IntRange(0, len(writeMethods)-1).Draw(t, "method")]
		role := nonViewerRoles[rapid.IntRange(0, len(nonViewerRoles)-1).Draw(t, "role")]

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		mw := RequireWriteAccess()(handler)

		req := httptest.NewRequest(method, "/test", nil)
		req = withRole(req, role)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s + %s: expected 200, got %d", role, method, rec.Code)
		}
		if !handlerCalled {
			t.Fatalf("%s + %s: handler must be called", role, method)
		}
	})
}

// TestProperty_RBAC_ReadMethodsAllowAllRoles verifies Requirement 3.5:
// For any read HTTP method and any valid role, RequireWriteAccess MUST allow the request.
func TestProperty_RBAC_ReadMethodsAllowAllRoles(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		method := propertyReadMethods[rapid.IntRange(0, len(propertyReadMethods)-1).Draw(t, "method")]
		role := propertyAllRoles[rapid.IntRange(0, len(propertyAllRoles)-1).Draw(t, "role")]

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		mw := RequireWriteAccess()(handler)

		req := httptest.NewRequest(method, "/test", nil)
		req = withRole(req, role)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s + %s: expected 200, got %d", role, method, rec.Code)
		}
		if !handlerCalled {
			t.Fatalf("%s + %s: handler must be called", role, method)
		}
	})
}

// TestProperty_RBAC_AnalystDeniedSellerCabinetAndMembers verifies Requirement 3.3:
// A user with role "analyst" MUST be denied access to endpoints restricted to
// owner/manager (e.g., seller cabinets, workspace members management).
// RequireRole(owner, manager) must block analyst for any HTTP method.
func TestProperty_RBAC_AnalystDeniedSellerCabinetAndMembers(t *testing.T) {
	allMethods := append(propertyReadMethods, writeMethods...)

	rapid.Check(t, func(t *rapid.T) {
		method := allMethods[rapid.IntRange(0, len(allMethods)-1).Draw(t, "method")]

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		mw := RequireRole(domain.RoleOwner, domain.RoleManager)(handler)

		req := httptest.NewRequest(method, "/seller-cabinets", nil)
		req = withRole(req, domain.RoleAnalyst)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("analyst + %s: expected 403, got %d", method, rec.Code)
		}
		if handlerCalled {
			t.Fatalf("analyst + %s: handler must NOT be called", method)
		}
	})
}

// TestProperty_RBAC_RequireRoleAllowsOnlyListed verifies Requirement 3.5:
// For any subset of allowed roles, RequireRole MUST allow exactly those roles
// and deny all others. This is the core RBAC invariant.
func TestProperty_RBAC_RequireRoleAllowsOnlyListed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a non-empty subset of roles using a bitmask (1..15 for 4 roles).
		bitmask := rapid.IntRange(1, (1<<len(propertyAllRoles))-1).Draw(t, "bitmask")
		allowedSet := make(map[string]bool)
		allowedSlice := make([]string, 0, len(propertyAllRoles))
		for i, role := range propertyAllRoles {
			if bitmask&(1<<i) != 0 {
				allowedSet[role] = true
				allowedSlice = append(allowedSlice, role)
			}
		}

		// Pick a random role to test.
		testRole := propertyAllRoles[rapid.IntRange(0, len(propertyAllRoles)-1).Draw(t, "testRole")]

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		mw := RequireRole(allowedSlice...)(handler)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req = withRole(req, testRole)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if allowedSet[testRole] {
			if rec.Code != http.StatusOK {
				t.Fatalf("role %q in allowed %v: expected 200, got %d", testRole, allowedSlice, rec.Code)
			}
			if !handlerCalled {
				t.Fatalf("role %q in allowed %v: handler must be called", testRole, allowedSlice)
			}
		} else {
			if rec.Code != http.StatusForbidden {
				t.Fatalf("role %q NOT in allowed %v: expected 403, got %d", testRole, allowedSlice, rec.Code)
			}
			if handlerCalled {
				t.Fatalf("role %q NOT in allowed %v: handler must NOT be called", testRole, allowedSlice)
			}
		}
	})
}

// TestProperty_RBAC_MissingRoleAlwaysDenied verifies Requirement 3.5:
// If no role is present in the context, both RequireRole and RequireWriteAccess
// MUST deny the request with HTTP 403, regardless of HTTP method.
func TestProperty_RBAC_MissingRoleAlwaysDenied(t *testing.T) {
	allMethods := append(propertyReadMethods, writeMethods...)

	rapid.Check(t, func(t *rapid.T) {
		method := allMethods[rapid.IntRange(0, len(allMethods)-1).Draw(t, "method")]

		// Test RequireRole
		handlerCalled1 := false
		h1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled1 = true
			w.WriteHeader(http.StatusOK)
		})
		mw1 := RequireRole(propertyAllRoles...)(h1)

		req1 := httptest.NewRequest(method, "/test", nil)
		// No role injected into context.
		rec1 := httptest.NewRecorder()
		mw1.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusForbidden {
			t.Fatalf("RequireRole no-role + %s: expected 403, got %d", method, rec1.Code)
		}
		if handlerCalled1 {
			t.Fatalf("RequireRole no-role + %s: handler must NOT be called", method)
		}

		// Test RequireWriteAccess
		handlerCalled2 := false
		h2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled2 = true
			w.WriteHeader(http.StatusOK)
		})
		mw2 := RequireWriteAccess()(h2)

		req2 := httptest.NewRequest(method, "/test", nil)
		rec2 := httptest.NewRecorder()
		mw2.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusForbidden {
			t.Fatalf("RequireWriteAccess no-role + %s: expected 403, got %d", method, rec2.Code)
		}
		if handlerCalled2 {
			t.Fatalf("RequireWriteAccess no-role + %s: handler must NOT be called", method)
		}
	})
}
