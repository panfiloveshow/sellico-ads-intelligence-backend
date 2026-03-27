package middleware

import (
	"net/http"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// readMethods are HTTP methods considered read-only.
var readMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// RequireRole returns middleware that checks the user's role is one of the
// allowed roles. If the role is not in the list, it responds with HTTP 403.
// The role is expected to be set in context by the TenantScope middleware.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, ok := MemberRoleFromContext(r.Context())
			if !ok {
				writeForbidden(w, "role not found in context")
				return
			}

			if !allowed[role] {
				writeForbidden(w, "insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireWriteAccess returns middleware that allows all roles for read-only
// methods (GET, HEAD, OPTIONS) but requires a non-viewer role for write
// methods (POST, PUT, PATCH, DELETE).
func RequireWriteAccess() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, ok := MemberRoleFromContext(r.Context())
			if !ok {
				writeForbidden(w, "role not found in context")
				return
			}

			if !readMethods[r.Method] && role == domain.RoleViewer {
				writeForbidden(w, "insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
