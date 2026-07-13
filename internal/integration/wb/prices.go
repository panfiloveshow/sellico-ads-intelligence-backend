package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// ErrPricesScopeMissing is returned when the WB token lacks the "Цены и скидки"
// (Prices and discounts) category — the discounts-prices-api answers 401/403.
// Callers mark the cabinet capability instead of failing the whole workspace.
var ErrPricesScopeMissing = errors.New("wb token missing prices scope")

// GoodsPrice is a product's current price/discount as reported by
// GET /api/v2/list/goods/filter. All *_rub-derived amounts are integer rubles.
type GoodsPrice struct {
	NmID              int64
	VendorCode        string
	Price             int64 // base price, integer rubles (sizes[0].price)
	DiscountedPrice   int64 // WB-computed effective price, integer rubles
	Discount          int
	ClubDiscount      int
	EditableSizePrice bool
	Currency          string
}

// PriceUpdateItem is one line of a POST /api/v2/upload/task batch.
type PriceUpdateItem struct {
	NmID     int64 `json:"nmID"`
	Price    int64 `json:"price"`    // base price, integer rubles
	Discount int   `json:"discount"` // percent
}

// PriceTaskStatus is the processing status of an upload task
// (GET /api/v2/history/tasks or /api/v2/buffer/tasks).
// WB status codes: 3 = processed OK, 5 = processed with per-product errors,
// 6 = processed, all products errored.
type PriceTaskStatus struct {
	ID     int64
	Status int
}

// PriceTaskGood is a per-product result of an upload task
// (GET /api/v2/history/goods/task), carrying the WB error text for failed rows.
type PriceTaskGood struct {
	NmID      int64
	Price     int64
	Discount  int
	Status    int
	ErrorText string
}

// QuarantineGood is a product WB parked in price quarantine
// (new discounted price ≥3x lower than the old one).
type QuarantineGood struct {
	NmID          int64
	OldPrice      int64
	NewPrice      int64
	DiscountedOld int64
	DiscountedNew int64
}

// --- wire envelopes ---

type pricesListResponse struct {
	Data struct {
		ListGoods []struct {
			NmID       int64  `json:"nmID"`
			VendorCode string `json:"vendorCode"`
			Sizes      []struct {
				SizeID int64 `json:"sizeID"`
				// WB returns rubles here, and discountedPrice can be fractional
				// (e.g. 2494.23) — parse as float, round to whole rubles below.
				Price           float64 `json:"price"`
				DiscountedPrice float64 `json:"discountedPrice"`
			} `json:"sizes"`
			CurrencyIsoCode4217 string `json:"currencyIsoCode4217"`
			Discount            int    `json:"discount"`
			ClubDiscount        int    `json:"clubDiscount"`
			EditableSizePrice   bool   `json:"editableSizePrice"`
		} `json:"listGoods"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

type priceUploadRequest struct {
	Data []PriceUpdateItem `json:"data"`
}

type priceUploadResponse struct {
	Data struct {
		ID            int64 `json:"id"`
		AlreadyExists bool  `json:"alreadyExists"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// ponytail: WB does not publish a stable schema for the task-status/goods/quarantine
// envelopes; these mirror the documented fields (Dakword/WBSeller + openapi.wildberries.ru).
// Verify field names against the live API on first integration and adjust here only.
type priceTaskStatusResponse struct {
	Data struct {
		UploadID int64 `json:"uploadID"`
		Status   int   `json:"status"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

type priceTaskGoodsResponse struct {
	Data struct {
		HistoryGoods []struct {
			NmID      int64  `json:"nmID"`
			Price     int64  `json:"price"`
			Discount  int    `json:"discount"`
			Status    int    `json:"status"`
			ErrorText string `json:"errorText"`
		} `json:"historyGoods"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

type quarantineResponse struct {
	Data struct {
		QuarantineGoods []struct {
			NmID int64 `json:"nmID"`
			// Rubles; discounted values can be fractional — round on mapping.
			Price           float64 `json:"price"`
			NewPrice        float64 `json:"newPrice"`
			DiscountedPrice float64 `json:"discountedPrice"`
			NewDiscounted   float64 `json:"newDiscountedPrice"`
		} `json:"quarantineGoods"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// classifyPricesError maps a 401/403 from the prices host to ErrPricesScopeMissing,
// leaving all other errors (rate limit, 5xx, transport) intact for the caller.
func classifyPricesError(resp *http.Response, err error) error {
	if err == nil {
		return nil
	}
	if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
		return ErrPricesScopeMissing
	}
	return err
}

// ListGoodsPrices returns one page of the seller's own current prices/discounts.
// nmID is optional (nil = all products). limit ≤ 1000.
func (c *Client) ListGoodsPrices(ctx context.Context, token string, limit, offset int, nmID *int64) ([]GoodsPrice, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	path := fmt.Sprintf("/api/v2/list/goods/filter?limit=%d&offset=%d", limit, offset)
	if nmID != nil {
		path += fmt.Sprintf("&filterNmID=%d", *nmID)
	}
	resp, body, err := c.doPricesRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, classifyPricesError(resp, err)
	}

	var parsed pricesListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal goods prices: %w", err)
	}
	if parsed.Error {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("wb prices list error: %s", parsed.ErrorText))
	}

	goods := make([]GoodsPrice, 0, len(parsed.Data.ListGoods))
	for _, g := range parsed.Data.ListGoods {
		gp := GoodsPrice{
			NmID:              g.NmID,
			VendorCode:        g.VendorCode,
			Discount:          g.Discount,
			ClubDiscount:      g.ClubDiscount,
			EditableSizePrice: g.EditableSizePrice,
			Currency:          g.CurrencyIsoCode4217,
		}
		if len(g.Sizes) > 0 {
			gp.Price = int64(math.Round(g.Sizes[0].Price))
			gp.DiscountedPrice = int64(math.Round(g.Sizes[0].DiscountedPrice))
		}
		goods = append(goods, gp)
	}
	return goods, nil
}

// UploadPriceTask submits a batch of price/discount changes (≤1000 items).
// A 208 response (task already exists) returns duplicate=true with the existing
// task id when WB echoes it.
func (c *Client) UploadPriceTask(ctx context.Context, token string, items []PriceUpdateItem) (taskID int64, duplicate bool, err error) {
	if len(items) == 0 {
		return 0, false, apperror.New(apperror.ErrValidation, "no price items to upload")
	}
	if len(items) > 1000 {
		return 0, false, apperror.New(apperror.ErrValidation, "price upload batch exceeds 1000 items")
	}
	payload, err := json.Marshal(priceUploadRequest{Data: items})
	if err != nil {
		return 0, false, fmt.Errorf("marshal price upload: %w", err)
	}
	resp, body, reqErr := c.doPricesRequest(ctx, http.MethodPost, "/api/v2/upload/task", token, bytes.NewReader(payload))

	// 208 = duplicate upload; WB still returns a body with the existing task id.
	dup := resp != nil && resp.StatusCode == http.StatusAlreadyReported
	if reqErr != nil && !dup {
		return 0, false, classifyPricesError(resp, reqErr)
	}

	var parsed priceUploadResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			return 0, dup, fmt.Errorf("unmarshal price upload response: %w", err)
		}
	}
	if parsed.Error && !dup {
		return 0, false, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("wb price upload error: %s", parsed.ErrorText))
	}
	return parsed.Data.ID, dup || parsed.Data.AlreadyExists, nil
}

// GetPriceTaskHistory returns the final processing status of an upload task.
func (c *Client) GetPriceTaskHistory(ctx context.Context, token string, uploadID int64) (*PriceTaskStatus, error) {
	return c.priceTaskStatus(ctx, token, "/api/v2/history/tasks", uploadID)
}

// GetPriceTaskBuffer returns the pending (not-yet-finalized) status of an upload task.
func (c *Client) GetPriceTaskBuffer(ctx context.Context, token string, uploadID int64) (*PriceTaskStatus, error) {
	return c.priceTaskStatus(ctx, token, "/api/v2/buffer/tasks", uploadID)
}

func (c *Client) priceTaskStatus(ctx context.Context, token, base string, uploadID int64) (*PriceTaskStatus, error) {
	path := fmt.Sprintf("%s?uploadID=%d", base, uploadID)
	resp, body, err := c.doPricesRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, classifyPricesError(resp, err)
	}
	var parsed priceTaskStatusResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal price task status: %w", err)
	}
	if parsed.Error {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("wb price task status error: %s", parsed.ErrorText))
	}
	return &PriceTaskStatus{ID: parsed.Data.UploadID, Status: parsed.Data.Status}, nil
}

// ListPriceTaskHistoryGoods returns per-product results (incl. error text) for a
// processed upload task. limit ≤ 1000.
func (c *Client) ListPriceTaskHistoryGoods(ctx context.Context, token string, uploadID int64, limit, offset int) ([]PriceTaskGood, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	path := fmt.Sprintf("/api/v2/history/goods/task?uploadID=%d&limit=%d&offset=%d", uploadID, limit, offset)
	resp, body, err := c.doPricesRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, classifyPricesError(resp, err)
	}
	var parsed priceTaskGoodsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal price task goods: %w", err)
	}
	if parsed.Error {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("wb price task goods error: %s", parsed.ErrorText))
	}
	goods := make([]PriceTaskGood, 0, len(parsed.Data.HistoryGoods))
	for _, g := range parsed.Data.HistoryGoods {
		goods = append(goods, PriceTaskGood{
			NmID:      g.NmID,
			Price:     g.Price,
			Discount:  g.Discount,
			Status:    g.Status,
			ErrorText: g.ErrorText,
		})
	}
	return goods, nil
}

// ListQuarantineGoods returns products currently held in price quarantine.
func (c *Client) ListQuarantineGoods(ctx context.Context, token string, limit, offset int) ([]QuarantineGood, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	path := fmt.Sprintf("/api/v2/quarantine/goods?limit=%d&offset=%d", limit, offset)
	resp, body, err := c.doPricesRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return nil, classifyPricesError(resp, err)
	}
	var parsed quarantineResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal quarantine goods: %w", err)
	}
	if parsed.Error {
		return nil, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("wb quarantine error: %s", parsed.ErrorText))
	}
	goods := make([]QuarantineGood, 0, len(parsed.Data.QuarantineGoods))
	for _, g := range parsed.Data.QuarantineGoods {
		goods = append(goods, QuarantineGood{
			NmID:          g.NmID,
			OldPrice:      int64(math.Round(g.Price)),
			NewPrice:      int64(math.Round(g.NewPrice)),
			DiscountedOld: int64(math.Round(g.DiscountedPrice)),
			DiscountedNew: int64(math.Round(g.NewDiscounted)),
		})
	}
	return goods, nil
}
