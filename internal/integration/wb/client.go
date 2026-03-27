package wb

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/rs/zerolog"
	"golang.org/x/time/rate"
)

const (
	maxRetries        = 3
	backoffMultiplier = 3.0
)

var (
	defaultRetryAfter = 60 * time.Second
	baseBackoff       = 1 * time.Second
	retryAfterUnit    = time.Second
)

// Client is the concrete HTTP client for the WB Advertising API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	rateLimit  int
	logger     zerolog.Logger

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// NewClient creates a new WB API client from the application config.
func NewClient(cfg *config.Config, logger zerolog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    cfg.WBAPIBaseURL,
		rateLimit:  cfg.WBAPIRateLimit,
		logger:     logger.With().Str("component", "wb_client").Logger(),
		limiters:   make(map[string]*rate.Limiter),
	}
}

// ValidateToken performs a lightweight authenticated request to verify the token.
func (c *Client) ValidateToken(ctx context.Context, token string) error {
	_, err := c.GetCategoryConfig(ctx, token)
	return err
}

// limiterForToken returns a per-token rate limiter, creating one if it doesn't exist.
func (c *Client) limiterForToken(token string) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()

	if lim, ok := c.limiters[token]; ok {
		return lim
	}

	lim := rate.NewLimiter(rate.Limit(c.rateLimit), c.rateLimit)
	c.limiters[token] = lim
	return lim
}

// doRequest executes an HTTP request with retry logic, rate-limiting, and logging.
// It handles HTTP 429 (rate limit) and HTTP 5xx (server errors) with appropriate backoff.
func (c *Client) doRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	url := c.baseURL + path
	lim := c.limiterForToken(token)

	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Wait for rate limiter
		if err := lim.Wait(ctx); err != nil {
			return nil, nil, fmt.Errorf("rate limiter wait: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return nil, nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		c.logger.Debug().
			Str("method", method).
			Str("url", url).
			Int("attempt", attempt).
			Msg("sending request")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http request failed: %w", err)
			c.logger.Debug().
				Err(err).
				Str("method", method).
				Str("url", url).
				Int("attempt", attempt).
				Msg("request error")

			if attempt < maxRetries {
				sleepDuration := backoffDuration(attempt)
				if err := sleepWithContext(ctx, sleepDuration); err != nil {
					return nil, nil, err
				}
			}
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("read response body: %w", err)
		}

		c.logger.Debug().
			Str("method", method).
			Str("url", url).
			Int("status_code", resp.StatusCode).
			Int("attempt", attempt).
			Msg("received response")

		// Handle HTTP 429 — rate limited
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			c.logger.Debug().
				Str("url", url).
				Dur("retry_after", retryAfter).
				Int("attempt", attempt).
				Msg("rate limited (429), pausing")

			lastErr = apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("rate limited (429) on %s", path))
			if attempt < maxRetries {
				if err := sleepWithContext(ctx, retryAfter); err != nil {
					return nil, nil, err
				}
			}
			continue
		}

		// Handle HTTP 5xx — server error
		if resp.StatusCode >= 500 {
			lastErr = apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("server error (%d) on %s", resp.StatusCode, path))
			c.logger.Debug().
				Str("url", url).
				Int("status_code", resp.StatusCode).
				Int("attempt", attempt).
				Msg("server error, retrying with backoff")

			if attempt < maxRetries {
				sleepDuration := backoffDuration(attempt)
				if err := sleepWithContext(ctx, sleepDuration); err != nil {
					return nil, nil, err
				}
			}
			continue
		}

		// Non-retryable error status codes (4xx except 429)
		if resp.StatusCode >= 400 {
			return resp, respBody, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("client error (%d) on %s", resp.StatusCode, path))
		}

		// Success
		return resp, respBody, nil
	}

	return nil, nil, fmt.Errorf("all %d attempts exhausted: %w", maxRetries, lastErr)
}

// backoffDuration calculates exponential backoff: 1s, 3s, 9s.
func backoffDuration(attempt int) time.Duration {
	return time.Duration(math.Pow(backoffMultiplier, float64(attempt-1))) * baseBackoff
}

// parseRetryAfter parses the Retry-After header value (seconds).
// Returns defaultRetryAfter if the header is missing or unparseable.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return defaultRetryAfter
	}
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds <= 0 {
		return defaultRetryAfter
	}
	return time.Duration(seconds) * retryAfterUnit
}

// sleepWithContext sleeps for the given duration, respecting context cancellation.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
