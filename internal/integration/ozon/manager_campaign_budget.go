package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

// Personal budget changes accept whole kopecks. Reads retain all six native
// fractional digits and never round a current budget into the proposed amount.
var exactManagerBudgetDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,12})(?:\.([0-9]{1,2}))?$`)

func exactManagerCampaignBudgetBody(id int64, period, value string) ([]byte, error) {
	if id <= 0 || period != "daily" && period != "weekly" {
		return nil, errors.New("one Ozon campaign and an explicit daily or weekly budget required")
	}
	match := exactManagerBudgetDecimal.FindStringSubmatch(value)
	if match == nil {
		return nil, errors.New("exact nonnegative RUB budget in whole kopecks required")
	}
	micro, ok := new(big.Int).SetString(match[1]+match[2]+strings.Repeat("0", 6-len(match[2])), 10)
	if !ok || !micro.IsInt64() {
		return nil, errors.New("budget outside exact native micro-ruble range")
	}
	return json.Marshal(map[string]string{period + "Budget": micro.String()})
}

func (c *PerfClient) AdvertisingBudgetRequest(creds Credentials, id int64, period, value string) (adguard.Request, error) {
	if c == nil {
		return adguard.Request{}, errors.New("Ozon budget transport unavailable")
	}
	body, err := exactManagerCampaignBudgetBody(id, period, value)
	if err != nil {
		return adguard.Request{}, err
	}
	return adguard.NewOzonRequest("PATCH", c.baseURL+fmt.Sprintf("/api/client/campaign/%d", id), creds.PerfClientID, body)
}

func (c *PerfClient) SetExactCampaignBudget(ctx context.Context, creds Credentials, id int64, period, value string) error {
	body, err := exactManagerCampaignBudgetBody(id, period, value)
	if err != nil {
		return adguard.BeforeSendError(err)
	}
	if err = c.quota.reserve(creds.PerfClientID, QuotaBudgetWrite, time.Now()); err != nil {
		return adguard.BeforeSendError(err)
	}
	_, err = c.doJSON(withPerfQuota(ctx, QuotaBudgetWrite), creds, "PATCH", fmt.Sprintf("/api/client/campaign/%d", id), nil, json.RawMessage(body))
	return err
}

func (c *PerfClient) ReadManagerCampaignBudget(ctx context.Context, creds Credentials, id int64, period string) (*domain.NativeAdBudgetContext, error) {
	if id <= 0 || period != "daily" && period != "weekly" {
		return nil, errors.New("specific daily or weekly Ozon budget required")
	}
	campaign, err := c.ReadCampaignContext(ctx, creds, id)
	if err != nil {
		return nil, err
	}
	if campaign.CampaignType == nil || *campaign.CampaignType != "SKU" || campaign.Status != "CAMPAIGN_STATE_RUNNING" && campaign.Status != "CAMPAIGN_STATE_INACTIVE" {
		return nil, errors.New("editable SKU Ozon campaign required; global CPO is a separate action")
	}
	selected, other := campaign.WeeklyBudgetRub, campaign.DailyBudgetRub
	if period == "daily" {
		selected, other = campaign.DailyBudgetRub, campaign.WeeklyBudgetRub
	}
	if selected == nil {
		return nil, errors.New("current selected Ozon budget not explicitly confirmed")
	}
	positive := func(v *string) bool { return v != nil && *v != "0" }
	confirmed := positive(selected) && !positive(other)
	warnings := []string{"Нулевой бюджет означает снятый лимит, а не бесплатное продвижение или нулевые расходы. Тип существующего бюджета нельзя менять произвольно. Пределы площадки, прибыль и общие расходы не подтверждены."}
	if !confirmed {
		warnings = append(warnings, "По текущим данным тип бюджета не установлен однозначно: выбранный период не имеет единственного положительного бюджета. Новое изменение требует отдельного подтверждения типа источником.")
	}
	if period == "daily" {
		warnings = append(warnings, "Дневной тип устаревший; его чтение не доказывает, что площадка разрешает новое изменение или активацию такой кампании.")
	}
	return &domain.NativeAdBudgetContext{Campaign: *campaign, Budget: domain.NativeAdBudget{Period: period, PeriodConfirmed: confirmed, Enabled: positive(selected), AmountRub: selected, Currency: "RUB", Warnings: warnings}}, nil
}
