package middleware

import (
	"encoding/json"
	"net/http"
	"runtime/debug"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"github.com/rs/zerolog/log"
)

// Recovery returns middleware that recovers from panics in downstream handlers.
// It logs the panic value and stack trace at Error level via zerolog and returns
// HTTP 500 with a generic error in Response_Envelope format (no internal details).
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()

				logger := log.Error().
					Interface("panic", rec).
					Bytes("stack_trace", stack)

				if reqID := RequestIDFromContext(r.Context()); reqID != "" {
					logger = logger.Str("request_id", reqID)
				}

				logger.Msg("panic recovered")

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)

				resp := envelope.Err(envelope.Error{
					Code:    apperror.ErrInternal.Code,
					Message: apperror.ErrInternal.Message,
				})
				json.NewEncoder(w).Encode(resp)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
