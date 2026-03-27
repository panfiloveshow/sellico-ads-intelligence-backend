package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 3: Tenant isolation — данные изолированы по workspace_id
// Проверяет: Требования 2.2, 2.5

// multiTenantChecker simulates a membership store where each user belongs to
// exactly one workspace. Requests for any other workspace return nil (not a member).
type multiTenantChecker struct {
	memberships map[uuid.UUID]map[uuid.UUID]*domain.WorkspaceMember
}

func (c *multiTenantChecker) GetWorkspaceMember(_ context.Context, workspaceID, userID uuid.UUID) (*domain.WorkspaceMember, error) {
	if users, ok := c.memberships[workspaceID]; ok {
		if m, ok := users[userID]; ok {
			return m, nil
		}
	}
	return nil, nil
}

var allRoles = []string{domain.RoleOwner, domain.RoleManager, domain.RoleAnalyst, domain.RoleViewer}

func TestProperty_TenantIsolation_MemberAccessOwnWorkspace(t *testing.T) {
	// Property: For any user who is a member of workspace W with any valid role,
	// TenantScope MUST allow the request and inject the correct workspace_id
	// and role into the context.
	rapid.Check(t, func(t *rapid.T) {
		role := allRoles[rapid.IntRange(0, len(allRoles)-1).Draw(t, "roleIndex")]
		userID := uuid.New()
		wsID := uuid.New()

		checker := &multiTenantChecker{
			memberships: map[uuid.UUID]map[uuid.UUID]*domain.WorkspaceMember{
				wsID: {
					userID: {
						ID:          uuid.New(),
						WorkspaceID: wsID,
						UserID:      userID,
						Role:        role,
					},
				},
			},
		}

		var capturedWsID uuid.UUID
		var capturedRole string
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedWsID, _ = WorkspaceIDFromContext(r.Context())
			capturedRole, _ = MemberRoleFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		mw := TenantScope(checker)(handler)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Workspace-ID", wsID.String())
		req = withUserID(req, userID)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d for member with role %s", rec.Code, role)
		}
		if capturedWsID != wsID {
			t.Fatalf("context workspace_id %s != requested %s", capturedWsID, wsID)
		}
		if capturedRole != role {
			t.Fatalf("context role %q != membership role %q", capturedRole, role)
		}
	})
}

func TestProperty_TenantIsolation_NonMemberDenied(t *testing.T) {
	// Property: For any user who is NOT a member of workspace W,
	// TenantScope MUST return HTTP 403 and never call the downstream handler.
	rapid.Check(t, func(t *rapid.T) {
		userID := uuid.New()
		ownWsID := uuid.New()
		foreignWsID := uuid.New()

		role := allRoles[rapid.IntRange(0, len(allRoles)-1).Draw(t, "roleIndex")]

		checker := &multiTenantChecker{
			memberships: map[uuid.UUID]map[uuid.UUID]*domain.WorkspaceMember{
				ownWsID: {
					userID: {
						ID:          uuid.New(),
						WorkspaceID: ownWsID,
						UserID:      userID,
						Role:        role,
					},
				},
			},
		}

		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		})

		mw := TenantScope(checker)(handler)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Workspace-ID", foreignWsID.String())
		req = withUserID(req, userID)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for non-member, got %d", rec.Code)
		}
		if handlerCalled {
			t.Fatal("handler must NOT be called for non-member")
		}
	})
}

func TestProperty_TenantIsolation_CrossTenantNeverLeaks(t *testing.T) {
	// Property: Given N workspaces each with their own user, when user_i
	// requests workspace_j (i != j), the middleware MUST deny access.
	// When user_i requests workspace_i, the context workspace_id MUST equal workspace_i.
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 10).Draw(t, "numWorkspaces")

		type tenant struct {
			userID uuid.UUID
			wsID   uuid.UUID
			role   string
		}

		tenants := make([]tenant, n)
		checker := &multiTenantChecker{
			memberships: make(map[uuid.UUID]map[uuid.UUID]*domain.WorkspaceMember),
		}

		for i := 0; i < n; i++ {
			tenants[i] = tenant{
				userID: uuid.New(),
				wsID:   uuid.New(),
				role:   allRoles[rapid.IntRange(0, len(allRoles)-1).Draw(t, fmt.Sprintf("role_%d", i))],
			}
			checker.memberships[tenants[i].wsID] = map[uuid.UUID]*domain.WorkspaceMember{
				tenants[i].userID: {
					ID:          uuid.New(),
					WorkspaceID: tenants[i].wsID,
					UserID:      tenants[i].userID,
					Role:        tenants[i].role,
				},
			}
		}

		requesterIdx := rapid.IntRange(0, n-1).Draw(t, "requester")
		targetIdx := rapid.IntRange(0, n-1).Draw(t, "target")

		var capturedWsID uuid.UUID
		handlerCalled := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			capturedWsID, _ = WorkspaceIDFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		mw := TenantScope(checker)(handler)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Workspace-ID", tenants[targetIdx].wsID.String())
		req = withUserID(req, tenants[requesterIdx].userID)
		rec := httptest.NewRecorder()

		mw.ServeHTTP(rec, req)

		if requesterIdx == targetIdx {
			if rec.Code != http.StatusOK {
				t.Fatalf("same tenant: expected 200, got %d", rec.Code)
			}
			if !handlerCalled {
				t.Fatal("same tenant: handler must be called")
			}
			if capturedWsID != tenants[targetIdx].wsID {
				t.Fatalf("same tenant: context ws %s != target ws %s", capturedWsID, tenants[targetIdx].wsID)
			}
		} else {
			if rec.Code != http.StatusForbidden {
				t.Fatalf("cross-tenant: expected 403, got %d (requester=%d, target=%d)", rec.Code, requesterIdx, targetIdx)
			}
			if handlerCalled {
				t.Fatal("cross-tenant: handler must NOT be called")
			}
		}
	})
}
