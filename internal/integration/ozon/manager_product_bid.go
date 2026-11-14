package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
)

var exactManagerBidDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,12})(?:\.([0-9]{1,6}))?$`)
var exactManagerNativeID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var exactManagerMinimumNumber = regexp.MustCompile(`^(0|[1-9][0-9]{0,18})(?:\.[0-9]{1,18})?$`)
var exactManagerUnsetStrategyNumber = regexp.MustCompile(`^0(?:\.0{1,18})?$`)

func exactManagerBidMicro(value string) (string, error) {
	match := exactManagerBidDecimal.FindStringSubmatch(value)
	if match == nil {
		return "", errors.New("exact positive RUB bid with at most six fractional digits required")
	}
	amount, ok := new(big.Int).SetString(match[1]+match[2]+strings.Repeat("0", 6-len(match[2])), 10)
	if !ok || !amount.IsInt64() || amount.Sign() <= 0 {
		return "", errors.New("bid outside exact native micro-ruble range")
	}
	return amount.String(), nil
}
func managerExactNativeID(raw json.RawMessage) (int64, error) {
	value := string(bytes.TrimSpace(raw))
	if strings.HasPrefix(value, `"`) {
		if json.Unmarshal(raw, &value) != nil {
			return 0, errors.New("invalid native ID")
		}
	}
	if !exactManagerNativeID.MatchString(value) {
		return 0, errors.New("positive integer native ID required")
	}
	return strconv.ParseInt(value, 10, 64)
}
func exactManagerProductBidBody(id, product int64, value string) ([]byte, error) {
	if id <= 0 || product <= 0 {
		return nil, errors.New("specific Ozon campaign and SKU required")
	}
	micro, err := exactManagerBidMicro(value)
	if err != nil {
		return nil, err
	}
	type item struct {
		SKU string `json:"sku"`
		Bid string `json:"bid"`
	}
	return json.Marshal(struct {
		Bids []item `json:"bids"`
	}{[]item{{strconv.FormatInt(product, 10), micro}}})
}
func (c *PerfClient) AdvertisingProductBidRequest(creds Credentials, id, product int64, value string) (adguard.Request, error) {
	if c == nil {
		return adguard.Request{}, errors.New("Ozon transport unavailable")
	}
	body, err := exactManagerProductBidBody(id, product, value)
	if err != nil {
		return adguard.Request{}, err
	}
	return adguard.NewOzonRequest("PUT", c.baseURL+fmt.Sprintf("/api/client/campaign/%d/products", id), creds.PerfClientID, body)
}
func (c *PerfClient) SetExactProductBid(ctx context.Context, creds Credentials, id, product int64, value string) error {
	body, err := exactManagerProductBidBody(id, product, value)
	if err != nil {
		return adguard.BeforeSendError(err)
	}
	if err = c.quota.reserve(creds.PerfClientID, QuotaBidWrite, time.Now()); err != nil {
		return adguard.BeforeSendError(err)
	}
	_, err = c.doJSON(withPerfQuota(ctx, QuotaBidWrite), creds, "PUT", fmt.Sprintf("/api/client/campaign/%d/products", id), nil, json.RawMessage(body))
	return err
}

type managerBidCampaign struct {
	ID        json.RawMessage `json:"id"`
	Title     string          `json:"title"`
	State     string          `json:"state"`
	Type      string          `json:"advObjectType"`
	Strategy  string          `json:"productAutopilotStrategy"`
	Placement json.RawMessage `json:"placement"`
	Daily     string          `json:"dailyBudget"`
	Weekly    string          `json:"weeklyBudget"`
}

func (c *PerfClient) managerBidCampaign(ctx context.Context, creds Credentials, id int64) (*managerBidCampaign, error) {
	for page := 1; page <= 100; page++ {
		body, err := c.doGet(ctx, creds, "/api/client/campaign", url.Values{"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(perfPageSize)}})
		if err != nil {
			return nil, err
		}
		var response struct {
			List []managerBidCampaign `json:"list"`
		}
		if json.Unmarshal(body, &response) != nil || len(response.List) > perfPageSize {
			return nil, errors.New("invalid bounded Ozon campaign page")
		}
		var selected *managerBidCampaign
		for _, row := range response.List {
			actual, err := managerExactNativeID(row.ID)
			if err != nil {
				return nil, err
			}
			if actual != id {
				continue
			}
			if selected != nil {
				return nil, errors.New("duplicate Ozon campaign")
			}
			copy := row
			selected = &copy
		}
		if selected != nil {
			return selected, nil
		}
		if len(response.List) < perfPageSize {
			return nil, errors.New("Ozon campaign missing from inventory")
		}
	}
	return nil, errors.New("Ozon campaign inventory exceeds bounded read")
}
func managerBidPlacement(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	var list []string
	if len(raw) > 0 && raw[0] == '"' {
		var single string
		if json.Unmarshal(raw, &single) != nil {
			return false
		}
		list = []string{single}
	} else if json.Unmarshal(raw, &list) != nil {
		return false
	}
	if len(list) == 0 {
		return false
	}
	for _, value := range list {
		switch value {
		case "PLACEMENT_SEARCH", "PLACEMENT_PDP", "PLACEMENT_SEARCH_AND_CATEGORY":
		default:
			return false
		}
	}
	return true
}

// ReadManagerProductBid never scales an unquoted Ozon minimum by assumption.
// Such a value remains raw evidence for personal review, not a verified RUB
// lower bound. Current product bids require the native micro-ruble string.
func (c *PerfClient) ReadManagerProductBid(ctx context.Context, creds Credentials, id, product int64) (*domain.NativeAdBidContext, error) {
	if id <= 0 || product <= 0 {
		return nil, errors.New("specific Ozon product bid required")
	}
	row, err := c.managerBidCampaign(ctx, creds, id)
	if err != nil {
		return nil, err
	}
	if row.Type != "SKU" || row.Strategy != "TARGET_BIDS" || (row.State != "CAMPAIGN_STATE_RUNNING" && row.State != "CAMPAIGN_STATE_INACTIVE") || !managerBidPlacement(row.Placement) {
		return nil, errors.New("explicit manual TARGET_BIDS SKU campaign and supported CPC placement required")
	}
	body, err := c.doGet(ctx, creds, fmt.Sprintf("/api/client/campaign/%d/v2/products", id), nil)
	if err != nil {
		return nil, err
	}
	var products struct {
		Products []struct {
			SKU       json.RawMessage `json:"sku"`
			Bid       json.RawMessage `json:"bid"`
			TargetCIR json.RawMessage `json:"targetCir"`
			Top       json.RawMessage `json:"topPosition"`
		} `json:"products"`
	}
	if json.Unmarshal(body, &products) != nil {
		return nil, errors.New("unreadable Ozon product bids")
	}
	var current *string
	for _, item := range products.Products {
		actual, err := managerExactNativeID(item.SKU)
		if err != nil {
			return nil, err
		}
		if actual != product {
			continue
		}
		if current != nil {
			return nil, errors.New("duplicate Ozon SKU bid")
		}
		var raw string
		if json.Unmarshal(item.Bid, &raw) != nil {
			return nil, errors.New("current Ozon bid requires an explicit native micro-ruble string")
		}
		current = nativeMicroBudget(raw)
		if current == nil || *current == "0" {
			return nil, errors.New("positive current Ozon product bid unavailable")
		}
		for _, other := range []json.RawMessage{item.TargetCIR, item.Top} {
			if len(other) == 0 || string(other) == "null" {
				continue
			}
			var number json.Number
			if json.Unmarshal(other, &number) != nil {
				return nil, errors.New("incompatible Ozon product strategy fields")
			}
			if !exactManagerUnsetStrategyNumber.MatchString(number.String()) {
				return nil, errors.New("manual bid cannot overwrite target CIR or top position")
			}
		}
	}
	if current == nil {
		return nil, errors.New("selected SKU missing from Ozon campaign")
	}
	campaign := domain.NativeAdCampaign{ExternalID: strconv.FormatInt(id, 10), Name: row.Title, Status: row.State, CampaignType: &row.Type, DailyBudgetRub: nativeMicroBudget(row.Daily), WeeklyBudgetRub: nativeMicroBudget(row.Weekly), Warnings: []string{}}
	result := &domain.NativeAdBidContext{Campaign: campaign, Bid: domain.NativeAdProductBid{ProductID: strconv.FormatInt(product, 10), Placement: "campaign_product", CurrentBidRub: *current, Strategy: row.Strategy, PaymentType: "CPC", PeerBidsRub: map[string]string{}, Warnings: []string{"Модель CPC выбрана по стратегии TARGET_BIDS и размещению. Верхняя граница ставки и полнота расходов не подтверждены; снимок не разрешает отправку."}}}
	sku := strconv.FormatInt(product, 10)
	body, err = c.doJSON(ctx, creds, "POST", "/api/client/min/sku", nil, map[string]any{"sku": []string{sku}, "skus": []string{sku}, "paymentType": "CPC", "marketplaceId": marketplaceIDRU})
	if err != nil {
		return nil, err
	}
	type minimum struct {
		SKU json.RawMessage `json:"sku"`
		Bid json.RawMessage `json:"bid"`
	}
	var minima map[string]json.RawMessage
	if json.Unmarshal(body, &minima) != nil {
		return nil, errors.New("unreadable Ozon minimum bids")
	}
	var values []minimum
	keys := 0
	for _, key := range []string{"minBids", "bids", "rows"} {
		if raw, ok := minima[key]; ok {
			keys++
			if json.Unmarshal(raw, &values) != nil {
				return nil, errors.New("invalid Ozon minimum rows")
			}
		}
	}
	if keys != 1 {
		return nil, errors.New("ambiguous Ozon minimum envelope")
	}
	found := false
	for _, item := range values {
		actual, err := managerExactNativeID(item.SKU)
		if err != nil {
			return nil, err
		}
		if actual != product {
			continue
		}
		if found {
			return nil, errors.New("duplicate Ozon minimum")
		}
		found = true
		raw := string(bytes.TrimSpace(item.Bid))
		if strings.HasPrefix(raw, `"`) {
			var micro string
			if json.Unmarshal(item.Bid, &micro) != nil {
				return nil, errors.New("invalid Ozon minimum")
			}
			result.Bid.MinimumBidRub = nativeMicroBudget(micro)
			if result.Bid.MinimumBidRub == nil || *result.Bid.MinimumBidRub == "0" {
				return nil, errors.New("positive Ozon minimum unavailable")
			}
			result.Bid.MinimumSource = "native_micro_rub_string"
		} else {
			if !exactManagerMinimumNumber.MatchString(raw) || !strings.ContainsAny(raw, "123456789") {
				return nil, errors.New("invalid raw Ozon minimum")
			}
			result.Bid.UnverifiedMinimumRaw = &raw
			result.Bid.MinimumSource = "unverified_numeric_unit"
			result.Bid.Warnings = append(result.Bid.Warnings, "Ozon вернул минимум числом без подтверждённого масштаба. RUB-минимум не установлен; проверьте ограничения лично в кабинете.")
		}
	}
	if !found {
		return nil, errors.New("exact Ozon SKU minimum missing")
	}
	return result, nil
}
