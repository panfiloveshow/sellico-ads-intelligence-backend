package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListCampaigns_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/adverts", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"advertId":123,"name":"Test","status":9,"type":9,"bidType":0,"paymentType":"cpm"}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.ListCampaigns(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, 123, result[0].AdvertID)
	assert.Equal(t, "Test", result[0].Name)
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
	assert.Contains(t, err.Error(), "unmarshal campaigns")
}

func TestGetCampaignStats_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/statistics", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"advertId":1,"date":"2025-01-01","views":100,"clicks":10,"sum":50.5}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetCampaignStats(context.Background(), "token", []int{1}, "2025-01-01", "2025-01-31")

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(100), result[0].Views)
	assert.Equal(t, 50.5, result[0].Sum)
}

func TestListSearchClusters_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/search-clusters/42/bids", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id":1,"keywords":["shoes"],"count":500,"bid":150}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.ListSearchClusters(context.Background(), "token", 42)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].ClusterID)
	assert.Equal(t, []string{"shoes"}, result[0].Keywords)
}

func TestGetSearchClusterStats_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/search-clusters/42/statistics", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id":1,"date":"2025-01-01","views":200,"clicks":20,"sum":30.0}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSearchClusterStats(context.Background(), "token", 42)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(200), result[0].Views)
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
		assert.Equal(t, "/adv/v2/products", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"nmId":999,"vendorCode":"VC1","title":"Widget","brand":"Acme","chrtId":555}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.ListProducts(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(999), result[0].NmID)
	assert.Equal(t, "Widget", result[0].Title)
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
