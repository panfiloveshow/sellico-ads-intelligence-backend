package wb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPricesTestClient(baseURL string) *Client {
	c := newTestClient(baseURL)
	c.pricesURL = baseURL
	return c
}

func TestListGoodsPrices_ParsesAndPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/list/goods/filter", r.URL.Path)
		assert.Equal(t, "500", r.URL.Query().Get("limit"))
		assert.Equal(t, "1000", r.URL.Query().Get("offset"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"listGoods":[
			{"nmID":111,"vendorCode":"A","currencyIsoCode4217":"RUB","discount":30,"clubDiscount":3,"editableSizePrice":false,"sizes":[{"sizeID":0,"price":1000,"discountedPrice":700}]},
			{"nmID":222,"vendorCode":"B","discount":0,"editableSizePrice":true,"sizes":[{"sizeID":1,"price":500,"discountedPrice":500}]},
			{"nmID":333,"vendorCode":"C","discount":45,"editableSizePrice":false,"sizes":[{"sizeID":2,"price":4535,"discountedPrice":2494.23}]}
		]},"error":false}`))
	}))
	defer server.Close()

	client := newPricesTestClient(server.URL)
	goods, err := client.ListGoodsPrices(context.Background(), "tok", 500, 1000, nil)
	require.NoError(t, err)
	require.Len(t, goods, 3)
	assert.Equal(t, int64(111), goods[0].NmID)
	assert.Equal(t, int64(1000), goods[0].Price)
	assert.Equal(t, int64(700), goods[0].DiscountedPrice)
	assert.Equal(t, 30, goods[0].Discount)
	assert.False(t, goods[0].EditableSizePrice)
	assert.True(t, goods[1].EditableSizePrice)
	// WB returns fractional discountedPrice (e.g. 2494.23) — rounded to rubles.
	assert.Equal(t, int64(2494), goods[2].DiscountedPrice)
	assert.Equal(t, int64(4535), goods[2].Price)
}

func TestListGoodsPrices_ScopeMissing(t *testing.T) {
	withFastWBRetryTiming(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":true,"errorText":"no access"}`))
	}))
	defer server.Close()

	client := newPricesTestClient(server.URL)
	_, err := client.ListGoodsPrices(context.Background(), "tok", 1000, 0, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPricesScopeMissing))
}

func TestUploadPriceTask_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v2/upload/task", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"id":123456,"alreadyExists":false},"error":false}`))
	}))
	defer server.Close()

	client := newPricesTestClient(server.URL)
	id, dup, err := client.UploadPriceTask(context.Background(), "tok", []PriceUpdateItem{{NmID: 111, Price: 1000, Discount: 30}})
	require.NoError(t, err)
	assert.Equal(t, int64(123456), id)
	assert.False(t, dup)
}

func TestUploadPriceTask_Duplicate208(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAlreadyReported)
		w.Write([]byte(`{"data":{"id":999,"alreadyExists":true},"error":false}`))
	}))
	defer server.Close()

	client := newPricesTestClient(server.URL)
	id, dup, err := client.UploadPriceTask(context.Background(), "tok", []PriceUpdateItem{{NmID: 111, Price: 1000, Discount: 30}})
	require.NoError(t, err)
	assert.Equal(t, int64(999), id)
	assert.True(t, dup)
}

func TestUploadPriceTask_BatchTooLarge(t *testing.T) {
	client := newPricesTestClient("http://localhost")
	items := make([]PriceUpdateItem, 1001)
	_, _, err := client.UploadPriceTask(context.Background(), "tok", items)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1000")
}

func TestGetPriceTaskHistory_Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/history/tasks", r.URL.Path)
		assert.Equal(t, "123456", r.URL.Query().Get("taskID"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"id":123456,"status":3},"error":false}`))
	}))
	defer server.Close()

	client := newPricesTestClient(server.URL)
	st, err := client.GetPriceTaskHistory(context.Background(), "tok", 123456)
	require.NoError(t, err)
	assert.Equal(t, 3, st.Status)
}

func TestListQuarantineGoods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/quarantine/goods", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"quarantineGoods":[{"nmID":111,"price":1000,"newPrice":300,"discountedPrice":900,"newDiscountedPrice":250}]},"error":false}`))
	}))
	defer server.Close()

	client := newPricesTestClient(server.URL)
	goods, err := client.ListQuarantineGoods(context.Background(), "tok", 1000, 0)
	require.NoError(t, err)
	require.Len(t, goods, 1)
	assert.Equal(t, int64(111), goods[0].NmID)
	assert.Equal(t, int64(300), goods[0].NewPrice)
}

func TestUploadPriceTask_RateLimited(t *testing.T) {
	withFastWBRetryTiming(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":true,"errorText":"too many requests"}`))
	}))
	defer server.Close()

	client := newPricesTestClient(server.URL)
	_, _, err := client.UploadPriceTask(context.Background(), "tok", []PriceUpdateItem{{NmID: 1, Price: 100}})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limited"))
}
