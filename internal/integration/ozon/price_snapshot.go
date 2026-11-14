package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// PriceSnapshot keeps exact kopecks and unknown optional values. It is a vendor
// observation, not the local hourly mirror or an optimistic import receipt.
type PriceSnapshot struct {
	ProductID     int64            `json:"product_id"`
	OfferID       string           `json:"offer_id"`
	PriceCents    int64            `json:"price_cents"`
	OldPriceCents *int64           `json:"old_price_cents"`
	MinPriceCents *int64           `json:"min_price_cents"`
	Conditions    *PriceConditions `json:"conditions,omitempty"`
}

// Nil means the API did not establish the complete promotion context. Ordinary
// historical readers retain their monetary behavior; approved writes require it.
type PriceConditions struct {
	MarketingPriceCents int64 `json:"marketing_price_cents"`
	AutoActionEnabled   bool  `json:"auto_action_enabled"`
	AutoAddToActions    bool  `json:"auto_add_to_actions"`
	HasActions          bool  `json:"has_actions"`
}

func (p PriceSnapshot) HasSimpleConditions() bool {
	return p.Conditions != nil && !p.Conditions.AutoActionEnabled && !p.Conditions.AutoAddToActions && !p.Conditions.HasActions && p.Conditions.MarketingPriceCents == p.PriceCents
}

func observedPriceConditions(marketingPrice json.RawMessage, auto, add *bool, marketing json.RawMessage) *PriceConditions {
	if auto == nil || add == nil {
		return nil
	}
	amount, err := priceSnapshotCents(marketingPrice, true)
	if err != nil || amount == nil {
		return nil
	}
	var data struct {
		Actions json.RawMessage `json:"actions"`
		Exists  *bool           `json:"ozon_actions_exist"`
	}
	if json.Unmarshal(marketing, &data) != nil || data.Exists == nil {
		return nil
	}
	raw := bytes.TrimSpace(data.Actions)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var actions []json.RawMessage
	if json.Unmarshal(raw, &actions) != nil || actions == nil {
		return nil
	}
	return &PriceConditions{MarketingPriceCents: *amount, AutoActionEnabled: *auto, AutoAddToActions: *add, HasActions: *data.Exists || len(actions) > 0}
}

var decimalMoneyPattern = regexp.MustCompile(`^[+]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,2})?$`)

func priceSnapshotCents(raw json.RawMessage, required bool) (*int64, error) {
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, `"`) {
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, errors.New("invalid money value")
		}
	}
	if value == "" || value == "null" {
		if required {
			return nil, errors.New("missing seller price")
		}
		return nil, nil
	}
	if len(value) > 64 || !decimalMoneyPattern.MatchString(value) {
		return nil, errors.New("invalid decimal money")
	}
	rational, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, errors.New("invalid decimal money")
	}
	rational.Mul(rational, big.NewRat(100, 1))
	if !rational.IsInt() || !rational.Num().IsInt64() {
		return nil, errors.New("money cannot be represented in kopecks")
	}
	cents := rational.Num().Int64()
	if cents < 0 || cents > 999999999999 || (required && cents == 0) {
		return nil, errors.New("money outside the supported range")
	}
	return &cents, nil
}

// ReadPriceSnapshots reads only the requested product_id values. Missing rows
// stay absent from the map: callers must never interpret absence as zero.
func (c *SellerClient) ReadPriceSnapshots(ctx context.Context, creds Credentials, productIDs []int64) (map[int64]PriceSnapshot, error) {
	if len(productIDs) == 0 || len(productIDs) > sellerPageSize {
		return nil, errors.New("1 to 1000 product IDs required")
	}
	wanted := make(map[int64]bool, len(productIDs))
	ids := make([]string, 0, len(productIDs))
	for _, id := range productIDs {
		if id <= 0 {
			return nil, errors.New("invalid product ID")
		}
		if !wanted[id] {
			ids = append(ids, strconv.FormatInt(id, 10))
			wanted[id] = true
		}
	}
	payload := struct {
		Filter struct {
			ProductID  []string `json:"product_id"`
			Visibility string   `json:"visibility"`
		} `json:"filter"`
		Limit int `json:"limit"`
	}{Limit: sellerPageSize}
	payload.Filter.ProductID = ids
	payload.Filter.Visibility = "ALL"
	body, err := c.do(ctx, creds, "/v5/product/info/prices", payload)
	if err != nil {
		return nil, err
	}
	var response struct {
		Items []struct {
			ProductID        flexInt64       `json:"product_id"`
			OfferID          string          `json:"offer_id"`
			MarketingActions json.RawMessage `json:"marketing_actions"`
			Price            struct {
				Currency       string          `json:"currency_code"`
				Price          json.RawMessage `json:"price"`
				OldPrice       json.RawMessage `json:"old_price"`
				MinPrice       json.RawMessage `json:"min_price"`
				MarketingPrice json.RawMessage `json:"marketing_seller_price"`
				AutoAction     *bool           `json:"auto_action_enabled"`
				AutoAdd        *bool           `json:"auto_add_to_ozon_actions_list_enabled"`
			} `json:"price"`
		} `json:"items"`
	}
	if err := decodeJSON(body, &response, "selected product prices"); err != nil {
		return nil, err
	}
	if response.Items == nil {
		return nil, errors.New("missing selected price rows")
	}
	result := make(map[int64]PriceSnapshot, len(response.Items))
	for _, row := range response.Items {
		id := int64(row.ProductID)
		if !wanted[id] || result[id].ProductID != 0 || row.Price.Currency != "RUB" {
			return nil, errors.New("unverified price scope, currency or duplicate")
		}
		price, priceErr := priceSnapshotCents(row.Price.Price, true)
		oldPrice, oldErr := priceSnapshotCents(row.Price.OldPrice, false)
		minPrice, minErr := priceSnapshotCents(row.Price.MinPrice, false)
		if priceErr != nil || oldErr != nil || minErr != nil {
			return nil, fmt.Errorf("product %d has an unverified price", id)
		}
		result[id] = PriceSnapshot{ProductID: id, OfferID: row.OfferID, PriceCents: *price, OldPriceCents: oldPrice, MinPriceCents: minPrice,
			Conditions: observedPriceConditions(row.Price.MarketingPrice, row.Price.AutoAction, row.Price.AutoAdd, row.MarketingActions)}
	}
	return result, nil
}
