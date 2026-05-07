package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListCampaigns_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/advert/v2/adverts", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "-1,4,7,8,9,11", r.URL.Query().Get("statuses"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"adverts":[{"advertId":123,"name":"Promo Test","type":8,"status":9,"dailyBudget":5000,"bid_type":"manual","paymentType":"cpm"},{"id":456,"type":9,"status":11,"bid_type":"manual","settings":{"name":"Auction Test","payment_type":"cpm"}}]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.ListCampaigns(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, 123, result[0].AdvertID)
	assert.Equal(t, "Promo Test", result[0].Name)
	assert.Equal(t, 9, result[0].Status)
	assert.Equal(t, 8, result[0].Type)
	assert.Equal(t, 0, result[0].BidType)
	assert.Equal(t, "cpm", result[0].PaymentType)
	assert.Equal(t, 456, result[1].AdvertID)
	assert.Equal(t, "Auction Test", result[1].Name)
	assert.Equal(t, 11, result[1].Status)
	assert.Equal(t, 9, result[1].Type)
}

func TestListCampaigns_UnmarshalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.ListCampaigns(context.Background(), "token")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal adverts v2 campaigns")
}

func TestGetCampaignStats_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v3/fullstats", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "1", r.URL.Query().Get("ids"))
		assert.Equal(t, "2025-01-01", r.URL.Query().Get("beginDate"))
		assert.Equal(t, "2025-01-31", r.URL.Query().Get("endDate"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"advertId":1,"days":[{"date":"2025-01-01","views":100,"clicks":10,"sum":50.5,"orders":4,"shks":7,"sum_price":1250.4,"apps":[{"appType":1,"nms":[{"nmId":111,"name":"One","views":40,"clicks":4,"sum":20,"orders":1,"shks":1,"sum_price":300,"atbs":2}]},{"appType":32,"nms":[{"nmId":111,"name":"One","views":60,"clicks":6,"sum":30.5,"orders":3,"shks":6,"sum_price":950.4,"atbs":5}]}]}]}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetCampaignStats(context.Background(), "token", []int{1}, "2025-01-01", "2025-01-31")

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(100), result[0].Views)
	assert.Equal(t, 50.5, result[0].Sum)
	require.NotNil(t, result[0].Orders)
	assert.Equal(t, int64(4), *result[0].Orders)
	require.NotNil(t, result[0].OrderedItems)
	assert.Equal(t, int64(7), *result[0].OrderedItems)
	require.NotNil(t, result[0].Revenue)
	assert.Equal(t, 1250.4, *result[0].Revenue)
	require.Len(t, result[0].Products, 1)
	assert.Equal(t, int64(111), result[0].Products[0].NmID)
	assert.Equal(t, int64(100), result[0].Products[0].Views)
	assert.Equal(t, int64(10), result[0].Products[0].Clicks)
	assert.Equal(t, 50.5, result[0].Products[0].Sum)
	require.NotNil(t, result[0].Products[0].SHKs)
	assert.Equal(t, int64(7), *result[0].Products[0].SHKs)
	require.NotNil(t, result[0].Products[0].SumPrice)
	assert.Equal(t, 1250.4, *result[0].Products[0].SumPrice)
}

func TestGetCampaignStats_SplitsOnClient400(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v3/fullstats", r.URL.Path)
		ids := strings.Split(r.URL.Query().Get("ids"), ",")
		if len(ids) > 1 {
			http.Error(w, `{"error":"too many ids"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"advertId":1,"days":[{"date":"2025-01-01","views":100,"clicks":10,"sum":50.5}]}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	stats, err := client.GetCampaignStats(context.Background(), "token", []int{1, 2}, "2025-01-01", "2025-01-31")

	require.NoError(t, err)
	assert.Len(t, stats, 2)
}

func TestListSearchClusters_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/advert/v2/adverts":
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"adverts":[{"advertId":42,"type":9,"status":9,"settings":{"name":"Auction Test","payment_type":"cpm"},"nm_settings":[{"nm_id":111}]}]}`))
		case "/adv/v0/normquery/stats":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"stats":[{"advert_id":42,"nm_id":111,"stats":[{"norm_query":"shoes","views":500,"clicks":25,"cpc":120}]}]}`))
		case "/adv/v0/normquery/get-bids":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"bids":[{"advert_id":42,"nm_id":111,"bid":150,"norm_query":"shoes"}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.ListSearchClusters(context.Background(), "token", 42)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.NotZero(t, result[0].ClusterID)
	assert.Equal(t, []string{"shoes"}, result[0].Keywords)
	assert.Equal(t, 500, result[0].Count)
	assert.Equal(t, int64(150), result[0].Bid)
}

func TestGetSearchClusterStats_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/advert/v2/adverts":
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"adverts":[{"advertId":42,"type":9,"status":9,"settings":{"name":"Auction Test","payment_type":"cpm"},"nm_settings":[{"nm_id":111}]}]}`))
		case "/adv/v0/normquery/stats":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"stats":[{"advert_id":42,"nm_id":111,"stats":[{"norm_query":"shoes","views":200,"clicks":20,"cpc":150}]}]}`))
		case "/adv/v0/normquery/get-bids":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"bids":[{"advert_id":42,"nm_id":111,"bid":150,"norm_query":"shoes"}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSearchClusterStats(context.Background(), "token", 42)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(200), result[0].Views)
	assert.Equal(t, int64(20), result[0].Clicks)
	assert.Equal(t, 30.0, result[0].Sum)
}

func TestGetRecommendedBids_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/recommended-bids", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"nmId":111,"competitiveBid":500,"leadershipBid":800}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetRecommendedBids(context.Background(), "token", 1, []int{111})

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(500), result[0].CompetitiveBid)
	assert.Equal(t, int64(800), result[0].LeadershipBid)
}

func TestGetCategoryConfig_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/config/categories", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id":10,"name":"Electronics","cpmMin":50}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetCategoryConfig(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Electronics", result[0].Name)
	assert.Equal(t, int64(50), result[0].CPMMin)
}

func TestListProducts_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/content/v2/get/cards/list", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"cards":[{"nmID":999,"vendorCode":"VC1","title":"Widget","brand":"Acme","object":"Electronics","mediaFiles":["https://cdn.example/item.jpg"],"sizes":[{"price":129900}]}]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.ListProducts(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(999), result[0].NmID)
	assert.Equal(t, "Widget", result[0].Title)
	assert.Equal(t, "Electronics", result[0].Category)
	require.NotNil(t, result[0].Price)
	assert.Equal(t, int64(129900), *result[0].Price)
}

func TestGetSalesFunnel_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/analytics/sales-funnel", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"nmId":1,"date":"2025-01-01","views":50,"addToCart":10,"orders":5,"ordersSum":1000.0}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSalesFunnel(context.Background(), "token", SalesFunnelParams{
		DateFrom: "2025-01-01",
		DateTo:   "2025-01-31",
		NmIDs:    []int64{1},
	})

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(5), result[0].Orders)
	assert.Equal(t, 1000.0, result[0].OrdersSum)
}

func TestGetSellerAnalytics_Success(t *testing.T) {
	csvData := "query,medianPosition,frequency,date\nshoes,3.5,1200,2025-01-01\nboots,7.2,800,2025-01-01\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/adv/v2/analytics/seller")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(csvData))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSellerAnalytics(context.Background(), "token", "2025-01-01", "2025-01-31")

	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "shoes", result[0].Query)
	assert.Equal(t, 3.5, result[0].MedianPosition)
	assert.Equal(t, int64(1200), result[0].Frequency)
	assert.Equal(t, "boots", result[1].Query)
}

func TestGetSellerAnalytics_MissingColumn(t *testing.T) {
	csvData := "query,frequency,date\nshoes,1200,2025-01-01\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(csvData))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetSellerAnalytics(context.Background(), "token", "2025-01-01", "2025-01-31")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing CSV column: medianPosition")
}

func TestGetSellerAnalytics_EmptyCSV(t *testing.T) {
	csvData := "query,medianPosition,frequency,date\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(csvData))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSellerAnalytics(context.Background(), "token", "2025-01-01", "2025-01-31")

	require.NoError(t, err)
	assert.Empty(t, result)
}
