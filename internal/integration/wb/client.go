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

// APIError preserves machine-readable WB response metadata across service
// boundaries. In particular, callers must honor WB's Retry-After value rather
// than replacing it with an endpoint-wide guess.
type APIError struct {
	StatusCode int
	RetryAfter time.Duration
	URL        string
	Message    string
}

func (e *APIError) Error() string { return e.Message }

func (e *APIError) Unwrap() error {
	return apperror.New(apperror.ErrWBAPIError, e.Message)
}

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

type requestRetryPolicyKey struct{}

type requestRetryPolicy struct {
	retryRateLimit bool
}

// withoutRateLimitRetry is used for live bid writes executed while the caller
// holds a short control-plane lease. The 429 response is definitive (WB did not
// apply the write), so surfacing it immediately preserves safety without
// sleeping under the workspace advisory lock.
func withoutRateLimitRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestRetryPolicyKey{}, requestRetryPolicy{retryRateLimit: false})
}

func retryRateLimitForRequest(ctx context.Context) bool {
	policy, ok := ctx.Value(requestRetryPolicyKey{}).(requestRetryPolicy)
	return !ok || policy.retryRateLimit
}

// serviceUARoundTripper stamps the registered service User-Agent on every
// outgoing WB request (official APIs and the public showcase alike).
type serviceUARoundTripper struct {
	ua   string
	next http.RoundTripper
}

func (t serviceUARoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.ua != "" && req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", t.ua)
	}
	return t.next.RoundTrip(req)
}

// Client is the concrete HTTP client for the WB Advertising API.
type Client struct {
	httpClient               *http.Client
	baseURL                  string
	contentURL               string
	statisticsURL            string
	analyticsURL             string
	commonURL                string
	feedbacksURL             string
	pricesURL                string
	showcaseURL              string
	rateLimit                int
	fullStatsInterBatchDelay time.Duration
	normQueryInterBatchDelay time.Duration
	logger                   zerolog.Logger

	limiters *boundedLRU[*rate.Limiter]
	// Prices & Discounts has a separate shared account bucket (10 requests per
	// 6 seconds) across list/upload/poll/quarantine endpoints.
	priceLimiters *boundedLRU[*rate.Limiter]
	// campaignProductLimiters enforces the documented account-wide limit for
	// PATCH /adv/v0/auction/nms (one request per second for Personal/Service
	// access). It is separate from the general advertising API limiter because
	// the endpoint has a stricter bucket than most campaign operations.
	campaignProductLimiters *boundedLRU[*rate.Limiter]
	breakers                *boundedLRU[*gobreaker.CircuitBreaker[[]byte]]
}

// NewClient creates a new WB API client from the application config.
func NewClient(cfg *config.Config, logger zerolog.Logger) *Client {
	contentURL := "https://content-api.wildberries.ru"
	statisticsURL := "https://statistics-api.wildberries.ru"
	analyticsURL := "https://seller-analytics-api.wildberries.ru"
	commonURL := "https://common-api.wildberries.ru"
	feedbacksURL := "https://feedbacks-api.wildberries.ru"
	pricesURL := "https://discounts-prices-api.wildberries.ru"
	showcaseURL := "https://card.wb.ru"
	if strings.Contains(cfg.WBAPIBaseURL, "localhost") || strings.Contains(cfg.WBAPIBaseURL, "127.0.0.1") {
		contentURL = cfg.WBAPIBaseURL
		statisticsURL = cfg.WBAPIBaseURL
		analyticsURL = cfg.WBAPIBaseURL
		commonURL = cfg.WBAPIBaseURL
		feedbacksURL = cfg.WBAPIBaseURL
		pricesURL = cfg.WBAPIBaseURL
		showcaseURL = cfg.WBAPIBaseURL
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			// Партнёрская программа WB для сервисов: зарегистрированный User-Agent
			// должен стоять на ВСЕХ запросах к WB (по нему WB отделяет запросы
			// сервиса от ботов и валидирует заявку). Инжектим на транспорте, чтобы
			// покрыть каждый вызов без правки мест использования.
			Transport: serviceUARoundTripper{ua: cfg.WBServiceUserAgent, next: http.DefaultTransport},
		},
		baseURL:                  cfg.WBAPIBaseURL,
		contentURL:               contentURL,
		statisticsURL:            statisticsURL,
		analyticsURL:             analyticsURL,
		commonURL:                commonURL,
		feedbacksURL:             feedbacksURL,
		pricesURL:                pricesURL,
		showcaseURL:              showcaseURL,
		rateLimit:                cfg.WBAPIRateLimit,
		fullStatsInterBatchDelay: 20 * time.Second,
		normQueryInterBatchDelay: 7 * time.Second,
		logger:                   logger.With().Str("component", "wb_client").Logger(),
		limiters:                 newBoundedLRU[*rate.Limiter](tokenCacheCapacity, tokenCacheTTL),
		priceLimiters:            newBoundedLRU[*rate.Limiter](tokenCacheCapacity, tokenCacheTTL),
		campaignProductLimiters:  newBoundedLRU[*rate.Limiter](tokenCacheCapacity, tokenCacheTTL),
		breakers:                 newBoundedLRU[*gobreaker.CircuitBreaker[[]byte]](tokenCacheCapacity, tokenCacheTTL),
	}
}

// doCommonRequest executes requests against common-api.wildberries.ru.
func (c *Client) doCommonRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	var resp *http.Response
	start := time.Now()

	cb := c.breakerForToken(token)
	result, err := cb.Execute(func() ([]byte, error) {
		r, b, e := c.doRequestInnerURL(ctx, method, c.commonURL+path, token, body)
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

// doFeedbacksRequest executes requests against feedbacks-api.wildberries.ru.
func (c *Client) doFeedbacksRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	var resp *http.Response
	start := time.Now()

	cb := c.breakerForToken(token)
	result, err := cb.Execute(func() ([]byte, error) {
		r, b, e := c.doRequestInnerURL(ctx, method, c.feedbacksURL+path, token, body)
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

// ValidateToken performs a lightweight authenticated request to verify the token.
//
// We hit /adv/v1/promotion/count — a stable, low-cost endpoint on the
// advert API that requires the same auth surface as the rest of the data
// we read. The previous probe (/adv/v2/config/categories) was removed by
// WB sometime in 2025 and now returns 404 "path not found" for every
// caller, which bricked POST /seller-cabinets for all new tenants.
//
// /promotion/count returns a small JSON listing all active campaigns in
// 5–6 categories — a few hundred bytes, no rate-limit pressure, and the
// only failure modes are 401 (bad token) / 403 (no advert scope), which
// is exactly what we want to surface as "validation failed".
func (c *Client) ValidateToken(ctx context.Context, token string) error {
	_, _, err := c.doRequest(ctx, "GET", "/adv/v1/promotion/count", token, nil)
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

func (c *Client) priceLimiterForToken(token string) *rate.Limiter {
	if lim, ok := c.priceLimiters.Get(token); ok {
		return lim
	}
	// One request every 600ms is the conservative form of WB's documented
	// account-wide 10 requests / 6 seconds bucket and prevents endpoint bursts
	// from starving polling or uploads.
	lim := rate.NewLimiter(rate.Every(600*time.Millisecond), 1)
	c.priceLimiters.Set(token, lim)
	return lim
}

func (c *Client) campaignProductLimiterForToken(token string) *rate.Limiter {
	if lim, ok := c.campaignProductLimiters.Get(token); ok {
		return lim
	}
	lim := rate.NewLimiter(rate.Every(time.Second), 1)
	c.campaignProductLimiters.Set(token, lim)
	return lim
}

// breakerForToken returns a per-token circuit breaker (audit fix: HIGH #9).
// Same bounded-LRU semantics as limiterForToken.
func (c *Client) breakerForToken(token string) *gobreaker.CircuitBreaker[[]byte] {
	return c.breakerForTokenScope(token, "core")
}

// breakerForTokenScope keeps failures in independent WB API operations from
// blocking one another. In particular, a throttled goods-list sync must not
// prevent polling the result of an already submitted price upload.
func (c *Client) breakerForTokenScope(token, scope string) *gobreaker.CircuitBreaker[[]byte] {
	key := token
	if len(key) > 20 {
		key = key[:20]
	}
	key = scope + ":" + key
	if cb, ok := c.breakers.Get(key); ok {
		return cb
	}
	cb := newCircuitBreaker("wb-api-" + key)
	c.breakers.Set(key, cb)
	return cb
}

func priceBreakerScope(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v2/history/"), strings.HasPrefix(path, "/api/v2/buffer/"):
		return "prices-poll"
	case strings.HasPrefix(path, "/api/v2/list/"):
		return "prices-list"
	case strings.HasPrefix(path, "/api/v2/upload/"):
		return "prices-upload"
	case strings.HasPrefix(path, "/api/v2/quarantine/"):
		return "prices-quarantine"
	default:
		return "prices-other"
	}
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

// doPricesRequest executes requests against the WB Discounts & Prices API
// (pricesURL) with the same circuit breaker, retry, rate-limiting, and metrics
// as doRequest.
func (c *Client) doPricesRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	var resp *http.Response
	start := time.Now()
	if err := c.priceLimiterForToken(token).Wait(ctx); err != nil {
		return nil, nil, fmt.Errorf("prices rate limiter wait: %w", err)
	}

	cb := c.breakerForTokenScope(token, priceBreakerScope(path))
	result, err := cb.Execute(func() ([]byte, error) {
		r, b, e := c.doRequestInnerURL(ctx, method, c.pricesURL+path, token, body)
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

// doStatisticsRequest executes requests against the WB Statistics API.
func (c *Client) doStatisticsRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	var resp *http.Response
	start := time.Now()

	cb := c.breakerForToken(token)
	result, err := cb.Execute(func() ([]byte, error) {
		r, b, e := c.doRequestInnerURL(ctx, method, c.statisticsURL+path, token, body)
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

// doAnalyticsRequest executes requests against the WB Seller Analytics API.
func (c *Client) doAnalyticsRequest(ctx context.Context, method, path, token string, body io.Reader) (*http.Response, []byte, error) {
	var resp *http.Response
	start := time.Now()

	cb := c.breakerForToken(token)
	result, err := cb.Execute(func() ([]byte, error) {
		r, b, e := c.doRequestInnerURL(ctx, method, c.analyticsURL+path, token, body)
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

	// Non-idempotent writes (POST/PATCH/DELETE) must not be auto-retried on transport
	// errors or 5xx: the request may already have reached WB and been applied, so a
	// blind retry risks a duplicate effect (e.g. double budget deposit, duplicate
	// campaign). 429 is still safe to retry for any method — it means the request was
	// rejected before processing.
	idempotent := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

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

			if !idempotent {
				return nil, nil, lastErr
			}
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

			lastErr = &APIError{
				StatusCode: http.StatusTooManyRequests,
				RetryAfter: retryAfter,
				URL:        url,
				Message:    fmt.Sprintf("rate limited (429) on %s", url),
			}
			if !retryRateLimitForRequest(ctx) {
				return resp, respBody, lastErr
			}
			if attempt < maxRetries {
				if err := sleepWithContext(ctx, retryAfter); err != nil {
					return nil, nil, fmt.Errorf("%w: retry wait interrupted: %v", lastErr, err)
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

			if !idempotent {
				// A write may have been applied before WB returned 5xx; surface the
				// error to the caller instead of risking a duplicate retry.
				return resp, respBody, lastErr
			}
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
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err == nil && seconds > 0 {
		return time.Duration(seconds) * retryAfterUnit
	}
	if retryAt, dateErr := http.ParseTime(header); dateErr == nil {
		delay := time.Until(retryAt)
		if delay > 0 {
			return delay
		}
	}
	return defaultRetryAfter
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
