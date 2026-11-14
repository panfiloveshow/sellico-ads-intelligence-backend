package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"slices"
	"strconv"
	"strings"
)

func (c *PerfClient) ReadManagerCampaignLaunch(ctx context.Context, creds Credentials, id int64) (*domain.NativeAdLaunchContext, error) {
	if id <= 0 {
		return nil, errors.New("specific Ozon launch target required")
	}
	row, err := c.managerBidCampaign(ctx, creds, id)
	if err != nil {
		return nil, err
	}
	if row.Type != "SKU" || row.Strategy != "TARGET_BIDS" || (row.State != "CAMPAIGN_STATE_RUNNING" && row.State != "CAMPAIGN_STATE_INACTIVE") || !managerBidPlacement(row.Placement) {
		return nil, errors.New("configured manual TARGET_BIDS SKU campaign required")
	}
	var placements []string
	if len(row.Placement) > 0 && row.Placement[0] == '"' {
		var p string
		if json.Unmarshal(row.Placement, &p) != nil {
			return nil, errors.New("invalid Ozon placement")
		}
		placements = []string{p}
	} else if json.Unmarshal(row.Placement, &placements) != nil {
		return nil, errors.New("invalid Ozon placements")
	}
	slices.Sort(placements)
	if len(slices.Compact(placements)) != len(placements) {
		return nil, errors.New("duplicate Ozon placements")
	}
	settings := domain.NativeAdLaunchSettings{Strategy: row.Strategy, PaymentType: "CPC", Placements: placements, Products: []domain.NativeAdLaunchProduct{}}
	body, err := c.doGet(ctx, creds, fmt.Sprintf("/api/client/campaign/%d/v2/products", id), nil)
	if err != nil {
		return nil, err
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || len(envelope) != 1 || envelope["products"] == nil {
		return nil, errors.New("Ozon product response contains unknown pagination or incomplete envelope")
	}
	var response struct {
		Products []struct {
			SKU       json.RawMessage `json:"sku"`
			Bid       *string         `json:"bid"`
			TargetCIR json.RawMessage `json:"targetCir"`
			Top       json.RawMessage `json:"topPosition"`
		} `json:"products"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Products) == 0 || len(response.Products) > 10000 {
		return nil, errors.New("complete bounded Ozon products required")
	}
	seen := map[int64]bool{}
	for _, item := range response.Products {
		product, err := managerExactNativeID(item.SKU)
		if err != nil || seen[product] || item.Bid == nil {
			return nil, errors.New("explicit unique Ozon product and micro bid required")
		}
		seen[product] = true
		value := nativeMicroBudget(*item.Bid)
		if value == nil || *value == "0" {
			return nil, errors.New("positive exact Ozon bid required")
		}
		for _, other := range []json.RawMessage{item.TargetCIR, item.Top} {
			if len(other) == 0 || string(other) == "null" {
				continue
			}
			var n json.Number
			if json.Unmarshal(other, &n) != nil || !exactManagerUnsetStrategyNumber.MatchString(n.String()) {
				return nil, errors.New("manual launch has incompatible strategy settings")
			}
		}
		settings.Products = append(settings.Products, domain.NativeAdLaunchProduct{ProductID: strconv.FormatInt(product, 10), BidsRub: map[string]string{"campaign_product": *value}})
	}
	slices.SortFunc(settings.Products, func(a, b domain.NativeAdLaunchProduct) int { return strings.Compare(a.ProductID, b.ProductID) })
	if row.Daily != "" && nativeMicroBudget(row.Daily) == nil || row.Weekly != "" && nativeMicroBudget(row.Weekly) == nil {
		return nil, errors.New("invalid Ozon launch budget")
	}
	campaign := domain.NativeAdCampaign{ExternalID: strconv.FormatInt(id, 10), Name: row.Title, Status: row.State, CampaignType: &row.Type, DailyBudgetRub: nativeMicroBudget(row.Daily), WeeklyBudgetRub: nativeMicroBudget(row.Weekly), Warnings: []string{}}
	return &domain.NativeAdLaunchContext{Campaign: campaign, Settings: settings}, nil
}
