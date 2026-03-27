package middleware

import (
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// statusResponseWriter wraps http.ResponseWriter to capture the status code.
type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and delegates to the underlying writer.
func (w *statusResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Logging returns middleware that logs each HTTP request with structured fields:
// method, path, status, duration, request_id, user_id (if present), workspace_id (if present).
// Log level: Info for 2xx/3xx, Warn for 4xx, Error for 5xx.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		sw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(sw, r)

		duration := time.Since(start)

		logger := log.With().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", sw.statusCode).
			Dur("duration", duration).Logger()

		if reqID := RequestIDFromContext(r.Context()); reqID != "" {
			logger = logger.With().Str("request_id", reqID).Logger()
		}

		if userID, ok := UserIDFromContext(r.Context()); ok {
			logger = logger.With().Str("user_id", userID.String()).Logger()
		}

		if wsID, ok := WorkspaceIDFromContext(r.Context()); ok {
			logger = logger.With().Str("workspace_id", wsID.String()).Logger()
		}

		switch {
		case sw.statusCode >= 500:
			logger.Error().Msg("request completed")
		case sw.statusCode >= 400:
			logger.Warn().Msg("request completed")
		default:
			logger.Info().Msg("request completed")
		}
	})
}
