package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// UpdateCampaignBidsRequest represents bid updates for product cards in campaigns.
type UpdateCampaignBidsRequest struct {
	Bids []CampaignBidGroup `json:"bids"`
}

type CampaignBidGroup struct {
	AdvertID int64       `json:"advert_id"`
	NMBids   []NMBidItem `json:"nm_bids"`
}

type NMBidItem struct {
	NMID       int64  `json:"nm_id"`
	BidKopecks int    `json:"bid_kopecks"`
	Placement  string `json:"placement"`
}

type CreateCampaignRequest struct {
	Name           string   `json:"name"`
	NMIDs          []int64  `json:"nms"`
	BidType        string   `json:"bid_type,omitempty"`
	PaymentType    string   `json:"payment_type,omitempty"`
	PlacementTypes []string `json:"placement_types,omitempty"`
}

func (c *Client) CreateCampaign(ctx context.Context, token string, request CreateCampaignRequest) (int64, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return 0, fmt.Errorf("marshal create campaign request: %w", err)
	}

	_, responseBody, err := c.doRequest(ctx, "POST", "/adv/v2/seacat/save-ad", token, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	var wbCampaignID int64
	if err := json.Unmarshal(responseBody, &wbCampaignID); err != nil {
		return 0, fmt.Errorf("unmarshal create campaign response: %w", err)
	}
	if wbCampaignID <= 0 {
		return 0, fmt.Errorf("create campaign response missing campaign id")
	}
	return wbCampaignID, nil
}

// UpdateCampaignBid sends a bid update to WB API.
// WB API endpoint: PATCH /api/advert/v1/bids
func (c *Client) UpdateCampaignBid(ctx context.Context, token string, wbCampaignID int64, campaignType int, nmID int64, placement string, newBid int) error {
	_ = campaignType

	nmIDs, err := c.resolveCampaignNMIDs(ctx, token, wbCampaignID, nmID)
	if err != nil {
		return err
	}
	if placement == "" {
		placement = "search"
	}

	nmBids := make([]NMBidItem, 0, len(nmIDs))
	for _, itemNMID := range nmIDs {
		nmBids = append(nmBids, NMBidItem{
			NMID:       itemNMID,
			BidKopecks: newBid,
			Placement:  placement,
		})
	}

	payload := UpdateCampaignBidsRequest{
		Bids: []CampaignBidGroup{
			{AdvertID: wbCampaignID, NMBids: nmBids},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal campaign bid request: %w", err)
	}

	_, _, err = c.doRequest(ctx, "PATCH", "/api/advert/v1/bids", token, bytes.NewReader(body))
	return err
}

func (c *Client) resolveCampaignNMIDs(ctx context.Context, token string, wbCampaignID int64, nmID int64) ([]int64, error) {
	if nmID != 0 {
		return []int64{nmID}, nil
	}

	campaigns, err := c.ListCampaigns(ctx, token)
	if err != nil {
		return nil, err
	}
	for _, campaign := range campaigns {
		if int64(campaign.AdvertID) == wbCampaignID && len(campaign.NMIDs) > 0 {
			return campaign.NMIDs, nil
		}
	}
	return nil, errors.New("campaign nm ids not found")
}

// StartCampaign starts a WB campaign.
func (c *Client) StartCampaign(ctx context.Context, token string, wbCampaignID int64) error {
	path := fmt.Sprintf("/adv/v0/start?id=%d", wbCampaignID)
	_, _, err := c.doRequest(ctx, "GET", path, token, nil)
	return err
}

// PauseCampaign pauses a WB campaign.
func (c *Client) PauseCampaign(ctx context.Context, token string, wbCampaignID int64) error {
	path := fmt.Sprintf("/adv/v0/pause?id=%d", wbCampaignID)
	_, _, err := c.doRequest(ctx, "GET", path, token, nil)
	return err
}

// StopCampaign stops a WB campaign.
func (c *Client) StopCampaign(ctx context.Context, token string, wbCampaignID int64) error {
	path := fmt.Sprintf("/adv/v0/stop?id=%d", wbCampaignID)
	_, _, err := c.doRequest(ctx, "GET", path, token, nil)
	return err
}

// DeleteCampaign deletes a WB campaign that is ready to launch.
func (c *Client) DeleteCampaign(ctx context.Context, token string, wbCampaignID int64) error {
	path := fmt.Sprintf("/adv/v0/delete?id=%d", wbCampaignID)
	_, _, err := c.doRequest(ctx, "GET", path, token, nil)
	return err
}

type renameCampaignRequest struct {
	AdvertID int64  `json:"advertId"`
	Name     string `json:"name"`
}

// RenameCampaign renames an existing WB campaign.
func (c *Client) RenameCampaign(ctx context.Context, token string, wbCampaignID int64, name string) error {
	payload := renameCampaignRequest{AdvertID: wbCampaignID, Name: name}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal rename campaign request: %w", err)
	}
	_, _, err = c.doRequest(ctx, "POST", "/adv/v0/rename", token, bytes.NewReader(body))
	return err
}

// SetClusterBids updates bids for search query clusters (normquery).
type ClusterBidRequest struct {
	Bids []ClusterBidItem `json:"bids"`
}

type ClusterBidItem struct {
	AdvertID  int64  `json:"advert_id,omitempty"`
	NMID      int64  `json:"nm_id,omitempty"`
	NormQuery string `json:"norm_query"`
	Bid       int    `json:"bid"`
}

type ClusterMinusRequest struct {
	Items []ClusterMinusItem `json:"items"`
}

type ClusterMinusListRequest struct {
	Items []ClusterMinusListRequestItem `json:"items"`
}

type ClusterMinusListRequestItem struct {
	AdvertID int64 `json:"advert_id"`
	NMID     int64 `json:"nm_id"`
}

type ClusterMinusListResponse struct {
	Items []ClusterMinusListItem `json:"items"`
}

type ClusterMinusListItem struct {
	AdvertID    int64    `json:"advert_id"`
	NMID        int64    `json:"nm_id"`
	NormQueries []string `json:"norm_queries"`
}

type ClusterMinusItem struct {
	AdvertID  int64  `json:"advert_id"`
	NMID      int64  `json:"nm_id,omitempty"`
	NormQuery string `json:"norm_query"`
}

// SetClusterBids sets bids for specific search clusters.
func (c *Client) SetClusterBids(ctx context.Context, token string, wbCampaignID int64, clusters []ClusterBidItem) error {
	bids := make([]ClusterBidItem, 0, len(clusters))
	for _, cluster := range clusters {
		cluster.AdvertID = wbCampaignID
		bids = append(bids, cluster)
	}
	payload := ClusterBidRequest{
		Bids: bids,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal cluster bid request: %w", err)
	}

	_, _, err = c.doRequest(ctx, "POST", "/adv/v0/normquery/bids", token, bytes.NewReader(body))
	return err
}

// DeleteClusterBids removes explicit bids for search query clusters.
func (c *Client) DeleteClusterBids(ctx context.Context, token string, wbCampaignID int64, clusters []ClusterBidItem) error {
	bids := make([]ClusterBidItem, 0, len(clusters))
	for _, cluster := range clusters {
		cluster.AdvertID = wbCampaignID
		if cluster.NMID <= 0 || cluster.NormQuery == "" || cluster.Bid <= 0 {
			continue
		}
		bids = append(bids, cluster)
	}
	if len(bids) == 0 {
		return errors.New("no normquery cluster bids to delete")
	}
	payload := ClusterBidRequest{
		Bids: bids,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal cluster bid delete request: %w", err)
	}

	_, _, err = c.doRequest(ctx, "DELETE", "/adv/v0/normquery/bids", token, bytes.NewReader(body))
	return err
}

// GetClusterMinus fetches real WB minus phrases for a campaign product.
func (c *Client) GetClusterMinus(ctx context.Context, token string, wbCampaignID int64, nmID int64) ([]string, error) {
	if wbCampaignID <= 0 || nmID <= 0 {
		return nil, errors.New("advert_id and nm_id are required")
	}
	payload := ClusterMinusListRequest{
		Items: []ClusterMinusListRequestItem{{
			AdvertID: wbCampaignID,
			NMID:     nmID,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal cluster minus list request: %w", err)
	}

	_, responseBody, err := c.doRequest(ctx, "POST", "/adv/v0/normquery/get-minus", token, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var response ClusterMinusListResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("unmarshal cluster minus list response: %w", err)
	}
	for _, item := range response.Items {
		if item.AdvertID == wbCampaignID && item.NMID == nmID {
			return item.NormQueries, nil
		}
	}
	return []string{}, nil
}

// SetClusterMinus excludes search query clusters in WB for manual CPM campaigns.
// WB API endpoint: POST /adv/v0/normquery/set-minus
func (c *Client) SetClusterMinus(ctx context.Context, token string, wbCampaignID int64, clusters []ClusterMinusItem) error {
	items := make([]ClusterMinusItem, 0, len(clusters))
	for _, cluster := range clusters {
		cluster.AdvertID = wbCampaignID
		if cluster.NormQuery == "" {
			continue
		}
		items = append(items, cluster)
	}
	if len(items) == 0 {
		return errors.New("no normquery clusters to minus")
	}
	payload := ClusterMinusRequest{Items: items}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal cluster minus request: %w", err)
	}

	_, _, err = c.doRequest(ctx, "POST", "/adv/v0/normquery/set-minus", token, bytes.NewReader(body))
	return err
}
