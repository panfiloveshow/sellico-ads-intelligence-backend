package ozon

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
)

const (
	perfPageSize = 100
	// The Performance API allows 100k requests/day; a couple of rps per
	// cabinet is far below any pressure point.
	perfRPS = 2
	// Tokens live 1800s; refresh 300s early so a token never expires mid-call.
	perfTokenEarlyRefresh = 300 * time.Second
)

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// PerfClient talks to api-performance.ozon.ru (OAuth client_credentials,
// Bearer token on every call). Tokens are cached per client_id with
// singleflight so concurrent cabinet syncs don't stampede the token endpoint.
type PerfClient struct {
	baseURL    string
	httpClient *http.Client
	logger     zerolog.Logger
	limiters   *limiterPool

	tokenMu     sync.Mutex
	tokens      map[string]cachedToken
	tokenFlight singleflight.Group
}

// NewPerfClient creates a Performance API client from the application config.
func NewPerfClient(cfg *config.Config, logger zerolog.Logger) *PerfClient {
	return &PerfClient{
		baseURL:    cfg.OzonPerfAPIBaseURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		logger:     logger.With().Str("component", "ozon_perf_client").Logger(),
		limiters:   newLimiterPool(rate.Limit(perfRPS), perfRPS),
		tokens:     make(map[string]cachedToken),
	}
}

// ValidateCredentials verifies the Performance pair by requesting a token.
func (c *PerfClient) ValidateCredentials(ctx context.Context, creds Credentials) error {
	_, err := c.token(ctx, creds)
	return err
}

// token returns a cached access token for the credential pair, requesting a
// fresh one via POST /api/client/token when the cache is empty or near expiry.
func (c *PerfClient) token(ctx context.Context, creds Credentials) (string, error) {
	c.tokenMu.Lock()
	if cached, ok := c.tokens[creds.PerfClientID]; ok && time.Now().Before(cached.expiresAt) {
		c.tokenMu.Unlock()
		return cached.token, nil
	}
	c.tokenMu.Unlock()

	result, err, _ := c.tokenFlight.Do(creds.PerfClientID, func() (any, error) {
		// Re-check under flight: a concurrent caller may have refreshed already.
		c.tokenMu.Lock()
		if cached, ok := c.tokens[creds.PerfClientID]; ok && time.Now().Before(cached.expiresAt) {
			c.tokenMu.Unlock()
			return cached.token, nil
		}
		c.tokenMu.Unlock()

		body, err := json.Marshal(map[string]string{
			"client_id":     creds.PerfClientID,
			"client_secret": creds.PerfClientSecret,
			"grant_type":    "client_credentials",
		})
		if err != nil {
			return nil, fmt.Errorf("ozon perf: marshal token request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/client/token", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("ozon perf: create token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		metrics.OzonAPILatency.WithLabelValues("performance", "/api/client/token").Observe(time.Since(start).Seconds())
		if err != nil {
			metrics.OzonAPIRequests.WithLabelValues("performance", "/api/client/token", "error").Inc()
			return nil, fmt.Errorf("ozon perf: token request: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("ozon perf: read token response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			metrics.OzonAPIRequests.WithLabelValues("performance", "/api/client/token", fmt.Sprintf("%d", resp.StatusCode)).Inc()
			snippet := string(respBody)
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return nil, &APIError{
				StatusCode: resp.StatusCode,
				URL:        c.baseURL + "/api/client/token",
				Message:    fmt.Sprintf("ozon perf: token request failed (%d): %s", resp.StatusCode, snippet),
			}
		}
		metrics.OzonAPIRequests.WithLabelValues("performance", "/api/client/token", "ok").Inc()

		var parsed struct {
			AccessToken string    `json:"access_token"`
			ExpiresIn   flexInt64 `json:"expires_in"`
		}
		if err := decodeJSON(respBody, &parsed, "token"); err != nil {
			return nil, err
		}
		if parsed.AccessToken == "" {
			return nil, fmt.Errorf("ozon perf: token response missing access_token")
		}

		ttl := time.Duration(parsed.ExpiresIn) * time.Second
		if ttl <= perfTokenEarlyRefresh {
			ttl = perfTokenEarlyRefresh + time.Minute
		}
		c.tokenMu.Lock()
		c.tokens[creds.PerfClientID] = cachedToken{
			token:     parsed.AccessToken,
			expiresAt: time.Now().Add(ttl - perfTokenEarlyRefresh),
		}
		c.tokenMu.Unlock()

		return parsed.AccessToken, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

type wireCampaign struct {
	ID                       flexInt64 `json:"id"`
	Title                    string    `json:"title"`
	State                    string    `json:"state"`
	AdvObjectType            string    `json:"advObjectType"`
	FromDate                 string    `json:"fromDate"`
	ToDate                   string    `json:"toDate"`
	DailyBudget              string    `json:"dailyBudget"`
	WeeklyBudget             string    `json:"weeklyBudget"`
	Placement                any       `json:"placement"`
	ProductAutopilotStrategy string    `json:"productAutopilotStrategy"`
}

// ListCampaigns pages through GET /api/client/campaign. Budgets are converted
// from micro-rubles to whole rubles at this boundary.
func (c *PerfClient) ListCampaigns(ctx context.Context, creds Credentials) ([]Campaign, error) {
	var out []Campaign
	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("pageSize", strconv.Itoa(perfPageSize))

		body, err := c.doGet(ctx, creds, "/api/client/campaign", query)
		if err != nil {
			return nil, err
		}
		var resp struct {
			List []wireCampaign `json:"list"`
		}
		if err := decodeJSON(body, &resp, "campaign list"); err != nil {
			return nil, err
		}
		for _, wc := range resp.List {
			out = append(out, campaignFromWire(wc, c.logger))
		}
		if len(resp.List) < perfPageSize {
			return out, nil
		}
	}
}

func campaignFromWire(wc wireCampaign, logger zerolog.Logger) Campaign {
	campaign := Campaign{
		ID:                int64(wc.ID),
		Title:             wc.Title,
		State:             wc.State,
		AdvObjectType:     wc.AdvObjectType,
		Placement:         placementString(wc.Placement),
		AutopilotStrategy: wc.ProductAutopilotStrategy,
	}
	if rub, err := MicroRubToRub(wc.DailyBudget); err == nil && strings.TrimSpace(wc.DailyBudget) != "" {
		campaign.DailyBudgetRub = &rub
	} else if err != nil {
		logger.Warn().Err(err).Int64("campaign_id", campaign.ID).Msg("unparseable dailyBudget")
	}
	if rub, err := MicroRubToRub(wc.WeeklyBudget); err == nil && strings.TrimSpace(wc.WeeklyBudget) != "" {
		campaign.WeeklyBudgetRub = &rub
	} else if err != nil {
		logger.Warn().Err(err).Int64("campaign_id", campaign.ID).Msg("unparseable weeklyBudget")
	}
	if t, err := parseOzonDate(wc.FromDate); err == nil {
		campaign.FromDate = t
	}
	if t, err := parseOzonDate(wc.ToDate); err == nil {
		campaign.ToDate = t
	}
	return campaign
}

// placementString normalizes the placement field, which the API returns as
// either a string or a list of strings depending on the campaign type.
func placementString(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}

func parseOzonDate(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "0001-01-01") {
		return nil, fmt.Errorf("empty date")
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("unparseable date %q", raw)
}

// ListCampaignProducts fetches GET /api/client/campaign/{id}/v2/products.
// Bids arrive in micro-rubles and are converted to rubles.
func (c *PerfClient) ListCampaignProducts(ctx context.Context, creds Credentials, campaignID int64) ([]CampaignProduct, error) {
	path := fmt.Sprintf("/api/client/campaign/%d/v2/products", campaignID)
	body, err := c.doGet(ctx, creds, path, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Products []struct {
			SKU flexInt64 `json:"sku"`
			Bid string    `json:"bid"`
			// targetCir arrives as a plain number for TARGET_CIR campaigns
			// (verified live 2026-08-10: {"targetCir":11}); topPosition mirrors
			// the docs' TOP_PROMOTION field. Both absent on TARGET_BIDS.
			TargetCIR   flexFloat `json:"targetCir"`
			TopPosition flexInt64 `json:"topPosition"`
		} `json:"products"`
	}
	if err := decodeJSON(body, &resp, "campaign products"); err != nil {
		return nil, err
	}
	out := make([]CampaignProduct, 0, len(resp.Products))
	for _, p := range resp.Products {
		bidRub, convErr := MicroRubToRubFloat(p.Bid)
		if convErr != nil {
			c.logger.Warn().Err(convErr).Int64("campaign_id", campaignID).Int64("sku", int64(p.SKU)).Msg("unparseable bid")
		}
		out = append(out, CampaignProduct{
			SKU:         int64(p.SKU),
			BidRub:      bidRub,
			TargetCIR:   float64(p.TargetCIR),
			TopPosition: int64(p.TopPosition),
		})
	}
	return out, nil
}

// DailyStats fetches GET /api/client/statistics/daily for the date range.
// The endpoint may answer JSON ({"rows":[...]}) or CSV depending on account
// settings — both shapes are accepted.
func (c *PerfClient) DailyStats(ctx context.Context, creds Credentials, dateFrom, dateTo time.Time, campaignIDs []int64) ([]DailyStatRow, error) {
	query := url.Values{}
	query.Set("dateFrom", dateFrom.Format("2006-01-02"))
	query.Set("dateTo", dateTo.Format("2006-01-02"))
	for _, id := range campaignIDs {
		query.Add("campaignIds", strconv.FormatInt(id, 10))
	}

	body, err := c.doGet(ctx, creds, "/api/client/statistics/daily", query)
	if err != nil {
		return nil, err
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return parseDailyStatsJSON(trimmed)
	}
	return parseDailyStatsCSV(trimmed)
}

type wireDailyRow struct {
	ID         flexInt64 `json:"id"`
	CampaignID flexInt64 `json:"campaignId"`
	Date       string    `json:"date"`
	Views      flexInt64 `json:"views"`
	Clicks     flexInt64 `json:"clicks"`
	MoneySpent flexFloat `json:"moneySpent"`
	Spend      flexFloat `json:"spend"`
	Orders     flexInt64 `json:"orders"`
	OrderMoney flexFloat `json:"ordersMoney"`
}

func parseDailyStatsJSON(body []byte) ([]DailyStatRow, error) {
	var resp struct {
		Rows []wireDailyRow `json:"rows"`
	}
	if err := decodeJSON(body, &resp, "statistics/daily"); err != nil {
		return nil, err
	}
	out := make([]DailyStatRow, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		campaignID := int64(r.ID)
		if campaignID == 0 {
			campaignID = int64(r.CampaignID)
		}
		date, err := time.Parse("2006-01-02", strings.TrimSpace(r.Date))
		if err != nil {
			continue
		}
		spend := float64(r.MoneySpent)
		if spend == 0 {
			spend = float64(r.Spend)
		}
		out = append(out, DailyStatRow{
			CampaignID: campaignID,
			Date:       date,
			Views:      int64(r.Views),
			Clicks:     int64(r.Clicks),
			SpendRub:   spend,
			Orders:     int64(r.Orders),
			RevenueRub: float64(r.OrderMoney),
		})
	}
	return out, nil
}

// parseDailyStatsCSV handles the CSV variant of statistics/daily. The header
// row names columns; we map the known ones and skip anything else.
func parseDailyStatsCSV(body []byte) ([]DailyStatRow, error) {
	if len(body) == 0 {
		return nil, nil
	}
	// Detect the delimiter from the header line: a comma-separated file parses
	// "successfully" with Comma=';' as one wide column and silently yields
	// zero rows, so error-based fallback alone is not enough.
	delimiter := byte(';')
	if head, _, _ := bytes.Cut(body, []byte("\n")); bytes.Count(head, []byte(",")) > bytes.Count(head, []byte(";")) {
		delimiter = ','
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.Comma = rune(delimiter)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		// Retry with the other separator before giving up.
		reader = csv.NewReader(bytes.NewReader(body))
		if delimiter == ';' {
			reader.Comma = ','
		} else {
			reader.Comma = ';'
		}
		reader.FieldsPerRecord = -1
		records, err = reader.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("ozon perf: parse statistics/daily CSV: %w", err)
		}
	}
	if len(records) < 2 {
		return nil, nil
	}

	// Header names carry units in the live export ("Расход, ₽", "Заказы, шт.",
	// "Заказы, ₽") — map the full normalized name; aliases below list the
	// unit-suffixed forms first so "Заказы, шт." and "Заказы, ₽" stay distinct.
	col := map[string]int{}
	for i, name := range records[0] {
		normalized := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
		col[normalized] = i
	}
	pick := func(row []string, names ...string) string {
		for _, name := range names {
			if idx, ok := col[name]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
		}
		return ""
	}
	parseI := func(raw string) int64 {
		v, _ := strconv.ParseInt(strings.ReplaceAll(raw, " ", ""), 10, 64)
		return v
	}
	parseF := func(raw string) float64 {
		normalized := strings.ReplaceAll(strings.ReplaceAll(raw, " ", ""), ",", ".")
		v, _ := strconv.ParseFloat(normalized, 64)
		return v
	}

	out := make([]DailyStatRow, 0, len(records)-1)
	for _, row := range records[1:] {
		date, dErr := time.Parse("2006-01-02", pick(row, "date", "день", "дата"))
		if dErr != nil {
			continue
		}
		out = append(out, DailyStatRow{
			CampaignID: parseI(pick(row, "id", "campaignid", "campaign_id")),
			Date:       date,
			Views:      parseI(pick(row, "views", "показы")),
			Clicks:     parseI(pick(row, "clicks", "клики")),
			SpendRub:   parseF(pick(row, "расход, ₽", "moneyspent", "spend", "расход")),
			Orders:     parseI(pick(row, "заказы, шт.", "заказы, шт", "orders", "заказы")),
			RevenueRub: parseF(pick(row, "заказы, ₽", "ordersmoney", "выручка")),
		})
	}
	return out, nil
}

// doGet executes one authenticated Performance API GET with rate limiting,
// retry and metrics. 401 invalidates the cached token once and retries.
func (c *PerfClient) doGet(ctx context.Context, creds Credentials, path string, query url.Values) ([]byte, error) {
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

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("ozon perf: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("ozon perf: %s: %w", path, err)
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
			// Token may have been revoked upstream — drop the cache and retry.
			metrics.OzonAPIRequests.WithLabelValues("performance", path, "401").Inc()
			c.tokenMu.Lock()
			delete(c.tokens, creds.PerfClientID)
			c.tokenMu.Unlock()
			lastErr = &APIError{
				StatusCode: resp.StatusCode,
				URL:        fullURL,
				Message:    fmt.Sprintf("ozon perf: unauthorized (401) on %s", path),
			}
			continue
		case resp.StatusCode == http.StatusTooManyRequests:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			metrics.OzonAPIRequests.WithLabelValues("performance", path, "429").Inc()
			lastErr = &APIError{
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfter,
				URL:        fullURL,
				Message:    fmt.Sprintf("ozon perf: rate limited (429) on %s", path),
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
				Message:    fmt.Sprintf("ozon perf: server error (%d) on %s", resp.StatusCode, path),
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
				Message:    fmt.Sprintf("ozon perf: client error (%d) on %s: %s", resp.StatusCode, path, snippet),
			}
		}

		metrics.OzonAPIRequests.WithLabelValues("performance", path, "ok").Inc()
		return respBody, nil
	}

	return nil, fmt.Errorf("ozon perf: all %d attempts exhausted: %w", maxRetries, lastErr)
}
