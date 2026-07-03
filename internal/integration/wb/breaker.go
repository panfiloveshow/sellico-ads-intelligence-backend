package wb

import (
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
	"github.com/sony/gobreaker/v2"
)

// isClientError reports whether err is a WB 4xx client error (e.g. a bad advert id
// or validation failure). These are application-level errors that must NOT trip the
// circuit breaker — only transport failures, 5xx, and exhausted 429s indicate the
// WB service itself is unhealthy. 429 is never reported here (it is retried inside
// the client and surfaces as "rate limited"/"exhausted", not "client error").
func isClientError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "client error (")
}

// newCircuitBreaker creates a circuit breaker for WB API calls.
// Opens after 5 consecutive failures, stays open for 30 seconds,
// then moves to half-open to test if the service has recovered.
func newCircuitBreaker(name string) *gobreaker.CircuitBreaker[[]byte] {
	metrics.WBBreakerState.WithLabelValues(name).Set(float64(gobreaker.StateClosed))
	return gobreaker.NewCircuitBreaker[[]byte](gobreaker.Settings{
		Name:        name,
		MaxRequests: 2,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		IsSuccessful: func(err error) bool {
			// Treat success and benign 4xx client errors as "not a failure" so a run
			// of legitimate 400/404s (common on /adv/v3/fullstats) cannot open the
			// breaker and lock out an entire cabinet's healthy calls.
			return err == nil || isClientError(err)
		},
		OnStateChange: func(name string, _, to gobreaker.State) {
			metrics.WBBreakerState.WithLabelValues(name).Set(float64(to))
		},
	})
}
