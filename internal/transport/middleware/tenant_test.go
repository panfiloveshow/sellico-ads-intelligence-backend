package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock MembershipChecker ---

type mockMembershipChecker struct {
	member *domain.WorkspaceMember
	err    error
}

func (m *mockMembershipChecker) GetWorkspaceMember(_ context.Context, _, _ uuid.UUID) (*domain.WorkspaceMember, error) {
	return m.member, m.err
}

// --- helpers ---

// tenantDummyHandler records whether it was called and captures context values.
func tenantDummyHandler() (http.Handler, *bool, *uuid.UUID, *string) {
	called := false
	var gotWsID uuid.UUID
	var gotRole string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if wsID, ok := WorkspaceIDFromContext(r.Context()); ok {
			gotWsID = wsID
		}
		if role, ok := MemberRoleFromContext(r.Context()); ok {
			gotRole = role
		}
		w.WriteHeader(http.StatusOK)
	})
	return h, &called, &gotWsID, &gotRole
}

// withUserID injects a user_id into the request context (simulates Auth middleware).
func withUserID(r *http.Request, userID uuid.UUID) *http.Request {
	ctx := context.WithValue(r.Context(), UserIDKey, userID)
	return r.WithContext(ctx)
}

// withChiURLParam sets a chi URL parameter on the request.
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// --- tests ---

func TestTenantScope_ValidHeader(t *testing.T) {
	userID := uuid.New()
	wsID := uuid.New()
	checker := &mockMembershipChecker{
		member: &domain.WorkspaceMember{
			ID:          uuid.New(),
			WorkspaceID: wsID,
			UserID:      userID,
			Role:        domain.RoleOwner,
		},
	}

	handler, called, gotWsID, gotRole := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Workspace-ID", wsID.String())
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *called)
	assert.Equal(t, wsID, *gotWsID)
	assert.Equal(t, domain.RoleOwner, *gotRole)
}

func TestTenantScope_ValidURLParam(t *testing.T) {
	userID := uuid.New()
	wsID := uuid.New()
	checker := &mockMembershipChecker{
		member: &domain.WorkspaceMember{
			ID:          uuid.New(),
			WorkspaceID: wsID,
			UserID:      userID,
			Role:        domain.RoleAnalyst,
		},
	}

	handler, called, gotWsID, gotRole := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+wsID.String(), nil)
	req = withUserID(req, userID)
	req = withChiURLParam(req, "workspaceId", wsID.String())
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *called)
	assert.Equal(t, wsID, *gotWsID)
	assert.Equal(t, domain.RoleAnalyst, *gotRole)
}

func TestTenantScope_HeaderTakesPrecedenceOverURLParam(t *testing.T) {
	userID := uuid.New()
	headerWsID := uuid.New()
	urlWsID := uuid.New()
	checker := &mockMembershipChecker{
		member: &domain.WorkspaceMember{
			ID:          uuid.New(),
			WorkspaceID: headerWsID,
			UserID:      userID,
			Role:        domain.RoleViewer,
		},
	}

	handler, called, gotWsID, _ := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Workspace-ID", headerWsID.String())
	req = withUserID(req, userID)
	req = withChiURLParam(req, "workspaceId", urlWsID.String())
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, *called)
	assert.Equal(t, headerWsID, *gotWsID)
}

func TestTenantScope_MissingWorkspaceID(t *testing.T) {
	userID := uuid.New()
	checker := &mockMembershipChecker{}

	handler, called, _, _ := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, *called)
	assertTenantErrorResponse(t, rec, "VALIDATION_ERROR", "missing workspace id")
}

func TestTenantScope_InvalidUUID(t *testing.T) {
	userID := uuid.New()
	checker := &mockMembershipChecker{}

	handler, called, _, _ := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Workspace-ID", "not-a-uuid")
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, *called)
	assertTenantErrorResponse(t, rec, "VALIDATION_ERROR", "invalid workspace id")
}

func TestTenantScope_NoAuthenticatedUser(t *testing.T) {
	wsID := uuid.New()
	checker := &mockMembershipChecker{}

	handler, called, _, _ := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Workspace-ID", wsID.String())
	// No user_id in context.
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, *called)
}

func TestTenantScope_NotAMember(t *testing.T) {
	userID := uuid.New()
	wsID := uuid.New()
	checker := &mockMembershipChecker{
		member: nil,
		err:    errors.New("no rows"),
	}

	handler, called, _, _ := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Workspace-ID", wsID.String())
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, *called)
	assertTenantErrorResponse(t, rec, "FORBIDDEN", "access denied")
}

func TestTenantScope_MemberReturnsNil(t *testing.T) {
	userID := uuid.New()
	wsID := uuid.New()
	checker := &mockMembershipChecker{
		member: nil,
		err:    nil,
	}

	handler, called, _, _ := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Workspace-ID", wsID.String())
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, *called)
}

func TestTenantScope_AllRoles(t *testing.T) {
	roles := []string{domain.RoleOwner, domain.RoleManager, domain.RoleAnalyst, domain.RoleViewer}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			userID := uuid.New()
			wsID := uuid.New()
			checker := &mockMembershipChecker{
				member: &domain.WorkspaceMember{
					ID:          uuid.New(),
					WorkspaceID: wsID,
					UserID:      userID,
					Role:        role,
				},
			}

			handler, called, _, gotRole := tenantDummyHandler()
			mw := TenantScope(checker)(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-Workspace-ID", wsID.String())
			req = withUserID(req, userID)
			rec := httptest.NewRecorder()

			mw.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.True(t, *called)
			assert.Equal(t, role, *gotRole)
		})
	}
}

func TestTenantScope_ResponseContentType(t *testing.T) {
	checker := &mockMembershipChecker{}
	handler, _, _, _ := tenantDummyHandler()
	mw := TenantScope(checker)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	// Missing workspace_id triggers 400.
	req = withUserID(req, uuid.New())
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestWorkspaceIDFromContext_NotPresent(t *testing.T) {
	ctx := context.Background()
	wsID, ok := WorkspaceIDFromContext(ctx)
	assert.False(t, ok)
	assert.Equal(t, uuid.Nil, wsID)
}

func TestMemberRoleFromContext_NotPresent(t *testing.T) {
	ctx := context.Background()
	role, ok := MemberRoleFromContext(ctx)
	assert.False(t, ok)
	assert.Equal(t, "", role)
}

// assertTenantErrorResponse checks that the response body is a valid Response_Envelope with the expected error.
func assertTenantErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	var resp envelope.Response
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Errors)
	assert.Equal(t, code, resp.Errors[0].Code)
	assert.Equal(t, message, resp.Errors[0].Message)
}
