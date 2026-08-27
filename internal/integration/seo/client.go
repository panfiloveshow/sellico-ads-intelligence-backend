// Package seo is a thin client for the Sellico SEO service's internal
// exports. The SEO service already syncs the Ozon card funnel
// (/v1/analytics/data → card_metrics_daily hourly); this client reads the
// aggregate instead of duplicating that sync here.
package seo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CardMetric is one product card's funnel over the requested window.
type CardMetric struct {
	// SKU is products.sku as the SEO service stores it — matched against the
	// campaign SKU (numeric) or the offer_id (string), whichever fits.
	SKU         string  `json:"sku"`
	ExternalID  string  `json:"external_id"`
	Name        string  `json:"name"`
	Impressions int64   `json:"impressions"`
	CardViews   int64   `json:"card_views"`
	CartAdds    int64   `json:"cart_adds"`
	Orders      int64   `json:"orders"`
	Revenue     float64 `json:"revenue"`
}

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient wires the SEO exports client. An empty token disables it —
// Enabled() reports false and calls fail fast without touching the network.
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

// GetOzonCardMetrics fetches the card funnel aggregate for one Ozon cabinet
// (by its seller Client-Id) over the last `days` days.
func (c *Client) GetOzonCardMetrics(ctx context.Context, clientID string, days int) ([]CardMetric, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("seo client is disabled (no token configured)")
	}
	endpoint := c.baseURL + "/internal/ozon/card-metrics?client_id=" + url.QueryEscape(clientID) + "&days=" + strconv.Itoa(days)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("seo card-metrics: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Service-Token", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("seo card-metrics: transport: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("seo card-metrics: status %d", resp.StatusCode)
	}

	var payload struct {
		Data []CardMetric `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("seo card-metrics: decode: %w", err)
	}
	return payload.Data, nil
}
