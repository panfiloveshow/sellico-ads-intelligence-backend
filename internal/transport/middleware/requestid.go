package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

const (
	// RequestIDKey is the context key for the request ID.
	RequestIDKey contextKey = "request_id"

	// requestIDHeader is the HTTP header used for request ID propagation.
	requestIDHeader = "X-Request-ID"
)

// RequestIDFromContext extracts the request ID from the context.
// Returns empty string if not present.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(RequestIDKey).(string)
	return id
}

// RequestID returns middleware that ensures every request has a unique request_id.
// If the incoming request already has an X-Request-ID header, that value is used;
// otherwise a new UUID is generated. The request_id is injected into the context
// and set on the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = uuid.New().String()
		}

		w.Header().Set(requestIDHeader, id)

		ctx := context.WithValue(r.Context(), RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
