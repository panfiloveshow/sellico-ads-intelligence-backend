package middleware

import "net/http"

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	AllowOrigins []string
}

// CORS returns middleware that sets Cross-Origin Resource Sharing headers.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	originSet := make(map[string]struct{}, len(cfg.AllowOrigins))
	allowAll := false
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			allowAll = true
		}
		originSet[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := false
			if allowAll {
				allowed = true
			} else {
				_, allowed = originSet[origin]
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, X-Workspace-ID, X-User-Token, Content-Type, X-Request-ID")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
