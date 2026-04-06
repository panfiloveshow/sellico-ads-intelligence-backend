package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

type normQueryRequest struct {
	Items []struct {
		AdvertID int64 `json:"advert_id"`
		NMID     int64 `json:"nm_id"`
	} `json:"items"`
}

type normQueryStatsRequest struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Items []struct {
		AdvertID int64 `json:"advert_id"`
		NMID     int64 `json:"nm_id"`
	} `json:"items"`
}

type normQueryBidsResponse struct {
	Bids []struct {
		AdvertID  int64  `json:"advert_id"`
		NMID      int64  `json:"nm_id"`
		Bid       int64  `json:"bid"`
		NormQuery string `json:"norm_query"`
	} `json:"bids"`
}

type normQueryStatsResponse struct {
	Stats []struct {
		AdvertID int64 `json:"advert_id"`
		NMID     int64 `json:"nm_id"`
		Stats    []struct {
			NormQuery string  `json:"norm_query"`
			Views     int64   `json:"views"`     // 0 for CPC campaigns (WB API 2026-04 change)
			Clicks    int64   `json:"clicks"`
			CPC       float64 `json:"cpc"`
			CPM       float64 `json:"cpm"`       // 0 for CPC campaigns
			CTR       float64 `json:"ctr"`       // 0 for CPC campaigns
			Orders    int64   `json:"orders"`
			Sum       float64 `json:"sum"`       // direct spend amount if provided
		} `json:"stats"`
	} `json:"stats"`
}

type aggregatedNormQuery struct {
	keyword string
	bid     int64
	views   int64
	clicks  int64
	orders  int64
	spend   float64
}

// ListSearchClusters fetches search clusters for a campaign from the current normquery API.
func (c *Client) ListSearchClusters(ctx context.Context, token string, campaignID int) ([]WBSearchClusterDTO, error) {
	aggregated, dateTo, err := c.loadNormQueryAggregates(ctx, token, campaignID)
	if err != nil {
		return nil, err
	}

	_ = dateTo
	return aggregatedToClusters(campaignID, aggregated), nil
}

// GetSearchClusterStats fetches phrase statistics for a campaign from the current normquery API.
func (c *Client) GetSearchClusterStats(ctx context.Context, token string, campaignID int) ([]WBSearchClusterStatDTO, error) {
	aggregated, dateTo, err := c.loadNormQueryAggregates(ctx, token, campaignID)
	if err != nil {
		return nil, err
	}

	return aggregatedToClusterStats(campaignID, dateTo, aggregated), nil
}

func (c *Client) loadNormQueryAggregates(ctx context.Context, token string, campaignID int) (map[string]*aggregatedNormQuery, string, error) {
	campaigns, err := c.ListCampaigns(ctx, token)
	if err != nil {
		return nil, "", err
	}

	nmIDs := campaignNMIDs(campaigns, campaignID)
	if len(nmIDs) == 0 {
		return map[string]*aggregatedNormQuery{}, time.Now().UTC().Format(dateFmt), nil
	}

	dateTo := time.Now().UTC().Format(dateFmt)
	dateFrom := time.Now().UTC().AddDate(0, 0, -30).Format(dateFmt)

	statsBody, err := json.Marshal(buildNormQueryStatsRequest(campaignID, nmIDs, dateFrom, dateTo))
	if err != nil {
		return nil, "", apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal normquery stats request: %v", err))
	}

	// Use v1 endpoint — supports CPC campaigns (WB API 2026-04 update)
	_, statsRaw, err := c.doRequest(ctx, "POST", "/adv/v1/normquery/stats", token, bytes.NewReader(statsBody))
	if err != nil {
		return nil, "", err
	}

	var statsResponse normQueryStatsResponse
	if err := json.Unmarshal(statsRaw, &statsResponse); err != nil {
		return nil, "", apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal normquery stats: %v", err))
	}

	bidsBody, err := json.Marshal(buildNormQueryRequest(campaignID, nmIDs))
	if err != nil {
		return nil, "", apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal normquery bids request: %v", err))
	}

	_, bidsRaw, err := c.doRequest(ctx, "POST", "/adv/v0/normquery/get-bids", token, bytes.NewReader(bidsBody))
	if err != nil {
		c.logger.Warn().Err(err).Int("campaign_id", campaignID).Msg("normquery get-bids failed, falling back without explicit bids")
	}

	bidsByQuery := make(map[string]int64)
	if len(bidsRaw) > 0 {
		var bidsResponse normQueryBidsResponse
		if err := json.Unmarshal(bidsRaw, &bidsResponse); err == nil {
			for _, bid := range bidsResponse.Bids {
				if bid.NormQuery == "" {
					continue
				}
				if bid.Bid > bidsByQuery[bid.NormQuery] {
					bidsByQuery[bid.NormQuery] = bid.Bid
				}
			}
		}
	}

	aggregated := make(map[string]*aggregatedNormQuery)
	for _, item := range statsResponse.Stats {
		for _, stat := range item.Stats {
			keyword := stat.NormQuery
			if keyword == "" {
				continue
			}
			entry, ok := aggregated[keyword]
			if !ok {
				entry = &aggregatedNormQuery{keyword: keyword}
				aggregated[keyword] = entry
			}
			entry.views += stat.Views
			entry.clicks += stat.Clicks
			entry.orders += stat.Orders
			if entry.bid == 0 {
				entry.bid = bidsByQuery[keyword]
			}
			// WB API 2026-04: CPC campaigns return sum directly, views/cpm are 0
			if stat.Sum > 0 {
				entry.spend += stat.Sum / 100 // sum is in kopecks
			} else {
				entry.spend += normQuerySpend(stat.Views, stat.Clicks, stat.CPC, stat.CPM)
			}
		}
	}

	return aggregated, dateTo, nil
}

func buildNormQueryRequest(campaignID int, nmIDs []int64) normQueryRequest {
	req := normQueryRequest{
		Items: make([]struct {
			AdvertID int64 `json:"advert_id"`
			NMID     int64 `json:"nm_id"`
		}, 0, len(nmIDs)),
	}
	for _, nmID := range nmIDs {
		req.Items = append(req.Items, struct {
			AdvertID int64 `json:"advert_id"`
			NMID     int64 `json:"nm_id"`
		}{
			AdvertID: int64(campaignID),
			NMID:     nmID,
		})
	}
	return req
}

func buildNormQueryStatsRequest(campaignID int, nmIDs []int64, dateFrom, dateTo string) normQueryStatsRequest {
	req := normQueryStatsRequest{
		From: dateFrom,
		To:   dateTo,
		Items: make([]struct {
			AdvertID int64 `json:"advert_id"`
			NMID     int64 `json:"nm_id"`
		}, 0, len(nmIDs)),
	}
	for _, nmID := range nmIDs {
		req.Items = append(req.Items, struct {
			AdvertID int64 `json:"advert_id"`
			NMID     int64 `json:"nm_id"`
		}{
			AdvertID: int64(campaignID),
			NMID:     nmID,
		})
	}
	return req
}

func campaignNMIDs(campaigns []WBCampaignDTO, campaignID int) []int64 {
	for _, campaign := range campaigns {
		if campaign.AdvertID == campaignID {
			return campaign.NMIDs
		}
	}
	return nil
}

func aggregatedToClusters(campaignID int, aggregated map[string]*aggregatedNormQuery) []WBSearchClusterDTO {
	keywords := sortedNormQueries(aggregated)
	result := make([]WBSearchClusterDTO, 0, len(keywords))
	for _, keyword := range keywords {
		entry := aggregated[keyword]
		// For CPC campaigns, views=0 → use clicks as count fallback
		count := int(entry.views)
		if count == 0 {
			count = int(entry.clicks)
		}
		result = append(result, WBSearchClusterDTO{
			ClusterID: syntheticClusterID(campaignID, keyword),
			Keywords:  []string{keyword},
			Count:     count,
			Bid:       entry.bid,
		})
	}
	return result
}

func aggregatedToClusterStats(campaignID int, date string, aggregated map[string]*aggregatedNormQuery) []WBSearchClusterStatDTO {
	keywords := sortedNormQueries(aggregated)
	result := make([]WBSearchClusterStatDTO, 0, len(keywords))
	for _, keyword := range keywords {
		entry := aggregated[keyword]
		// For CPC campaigns views=0 → store clicks as views so phrase stats are meaningful
		views := entry.views
		if views == 0 && entry.clicks > 0 {
			views = entry.clicks
		}
		result = append(result, WBSearchClusterStatDTO{
			ClusterID: syntheticClusterID(campaignID, keyword),
			Date:      date,
			Views:     views,
			Clicks:    entry.clicks,
			Sum:       entry.spend,
		})
	}
	return result
}

func sortedNormQueries(aggregated map[string]*aggregatedNormQuery) []string {
	keywords := make([]string, 0, len(aggregated))
	for keyword := range aggregated {
		keywords = append(keywords, keyword)
	}
	sort.Strings(keywords)
	return keywords
}

func normQuerySpend(views, clicks int64, cpc, cpm float64) float64 {
	if cpc > 0 && clicks > 0 {
		return (float64(clicks) * cpc) / 100
	}
	if cpm > 0 && views > 0 {
		return (float64(views) / 1000) * (cpm / 100)
	}
	return 0
}

func syntheticClusterID(campaignID int, keyword string) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(fmt.Sprintf("%d:%s", campaignID, keyword)))
	return int64(hash.Sum64() & 0x7fffffffffffffff)
}
