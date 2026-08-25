package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// PermissionChecker verifies a CRM permission slug for a shared-auth user.
// Implementations are expected to cache results (see sellico.PermissionChecker).
type PermissionChecker interface {
	HasPermission(ctx context.Context, userToken, userID, workspaceRef, permission string) (bool, error)
}

// RequirePermission gates a route on a Sellico CRM permission slug, matching
// how the other microservices (products, finance, seo, meet) enforce RBAC.
//
// Fallbacks that keep the system bootable and extension flows working:
//   - local JWTs (access/extension tokens) carry no CRM token, so the CRM
//     cannot be asked; the legacy role model applies (reads for everyone,
//     writes for non-viewers);
//   - a nil checker (service account not configured) also falls back to the
//     legacy role model.
func RequirePermission(checker PermissionChecker, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, isLocalJWT := TokenClaimsFromContext(r.Context()); isLocalJWT || checker == nil {
				if !legacyRoleAllows(r) {
					writeForbidden(w, "insufficient permissions")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				writeForbidden(w, "principal not found in context")
				return
			}

			workspaceRef, ok := WorkspaceRefFromContext(r.Context())
			if !ok {
				writeForbidden(w, "workspace not resolved")
				return
			}

			allowed, err := checker.HasPermission(
				r.Context(), principal.Token, principal.ExternalUserID, workspaceRef, permission,
			)
			if err != nil {
				writeServiceUnavailable(w, "permission check unavailable")
				return
			}
			if !allowed {
				writeForbiddenPermission(w, permission)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// legacyRoleAllows mirrors RequireWriteAccess: reads for any member,
// writes only for non-viewer roles.
func legacyRoleAllows(r *http.Request) bool {
	role, ok := MemberRoleFromContext(r.Context())
	if !ok {
		return false
	}

	return readMethods[r.Method] || role != domain.RoleViewer
}

func writeForbiddenPermission(w http.ResponseWriter, permission string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]any{
		"data": nil,
		"errors": []map[string]string{{
			"code":       "PERMISSION_DENIED",
			"message":    "insufficient permissions",
			"permission": permission,
		}},
	})
}

func writeServiceUnavailable(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	json.NewEncoder(w).Encode(map[string]any{
		"data": nil,
		"errors": []map[string]string{{
			"code":    "PERMISSION_CHECK_UNAVAILABLE",
			"message": message,
		}},
	})
}
