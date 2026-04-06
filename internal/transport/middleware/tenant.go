package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
)

const (
	// WorkspaceIDKey is the context key for the current workspace ID.
	WorkspaceIDKey contextKey = "workspace_id"
	// MemberRoleKey is the context key for the user's role in the current workspace.
	MemberRoleKey contextKey = "member_role"
	// WorkspaceRefKey stores the raw shared workspace reference from headers.
	WorkspaceRefKey contextKey = "workspace_ref"
)

// WorkspaceIDFromContext extracts the workspace ID from the request context.
// Returns uuid.Nil and false if not present.
func WorkspaceIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(WorkspaceIDKey).(uuid.UUID)
	return id, ok
}

// MemberRoleFromContext extracts the user's workspace role from the request context.
// Returns empty string and false if not present.
func MemberRoleFromContext(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(MemberRoleKey).(string)
	return role, ok
}

// WorkspaceRefFromContext extracts the raw workspace reference used for shared auth.
func WorkspaceRefFromContext(ctx context.Context) (string, bool) {
	ref, ok := ctx.Value(WorkspaceRefKey).(string)
	return ref, ok
}

// MembershipChecker abstracts workspace membership lookup so the middleware
// does not depend on the sqlc-generated code directly.
type MembershipChecker interface {
	GetWorkspaceMember(ctx context.Context, workspaceID, userID uuid.UUID) (*domain.WorkspaceMember, error)
}

// WorkspaceAccess contains the local workspace and the resolved role for the principal.
// WorkspaceAccess is an alias for domain.WorkspaceAccess.
type WorkspaceAccess = domain.WorkspaceAccess

// WorkspaceResolver validates a shared workspace identifier and resolves it to a local UUID.
type WorkspaceResolver interface {
	ResolveWorkspaceAccess(ctx context.Context, principal domain.AuthPrincipal, rawWorkspaceID string) (*domain.WorkspaceAccess, error)
}

// TenantScope returns middleware that extracts workspace_id from the
// X-Workspace-ID header or the "workspaceId" chi URL parameter, verifies
// that the authenticated user is a member of that workspace, and injects
// both workspace_id and the member's role into the request context.
func TenantScope(checker MembershipChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Extract workspace_id from header or URL param.
			raw := r.Header.Get("X-Workspace-ID")
			if raw == "" {
				raw = chi.URLParam(r, "workspaceId")
			}
			if raw == "" {
				writeBadRequest(w, "missing workspace id")
				return
			}

			workspaceID, err := uuid.Parse(raw)
			if err != nil {
				writeBadRequest(w, "invalid workspace id")
				return
			}

			// 2. Get authenticated user from context (set by Auth middleware).
			userID, ok := UserIDFromContext(r.Context())
			if !ok {
				writeUnauthorized(w, "authentication required")
				return
			}

			// 3. Check membership.
			member, err := checker.GetWorkspaceMember(r.Context(), workspaceID, userID)
			if err != nil || member == nil {
				writeForbidden(w, "access denied")
				return
			}

			// 4. Inject workspace_id and role into context.
			ctx := context.WithValue(r.Context(), WorkspaceIDKey, workspaceID)
			ctx = context.WithValue(ctx, MemberRoleKey, member.Role)
			ctx = context.WithValue(ctx, WorkspaceRefKey, raw)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SharedTenantScope resolves the shared workspace headers to a local workspace UUID.
func SharedTenantScope(resolver WorkspaceResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := extractWorkspaceRef(r)
			if raw == "" {
				writeBadRequest(w, "missing workspace id")
				return
			}

			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				writeUnauthorized(w, "authentication required")
				return
			}

			access, err := resolver.ResolveWorkspaceAccess(r.Context(), principal, raw)
			if err != nil || access == nil {
				writeForbidden(w, "access denied")
				return
			}

			ctx := context.WithValue(r.Context(), WorkspaceIDKey, access.WorkspaceID)
			ctx = context.WithValue(ctx, MemberRoleKey, access.Role)
			ctx = context.WithValue(ctx, WorkspaceRefKey, raw)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractWorkspaceRef(r *http.Request) string {
	headerCandidates := []string{
		r.Header.Get("X-Account-Id"),
		r.Header.Get("X-Workspace-Id"),
		r.Header.Get("X-Workspace-ID"),
	}

	for _, candidate := range headerCandidates {
		if candidate != "" {
			return candidate
		}
	}

	return chi.URLParam(r, "workspaceId")
}

// writeBadRequest writes an HTTP 400 response in Response_Envelope format.
func writeBadRequest(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apperror.ErrValidation.Status)

	resp := envelope.Err(envelope.Error{
		Code:    apperror.ErrValidation.Code,
		Message: message,
	})

	json.NewEncoder(w).Encode(resp)
}

// writeForbidden writes an HTTP 403 response in Response_Envelope format.
func writeForbidden(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apperror.ErrForbidden.Status)

	resp := envelope.Err(envelope.Error{
		Code:    apperror.ErrForbidden.Code,
		Message: message,
	})

	json.NewEncoder(w).Encode(resp)
}
