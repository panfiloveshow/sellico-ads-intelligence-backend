package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	"github.com/rs/zerolog"
)

const (
	defaultSearchURL  = "https://search.wb.ru/exactmatch/ru/common/v7/search"
	maxRetries        = 3
	backoffMultiplier = 3.0
	resultsPerPage    = 100
)

var baseBackoff = 1 * time.Second

// Parser is the WB_Catalog_Parser that searches the public WB catalog.
type Parser struct {
	searchURL string
	pool      *ProxyPool
	minDelay  time.Duration
	logger    zerolog.Logger

	mu      sync.Mutex
	lastReq time.Time
}

// NewParser creates a new catalog parser from the application config.
func NewParser(cfg *config.Config, logger zerolog.Logger) *Parser {
	return &Parser{
		searchURL: defaultSearchURL,
		pool:      NewProxyPool(cfg.WBParserProxies),
		minDelay:  cfg.WBParserMinDelay,
		logger:    logger.With().Str("component", "wb_catalog_parser").Logger(),
	}
}

// SearchProducts queries search.wb.ru for the given query and region,
// returning structured products with computed positions.
func (p *Parser) SearchProducts(ctx context.Context, query, region string) ([]CatalogProduct, error) {
	body, err := p.doRequestWithRetry(ctx, query, region)
	if err != nil {
		return nil, err
	}

	var resp CatalogSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		p.logger.Error().Err(err).Str("query", query).Msg("failed to parse catalog response — format may have changed")
		return nil, apperror.New(apperror.ErrInternal, "catalog response format mismatch")
	}

	products := make([]CatalogProduct, 0, len(resp.Data.Products))
	for i, dto := range resp.Data.Products {
		products = append(products, CatalogProduct{
			ID:       dto.ID,
			Name:     dto.Name,
			Brand:    dto.Brand,
			Price:    dto.PriceU,
			Rating:   dto.Rating,
			Reviews:  dto.Feedbacks,
			Position: i + 1,
			Page:     (i / resultsPerPage) + 1,
		})
	}

	return products, nil
}

// FindProductPosition searches for a specific product in the catalog results
// and returns its 1-based position. Returns -1 if the product is not found.
func (p *Parser) FindProductPosition(ctx context.Context, query, region string, productID int64) (int, error) {
	products, err := p.SearchProducts(ctx, query, region)
	if err != nil {
		return -1, err
	}

	for _, prod := range products {
		if prod.ID == productID {
			return prod.Position, nil
		}
	}

	return -1, nil
}

// doRequestWithRetry performs the HTTP request with retry, proxy rotation, and rate limiting.
func (p *Parser) doRequestWithRetry(ctx context.Context, query, region string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Enforce minimum delay between requests.
		if err := p.waitMinDelay(ctx); err != nil {
			return nil, err
		}

		proxy := p.pool.Next()
		if proxy == "" && p.pool.AllBlocked() {
			p.logger.Error().Msg("all proxies blocked — graceful degradation")
			return nil, apperror.New(apperror.ErrInternal, "all catalog proxies are blocked")
		}

		body, err := p.doSingleRequest(ctx, query, region, proxy, attempt)
		if err == nil {
			return body, nil
		}

		lastErr = err

		// On HTTP 403/429 block the proxy and try the next one immediately.
		if isForbiddenOrRateLimited(err) && proxy != "" {
			p.pool.Block(proxy)
			p.logger.Debug().Str("proxy", proxy).Int("attempt", attempt).Msg("proxy blocked, rotating")
			continue
		}

		// Exponential backoff for other errors.
		if attempt < maxRetries {
			sleepDur := backoffDuration(attempt)
			if err := sleepWithContext(ctx, sleepDur); err != nil {
				return nil, err
			}
		}
	}

	return nil, fmt.Errorf("catalog parser: all %d attempts exhausted: %w", maxRetries, lastErr)
}

func (p *Parser) doSingleRequest(ctx context.Context, query, region, proxy string, attempt int) ([]byte, error) {
	reqURL := p.buildURL(query, region)

	client := &http.Client{Timeout: 15 * time.Second}
	if t := ProxyTransport(proxy); t != nil {
		client.Transport = t
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)

	p.logger.Debug().
		Str("url", reqURL).
		Str("proxy", proxy).
		Int("attempt", attempt).
		Dur("elapsed", elapsed).
		Err(err).
		Msg("catalog request")

	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	p.logger.Debug().
		Int("status", resp.StatusCode).
		Str("url", reqURL).
		Str("proxy", proxy).
		Msg("catalog response")

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, &proxyError{statusCode: resp.StatusCode}
	}

	if resp.StatusCode >= 400 {
		return nil, apperror.New(apperror.ErrInternal, fmt.Sprintf("catalog HTTP %d", resp.StatusCode))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return body, nil
}

func (p *Parser) buildURL(query, region string) string {
	u, _ := url.Parse(p.searchURL)
	q := u.Query()
	q.Set("query", query)
	if region != "" {
		q.Set("dest", region)
	}
	q.Set("resultset", "catalog")
	q.Set("suppressSpellcheck", "false")
	u.RawQuery = q.Encode()
	return u.String()
}

func (p *Parser) waitMinDelay(ctx context.Context) error {
	p.mu.Lock()
	elapsed := time.Since(p.lastReq)
	wait := p.minDelay - elapsed
	p.lastReq = time.Now()
	if wait > 0 {
		p.lastReq = p.lastReq.Add(wait)
	}
	p.mu.Unlock()

	if wait > 0 {
		return sleepWithContext(ctx, wait)
	}
	return nil
}

// --- helpers ---

type proxyError struct {
	statusCode int
}

func (e *proxyError) Error() string {
	return fmt.Sprintf("proxy error: HTTP %d", e.statusCode)
}

func isForbiddenOrRateLimited(err error) bool {
	if pe, ok := err.(*proxyError); ok {
		return pe.statusCode == http.StatusForbidden || pe.statusCode == http.StatusTooManyRequests
	}
	return false
}

func backoffDuration(attempt int) time.Duration {
	return time.Duration(math.Pow(backoffMultiplier, float64(attempt-1))) * baseBackoff
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
