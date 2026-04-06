package middleware

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/envelope"
	"golang.org/x/time/rate"
)

// RateLimitConfig defines per-user rate limiting parameters.
type RateLimitConfig struct {
	// RequestsPerSecond is the sustained rate (tokens added per second).
	RequestsPerSecond float64
	// Burst is the maximum number of requests allowed in a burst.
	Burst int
}

// RateLimit returns middleware that applies per-user token-bucket rate limiting.
// Users are identified by their authenticated user ID from context.
// Unauthenticated requests are rate-limited by remote IP.
func RateLimit(cfg RateLimitConfig) func(http.Handler) http.Handler {
	var (
		mu       sync.Mutex
		limiters = make(map[string]*rate.Limiter)
	)

	getLimiter := func(key string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()

		if lim, ok := limiters[key]; ok {
			return lim
		}

		lim := rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), cfg.Burst)
		limiters[key] = lim
		return lim
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.RemoteAddr

			if userID, ok := r.Context().Value(UserIDKey).(uuid.UUID); ok {
				key = userID.String()
			}

			lim := getLimiter(key)
			if !lim.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)

				resp := envelope.Err(envelope.Error{
					Code:    apperror.ErrRateLimited.Code,
					Message: "too many requests, please slow down",
				})
				json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
