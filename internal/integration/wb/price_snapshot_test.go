package wb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

const wbObservedPriceRow = `{"nmID":101,"vendorCode":"TEST-101","currencyIsoCode4217":"RUB","discount":13,"clubDiscount":0,"editableSizePrice":false,"sizes":[{"sizeID":12,"price":1001,"discountedPrice":870.87,"clubDiscountedPrice":870.87},{"sizeID":11,"price":1001.0,"discountedPrice":"870.8700","clubDiscountedPrice":"870.87"}]}`

func TestWBSelectedPricesKeepKopecksSizesAndUnknownConditions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v2/list/goods/filter", r.URL.Path)
		var request struct {
			NmList []int64 `json:"nmList"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, []int64{101, 102}, request.NmList)
		_, _ = w.Write([]byte(`{"data":{"listGoods":[` + wbObservedPriceRow + `]},"error":false}`))
	}))
	defer srv.Close()
	c := NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop())
	rows, err := c.ReadPriceSnapshots(context.Background(), "test-price-token", []int64{101, 102, 101})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, missing := rows[102]
	require.False(t, missing)
	p := rows[101]
	require.Equal(t, int64(101), p.NmID)
	require.Equal(t, 13, p.DiscountPercent)
	require.NotNil(t, p.ClubDiscountPercent)
	require.Zero(t, *p.ClubDiscountPercent)
	require.NotNil(t, p.EditableSizePrice)
	require.False(t, *p.EditableSizePrice)
	require.Nil(t, p.WholesaleConditions, "absence is unknown, not an invented empty wholesale list")
	require.Len(t, p.Sizes, 2)
	require.Equal(t, int64(11), p.Sizes[0].SizeID)
	require.Equal(t, int64(100100), p.Sizes[0].PriceCents)
	require.Equal(t, int64(87087), p.Sizes[0].DiscountedPriceCents)
}

func TestWBSelectedPricesRejectUnverifiedScopeSchemaAndMoney(t *testing.T) {
	valid := `{"data":{"listGoods":[` + wbObservedPriceRow + `]},"error":false}`
	cases := map[string]string{
		"missing envelope":  `{}`,
		"null rows":         `{"data":{"listGoods":null},"error":false}`,
		"vendor failure":    strings.Replace(valid, `"error":false`, `"error":true`, 1),
		"foreign product":   strings.Replace(valid, `"nmID":101`, `"nmID":999`, 1),
		"duplicate product": `{"data":{"listGoods":[` + wbObservedPriceRow + `,` + wbObservedPriceRow + `]},"error":false}`,
		"foreign currency":  strings.Replace(valid, `"RUB"`, `"USD"`, 1),
		"inexact money":     strings.Replace(valid, `870.87,`, `870.871,`, 1),
		"fractional base":   strings.Replace(valid, `"price":1001,`, `"price":1001.01,`, 1),
		"missing discount":  strings.Replace(valid, `"discount":13,`, ``, 1),
		"duplicate size":    strings.Replace(valid, `"sizeID":12`, `"sizeID":11`, 1),
		"invalid wholesale": strings.Replace(valid, `"editableSizePrice":false,`, `"editableSizePrice":false,"wholesaleDiscountThreshold":{},`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			c := NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop())
			_, err := c.ReadPriceSnapshots(context.Background(), "test-price-token", []int64{101})
			require.Error(t, err)
		})
	}
}

func TestWBSelectedPricesPreserveExplicitEmptyWholesale(t *testing.T) {
	body := strings.Replace(wbObservedPriceRow, `"editableSizePrice":false,`, `"editableSizePrice":false,"wholesaleDiscountThreshold":[],`, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"listGoods":[` + body + `]},"error":false}`))
	}))
	defer srv.Close()
	c := NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop())
	rows, err := c.ReadPriceSnapshots(context.Background(), "test-price-token", []int64{101})
	require.NoError(t, err)
	require.NotNil(t, rows[101].WholesaleConditions)
	require.Empty(t, *rows[101].WholesaleConditions)
}

func TestWBSelectedPricesRejectInvalidInputBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	c := NewClient(&config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000}, zerolog.Nop())
	for _, ids := range [][]int64{nil, {0}, {-1}, make([]int64, 1001)} {
		_, err := c.ReadPriceSnapshots(context.Background(), "test-price-token", ids)
		require.Error(t, err)
	}
	require.Zero(t, calls.Load())
}
