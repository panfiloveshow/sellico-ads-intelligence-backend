package wb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Showcase is the public (tokenless) storefront price for one product, taken
// from card.wb.ru. All *Rub fields are integer rubles.
type Showcase struct {
	Name     string
	BasicRub int64 // base/struck price before the seller discount
	BuyerRub int64 // buyer-facing storefront price after WB discounts
}

// showcaseDest is the RF geo (delivery point); prices are only returned for
// products in stock at this destination.
const showcaseDest = -1257786
const showcaseChunk = 100

type wbShowcaseResponse struct {
	Products []struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Sizes []struct {
			Price struct {
				Basic   int64 `json:"basic"`   // kopecks
				Product int64 `json:"product"` // kopecks
			} `json:"price"`
		} `json:"sizes"`
	} `json:"products"`
}

// ShowcaseByNmIDs fetches storefront prices + СПП for the given nmIDs from the
// public card.wb.ru API. No seller token is required, so this works even for
// cabinets whose token lacks the "Цены и скидки" scope. Errors are soft: a
// failed chunk is skipped, partial results are returned.
func (c *Client) ShowcaseByNmIDs(ctx context.Context, nmIDs []int64) (map[int64]Showcase, error) {
	out := make(map[int64]Showcase, len(nmIDs))
	var chunkErrs []error
	if len(nmIDs) == 0 {
		return out, nil
	}
	for start := 0; start < len(nmIDs); start += showcaseChunk {
		end := start + showcaseChunk
		if end > len(nmIDs) {
			end = len(nmIDs)
		}
		chunk := nmIDs[start:end]
		ids := make([]string, len(chunk))
		for i, nm := range chunk {
			ids[i] = strconv.FormatInt(nm, 10)
		}
		q := url.Values{}
		q.Set("appType", "1")
		q.Set("curr", "rub")
		q.Set("dest", strconv.Itoa(showcaseDest))
		q.Set("nm", strings.Join(ids, ";"))
		endpoint := c.showcaseURL + "/cards/v4/detail?" + q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			c.logger.Warn().Err(err).Msg("showcase: build request failed")
			chunkErrs = append(chunkErrs, fmt.Errorf("build showcase request: %w", err))
			continue
		}
		req.Header.Set("Accept", "*/*")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.logger.Warn().Err(err).Int("count", len(chunk)).Msg("showcase: request failed")
			chunkErrs = append(chunkErrs, fmt.Errorf("request showcase chunk: %w", err))
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			c.logger.Warn().Int("status", resp.StatusCode).Int("count", len(chunk)).Msg("showcase: non-OK response")
			chunkErrs = append(chunkErrs, fmt.Errorf("showcase request returned HTTP %d", resp.StatusCode))
			continue
		}
		var parsed wbShowcaseResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			c.logger.Warn().Err(err).Msg("showcase: unmarshal failed")
			chunkErrs = append(chunkErrs, fmt.Errorf("decode showcase response: %w", err))
			continue
		}
		for _, p := range parsed.Products {
			if p.ID == 0 {
				continue
			}
			// Name + stock are available even for out-of-stock items (no price).
			sc := Showcase{Name: p.Name}
			if len(p.Sizes) > 0 {
				basic := p.Sizes[0].Price.Basic
				buyer := p.Sizes[0].Price.Product
				if basic > 0 && buyer > 0 {
					sc.BasicRub = int64(math.Round(float64(basic) / 100))
					sc.BuyerRub = int64(math.Round(float64(buyer) / 100))
				}
			}
			out[p.ID] = sc
		}
	}
	return out, errors.Join(chunkErrs...)
}

// WBImageURL builds the WB CDN thumbnail URL directly from an nmID (no token).
// ponytail: the basket-host ranges grow as WB adds CDN baskets; extend the
// table if new (higher) nmIDs 404.
func WBImageURL(nmID int64) string {
	vol := nmID / 100000
	part := nmID / 1000
	ranges := []struct {
		hi   int64
		host string
	}{
		{143, "01"}, {287, "02"}, {431, "03"}, {719, "04"}, {1007, "05"}, {1061, "06"},
		{1115, "07"}, {1169, "08"}, {1313, "09"}, {1601, "10"}, {1655, "11"}, {1919, "12"},
		{2045, "13"}, {2189, "14"}, {2405, "15"}, {2621, "16"}, {2837, "17"}, {3119, "18"},
		{3299, "19"}, {3479, "20"}, {3659, "21"}, {3839, "22"}, {4019, "23"}, {4199, "24"},
	}
	host := "25"
	for _, r := range ranges {
		if vol <= r.hi {
			host = r.host
			break
		}
	}
	return fmt.Sprintf("https://basket-%s.wbbasket.ru/vol%d/part%d/%d/images/c246x328/1.webp", host, vol, part, nmID)
}
