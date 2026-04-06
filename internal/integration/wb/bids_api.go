package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// UpdateBidRequest represents a bid update for a campaign.
type UpdateBidRequest struct {
	AdvertID int64 `json:"advertId"`
	Type     int   `json:"type"`
	CPM      int   `json:"cpm"`
	Param    int   `json:"param"`
}

// UpdateAuctionBidRequest represents a bid update for auction campaigns (type 9).
type UpdateAuctionBidRequest struct {
	AdvertID int64             `json:"advertId"`
	Bids     []AuctionBidItem `json:"bids"`
}

// AuctionBidItem represents a single bid for an NM item in auction campaign.
type AuctionBidItem struct {
	NMID int64 `json:"nmId"`
	Bid  int   `json:"bid"`
}

// UpdateCampaignBid sends a bid update to WB API.
// For auction campaigns (type 9), uses PATCH /adv/v0/auction/bids.
// For promotion campaigns, uses PATCH /adv/v0/bids.
func (c *Client) UpdateCampaignBid(ctx context.Context, token string, wbCampaignID int64, campaignType int, nmID int64, placement string, newBid int) error {
	if campaignType == 9 {
		return c.updateAuctionBid(ctx, token, wbCampaignID, nmID, newBid)
	}
	return c.updatePromotionBid(ctx, token, wbCampaignID, campaignType, newBid, placement)
}

func (c *Client) updateAuctionBid(ctx context.Context, token string, wbCampaignID int64, nmID int64, newBid int) error {
	payload := UpdateAuctionBidRequest{
		AdvertID: wbCampaignID,
		Bids: []AuctionBidItem{
			{NMID: nmID, Bid: newBid},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal auction bid request: %w", err)
	}

	_, _, err = c.doRequest(ctx, "PATCH", "/adv/v0/auction/bids", token, bytes.NewReader(body))
	return err
}

func (c *Client) updatePromotionBid(ctx context.Context, token string, wbCampaignID int64, campaignType int, newBid int, placement string) error {
	param := 6 // search
	if placement == "recommendations" {
		param = 8
	}

	payload := UpdateBidRequest{
		AdvertID: wbCampaignID,
		Type:     campaignType,
		CPM:      newBid,
		Param:    param,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal promotion bid request: %w", err)
	}

	_, _, err = c.doRequest(ctx, "PATCH", "/adv/v0/bids", token, bytes.NewReader(body))
	return err
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

// SetClusterBids updates bids for search query clusters (normquery).
type ClusterBidRequest struct {
	AdvertID int64            `json:"id"`
	Clusters []ClusterBidItem `json:"clusters"`
}

type ClusterBidItem struct {
	Query string `json:"query"`
	Bid   int    `json:"bid"`
}

// SetClusterBids sets bids for specific search clusters.
func (c *Client) SetClusterBids(ctx context.Context, token string, wbCampaignID int64, clusters []ClusterBidItem) error {
	payload := ClusterBidRequest{
		AdvertID: wbCampaignID,
		Clusters: clusters,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal cluster bid request: %w", err)
	}

	_, _, err = c.doRequest(ctx, "PATCH", "/adv/v0/normquery/set-bids", token, bytes.NewReader(body))
	return err
}
