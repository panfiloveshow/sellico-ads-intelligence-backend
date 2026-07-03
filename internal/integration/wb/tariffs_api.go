package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type WBCommissionTariffDTO struct {
	ParentID            int64   `json:"parentID"`
	ParentName          string  `json:"parentName"`
	SubjectID           int64   `json:"subjectID"`
	SubjectName         string  `json:"subjectName"`
	KGVPBooking         float64 `json:"kgvpBooking"`
	KGVPPickup          float64 `json:"kgvpPickup"`
	KGVPSupplier        float64 `json:"kgvpSupplier"`
	KGVPSupplierExpress float64 `json:"kgvpSupplierExpress"`
	KGVPMarketplace     float64 `json:"kgvpMarketplace"`
	PaidStorageKGVP     float64 `json:"paidStorageKgvp"`
}

type commissionTariffsResponse struct {
	Report []WBCommissionTariffDTO `json:"report"`
}

// GetCommissionTariffs fetches WB commission by product categories.
// WB API: GET https://common-api.wildberries.ru/api/v1/tariffs/commission
func (c *Client) GetCommissionTariffs(ctx context.Context, token string, locale string) ([]WBCommissionTariffDTO, error) {
	if locale == "" {
		locale = "ru"
	}
	path := fmt.Sprintf("/api/v1/tariffs/commission?locale=%s", locale)
	_, body, err := c.doCommonRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, err
	}

	var response commissionTariffsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal commission tariffs: %w", err)
	}
	return response.Report, nil
}
