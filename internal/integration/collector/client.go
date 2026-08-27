// Package collector is a thin client for the Sellico reviews collector's
// internal export: the shop rating and per-product review aggregates the
// collector already gathers (WB itself, Ozon/Uzum via the bot server).
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProductRating is one product's review aggregate. There is no hard SKU key
// in the collector's Ozon reviews (the bot stores productName only), so the
// consumer matches by the card name.
type ProductRating struct {
	Name         string  `json:"name"`
	Rating       float64 `json:"rating"`
	ReviewsCount int64   `json:"reviews_count"`
}

// ShopRating is the whole shop's aggregate — the reliable, keyless signal.
type ShopRating struct {
	Rating       float64 `json:"rating"`
	ReviewsCount int64   `json:"reviews_count"`
}

// ReviewSummary is the GET /internal/reviews/summary payload.
type ReviewSummary struct {
	Shop     *ShopRating     `json:"shop"`
	Products []ProductRating `json:"products"`
}

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient wires the collector exports client. An empty token disables it.
func NewClient(baseURL, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.token != "" && c.baseURL != ""
}

// GetReviewSummary fetches the review aggregate for one CRM integration id
// (seller_cabinets.external_integration_id on our side).
func (c *Client) GetReviewSummary(ctx context.Context, integrationID string) (*ReviewSummary, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("collector client is disabled (no token configured)")
	}
	endpoint := c.baseURL + "/internal/reviews/summary?integration_id=" + url.QueryEscape(integrationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("collector reviews summary: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Service-Token", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("collector reviews summary: transport: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("collector reviews summary: status %d", resp.StatusCode)
	}
	var payload ReviewSummary
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("collector reviews summary: decode: %w", err)
	}
	return &payload, nil
}
