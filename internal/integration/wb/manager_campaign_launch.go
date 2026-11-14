package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"slices"
	"strconv"
	"strings"
)

// Reads every configured manual product/placement. The vendor eligibility and
// current minimum limits are not inferred from positive bid values.
func (c *Client) ReadManagerCampaignLaunch(ctx context.Context, token string, id int64) (*domain.NativeAdLaunchContext, error) {
	if id <= 0 {
		return nil, errors.New("specific WB launch campaign required")
	}
	campaign, err := c.ReadCampaignControlContext(ctx, token, id)
	if err != nil {
		return nil, err
	}
	if campaign.Status != "4" && campaign.Status != "11" && campaign.Status != "9" {
		return nil, errors.New("ready, paused or running WB campaign required")
	}
	_, body, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/advert/v2/adverts?ids=%d&statuses=%s", id, advertsV2DetailStatuses), token, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Adverts []struct {
			ID          int64  `json:"id"`
			AdvertID    int64  `json:"advertId"`
			Status      *int   `json:"status"`
			BidType     string `json:"bid_type"`
			Name        string `json:"name"`
			PaymentType string `json:"paymentType"`
			Settings    struct {
				Name        string           `json:"name"`
				PaymentType string           `json:"payment_type"`
				Placements  map[string]*bool `json:"placements"`
			} `json:"settings"`
			Products []struct {
				ID   int64             `json:"nm_id"`
				Bids map[string]*int64 `json:"bids_kopecks"`
			} `json:"nm_settings"`
		} `json:"adverts"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil, errors.New("unreadable WB launch settings")
	}
	var settings *domain.NativeAdLaunchSettings
	for _, row := range response.Adverts {
		if row.ID != 0 && row.AdvertID != 0 && row.ID != row.AdvertID {
			return nil, errors.New("conflicting WB launch identity")
		}
		if firstNonZeroInt64(row.ID, row.AdvertID) != id {
			continue
		}
		payment := row.PaymentType
		if payment == "" {
			payment = row.Settings.PaymentType
		}
		name := row.Name
		if name == "" {
			name = row.Settings.Name
		}
		if settings != nil || row.Status == nil || strconv.Itoa(*row.Status) != campaign.Status || name != campaign.Name || row.BidType != "manual" || (payment != "cpc" && payment != "cpm") || campaign.PaymentType == nil || payment != *campaign.PaymentType || campaign.BidType == nil || row.BidType != *campaign.BidType || len(row.Products) == 0 || len(row.Products) > 10000 {
			return nil, errors.New("complete consistent manual WB launch settings required")
		}
		v := domain.NativeAdLaunchSettings{Strategy: row.BidType, PaymentType: payment, Placements: []string{}, Products: []domain.NativeAdLaunchProduct{}}
		for key, value := range row.Settings.Placements {
			if (key != "search" && key != "recommendations") || value == nil {
				return nil, errors.New("unknown WB placement")
			}
			if *value {
				v.Placements = append(v.Placements, key)
			}
		}
		if len(v.Placements) == 0 {
			return nil, errors.New("no WB placement explicitly enabled")
		}
		slices.Sort(v.Placements)
		seen := map[int64]bool{}
		for _, item := range row.Products {
			if item.ID <= 0 || seen[item.ID] {
				return nil, errors.New("duplicate or invalid WB product")
			}
			seen[item.ID] = true
			p := domain.NativeAdLaunchProduct{ProductID: strconv.FormatInt(item.ID, 10), BidsRub: map[string]string{}}
			for key, value := range item.Bids {
				if (key != "search" && key != "recommendations") || value == nil || *value < 0 {
					return nil, errors.New("unknown or invalid configured WB bid")
				}
				p.BidsRub[key] = wbExactBidRub(*value)
			}
			for _, placement := range v.Placements {
				value := item.Bids[placement]
				if value == nil || *value <= 0 {
					return nil, errors.New("positive bid required for every enabled WB placement")
				}
			}
			v.Products = append(v.Products, p)
		}
		slices.SortFunc(v.Products, func(a, b domain.NativeAdLaunchProduct) int { return strings.Compare(a.ProductID, b.ProductID) })
		settings = &v
	}
	if settings == nil {
		return nil, errors.New("WB launch campaign absent")
	}
	if err = c.readLaunchMinimumBids(ctx, token, id, settings); err != nil {
		return nil, err
	}
	if settings.PaymentType == "cpc" {
		budget, err := c.ReadManagerCampaignBudget(ctx, token, id, "daily")
		if err != nil {
			return nil, err
		}
		if budget.Campaign.Status != campaign.Status || budget.Campaign.Name != campaign.Name {
			return nil, errors.New("WB configuration changed during launch read")
		}
		settings.Budget = &budget.Budget
	}
	campaign.Warnings = []string{"Допуски товаров и полнота ограничений площадки не подтверждены. Личный запуск требует отдельных проверок."}
	return &domain.NativeAdLaunchContext{Campaign: *campaign, Settings: *settings}, nil
}

func (c *Client) readLaunchMinimumBids(ctx context.Context, token string, id int64, settings *domain.NativeAdLaunchSettings) error {
	placements := []string{}
	for _, p := range settings.Placements {
		if p == "recommendations" {
			placements = append(placements, "recommendation")
		} else {
			placements = append(placements, p)
		}
	}
	for start := 0; start < len(settings.Products); start += 100 {
		end := min(start+100, len(settings.Products))
		ids := []int64{}
		requested := map[int64]int{}
		for i := start; i < end; i++ {
			v, err := strconv.ParseInt(settings.Products[i].ProductID, 10, 64)
			if err != nil {
				return err
			}
			ids = append(ids, v)
			requested[v] = i
			settings.Products[i].MinimumBidsRub = map[string]string{}
		}
		if err := c.managerBudgetLimiter("launch_minimum", token).Wait(ctx); err != nil {
			return err
		}
		payload, _ := json.Marshal(MinimumBidsRequest{AdvertID: id, NMIDs: ids, PaymentType: settings.PaymentType, PlacementTypes: placements})
		_, body, err := c.doRequest(ctx, "POST", "/api/advert/v1/bids/min", token, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		var response struct {
			Bids []struct {
				ID   int64 `json:"nm_id"`
				Bids []struct {
					Type  string `json:"type"`
					Value *int64 `json:"value"`
				} `json:"bids"`
			} `json:"bids"`
		}
		if json.Unmarshal(body, &response) != nil {
			return errors.New("unreadable WB launch minima")
		}
		seen := map[int64]bool{}
		for _, row := range response.Bids {
			i, ok := requested[row.ID]
			if !ok || seen[row.ID] {
				return errors.New("ambiguous WB launch minimum product")
			}
			seen[row.ID] = true
			for _, bid := range row.Bids {
				key := bid.Type
				if key == "recommendation" {
					key = "recommendations"
				}
				if !slices.Contains(settings.Placements, key) || bid.Value == nil || *bid.Value <= 0 {
					return errors.New("unrequested or invalid WB minimum")
				}
				if _, ok := settings.Products[i].MinimumBidsRub[key]; ok {
					return errors.New("duplicate WB minimum placement")
				}
				current, err := wbExactBidKopecks(settings.Products[i].BidsRub[key])
				if err != nil || current < *bid.Value {
					return errors.New("configured WB bid below confirmed minimum")
				}
				settings.Products[i].MinimumBidsRub[key] = wbExactBidRub(*bid.Value)
			}
		}
		for i := start; i < end; i++ {
			if len(settings.Products[i].MinimumBidsRub) != len(settings.Placements) {
				return errors.New("WB minimum missing for configured product/placement")
			}
		}
	}
	return nil
}
