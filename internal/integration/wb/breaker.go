package wb

import (
	"time"

	"github.com/sony/gobreaker/v2"
)

// newCircuitBreaker creates a circuit breaker for WB API calls.
// Opens after 5 consecutive failures, stays open for 30 seconds,
// then moves to half-open to test if the service has recovered.
func newCircuitBreaker(name string) *gobreaker.CircuitBreaker[[]byte] {
	return gobreaker.NewCircuitBreaker[[]byte](gobreaker.Settings{
		Name:        name,
		MaxRequests: 2,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
	})
}
