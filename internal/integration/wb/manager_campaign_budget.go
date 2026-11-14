package wb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"golang.org/x/time/rate"
)

// The documented daily-limit methods have a separate 5/minute, burst-5 bucket;
// config has 1/minute, burst-10. Rotated JWTs of the same seller share buckets.
// Creation is serialized so concurrent first calls cannot create extra bursts.
func (c *Client) managerBudgetLimiter(kind, token string) *rate.Limiter {
	account, err := adguard.WBAccountKey(token)
	if err != nil {
		hash := sha256.Sum256([]byte(token))
		account = hex.EncodeToString(hash[:])
	}
	key := kind + ":" + account
	c.managerBudgetLimiterMu.Lock()
	defer c.managerBudgetLimiterMu.Unlock()
	if c.managerBudgetLimiters == nil {
		c.managerBudgetLimiters = newBoundedLRU[*rate.Limiter](tokenCacheCapacity, tokenCacheTTL)
	}
	if limiter, ok := c.managerBudgetLimiters.Get(key); ok {
		return limiter
	}
	interval, burst := 12*time.Second, 5
	if kind == "config" {
		interval, burst = time.Minute, 10
	}
	limiter := rate.NewLimiter(rate.Every(interval), burst)
	c.managerBudgetLimiters.Set(key, limiter)
	return limiter
}

func exactManagerCampaignBudgetBody(id int64, period string, enabled bool, amount *string, carry *bool) ([]byte, error) {
	if id <= 0 || period != "daily" {
		return nil, errors.New("one exact WB CPC campaign and daily limit required")
	}
	if !enabled {
		if amount != nil || carry != nil {
			return nil, errors.New("disabling a daily limit must not contain a hidden amount or carry setting")
		}
		return json.Marshal(struct {
			IDs     []int64 `json:"advertIds"`
			Enabled bool    `json:"enabled"`
		}{[]int64{id}, false})
	}
	if amount == nil || carry == nil {
		return nil, errors.New("explicit amount and carry-over choice required")
	}
	kopecks, err := wbExactBidKopecks(*amount)
	if err != nil || kopecks < 1000 {
		return nil, errors.New("exact positive WB daily limit outside native precision or floor")
	}
	return json.Marshal(struct {
		IDs     []int64 `json:"advertIds"`
		Enabled bool    `json:"enabled"`
		Amount  int64   `json:"dailyLimit"`
		Carry   bool    `json:"carryOverEnabled"`
	}{[]int64{id}, true, kopecks, *carry})
}

func (c *Client) AdvertisingBudgetRequest(token string, id int64, period string, enabled bool, amount *string, carry *bool) (adguard.Request, error) {
	if c == nil {
		return adguard.Request{}, errors.New("WB budget transport unavailable")
	}
	body, err := exactManagerCampaignBudgetBody(id, period, enabled, amount, carry)
	if err != nil {
		return adguard.Request{}, err
	}
	return adguard.NewWBRequest("PUT", c.baseURL+"/api/advert/v0/daily-limits", token, body)
}

func (c *Client) SetExactCampaignBudget(ctx context.Context, token string, id int64, period string, enabled bool, amount *string, carry *bool) error {
	body, err := exactManagerCampaignBudgetBody(id, period, enabled, amount, carry)
	if err != nil {
		return adguard.BeforeSendError(err)
	}
	if err = c.managerBudgetLimiter("write", token).Wait(ctx); err != nil {
		return adguard.BeforeSendError(err)
	}
	ctx = priceRequestContext(withoutRateLimitRetry(ctx), 1<<20)
	policy, _ := ctx.Value(requestRetryPolicyKey{}).(requestRetryPolicy)
	policy.oneAttempt = true
	ctx = context.WithValue(ctx, requestRetryPolicyKey{}, policy)
	_, _, err = c.doRequest(ctx, "PUT", "/api/advert/v0/daily-limits", token, bytes.NewReader(body))
	return err
}

// ReadManagerCampaignBudget is strictly read-only. DailyLimit, spentToday and
// minDailyLimit use 0.01 of the seller currency; remaining /v2/budget is a
// different source in base units and is deliberately not substituted here.
func (c *Client) ReadManagerCampaignBudget(ctx context.Context, token string, id int64, period string) (*domain.NativeAdBudgetContext, error) {
	if id <= 0 || period != "daily" {
		return nil, errors.New("specific WB daily limit required")
	}
	ctx = priceRequestContext(withoutRateLimitRetry(ctx), 1<<20)
	campaign, err := c.ReadCampaignContext(ctx, token, id)
	if err != nil {
		return nil, err
	}
	if campaign.PaymentType == nil || *campaign.PaymentType != "cpc" || (campaign.Status != "4" && campaign.Status != "9" && campaign.Status != "11") {
		return nil, errors.New("daily limits require a confirmed CPC campaign in an editable state")
	}
	if err = c.managerBudgetLimiter("read", token).Wait(ctx); err != nil {
		return nil, err
	}
	_, body, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/advert/v0/daily-limits?advertIds=%d", id), token, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Adverts []struct {
			ID          int64  `json:"advertId"`
			Enabled     *bool  `json:"enabled"`
			Amount      *int64 `json:"dailyLimit"`
			Spent       *int64 `json:"spentToday"`
			Currency    string `json:"currency"`
			Carry       *bool  `json:"carryOverEnabled"`
			Valid       *bool  `json:"valid"`
			Recommended *int64 `json:"requiredLimit"`
		} `json:"adverts"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil, errors.New("unreadable WB daily-limit response")
	}
	var result *domain.NativeAdBudgetContext
	for _, row := range response.Adverts {
		if row.ID != id {
			continue
		}
		if result != nil || row.Enabled == nil || row.Amount == nil || *row.Amount < 0 || *row.Enabled && *row.Amount == 0 || row.Spent == nil || *row.Spent < 0 || row.Carry == nil || row.Valid == nil || row.Recommended == nil || *row.Recommended < 0 || row.Currency != "RUB" {
			return nil, errors.New("explicit unique RUB daily limit and flags required")
		}
		amount, spent, recommended := wbExactBidRub(*row.Amount), wbExactBidRub(*row.Spent), wbExactBidRub(*row.Recommended)
		result = &domain.NativeAdBudgetContext{Campaign: *campaign, Budget: domain.NativeAdBudget{Period: "daily", PeriodConfirmed: true, Enabled: *row.Enabled, AmountRub: &amount, CarryOverEnabled: row.Carry, SpentTodayRub: &spent, RecommendedMinimumRub: &recommended, VendorLimitValid: row.Valid, Currency: row.Currency, Warnings: []string{"Это дневной лимит CPC, а не остаток или пополнение кампании. При переносе неистраченных сумм расход следующего дня может отличаться от номинального лимита. Прибыль и общие расходы не подтверждены."}}}
	}
	if result == nil {
		return nil, errors.New("selected WB campaign missing from daily limits")
	}
	if err = c.managerBudgetLimiter("config", token).Wait(ctx); err != nil {
		return nil, err
	}
	_, body, err = c.doRequest(ctx, "GET", "/api/advert/v1/config", token, nil)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Currency     string `json:"currency"`
		CurrencyCode *int   `json:"currencyCode"`
		Minimum      *int64 `json:"minDailyLimit"`
	}
	if json.Unmarshal(body, &cfg) != nil || cfg.Currency != "RUB" || cfg.CurrencyCode == nil || *cfg.CurrencyCode != 643 || cfg.Minimum == nil || *cfg.Minimum <= 0 {
		return nil, errors.New("RUB currency and minimum daily limit not explicitly confirmed by config")
	}
	minimum := wbExactBidRub(*cfg.Minimum)
	result.Budget.MinimumAmountRub = &minimum
	return result, nil
}
