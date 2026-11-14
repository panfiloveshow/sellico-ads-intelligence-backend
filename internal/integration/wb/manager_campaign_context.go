package wb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

// ReadCampaignContext reads one explicit campaign without inventory enrichment
// or a write endpoint. Absent optional fields never become zero/default facts.
func (c *Client) ReadCampaignContext(ctx context.Context, token string, id int64) (*domain.NativeAdCampaign, error) {
	if id <= 0 {
		return nil, errors.New("positive WB campaign ID required")
	}
	path := fmt.Sprintf("/api/advert/v2/adverts?ids=%d&statuses=%s", id, advertsV2DetailStatuses)
	_, body, err := c.doRequest(ctx, "GET", path, token, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Adverts []struct {
			ID          int64  `json:"id"`
			AdvertID    int64  `json:"advertId"`
			Name        string `json:"name"`
			Status      *int   `json:"status"`
			Type        *int   `json:"type"`
			DailyBudget *int64 `json:"dailyBudget"`
			BidType     string `json:"bid_type"`
			PaymentType string `json:"paymentType"`
			Settings    struct {
				Name        string `json:"name"`
				PaymentType string `json:"payment_type"`
			} `json:"settings"`
		} `json:"adverts"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.New("unreadable WB campaign context")
	}
	var result *domain.NativeAdCampaign
	for _, row := range response.Adverts {
		if row.ID != 0 && row.AdvertID != 0 && row.ID != row.AdvertID {
			return nil, errors.New("conflicting WB campaign identity")
		}
		actualID := firstNonZeroInt64(row.ID, row.AdvertID)
		if actualID != id {
			continue
		}
		if result != nil || row.Status == nil {
			return nil, errors.New("ambiguous or incomplete WB campaign context")
		}
		result = &domain.NativeAdCampaign{ExternalID: strconv.FormatInt(id, 10), Name: row.Name, Status: strconv.Itoa(*row.Status),
			Warnings: []string{"Ставки товаров, текущий остаток бюджета и ограничения исполнения не проверены. Снимок не разрешает изменение кампании."}}
		if result.Name == "" {
			result.Name = row.Settings.Name
		}
		if row.Type != nil {
			v := strconv.Itoa(*row.Type)
			result.CampaignType = &v
		}
		if row.BidType != "" {
			v := row.BidType
			result.BidType = &v
		}
		payment := row.PaymentType
		if payment == "" {
			payment = row.Settings.PaymentType
		}
		if payment != "" {
			result.PaymentType = &payment
		}
		if row.DailyBudget != nil && *row.DailyBudget >= 0 {
			v := strconv.FormatInt(*row.DailyBudget, 10)
			result.DailyBudgetRub = &v
		} else {
			result.Warnings = append(result.Warnings, "Текущий дневной бюджет не подтверждён ответом WB.")
		}
	}
	if result == nil {
		return nil, errors.New("requested campaign absent from WB response")
	}
	return result, nil
}
