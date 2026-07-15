package sellico

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WBUnitEconomics is one product's cost data pulled from Sellico's unit economics
// (GET /products/unit-economics/export), keyed by WB nmID. Costs are in rubles.
type WBUnitEconomics struct {
	NmID              int64
	CostPrice         float64
	CommissionPercent *float64
	TaxPercent        *float64
	SppPercent        *float64
	CustomerPrice     *float64
	LogisticsCost     *float64
	OtherCosts        *float64
	MaxAllowedDRR     *float64
	MarginBeforeAds   *float64
	CalculatedAt      *time.Time
	Source            string
	Ready             bool
}

// ListWBUnitEconomics fetches cost/commission/tax by nmID for one Sellico
// integration. path is the export endpoint (default "/products/unit-economics/export").
func (c *Client) ListWBUnitEconomics(ctx context.Context, serviceToken, path, integrationID string) ([]WBUnitEconomics, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("sellico unit economics export path is empty")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	u := c.baseURL + path + "?integration_id=" + url.QueryEscape(integrationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("sellico unit economics export: new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+serviceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sellico unit economics export returned status %d", resp.StatusCode)
	}

	var payload struct {
		Items []struct {
			NmID              json.Number  `json:"nm_id"`
			CostPrice         json.Number  `json:"cost_price"`
			CommissionPercent *json.Number `json:"commission_percent"`
			TaxPercent        *json.Number `json:"tax_percent"`
			SppPercent        *json.Number `json:"spp_percent"`
			CustomerPrice     *json.Number `json:"customer_price"`
			LogisticsCost     *json.Number `json:"logistics_cost"`
			OtherCosts        *json.Number `json:"other_costs"`
			MaxAllowedDRR     *json.Number `json:"max_allowed_drr"`
			MarginBeforeAds   *json.Number `json:"margin_before_ads"`
			CalculatedAt      string       `json:"calculated_at"`
			Ready             bool         `json:"ready"`
		} `json:"items"`
		Source        string      `json:"source"`
		IntegrationID json.Number `json:"integration_id"`
		Complete      bool        `json:"complete"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	payloadIntegrationID, err := payload.IntegrationID.Int64()
	if err != nil || payloadIntegrationID <= 0 || fmt.Sprintf("%d", payloadIntegrationID) != strings.TrimSpace(integrationID) {
		return nil, fmt.Errorf("sellico unit economics export integration mismatch")
	}
	if !payload.Complete {
		return nil, fmt.Errorf("sellico unit economics export is incomplete")
	}
	if strings.TrimSpace(payload.Source) == "" {
		return nil, fmt.Errorf("sellico unit economics export source is missing")
	}

	out := make([]WBUnitEconomics, 0, len(payload.Items))
	for _, it := range payload.Items {
		nm, err := it.NmID.Int64()
		if err != nil || nm <= 0 {
			continue
		}
		cost, err := it.CostPrice.Float64()
		if err != nil || cost <= 0 {
			continue
		}
		row := WBUnitEconomics{NmID: nm, CostPrice: cost, Source: strings.TrimSpace(payload.Source), Ready: it.Ready}
		if it.CommissionPercent != nil {
			if v, err := it.CommissionPercent.Float64(); err == nil {
				row.CommissionPercent = &v
			}
		}
		if it.TaxPercent != nil {
			if v, err := it.TaxPercent.Float64(); err == nil {
				row.TaxPercent = &v
			}
		}
		if it.SppPercent != nil {
			if v, err := it.SppPercent.Float64(); err == nil && v >= 0 && v <= 100 {
				row.SppPercent = &v
			}
		}
		if it.CustomerPrice != nil {
			if v, err := it.CustomerPrice.Float64(); err == nil && v > 0 {
				row.CustomerPrice = &v
			}
		}
		if it.LogisticsCost != nil {
			if v, err := it.LogisticsCost.Float64(); err == nil && v >= 0 {
				row.LogisticsCost = &v
			}
		}
		if it.OtherCosts != nil {
			if v, err := it.OtherCosts.Float64(); err == nil && v >= 0 {
				row.OtherCosts = &v
			}
		}
		if it.MaxAllowedDRR != nil {
			if v, err := it.MaxAllowedDRR.Float64(); err == nil && v >= 0 && v <= 100 {
				row.MaxAllowedDRR = &v
			}
		}
		if it.MarginBeforeAds != nil {
			if v, err := it.MarginBeforeAds.Float64(); err == nil && v > 0 {
				row.MarginBeforeAds = &v
			}
		}
		if parsed := parseTimeField(it.CalculatedAt); !parsed.IsZero() {
			row.CalculatedAt = &parsed
		}
		out = append(out, row)
	}
	return out, nil
}
