package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// PriceSnapshot is a scoped vendor observation, not the hourly integer-ruble
// mirror. Optional pointers preserve unknown conditions instead of zeroes.
type PriceSnapshot struct {
	NmID                int64               `json:"nm_id"`
	VendorCode          string              `json:"vendor_code"`
	DiscountPercent     int                 `json:"discount_percent"`
	ClubDiscountPercent *int                `json:"club_discount_percent"`
	EditableSizePrice   *bool               `json:"editable_size_price"`
	WholesaleConditions *[]json.RawMessage  `json:"wholesale_conditions"`
	Sizes               []PriceSizeSnapshot `json:"sizes"`
}

type PriceSizeSnapshot struct {
	SizeID               int64 `json:"size_id"`
	PriceCents           int64 `json:"price_cents"`
	DiscountedPriceCents int64 `json:"discounted_price_cents"`
	ClubPriceCents       int64 `json:"club_price_cents"`
}

var wbPriceDecimalPattern = regexp.MustCompile(`^[+]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,2})?$`)

func exactObservedWBCents(raw json.RawMessage, positive bool) (int64, error) {
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, `"`) {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, errors.New("invalid WB money")
		}
	}
	if len(value) > 64 || !wbPriceDecimalPattern.MatchString(value) {
		return 0, errors.New("unknown WB money")
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, errors.New("invalid WB decimal money")
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, errors.New("WB money is not exact kopecks")
	}
	cents := r.Num().Int64()
	if cents < 0 || cents > 999999999999 || (positive && cents == 0) {
		return 0, errors.New("WB money outside supported range")
	}
	return cents, nil
}

// ReadPriceSnapshots uses the documented read-only batch lookup. Missing
// requested rows stay absent; unexpected or duplicate rows invalidate scope.
func (c *Client) ReadPriceSnapshots(ctx context.Context, token string, nmIDs []int64) (map[int64]PriceSnapshot, error) {
	if len(nmIDs) == 0 || len(nmIDs) > 1000 {
		return nil, errors.New("1 to 1000 WB articles required")
	}
	wanted := make(map[int64]bool, len(nmIDs))
	ids := make([]int64, 0, len(nmIDs))
	for _, id := range nmIDs {
		if id <= 0 {
			return nil, errors.New("invalid WB article")
		}
		if !wanted[id] {
			wanted[id] = true
			ids = append(ids, id)
		}
	}
	payload, _ := json.Marshal(struct {
		NmList []int64 `json:"nmList"`
	}{ids})
	resp, body, err := c.doPricesRequest(priceRequestContext(ctx, 16<<20), http.MethodPost, "/api/v2/list/goods/filter", token, bytes.NewReader(payload))
	if err != nil {
		return nil, classifyPricesError(resp, err)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		return nil, errors.New("unverified WB price response status")
	}
	var response struct {
		Error *bool `json:"error"`
		Data  struct {
			Rows []struct {
				NmID       int64           `json:"nmID"`
				VendorCode string          `json:"vendorCode"`
				Currency   string          `json:"currencyIsoCode4217"`
				Discount   *int            `json:"discount"`
				Club       *int            `json:"clubDiscount"`
				Editable   *bool           `json:"editableSizePrice"`
				Wholesale  json.RawMessage `json:"wholesaleDiscountThreshold"`
				Sizes      []struct {
					ID         int64           `json:"sizeID"`
					Price      json.RawMessage `json:"price"`
					Discounted json.RawMessage `json:"discountedPrice"`
					Club       json.RawMessage `json:"clubDiscountedPrice"`
				} `json:"sizes"`
			} `json:"listGoods"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.New("invalid WB price response")
	}
	if response.Error == nil || *response.Error || response.Data.Rows == nil {
		return nil, errors.New("WB price response is incomplete or rejected")
	}
	result := make(map[int64]PriceSnapshot, len(response.Data.Rows))
	for _, row := range response.Data.Rows {
		if !wanted[row.NmID] || result[row.NmID].NmID != 0 || row.Currency != "RUB" || row.Discount == nil || *row.Discount < 0 || *row.Discount > 100 || len(row.Sizes) == 0 {
			return nil, errors.New("unverified WB price scope or schema")
		}
		if row.Club != nil && (*row.Club < 0 || *row.Club > 100) {
			return nil, errors.New("invalid WB club conditions")
		}
		item := PriceSnapshot{NmID: row.NmID, VendorCode: row.VendorCode, DiscountPercent: *row.Discount, ClubDiscountPercent: row.Club, EditableSizePrice: row.Editable}
		if raw := bytes.TrimSpace(row.Wholesale); len(raw) > 0 && string(raw) != "null" {
			var conditions []json.RawMessage
			if err := json.Unmarshal(raw, &conditions); err != nil {
				return nil, errors.New("invalid WB wholesale conditions")
			}
			item.WholesaleConditions = &conditions
		}
		seenSizes := map[int64]bool{}
		for _, size := range row.Sizes {
			base, baseErr := exactObservedWBCents(size.Price, true)
			discounted, discountErr := exactObservedWBCents(size.Discounted, false)
			club, clubErr := exactObservedWBCents(size.Club, false)
			if size.ID <= 0 || seenSizes[size.ID] || baseErr != nil || base%100 != 0 || discountErr != nil || clubErr != nil {
				return nil, errors.New("unverified WB size price")
			}
			seenSizes[size.ID] = true
			item.Sizes = append(item.Sizes, PriceSizeSnapshot{SizeID: size.ID, PriceCents: base, DiscountedPriceCents: discounted, ClubPriceCents: club})
		}
		sort.Slice(item.Sizes, func(i, j int) bool { return item.Sizes[i].SizeID < item.Sizes[j].SizeID })
		result[item.NmID] = item
	}
	return result, nil
}
