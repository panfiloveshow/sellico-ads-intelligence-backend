package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

type wbContentCardsListRequest struct {
	Settings wbContentSettings `json:"settings"`
}

type wbContentSettings struct {
	Cursor wbContentCursor `json:"cursor"`
	Filter wbContentFilter `json:"filter"`
}

type wbContentCursor struct {
	Limit     int    `json:"limit"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	NmID      int64  `json:"nmID,omitempty"`
}

type wbContentFilter struct {
	WithPhoto int `json:"withPhoto"`
}

type wbContentCardsListResponse struct {
	Cards  []wbContentCard    `json:"cards"`
	Cursor wbContentCursorOut `json:"cursor"`
}

type wbContentCursorOut struct {
	UpdatedAt string `json:"updatedAt"`
	NmID      int64  `json:"nmID"`
	Total     int    `json:"total"`
}

type wbContentCard struct {
	NmID       int64    `json:"nmID"`
	VendorCode string   `json:"vendorCode"`
	Title      string   `json:"title"`
	Brand      string   `json:"brand"`
	Object     string   `json:"object"`
	MediaFiles []string `json:"mediaFiles"`
	Sizes      []struct {
		Price int `json:"price"`
	} `json:"sizes"`
}

// ListProducts fetches ALL product cards from the WB Content API with cursor pagination.
// Uses circuit breaker, retry, and rate limiting via doContentRequest.
func (c *Client) ListProducts(ctx context.Context, token string) ([]WBProductDTO, error) {
	var allProducts []WBProductDTO
	cursor := wbContentCursor{Limit: 100}

	const contentPath = "/content/v2/get/cards/list"

	for i := 0; i < 50; i++ { // max 50 pages = 5000 products safety limit
		requestBody, err := json.Marshal(wbContentCardsListRequest{
			Settings: wbContentSettings{
				Cursor: cursor,
				Filter: wbContentFilter{WithPhoto: -1},
			},
		})
		if err != nil {
			return allProducts, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("marshal products request: %v", err))
		}

		resp, body, err := c.doContentRequest(ctx, http.MethodPost, contentPath, token, bytes.NewReader(requestBody))
		if err != nil {
			return allProducts, err
		}

		if resp != nil && resp.StatusCode >= http.StatusBadRequest {
			return allProducts, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("client error (%d) on %s", resp.StatusCode, contentPath))
		}

		var contentResp wbContentCardsListResponse
		if err := json.Unmarshal(body, &contentResp); err != nil {
			return allProducts, apperror.New(apperror.ErrWBAPIError, fmt.Sprintf("unmarshal products: %v", err))
		}

		for _, card := range contentResp.Cards {
			product := WBProductDTO{
				NmID:       card.NmID,
				VendorCode: card.VendorCode,
				Title:      card.Title,
				Brand:      card.Brand,
				Category:   card.Object,
			}
			if len(card.MediaFiles) > 0 {
				product.ImageURL = card.MediaFiles[0]
			}
			if len(card.Sizes) > 0 && card.Sizes[0].Price > 0 {
				price := int64(card.Sizes[0].Price)
				product.Price = &price
			}
			allProducts = append(allProducts, product)
		}

		// Stop if no more pages
		if len(contentResp.Cards) < 100 || contentResp.Cursor.NmID == 0 {
			break
		}

		// Set cursor for next page
		cursor.UpdatedAt = contentResp.Cursor.UpdatedAt
		cursor.NmID = contentResp.Cursor.NmID
	}

	return allProducts, nil
}
