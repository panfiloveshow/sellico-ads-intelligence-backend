package wb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
	"github.com/rs/zerolog"
	"github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"
)

const (
	maxRetries        = 3
	backoffMultiplier = 3.0

	// Per-token caches are bounded so a high-cardinality token stream (e.g.
	// many workspaces, or rotated tokens) cannot grow the resident set
	// without limit. TTL is set conservatively — a token unused for an hour
	// gets a fresh limiter/breaker on next use, which matches the behaviour
	// callers would expect after that idle period anyway.
	tokenCacheCapacity = 1000
	tokenCacheTTL      = time.Hour
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
	contentURL string
	rateLimit  int
	logger     zerolog.Logger

	limiters *boundedLRU[*rate.Limiter]
	breakers *boundedLRU[*gobreaker.CircuitBreaker[[]byte]]
}

// NewClient creates a new WB API client from the application config.
func NewClient(cfg *config.Config, logger zerolog.Logger) *Client {
	contentURL := "https://content-api.wildberries.ru"
	if strings.Contains(cfg.WBAPIBaseURL, "localhost") || strings.Contains(cfg.WBAPIBaseURL, "127.0.0.1") {
		contentURL = cfg.WBAPIBaseURL
	}

	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    cfg.WBAPIBaseURL,
		contentURL: contentURL,
		rateLimit:  cfg.WBAPIRateLimit,
		logger:     logger.With().Str("component", "wb_client").Logger(),
		limiters:   newBoundedLRU[*rate.Limiter](tokenCacheCapacity, tokenCacheTTL),
		breakers:   newBoundedLRU[*gobreaker.CircuitBreaker[[]byte]](tokenCacheCapacity, tokenCacheTTL),
	}
}

// ValidateToken performs a lightweight authenticated request to verify the token.
func (c *Client) ValidateToken(ctx context.Context, token string) error {
	_, err := c.GetCategoryConfig(ctx, token)
	return err
}

// limiterForToken returns a per-token rate limiter, creating one if it doesn't exist.
// Backed by a bounded LRU+TTL cache (see boundedLRU): unused tokens age out so
// the resident set stays bounded even with high-cardinality token streams.
func (c *Client) limiterForToken(token string) *rate.Limiter {
	if lim, ok := c.limiters.Get(token); ok {
		return lim
	}
	lim := rate.NewLimiter(rate.Limit(c.rateLimit), c.rateLimit)
	c.limiters.Set(token, lim)
	return lim
}

// breakerForToken returns a per-token circuit breaker (audit fix: HIGH #9).
// Same bounded-LRU semantics as limiterForToken.
func (c *Client) breakerForToken(token string) *gobreaker.CircuitBreaker[[]byte] {
	key := token
	if len(key) > 20 {
		key = key[:20]
	}
	if cb, ok := c.breakers.Get(key); ok {
		return cb
	}
	cb := newCircuitBreaker("wb-api-" + key)
	c.breakers.Set(key, cb)
	return cb
}

// doRequest executes an HTTP request with circuit breaker, retry logic, rate-limiting, and logging.
// It handles HTTP 429 (rate limit) and HTTP 5xx (server errors) with appropriate backoff.

func (c *Client) doRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	var resp *http.Response
	start := time.Now()

	cb := c.breakerForToken(token)
	result, err := cb.Execute(func() ([]byte, error) {
		r, b, e := c.doRequestInner(ctx, method, path, token, body)
		resp = r
		return b, e
	})

	duration := time.Since(start).Seconds()
	metrics.WBAPILatency.WithLabelValues(path).Observe(duration)

	if err != nil {
		metrics.WBAPIRequests.WithLabelValues(path, "error").Inc()
		if resp != nil {
			return resp, result, err
		}
		return nil, nil, err
	}

	status := "ok"
	if resp != nil && resp.StatusCode >= 400 {
		status = fmt.Sprintf("%d", resp.StatusCode)
	}
	metrics.WBAPIRequests.WithLabelValues(path, status).Inc()

	return resp, result, nil
}

// doContentRequest executes requests against the WB Content API (contentURL)
// with the same circuit breaker, retry, rate-limiting, and metrics as doRequest.
func (c *Client) doContentRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	var resp *http.Response
	start := time.Now()

	cb := c.breakerForToken(token)
	result, err := cb.Execute(func() ([]byte, error) {
		r, b, e := c.doRequestInnerURL(ctx, method, c.contentURL+path, token, body)
		resp = r
		return b, e
	})

	duration := time.Since(start).Seconds()
	metrics.WBAPILatency.WithLabelValues(path).Observe(duration)

	if err != nil {
		metrics.WBAPIRequests.WithLabelValues(path, "error").Inc()
		if resp != nil {
			return resp, result, err
		}
		return nil, nil, err
	}

	status := "ok"
	if resp != nil && resp.StatusCode >= 400 {
		status = fmt.Sprintf("%d", resp.StatusCode)
	}
	metrics.WBAPIRequests.WithLabelValues(path, status).Inc()

	return resp, result, nil
}

// doRequestInner is the actual HTTP execution with retry logic.
func (c *Client) doRequestInner(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	return c.doRequestInnerURL(ctx, method, c.baseURL+path, token, body)
}

// doRequestInnerURL is the actual HTTP execution with retry logic for an arbitrary base URL.
func (c *Client) doRequestInnerURL(ctx context.Context, method, url, token string, body io.Reader) (*http.Response, []byte, error) {
	lim := c.limiterForToken(token)

	// Buffer body so it can be re-read on retry (fix: audit MEDIUM #15)
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("read request body: %w", err)
		}
	}

	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Wait for rate limiter
		if err := lim.Wait(ctx); err != nil {
			return nil, nil, fmt.Errorf("rate limiter wait: %w", err)
		}

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
		if err != nil {
			return nil, nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", token)
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

			lastErr = apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("rate limited (429) on %s", url))
			if attempt < maxRetries {
				if err := sleepWithContext(ctx, retryAfter); err != nil {
					return nil, nil, err
				}
			}
			continue
		}

		// Handle HTTP 5xx — server error
		if resp.StatusCode >= 500 {
			lastErr = apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("server error (%d) on %s", resp.StatusCode, url))
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
			return resp, respBody, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("client error (%d) on %s", resp.StatusCode, url))
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
