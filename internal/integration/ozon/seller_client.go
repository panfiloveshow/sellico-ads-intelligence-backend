package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
)

const (
	sellerPageSize = 1000
	// Documented account limit is 50 rps; we stay far below it (5 rps per
	// cabinet) — phase 1 syncs are batch jobs with no latency pressure.
	sellerRPS = 5
)

// SellerClient talks to api-seller.ozon.ru (auth: Client-Id + Api-Key headers,
// all endpoints are POST JSON).
type SellerClient struct {
	baseURL    string
	httpClient *http.Client
	logger     zerolog.Logger
	limiters   *limiterPool
}

// NewSellerClient creates a Seller API client from the application config.
func NewSellerClient(cfg *config.Config, logger zerolog.Logger) *SellerClient {
	return &SellerClient{
		baseURL:    cfg.OzonSellerAPIBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger.With().Str("component", "ozon_seller_client").Logger(),
		limiters:   newLimiterPool(rate.Limit(sellerRPS), sellerRPS),
	}
}

// ListProducts pages through POST /v3/product/list (visibility ALL) and
// returns every product_id/offer_id pair in the cabinet.
func (c *SellerClient) ListProducts(ctx context.Context, creds Credentials) ([]ProductRef, error) {
	type request struct {
		Filter struct {
			Visibility string `json:"visibility"`
		} `json:"filter"`
		LastID string `json:"last_id"`
		Limit  int    `json:"limit"`
	}
	type response struct {
		Result struct {
			Items []struct {
				ProductID flexInt64 `json:"product_id"`
				OfferID   string    `json:"offer_id"`
			} `json:"items"`
			LastID string    `json:"last_id"`
			Total  flexInt64 `json:"total"`
		} `json:"result"`
	}

	var out []ProductRef
	lastID := ""
	for {
		req := request{LastID: lastID, Limit: sellerPageSize}
		req.Filter.Visibility = "ALL"

		body, err := c.do(ctx, creds, "/v3/product/list", req)
		if err != nil {
			return nil, err
		}
		var resp response
		if err := decodeJSON(body, &resp, "product/list"); err != nil {
			return nil, err
		}
		for _, item := range resp.Result.Items {
			out = append(out, ProductRef{ProductID: int64(item.ProductID), OfferID: item.OfferID})
		}
		if len(resp.Result.Items) < sellerPageSize || resp.Result.LastID == "" {
			return out, nil
		}
		lastID = resp.Result.LastID
	}
}

// ListProductPrices pages through POST /v5/product/info/prices (cursor
// pagination) and returns parsed price + commission rows. The API sends
// money values as strings — they are parsed to float rubles here.
func (c *SellerClient) ListProductPrices(ctx context.Context, creds Credentials) ([]ProductPrice, error) {
	type request struct {
		Filter struct {
			Visibility string `json:"visibility"`
		} `json:"filter"`
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit"`
	}
	// Docs describe money fields as strings, but the live API sends plain
	// numbers — flexFloat accepts both.
	type wirePrice struct {
		Price                flexFloat `json:"price"`
		OldPrice             flexFloat `json:"old_price"`
		MinPrice             flexFloat `json:"min_price"`
		NetPrice             flexFloat `json:"net_price"`
		MarketingSellerPrice flexFloat `json:"marketing_seller_price"`
		VAT                  flexFloat `json:"vat"`
	}
	// wireIndexData is one competitor block inside price_indexes. The docs
	// have renamed the minimum-price key across versions (min_price vs
	// minimal_price) — parse both defensively and take whichever is set.
	type wireIndexData struct {
		MinPrice     flexFloat `json:"min_price"`
		MinimalPrice flexFloat `json:"minimal_price"`
	}
	type wireItem struct {
		ProductID   flexInt64 `json:"product_id"`
		OfferID     string    `json:"offer_id"`
		Price       wirePrice `json:"price"`
		Commissions struct {
			SalesPercentFBO flexFloat `json:"sales_percent_fbo"`
			SalesPercentFBS flexFloat `json:"sales_percent_fbs"`
		} `json:"commissions"`
		Acquiring    flexFloat `json:"acquiring"`
		PriceIndexes struct {
			ColorIndex        string        `json:"color_index"`
			OzonIndexData     wireIndexData `json:"ozon_index_data"`
			ExternalIndexData wireIndexData `json:"external_index_data"`
			SelfIndexData     wireIndexData `json:"self_marketplaces_index_data"`
		} `json:"price_indexes"`
	}
	indexMin := func(d wireIndexData) float64 {
		if v := float64(d.MinPrice); v > 0 {
			return v
		}
		return float64(d.MinimalPrice)
	}
	type response struct {
		Items  []wireItem `json:"items"`
		Cursor string     `json:"cursor"`
		Total  flexInt64  `json:"total"`
	}

	var out []ProductPrice
	cursor := ""
	for {
		req := request{Cursor: cursor, Limit: sellerPageSize}
		req.Filter.Visibility = "ALL"

		body, err := c.do(ctx, creds, "/v5/product/info/prices", req)
		if err != nil {
			return nil, err
		}
		var resp response
		if err := decodeJSON(body, &resp, "product/info/prices"); err != nil {
			return nil, err
		}
		for _, item := range resp.Items {
			row := ProductPrice{
				ProductID:        int64(item.ProductID),
				OfferID:          item.OfferID,
				VATPct:           float64(item.Price.VAT) * 100,
				ColorIndex:       item.PriceIndexes.ColorIndex,
				CommissionFBOPct: float64(item.Commissions.SalesPercentFBO),
				CommissionFBSPct: float64(item.Commissions.SalesPercentFBS),
				AcquiringPct:     float64(item.Acquiring),
			}
			row.PriceRub = float64(item.Price.Price)
			if row.PriceRub == 0 {
				c.logger.Warn().Int64("product_id", row.ProductID).Msg("skip product with zero price")
				continue
			}
			row.OldPriceRub = float64(item.Price.OldPrice)
			row.MinPriceRub = float64(item.Price.MinPrice)
			row.NetPriceRub = float64(item.Price.NetPrice)
			row.MarketingSellerPriceRub = float64(item.Price.MarketingSellerPrice)
			row.OzonIndexMinPriceRub = indexMin(item.PriceIndexes.OzonIndexData)
			row.ExternalIndexMinPriceRub = indexMin(item.PriceIndexes.ExternalIndexData)
			row.SelfIndexMinPriceRub = indexMin(item.PriceIndexes.SelfIndexData)
			out = append(out, row)
		}
		if len(resp.Items) < sellerPageSize || resp.Cursor == "" {
			return out, nil
		}
		cursor = resp.Cursor
	}
}

// updatePricesChunkSize is the Ozon limit for one import/prices request.
const updatePricesChunkSize = 1000

// UpdatePrices writes product prices via POST /v1/product/import/prices,
// chunked at 1000 items per request (the Ozon limit). Money is SENT as
// decimal strings per the docs (responses may be numbers, but the write side
// keeps strings). The returned slice carries one outcome per submitted item;
// a transport error aborts the remaining chunks and is returned alongside
// the outcomes collected so far.
//
// NOTE: Ozon rejects more than 10 price changes per product per hour; that
// budget is enforced by the service layer via ozon_price_changes history,
// not here.
func (c *SellerClient) UpdatePrices(ctx context.Context, creds Credentials, items []PriceUpdate) ([]PriceUpdateResult, error) {
	type wirePrice struct {
		ProductID    int64  `json:"product_id,omitempty"`
		OfferID      string `json:"offer_id,omitempty"`
		Price        string `json:"price"`
		OldPrice     string `json:"old_price,omitempty"`
		MinPrice     string `json:"min_price,omitempty"`
		CurrencyCode string `json:"currency_code"`
	}
	type request struct {
		Prices []wirePrice `json:"prices"`
	}
	type wireError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	type wireResult struct {
		ProductID flexInt64   `json:"product_id"`
		OfferID   string      `json:"offer_id"`
		Updated   bool        `json:"updated"`
		Errors    []wireError `json:"errors"`
	}
	type response struct {
		Result []wireResult `json:"result"`
	}

	formatRub := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

	out := make([]PriceUpdateResult, 0, len(items))
	for start := 0; start < len(items); start += updatePricesChunkSize {
		end := start + updatePricesChunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		req := request{Prices: make([]wirePrice, 0, len(chunk))}
		for _, item := range chunk {
			price := wirePrice{
				ProductID:    item.ProductID,
				OfferID:      item.OfferID,
				Price:        formatRub(item.PriceRub),
				CurrencyCode: "RUB",
			}
			if item.OldPriceRub != nil {
				price.OldPrice = formatRub(*item.OldPriceRub)
			}
			if item.MinPriceRub != nil {
				price.MinPrice = formatRub(*item.MinPriceRub)
			}
			req.Prices = append(req.Prices, price)
		}

		body, err := c.do(ctx, creds, "/v1/product/import/prices", req)
		if err != nil {
			return out, err
		}
		var resp response
		if err := decodeJSON(body, &resp, "product/import/prices"); err != nil {
			return out, err
		}
		for _, result := range resp.Result {
			item := PriceUpdateResult{
				ProductID: int64(result.ProductID),
				OfferID:   result.OfferID,
				Updated:   result.Updated,
			}
			for _, wireErr := range result.Errors {
				item.Errors = append(item.Errors, fmt.Sprintf("%s: %s", wireErr.Code, wireErr.Message))
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// do executes one Seller API POST with rate limiting, retry (429 honors
// Retry-After, 5xx uses exponential backoff — these are idempotent reads)
// and Prometheus metrics.
func (c *SellerClient) do(ctx context.Context, creds Credentials, path string, payload any) ([]byte, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("ozon seller: marshal %s request: %w", path, err)
	}

	lim := c.limiters.get(creds.ClientID)
	start := time.Now()
	defer func() {
		metrics.OzonAPILatency.WithLabelValues("seller", path).Observe(time.Since(start).Seconds())
	}()

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := lim.Wait(ctx); err != nil {
			return nil, fmt.Errorf("ozon seller: rate limiter wait: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("ozon seller: create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Client-Id", creds.ClientID)
		req.Header.Set("Api-Key", creds.APIKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("ozon seller: %s: %w", path, err)
			metrics.OzonAPIRequests.WithLabelValues("seller", path, "error").Inc()
			if attempt < maxRetries {
				if serr := sleepWithContext(ctx, backoffDuration(attempt)); serr != nil {
					return nil, serr
				}
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("ozon seller: read %s response: %w", path, readErr)
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			metrics.OzonAPIRequests.WithLabelValues("seller", path, "429").Inc()
			lastErr = &APIError{
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfter,
				URL:        c.baseURL + path,
				Message:    fmt.Sprintf("ozon seller: rate limited (429) on %s", path),
			}
			if attempt < maxRetries {
				if serr := sleepWithContext(ctx, retryAfter); serr != nil {
					return nil, fmt.Errorf("%w: retry wait interrupted: %v", lastErr, serr)
				}
			}
			continue
		case resp.StatusCode >= 500:
			metrics.OzonAPIRequests.WithLabelValues("seller", path, fmt.Sprintf("%d", resp.StatusCode)).Inc()
			lastErr = &APIError{
				StatusCode: resp.StatusCode,
				URL:        c.baseURL + path,
				Message:    fmt.Sprintf("ozon seller: server error (%d) on %s", resp.StatusCode, path),
			}
			if attempt < maxRetries {
				if serr := sleepWithContext(ctx, backoffDuration(attempt)); serr != nil {
					return nil, serr
				}
			}
			continue
		case resp.StatusCode >= 400:
			metrics.OzonAPIRequests.WithLabelValues("seller", path, fmt.Sprintf("%d", resp.StatusCode)).Inc()
			snippet := string(respBody)
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			return nil, &APIError{
				StatusCode: resp.StatusCode,
				URL:        c.baseURL + path,
				Message:    fmt.Sprintf("ozon seller: client error (%d) on %s: %s", resp.StatusCode, path, snippet),
			}
		}

		metrics.OzonAPIRequests.WithLabelValues("seller", path, "ok").Inc()
		return respBody, nil
	}

	return nil, fmt.Errorf("ozon seller: all %d attempts exhausted: %w", maxRetries, lastErr)
}
