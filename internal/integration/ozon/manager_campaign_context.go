package ozon

import (
	"context"
	"errors"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// ReadCampaignContext uses the existing read-only paginated campaign contract.
// A bounded scan fails honestly rather than returning cached/matching-by-name data.
func (c *PerfClient) ReadCampaignContext(ctx context.Context, creds Credentials, id int64) (*domain.NativeAdCampaign, error) {
	if id <= 0 {
		return nil, errors.New("positive Ozon campaign ID required")
	}
	for page := 1; page <= 100; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(perfPageSize)}}
		body, err := c.doGet(ctx, creds, "/api/client/campaign", query)
		if err != nil {
			return nil, err
		}
		var response struct {
			List []wireCampaign `json:"list"`
		}
		if err := decodeJSON(body, &response, "campaign context"); err != nil {
			return nil, err
		}
		if len(response.List) > perfPageSize {
			return nil, errors.New("unbounded Ozon campaign page")
		}
		var result *domain.NativeAdCampaign
		for _, row := range response.List {
			if int64(row.ID) != id {
				continue
			}
			if result != nil || row.State == "" {
				return nil, errors.New("ambiguous or incomplete Ozon campaign context")
			}
			result = &domain.NativeAdCampaign{ExternalID: strconv.FormatInt(id, 10), Name: row.Title, Status: row.State,
				Warnings: []string{"Ставки товаров, ограничения бюджета и доступность действий не проверены. Снимок не разрешает изменение кампании."}}
			if row.AdvObjectType != "" {
				v := row.AdvObjectType
				result.CampaignType = &v
			}
			result.DailyBudgetRub = nativeMicroBudget(row.DailyBudget)
			result.WeeklyBudgetRub = nativeMicroBudget(row.WeeklyBudget)
			if result.DailyBudgetRub == nil {
				result.Warnings = append(result.Warnings, "Текущий дневной бюджет не подтверждён ответом Ozon.")
			}
			if result.WeeklyBudgetRub == nil {
				result.Warnings = append(result.Warnings, "Текущий недельный бюджет не подтверждён ответом Ozon.")
			}
		}
		if result != nil {
			return result, nil
		}
		if len(response.List) < perfPageSize {
			return nil, errors.New("requested campaign absent from Ozon response")
		}
	}
	return nil, errors.New("Ozon campaign inventory exceeds the bounded context read")
}

// Keep all six native fractional digits; campaignFromWire intentionally uses
// a legacy whole-ruble representation and cannot authorize an exact change.
func nativeMicroBudget(raw string) *string {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 19 {
		return nil
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return nil
		}
	}
	micro, ok := new(big.Int).SetString(raw, 10)
	if !ok || !micro.IsInt64() {
		return nil
	}
	rub, rem := new(big.Int), new(big.Int)
	rub.QuoRem(micro, big.NewInt(1_000_000), rem)
	value := rub.String()
	if rem.Sign() != 0 {
		fraction := strconv.FormatInt(rem.Int64()+1_000_000, 10)[1:]
		value += "." + strings.TrimRight(fraction, "0")
	}
	return &value
}
