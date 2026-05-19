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

// WBBudgetDTO represents a campaign's budget.
type WBBudgetDTO struct {
	Cash    float64 `json:"cash"`    // Денежные средства (руб)
	Netting float64 `json:"netting"` // Взаимозачёт
	Total   float64 `json:"total"`   // Итого
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

// GetCampaignBudget fetches the budget for a specific campaign.
// WB API: GET /adv/v1/budget?id={campaignID}
func (c *Client) GetCampaignBudget(ctx context.Context, token string, wbCampaignID int64) (*WBBudgetDTO, error) {
	path := fmt.Sprintf("/adv/v1/budget?id=%d", wbCampaignID)
	_, body, err := c.doRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, err
	}

	var result WBBudgetDTO
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal budget: %w", err)
	}
	return &result, nil
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
