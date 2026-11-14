package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
)

type ManagerAccessVerifier interface {
	VerifyManagerAccess(context.Context, string, string) (*domain.ManagerAccess, error)
}

type managerAccessKey struct{}

// ManagerWorkspaceHeaders adapts the canonical Sellico header to the legacy
// tenant resolver. Conflicting aliases are rejected before resolving a workspace.
func ManagerWorkspaceHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspace := r.Header.Get("X-Sellico-Workspace")
		if workspace == "" {
			writeBadRequest(w, "a canonical Sellico workspace is required")
			return
		}
		for _, key := range []string{"X-Account-Id", "X-Workspace-ID"} {
			if value := r.Header.Get(key); value != "" && value != workspace {
				writeBadRequest(w, "conflicting workspace headers")
				return
			}
		}
		request := r.Clone(r.Context())
		request.Header.Set("X-Account-Id", workspace)
		request.Header.Set("X-Workspace-ID", workspace)
		next.ServeHTTP(w, request)
	})
}

func ManagerAccessFromContext(ctx context.Context) (domain.ManagerAccess, bool) {
	identity, ok := ctx.Value(managerAccessKey{}).(domain.ManagerAccess)
	return identity, ok
}

// RequireManagerPermissions runs after shared auth and tenant resolution, and does
// not accept the legacy local-JWT/role fallback or a cached positive CRM verdict.
func RequireManagerPermissions(verifier ManagerAccessVerifier, permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			userID, userOK := UserIDFromContext(r.Context())
			if !ok || !userOK || principal.UserID == uuid.Nil || principal.UserID != userID || principal.Token == "" {
				writeUnauthorized(w, "a personal Sellico session is required")
				return
			}
			if _, localJWT := TokenClaimsFromContext(r.Context()); localJWT {
				writeUnauthorized(w, "manager operations require a Sellico session")
				return
			}
			workspace, workspaceOK := WorkspaceRefFromContext(r.Context())
			localWorkspace, localOK := WorkspaceIDFromContext(r.Context())
			if !workspaceOK || !localOK || localWorkspace == uuid.Nil || r.Header.Get("X-Sellico-Workspace") != workspace {
				writeForbidden(w, "manager workspace was not resolved")
				return
			}
			for _, header := range []string{"X-Account-Id", "X-Workspace-ID"} {
				if value := r.Header.Get(header); value != "" && value != workspace {
					writeBadRequest(w, "conflicting workspace headers")
					return
				}
			}
			if verifier == nil {
				writeServiceUnavailable(w, "manager identity verifier is not configured")
				return
			}
			identity, err := verifier.VerifyManagerAccess(r.Context(), principal.Token, workspace)
			if errors.Is(err, sellico.ErrUnauthorized) {
				writeUnauthorized(w, "the Sellico session could not be confirmed")
				return
			}
			if errors.Is(err, sellico.ErrManagerScopeDenied) {
				writeForbidden(w, "manager workspace access denied")
				return
			}
			if err != nil || identity == nil || identity.ExternalUserID != principal.ExternalUserID || identity.ExternalWorkspaceID != workspace {
				writeServiceUnavailable(w, "manager permissions could not be confirmed")
				return
			}
			for _, permission := range permissions {
				if !identity.Allows(permission) {
					writeForbiddenPermission(w, permission)
					return
				}
			}
			ctx := context.WithValue(r.Context(), managerAccessKey{}, *identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
