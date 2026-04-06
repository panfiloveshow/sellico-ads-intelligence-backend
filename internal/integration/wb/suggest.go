package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// SuggestResult represents a WB search suggestion with estimated frequency.
type SuggestResult struct {
	Query     string `json:"query"`
	Frequency int    `json:"frequency"` // estimated from total results
}

// GetSuggest fetches search suggestions from WB suggest API.
// Uses the public suggest endpoint — no auth token needed.
func (c *Client) GetSuggest(ctx context.Context, query string) ([]SuggestResult, error) {
	encodedQuery := url.QueryEscape(query)
	path := fmt.Sprintf("/api/v2/search/hint?query=%s", encodedQuery)

	// Use content URL for suggest (public endpoint)
	origBase := c.baseURL
	c.baseURL = "https://search.wb.ru"
	defer func() { c.baseURL = origBase }()

	_, body, err := c.doRequestInner(ctx, "GET", path, "", nil)
	if err != nil {
		return nil, fmt.Errorf("suggest request: %w", err)
	}

	var raw struct {
		Query   string `json:"query"`
		Results []struct {
			Name string `json:"name"`
			Freq int    `json:"freq"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse suggest response: %w", err)
	}

	results := make([]SuggestResult, len(raw.Results))
	for i, r := range raw.Results {
		results[i] = SuggestResult{
			Query:     r.Name,
			Frequency: r.Freq,
		}
	}
	return results, nil
}

// GetSearchTotalResults fetches total results count for a query.
// Uses the public search API to estimate keyword frequency.
func (c *Client) GetSearchTotalResults(ctx context.Context, query string) (int, error) {
	encodedQuery := url.QueryEscape(query)
	path := fmt.Sprintf("/catalog/search?query=%s&page=1&limit=1", encodedQuery)

	origBase := c.baseURL
	c.baseURL = "https://search.wb.ru"
	defer func() { c.baseURL = origBase }()

	_, body, err := c.doRequestInner(ctx, "GET", path, "", nil)
	if err != nil {
		return 0, err
	}

	var raw struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, err
	}

	return raw.Data.Total, nil
}
