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
		"id":  wbCampaignID,
		"sum": amount,
		"type": 1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal deposit: %w", err)
	}

	_, _, err = c.doRequest(ctx, http.MethodPost, "/adv/v1/budget/deposit", token, bytes.NewReader(body))
	return err
}
