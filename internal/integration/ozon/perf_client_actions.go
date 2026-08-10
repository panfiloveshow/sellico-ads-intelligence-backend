package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
)

// Phase 2: Performance API writes (campaign lifecycle, budgets, per-SKU bids)
// and bid signals (competitive/minimum bids, limits, CPO search promo).
//
// Money conventions on this surface:
//   - CPC endpoints (campaign budgets, product bids, min/sku, competitive)
//     speak MICRO-rubles as strings — converted here at the boundary.
//   - CPO (search promo) endpoints speak plain rubles.

const (
	// competitiveBidsChunk is the documented SKU cap per competitive-bids call.
	competitiveBidsChunk = 200
	// minSKUBidsChunk is the documented SKU cap per min/sku call.
	minSKUBidsChunk = 200
	// cpoMinBidsChunk is the documented SKU cap per get_cpo_min_bids call.
	cpoMinBidsChunk = 200
	// searchPromoPageSize is the page size for search_promo/v2/products.
	searchPromoPageSize = 100
	// searchPromoMaxPages bounds pagination defensively (100 * 500 = 50k SKUs).
	searchPromoMaxPages = 500
)

// ActivateCampaign starts a campaign: POST /api/client/campaign/{id}/activate.
func (c *PerfClient) ActivateCampaign(ctx context.Context, creds Credentials, campaignID int64) error {
	_, err := c.doJSON(ctx, creds, http.MethodPost, fmt.Sprintf("/api/client/campaign/%d/activate", campaignID), nil, struct{}{})
	return err
}

// DeactivateCampaign stops a campaign: POST /api/client/campaign/{id}/deactivate.
func (c *PerfClient) DeactivateCampaign(ctx context.Context, creds Credentials, campaignID int64) error {
	_, err := c.doJSON(ctx, creds, http.MethodPost, fmt.Sprintf("/api/client/campaign/%d/deactivate", campaignID), nil, struct{}{})
	return err
}

// UpdateCampaign patches campaign budgets/dates: PATCH /api/client/campaign/{id}.
// Only non-nil patch fields are sent.
func (c *PerfClient) UpdateCampaign(ctx context.Context, creds Credentials, campaignID int64, patch CampaignPatch) error {
	body := map[string]any{}
	if patch.DailyBudgetRub != nil {
		body["dailyBudget"] = RubToMicroRub(*patch.DailyBudgetRub)
	}
	if patch.WeeklyBudgetRub != nil {
		body["weeklyBudget"] = RubToMicroRub(*patch.WeeklyBudgetRub)
	}
	if patch.FromDate != nil {
		body["fromDate"] = patch.FromDate.Format("2006-01-02")
	}
	if patch.ToDate != nil {
		body["toDate"] = patch.ToDate.Format("2006-01-02")
	}
	if len(body) == 0 {
		return fmt.Errorf("ozon perf: empty campaign patch")
	}
	_, err := c.doJSON(ctx, creds, http.MethodPatch, fmt.Sprintf("/api/client/campaign/%d", campaignID), nil, body)
	return err
}

// SetCampaignProductBids writes per-SKU bids: PUT /api/client/campaign/{id}/products.
// Field casing mirrors the phase-1 GET .../v2/products read shape (sku + bid,
// bid as a micro-ruble string); Ozon's uint64 SKUs are sent as strings.
func (c *PerfClient) SetCampaignProductBids(ctx context.Context, creds Credentials, campaignID int64, bids []ProductBid) error {
	if len(bids) == 0 {
		return fmt.Errorf("ozon perf: no bids to set")
	}
	type wireBid struct {
		SKU       string `json:"sku"`
		Bid       string `json:"bid"`
		TargetCIR string `json:"targetCir,omitempty"`
	}
	payload := struct {
		Bids []wireBid `json:"bids"`
	}{Bids: make([]wireBid, 0, len(bids))}
	for _, bid := range bids {
		wb := wireBid{
			SKU: strconv.FormatInt(bid.SKU, 10),
			Bid: RubFloatToMicroRub(bid.BidRub),
		}
		if bid.TargetCIR != nil {
			wb.TargetCIR = strconv.FormatFloat(*bid.TargetCIR, 'f', -1, 64)
		}
		payload.Bids = append(payload.Bids, wb)
	}
	_, err := c.doJSON(ctx, creds, http.MethodPut, fmt.Sprintf("/api/client/campaign/%d/products", campaignID), nil, payload)
	return err
}

// GetCompetitiveBids fetches competitor bid levels for campaign SKUs:
// GET /api/client/campaign/{id}/products/bids/competitive (≤200 SKU/call,
// chunked here). Bids arrive in micro-rubles and are converted to rubles.
func (c *PerfClient) GetCompetitiveBids(ctx context.Context, creds Credentials, campaignID int64, skus []int64) ([]CompetitiveBid, error) {
	var out []CompetitiveBid
	for start := 0; start < len(skus); start += competitiveBidsChunk {
		end := start + competitiveBidsChunk
		if end > len(skus) {
			end = len(skus)
		}
		// Параметр называется skus (Ozon: «have no skus in request» на sku=).
		query := url.Values{}
		for _, sku := range skus[start:end] {
			query.Add("skus", strconv.FormatInt(sku, 10))
		}
		body, err := c.doJSON(ctx, creds, http.MethodGet, fmt.Sprintf("/api/client/campaign/%d/products/bids/competitive", campaignID), query, nil)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Bids []struct {
				SKU flexInt64 `json:"sku"`
				Bid string    `json:"bid"`
			} `json:"bids"`
		}
		if err := decodeJSON(body, &resp, "competitive bids"); err != nil {
			return nil, err
		}
		for _, row := range resp.Bids {
			bidRub, convErr := MicroRubToRubFloat(row.Bid)
			if convErr != nil {
				c.logger.Warn().Err(convErr).Int64("sku", int64(row.SKU)).Msg("unparseable competitive bid")
				continue
			}
			out = append(out, CompetitiveBid{SKU: int64(row.SKU), BidRub: bidRub})
		}
	}
	return out, nil
}

// GetMinSKUBids fetches minimum allowed bids per SKU: POST /api/client/min/sku
// (≤200 SKU/call, chunked here). paymentType is CPC, CPC_TOP or CPO.
func (c *PerfClient) GetMinSKUBids(ctx context.Context, creds Credentials, skus []int64, paymentType string) ([]MinSKUBid, error) {
	var out []MinSKUBid
	for start := 0; start < len(skus); start += minSKUBidsChunk {
		end := start + minSKUBidsChunk
		if end > len(skus) {
			end = len(skus)
		}
		skuStrings := make([]string, 0, end-start)
		for _, sku := range skus[start:end] {
			skuStrings = append(skuStrings, strconv.FormatInt(sku, 10))
		}
		// Both key spellings are sent — the API has used "sku" and "skus"
		// across revisions and ignores unknown fields.
		payload := map[string]any{
			"sku":         skuStrings,
			"skus":        skuStrings,
			"paymentType": paymentType,
		}
		body, err := c.doJSON(ctx, creds, http.MethodPost, "/api/client/min/sku", nil, payload)
		if err != nil {
			return nil, err
		}
		rows, err := parseMinSKUBids(body)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// parseMinSKUBids tolerates the response arriving under different list keys.
func parseMinSKUBids(body []byte) ([]MinSKUBid, error) {
	type wireRow struct {
		SKU flexInt64 `json:"sku"`
		Bid string    `json:"bid"`
	}
	var resp struct {
		MinBids []wireRow `json:"minBids"`
		Bids    []wireRow `json:"bids"`
		Rows    []wireRow `json:"rows"`
	}
	if err := decodeJSON(body, &resp, "min sku bids"); err != nil {
		return nil, err
	}
	rows := resp.MinBids
	if len(rows) == 0 {
		rows = resp.Bids
	}
	if len(rows) == 0 {
		rows = resp.Rows
	}
	out := make([]MinSKUBid, 0, len(rows))
	for _, row := range rows {
		bidRub, err := MicroRubToRubFloat(row.Bid)
		if err != nil {
			continue // defensive: skip unparseable rows, keep the rest
		}
		out = append(out, MinSKUBid{SKU: int64(row.SKU), BidRub: bidRub})
	}
	return out, nil
}

// GetBidLimits fetches GET /api/client/limits/list and returns the raw JSON —
// the payload is a reference table served to the frontend as-is.
func (c *PerfClient) GetBidLimits(ctx context.Context, creds Credentials) (json.RawMessage, error) {
	body, err := c.doJSON(ctx, creds, http.MethodGet, "/api/client/limits/list", nil, nil)
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("ozon perf: limits/list returned invalid JSON")
	}
	return json.RawMessage(body), nil
}

// ListSearchPromoProducts pages through POST /api/client/campaign/search_promo/v2/products.
// CPO bids are plain rubles on this surface.
func (c *PerfClient) ListSearchPromoProducts(ctx context.Context, creds Credentials) ([]CPOProduct, error) {
	var out []CPOProduct
	// Pagination is 0-based (verified live 2026-08-10: page=0 returns the
	// first page; starting at 1 skipped straight past a single-page result
	// and returned nothing).
	for page := 0; page < searchPromoMaxPages; page++ {
		payload := map[string]any{"page": page, "pageSize": searchPromoPageSize}
		body, err := c.doJSON(ctx, creds, http.MethodPost, "/api/client/campaign/search_promo/v2/products", nil, payload)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Products []struct {
				SKU               flexInt64 `json:"sku"`
				SourceSku         string    `json:"sourceSku"`
				Title             string    `json:"title"`
				Price             flexFloat `json:"price"`
				Bid               flexFloat `json:"bid"`
				BidPrice          flexFloat `json:"bidPrice"`
				ImageURL          string    `json:"imageUrl"`
				VisibilityIndex   string    `json:"visibilityIndex"`
				Enabled           *bool     `json:"enabled"`
				IsEnabled         *bool     `json:"isEnabled"`
				SearchPromoStatus string    `json:"searchPromoStatus"`
			} `json:"products"`
		}
		if err := decodeJSON(body, &resp, "search promo products"); err != nil {
			return nil, err
		}
		for _, p := range resp.Products {
			// Presence in the response == in the promo: the v2 payload carries no
			// per-product enabled flag, so default to true and only honour an
			// explicit flag when an older/alternate response shape provides one.
			enabled := true
			switch {
			case p.Enabled != nil:
				enabled = *p.Enabled
			case p.IsEnabled != nil:
				enabled = *p.IsEnabled
			case p.SearchPromoStatus != "":
				enabled = p.SearchPromoStatus == "ENABLED" || p.SearchPromoStatus == "SEARCH_PROMO_STATUS_ENABLED"
			}
			out = append(out, CPOProduct{
				SKU:             int64(p.SKU),
				OfferID:         p.SourceSku,
				Name:            p.Title,
				PriceRub:        float64(p.Price),
				BidRub:          float64(p.Bid),
				BidPriceRub:     float64(p.BidPrice),
				ImageURL:        p.ImageURL,
				VisibilityIndex: p.VisibilityIndex,
				Enabled:         enabled,
			})
		}
		if len(resp.Products) < searchPromoPageSize {
			return out, nil
		}
	}
	c.logger.Warn().Int("pages", searchPromoMaxPages).Msg("search promo product pagination hit the defensive page cap")
	return out, nil
}

// EnableSearchPromo enables CPO promotion for SKUs:
// POST /api/client/search_promo/product/enable.
func (c *PerfClient) EnableSearchPromo(ctx context.Context, creds Credentials, skus []int64) error {
	return c.setSearchPromoEnabled(ctx, creds, "/api/client/search_promo/product/enable", skus)
}

// DisableSearchPromo disables CPO promotion for SKUs:
// POST /api/client/search_promo/product/disable.
func (c *PerfClient) DisableSearchPromo(ctx context.Context, creds Credentials, skus []int64) error {
	return c.setSearchPromoEnabled(ctx, creds, "/api/client/search_promo/product/disable", skus)
}

func (c *PerfClient) setSearchPromoEnabled(ctx context.Context, creds Credentials, path string, skus []int64) error {
	if len(skus) == 0 {
		return fmt.Errorf("ozon perf: no skus")
	}
	skuStrings := make([]string, 0, len(skus))
	for _, sku := range skus {
		skuStrings = append(skuStrings, strconv.FormatInt(sku, 10))
	}
	_, err := c.doJSON(ctx, creds, http.MethodPost, path, nil, map[string]any{"skus": skuStrings})
	return err
}

// SetSearchPromoBids writes fixed CPO bids (rubles):
// POST /api/client/campaign/search_promo/v2/bids/set.
func (c *PerfClient) SetSearchPromoBids(ctx context.Context, creds Credentials, bids []CPOBid) error {
	if len(bids) == 0 {
		return fmt.Errorf("ozon perf: no cpo bids to set")
	}
	type wireBid struct {
		SKU string  `json:"sku"`
		Bid float64 `json:"bid"`
	}
	payload := struct {
		Bids []wireBid `json:"bids"`
	}{Bids: make([]wireBid, 0, len(bids))}
	for _, bid := range bids {
		payload.Bids = append(payload.Bids, wireBid{SKU: strconv.FormatInt(bid.SKU, 10), Bid: bid.BidRub})
	}
	_, err := c.doJSON(ctx, creds, http.MethodPost, "/api/client/campaign/search_promo/v2/bids/set", nil, payload)
	return err
}

// GetCPOMinBids fetches minimum fixed CPO bids (rubles):
// POST /api/client/search_promo/get_cpo_min_bids (≤200 SKU/call, chunked).
func (c *PerfClient) GetCPOMinBids(ctx context.Context, creds Credentials, skus []int64) ([]MinSKUBid, error) {
	var out []MinSKUBid
	for start := 0; start < len(skus); start += cpoMinBidsChunk {
		end := start + cpoMinBidsChunk
		if end > len(skus) {
			end = len(skus)
		}
		skuStrings := make([]string, 0, end-start)
		for _, sku := range skus[start:end] {
			skuStrings = append(skuStrings, strconv.FormatInt(sku, 10))
		}
		body, err := c.doJSON(ctx, creds, http.MethodPost, "/api/client/search_promo/get_cpo_min_bids", nil, map[string]any{"skus": skuStrings})
		if err != nil {
			return nil, err
		}
		var resp struct {
			Bids []struct {
				SKU flexInt64 `json:"sku"`
				Bid flexFloat `json:"bid"`
			} `json:"bids"`
		}
		if err := decodeJSON(body, &resp, "cpo min bids"); err != nil {
			return nil, err
		}
		for _, row := range resp.Bids {
			out = append(out, MinSKUBid{SKU: int64(row.SKU), BidRub: float64(row.Bid)})
		}
	}
	return out, nil
}

// doJSON executes one authenticated Performance API call of any method with
// rate limiting, retry and metrics. Writes here are idempotent (activate,
// deactivate, absolute bid/budget values), so the same retry policy as reads
// applies: 401 invalidates the token cache, 429 honors Retry-After, 5xx backs
// off exponentially.
func (c *PerfClient) doJSON(ctx context.Context, creds Credentials, method, path string, query url.Values, payload any) ([]byte, error) {
	var bodyBytes []byte
	if payload != nil {
		var err error
		bodyBytes, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("ozon perf: marshal %s request: %w", path, err)
		}
	}

	lim := c.limiters.get(creds.PerfClientID)
	start := time.Now()
	defer func() {
		metrics.OzonAPILatency.WithLabelValues("performance", path).Observe(time.Since(start).Seconds())
	}()

	fullURL := c.baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		token, err := c.token(ctx, creds)
		if err != nil {
			return nil, err
		}
		if err := lim.Wait(ctx); err != nil {
			return nil, fmt.Errorf("ozon perf: rate limiter wait: %w", err)
		}

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
		if err != nil {
			return nil, fmt.Errorf("ozon perf: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("ozon perf: %s %s: %w", method, path, err)
			metrics.OzonAPIRequests.WithLabelValues("performance", path, "error").Inc()
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
			return nil, fmt.Errorf("ozon perf: read %s response: %w", path, readErr)
		}

		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			metrics.OzonAPIRequests.WithLabelValues("performance", path, "401").Inc()
			c.tokenMu.Lock()
			delete(c.tokens, creds.PerfClientID)
			c.tokenMu.Unlock()
			lastErr = &APIError{
				StatusCode: resp.StatusCode,
				URL:        fullURL,
				Message:    fmt.Sprintf("ozon perf: unauthorized (401) on %s %s", method, path),
			}
			continue
		case resp.StatusCode == http.StatusTooManyRequests:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			metrics.OzonAPIRequests.WithLabelValues("performance", path, "429").Inc()
			lastErr = &APIError{
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfter,
				URL:        fullURL,
				Message:    fmt.Sprintf("ozon perf: rate limited (429) on %s %s", method, path),
			}
			if attempt < maxRetries {
				if serr := sleepWithContext(ctx, retryAfter); serr != nil {
					return nil, fmt.Errorf("%w: retry wait interrupted: %v", lastErr, serr)
				}
			}
			continue
		case resp.StatusCode >= 500:
			metrics.OzonAPIRequests.WithLabelValues("performance", path, fmt.Sprintf("%d", resp.StatusCode)).Inc()
			lastErr = &APIError{
				StatusCode: resp.StatusCode,
				URL:        fullURL,
				Message:    fmt.Sprintf("ozon perf: server error (%d) on %s %s", resp.StatusCode, method, path),
			}
			if attempt < maxRetries {
				if serr := sleepWithContext(ctx, backoffDuration(attempt)); serr != nil {
					return nil, serr
				}
			}
			continue
		case resp.StatusCode >= 400:
			metrics.OzonAPIRequests.WithLabelValues("performance", path, fmt.Sprintf("%d", resp.StatusCode)).Inc()
			snippet := string(respBody)
			if len(snippet) > 300 {
				snippet = snippet[:300]
			}
			return nil, &APIError{
				StatusCode: resp.StatusCode,
				URL:        fullURL,
				Message:    fmt.Sprintf("ozon perf: client error (%d) on %s %s: %s", resp.StatusCode, method, path, snippet),
			}
		}

		metrics.OzonAPIRequests.WithLabelValues("performance", path, "ok").Inc()
		return respBody, nil
	}

	return nil, fmt.Errorf("ozon perf: all %d attempts exhausted: %w", maxRetries, lastErr)
}
