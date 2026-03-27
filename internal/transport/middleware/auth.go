package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/jwt"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const (
	// UserIDKey is the context key for the authenticated user's ID.
	UserIDKey contextKey = "user_id"
)

// UserIDFromContext extracts the authenticated user's ID from the request context.
// Returns uuid.Nil and false if not present.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(UserIDKey).(uuid.UUID)
	return id, ok
}

// Auth returns middleware that validates JWT Bearer tokens from the Authorization header.
// On success it injects the user_id into the request context.
// On failure it responds with HTTP 401 in Response_Envelope format.
func Auth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeUnauthorized(w, "missing authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeUnauthorized(w, "invalid authorization header format")
				return
			}

			tokenString := parts[1]
			if tokenString == "" {
				writeUnauthorized(w, "empty bearer token")
				return
			}

			claims, err := jwt.ValidateToken(tokenString, jwtSecret)
			if err != nil {
				writeUnauthorized(w, "invalid or expired token")
				return
			}

			if claims.TokenType != "access" {
				writeUnauthorized(w, "invalid token type")
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeUnauthorized writes an HTTP 401 response in Response_Envelope format.
func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apperror.ErrUnauthorized.Status)

	resp := envelope.Err(envelope.Error{
		Code:    apperror.ErrUnauthorized.Code,
		Message: message,
	})

	json.NewEncoder(w).Encode(resp)
}
