package sellico

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type UnitEconomicsReadinessRequest struct {
	WorkspaceID     string   `json:"workspace_id"`
	SellerCabinetID string   `json:"seller_cabinet_id"`
	ProductIDs      []string `json:"product_ids"`
	WBProductIDs    []int64  `json:"wb_product_ids"`
}

type UnitEconomicsReadinessResponse struct {
	Source                     string
	CheckedAt                  time.Time
	MissingEconomicsProductIDs []int64
	UnprofitableProductIDs     []int64
	StaleProductIDs            []int64
}

func (c *Client) CheckUnitEconomicsReadiness(ctx context.Context, serviceToken, path string, req UnitEconomicsReadinessRequest) (*UnitEconomicsReadinessResponse, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("sellico unit economics readiness path is empty")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("sellico unit economics readiness: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sellico unit economics readiness: new request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+serviceToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sellico unit economics readiness returned status %d", resp.StatusCode)
	}

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	parsed := parseUnitEconomicsReadinessPayload(unwrapPayload(payload))
	if parsed.Source == "" {
		return nil, fmt.Errorf("sellico unit economics readiness response missing source")
	}
	return &parsed, nil
}

func parseUnitEconomicsReadinessPayload(payload any) UnitEconomicsReadinessResponse {
	raw := unwrapObject(payload)
	if nested, ok := raw["readiness"]; ok {
		raw = unwrapObject(nested)
	}

	return UnitEconomicsReadinessResponse{
		Source:                     firstNonEmpty(stringify(raw["source"]), stringify(raw["data_source"])),
		CheckedAt:                  parseTimeField(raw["checked_at"]),
		MissingEconomicsProductIDs: int64List(raw["missing_economics_product_ids"], raw["missingEconomicsProductIds"], raw["missing_nm_ids"]),
		UnprofitableProductIDs:     int64List(raw["unprofitable_product_ids"], raw["unprofitableProductIds"], raw["unprofitable_nm_ids"]),
		StaleProductIDs:            int64List(raw["stale_product_ids"], raw["staleProductIds"], raw["stale_nm_ids"]),
	}
}

func parseTimeField(value any) time.Time {
	raw := stringify(value)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func int64List(values ...any) []int64 {
	for _, value := range values {
		items := unwrapAnyList(value)
		if len(items) == 0 {
			continue
		}
		result := make([]int64, 0, len(items))
		for _, item := range items {
			if parsed, ok := parseInt64(item); ok {
				result = append(result, parsed)
			}
		}
		return result
	}
	return nil
}

func unwrapAnyList(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []int64:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	case []int:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	case []string:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	default:
		return nil
	}
}

func parseInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		var parsed json.Number = json.Number(strings.TrimSpace(typed))
		value, err := parsed.Int64()
		return value, err == nil
	default:
		return 0, false
	}
}
