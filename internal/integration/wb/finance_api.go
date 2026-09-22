package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// WBBalanceDTO represents the seller's advertising account balance.
type WBBalanceDTO struct {
	Balance float64 `json:"balance"` // Общий баланс (руб)
	Net     float64 `json:"net"`     // Баланс нетто
	Bonus   float64 `json:"bonus"`   // Бонусы
}

// CampaignBudgetsBatch — максимум advertIds в одном POST /api/advert/v2/budget
// (V2BudgetRequest.advertIds maxItems).
const CampaignBudgetsBatch = 50

// WBCampaignBudgetDTO — остаток бюджета кампании (V1BudgetAdvert).
type WBCampaignBudgetDTO struct {
	AdvertID int64  `json:"advertId"`
	Currency string `json:"currency"` // ISO 4217, валюта аккаунта продавца
	Total    int64  `json:"total"`    // в базовых единицах валюты (рубли)
}

type WBFinanceDocumentDTO struct {
	ID       string          `json:"id"`
	AdvertID int64           `json:"advertId"`
	Type     string          `json:"type"`
	Sum      float64         `json:"sum"`
	Date     string          `json:"date"`
	Raw      json.RawMessage `json:"raw,omitempty"`
}

// GetBalance fetches the seller's advertising account balance.
// WB API: GET /adv/v1/balance
func (c *Client) GetBalance(ctx context.Context, token string) (*WBBalanceDTO, error) {
	_, body, err := c.doRequest(ctx, http.MethodGet, "/adv/v1/balance", token, nil)
	if err != nil {
		return nil, err
	}

	var result WBBalanceDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal balance: %w", err)
	}
	return &result, nil
}

// GetCampaignBudgets fetches remaining budgets of campaigns (statuses 4/9/11),
// пачками по CampaignBudgetsBatch.
// WB API: POST /api/advert/v2/budget — замена GET /adv/v1/budget (отключается 16.11.2026).
//
// Лимит метода: Базовый токен — 4 запроса в час (1 в 15 мин), Персональный и
// Сервисный — 20 в минуту (интервал 3 с). Пачки идут не чаще 1 в 3 с, 429 не
// повторяется: ждать 15 минут внутри синка бессмысленно, паузу ставит
// вызывающий. При ошибке возвращается уже полученное и ошибка.
func (c *Client) GetCampaignBudgets(ctx context.Context, token string, wbCampaignIDs []int64) ([]WBCampaignBudgetDTO, error) {
	var out []WBCampaignBudgetDTO
	for start := 0; start < len(wbCampaignIDs); start += CampaignBudgetsBatch {
		end := min(start+CampaignBudgetsBatch, len(wbCampaignIDs))
		if err := c.budgetLimiterForToken(token).Wait(ctx); err != nil {
			return out, fmt.Errorf("budget rate limiter wait: %w", err)
		}
		payload, err := json.Marshal(map[string][]int64{"advertIds": wbCampaignIDs[start:end]})
		if err != nil {
			return out, fmt.Errorf("marshal budget request: %w", err)
		}
		_, body, err := c.doRequest(withoutRateLimitRetry(ctx), http.MethodPost, "/api/advert/v2/budget", token, bytes.NewReader(payload))
		if err != nil {
			return out, err
		}
		var resp struct {
			Adverts []WBCampaignBudgetDTO `json:"adverts"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return out, fmt.Errorf("unmarshal budget: %w", err)
		}
		out = append(out, resp.Adverts...)
	}
	return out, nil
}

// DepositCampaignBudget adds funds to a campaign's budget.
// WB API: POST /adv/v1/budget/deposit
func (c *Client) DepositCampaignBudget(ctx context.Context, token string, wbCampaignID int64, amount int64) error {
	payload := map[string]any{
		"id":   wbCampaignID,
		"sum":  amount,
		"type": 1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal deposit: %w", err)
	}

	_, _, err = c.doRequest(ctx, http.MethodPost, "/adv/v1/budget/deposit", token, bytes.NewReader(body))
	return err
}

// GetUPDDocuments fetches WB advertising closing documents.
// WB API endpoint: GET /adv/v1/upd
func (c *Client) GetUPDDocuments(ctx context.Context, token string) ([]WBFinanceDocumentDTO, error) {
	return c.getFinanceDocuments(ctx, token, "/adv/v1/upd", "upd")
}

// GetPayments fetches WB advertising payment operations.
// WB API endpoint: GET /adv/v1/payments
func (c *Client) GetPayments(ctx context.Context, token string) ([]WBFinanceDocumentDTO, error) {
	return c.getFinanceDocuments(ctx, token, "/adv/v1/payments", "payment")
}

func (c *Client) getFinanceDocuments(ctx context.Context, token, path, docType string) ([]WBFinanceDocumentDTO, error) {
	_, body, err := c.doRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, err
	}
	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	var docs []WBFinanceDocumentDTO
	collectFinanceDocuments(raw, docType, &docs)
	return docs, nil
}

func collectFinanceDocuments(value interface{}, docType string, out *[]WBFinanceDocumentDTO) {
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			collectFinanceDocuments(item, docType, out)
		}
	case map[string]interface{}:
		encoded, _ := json.Marshal(typed)
		*out = append(*out, WBFinanceDocumentDTO{
			ID:       stringValue(firstValue(typed, "id", "updNum", "paymentId", "documentId")),
			AdvertID: int64Value(firstValue(typed, "advertId", "advert_id", "idAdvert")),
			Type:     docType,
			Sum:      floatValue(firstValue(typed, "sum", "amount", "price", "total")),
			Date:     stringValue(firstValue(typed, "date", "updTime", "paymentTime", "createdAt")),
			Raw:      encoded,
		})
	}
}

func firstValue(values map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func stringValue(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return fmt.Sprintf("%.0f", typed)
	default:
		return ""
	}
}

func int64Value(value interface{}) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}

func floatValue(value interface{}) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	default:
		return 0
	}
}
