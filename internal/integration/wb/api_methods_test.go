package wb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingRoundTripper func(*http.Request) (*http.Response, error)

func (f failingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestListCampaigns_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/advert/v2/adverts", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "-1,4,7,8,9,11", r.URL.Query().Get("statuses"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"adverts":[{"advertId":123,"name":"Promo Test","type":8,"status":9,"dailyBudget":5000,"bid_type":"manual","paymentType":"cpm","restrictions":{"can_change_nms":true}},{"id":456,"type":9,"status":11,"bid_type":"manual","settings":{"name":"Auction Test","payment_type":"cpm"},"restrictions":{"can_change_nms":false}},{"id":789,"name":"Restriction Unknown","type":9,"status":4,"nm_settings":[{"nm_id":777}]}]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.ListCampaigns(context.Background(), "token")

	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, 123, result[0].AdvertID)
	assert.Equal(t, "Promo Test", result[0].Name)
	assert.Equal(t, 9, result[0].Status)
	assert.Equal(t, 8, result[0].Type)
	assert.Equal(t, 0, result[0].BidType)
	assert.Equal(t, "cpm", result[0].PaymentType)
	require.NotNil(t, result[0].CanChangeNMs)
	assert.True(t, *result[0].CanChangeNMs)
	assert.Equal(t, 456, result[1].AdvertID)
	assert.Equal(t, "Auction Test", result[1].Name)
	assert.Equal(t, 11, result[1].Status)
	assert.Equal(t, 9, result[1].Type)
	require.NotNil(t, result[1].CanChangeNMs)
	assert.False(t, *result[1].CanChangeNMs)
	assert.Nil(t, result[2].CanChangeNMs)
}

func TestMergeAdvertV2FillsRestrictionFromDetailWithoutOverwritingBase(t *testing.T) {
	fromDetail := true
	baseValue := false

	merged := mergeAdvertV2(
		wbAdvertV2{},
		wbAdvertV2{Restrictions: wbAdvertRestrictions{CanChangeNMs: &fromDetail}},
	)
	require.NotNil(t, merged.Restrictions.CanChangeNMs)
	assert.True(t, *merged.Restrictions.CanChangeNMs)

	merged = mergeAdvertV2(
		wbAdvertV2{Restrictions: wbAdvertRestrictions{CanChangeNMs: &baseValue}},
		wbAdvertV2{Restrictions: wbAdvertRestrictions{CanChangeNMs: &fromDetail}},
	)
	require.NotNil(t, merged.Restrictions.CanChangeNMs)
	assert.False(t, *merged.Restrictions.CanChangeNMs)
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

func TestCreateCampaign_UsesCurrentEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v2/seacat/save-ad", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "token", r.Header.Get("Authorization"))
		var payload CreateCampaignRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, "Growth Search", payload.Name)
		assert.Equal(t, []int64{146168367, 200425104}, payload.NMIDs)
		assert.Equal(t, "manual", payload.BidType)
		assert.Equal(t, "cpm", payload.PaymentType)
		assert.Equal(t, []string{"search", "recommendations"}, payload.PlacementTypes)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`1234567`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	wbCampaignID, err := client.CreateCampaign(context.Background(), "token", CreateCampaignRequest{
		Name:           "Growth Search",
		NMIDs:          []int64{146168367, 200425104},
		BidType:        "manual",
		PaymentType:    "cpm",
		PlacementTypes: []string{"search", "recommendations"},
	})

	require.NoError(t, err)
	assert.Equal(t, int64(1234567), wbCampaignID)
}

func TestGetMinimumBids_UsesCurrentPostContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/advert/v1/bids/min", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Empty(t, r.URL.RawQuery)
		var payload MinimumBidsRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, int64(98765432), payload.AdvertID)
		assert.Equal(t, []int64{12345678, 87654321}, payload.NMIDs)
		assert.Equal(t, "cpm", payload.PaymentType)
		assert.Equal(t, []string{"search", "recommendation"}, payload.PlacementTypes)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"bids":[{"nm_id":12345678,"bids":[{"type":"search","value":250},{"type":"recommendation","value":300}]}]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	bids, err := client.GetMinimumBids(context.Background(), "token", MinimumBidsRequest{
		AdvertID:       98765432,
		NMIDs:          []int64{12345678, 87654321},
		PaymentType:    "cpm",
		PlacementTypes: []string{"search", "recommendation"},
	})

	require.NoError(t, err)
	require.Len(t, bids, 2)
	assert.Equal(t, WBMinimumBidDTO{NmID: 12345678, Placement: "search", MinBid: 250}, bids[0])
	assert.Equal(t, WBMinimumBidDTO{NmID: 12345678, Placement: "recommendation", MinBid: 300}, bids[1])
}

func TestSetClusterMinus_UsesCurrentRequestBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v0/normquery/set-minus", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		var payload ClusterMinusRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, int64(1825035), payload.AdvertID)
		assert.Equal(t, int64(983512347), payload.NMID)
		assert.Equal(t, []string{"Фраза 1"}, payload.NormQueries)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.SetClusterMinus(context.Background(), "token", 1825035, []ClusterMinusItem{{
		NMID:      983512347,
		NormQuery: "Фраза 1",
	}})
	require.NoError(t, err)
}

func TestUpdateCampaignBid_UsesPlacementContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/advert/v1/bids", r.URL.Path)
		assert.Equal(t, http.MethodPatch, r.Method)
		var payload UpdateCampaignBidsRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Len(t, payload.Bids, 1)
		require.Len(t, payload.Bids[0].NMBids, 1)
		assert.Equal(t, "recommendations", payload.Bids[0].NMBids[0].Placement)
		assert.Equal(t, 250, payload.Bids[0].NMBids[0].BidKopecks)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"bids":[]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.UpdateCampaignBid(context.Background(), "token", 12345, 9, 13335157, "recommendations", 250)
	require.NoError(t, err)
}

func TestUpdateCampaignBid_RejectsUnknownPlacementBeforeHTTP(t *testing.T) {
	client := newTestClient("http://127.0.0.1:1")
	err := client.UpdateCampaignBid(context.Background(), "token", 12345, 9, 13335157, "recommendation", 250)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported WB bid placement")
}

func TestUpdateCampaignBid_Classifies5xxAsOutcomeUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporary WB failure", http.StatusBadGateway)
	}))
	defer server.Close()

	err := newTestClient(server.URL).UpdateCampaignBid(context.Background(), "token", 12345, 9, 13335157, "search", 250)
	require.Error(t, err)
	assert.True(t, CampaignBidUpdateOutcomeUnknown(err))
}

func TestUpdateCampaignBid_Classifies4xxAsDefiniteFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid bid", http.StatusBadRequest)
	}))
	defer server.Close()

	err := newTestClient(server.URL).UpdateCampaignBid(context.Background(), "token", 12345, 9, 13335157, "search", 250)
	require.Error(t, err)
	assert.False(t, CampaignBidUpdateOutcomeUnknown(err))
}

func TestUpdateCampaignBid_ClassifiesTransportFailureAsOutcomeUnknown(t *testing.T) {
	client := newTestClient("https://example.invalid")
	client.httpClient.Transport = failingRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset after write")
	})

	err := client.UpdateCampaignBid(context.Background(), "token", 12345, 9, 13335157, "search", 250)
	require.Error(t, err)
	assert.True(t, CampaignBidUpdateOutcomeUnknown(err))
}

func TestGetCommissionTariffs_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/tariffs/commission", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "ru", r.URL.Query().Get("locale"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"report":[{"parentID":657,"parentName":"Бытовая техника","subjectID":6461,"subjectName":"Оборудование зуботехническое","kgvpMarketplace":15.5,"kgvpSupplier":12.5,"kgvpPickup":14.5,"kgvpBooking":14.5,"kgvpSupplierExpress":3,"paidStorageKgvp":15.5}]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetCommissionTariffs(context.Background(), "token", "")

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(6461), result[0].SubjectID)
	assert.Equal(t, "Оборудование зуботехническое", result[0].SubjectName)
	assert.Equal(t, 15.5, result[0].KGVPMarketplace)
}

func TestGetCampaignStats_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v3/fullstats", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "1", r.URL.Query().Get("ids"))
		assert.Equal(t, "2025-01-01", r.URL.Query().Get("beginDate"))
		assert.Equal(t, "2025-01-31", r.URL.Query().Get("endDate"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"advertId":1,"days":[{"date":"2025-01-01","views":100,"clicks":10,"sum":50.5,"orders":4,"shks":7,"sum_price":1250.4}]}]`))
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
}

func TestGetCampaignStats_ExtractsAndAggregatesAppProductStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v3/fullstats", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"advertId":1,"days":[{"date":"2025-01-01","views":100,"clicks":10,"sum":50.5,"apps":[{"nms":[{"nmId":111,"name":"Петля","views":10,"clicks":2,"sum":5.5,"orders":1,"sum_price":100}]},{"nms":[{"nmId":111,"name":"Петля","views":20,"clicks":3,"sum":7.5,"orders":2,"sum_price":200},{"nmId":222,"name":"Направляющая","views":5,"clicks":1,"sum":2.0}]}]}]}]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetCampaignStats(context.Background(), "token", []int{1}, "2025-01-01", "2025-01-31")

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0].Products, 2)
	assert.Equal(t, int64(111), result[0].Products[0].NmID)
	assert.Equal(t, int64(30), result[0].Products[0].Views)
	assert.Equal(t, int64(5), result[0].Products[0].Clicks)
	assert.Equal(t, 13.0, result[0].Products[0].Sum)
	require.NotNil(t, result[0].Products[0].Orders)
	assert.Equal(t, int64(3), *result[0].Products[0].Orders)
	require.NotNil(t, result[0].Products[0].Revenue)
	assert.Equal(t, 300.0, *result[0].Products[0].Revenue)
	assert.Equal(t, int64(222), result[0].Products[1].NmID)
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

func TestGetCampaignStats_ChunksCampaignIDsByFullstatsLimit(t *testing.T) {
	var batches [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v3/fullstats", r.URL.Path)
		batches = append(batches, strings.Split(r.URL.Query().Get("ids"), ","))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	campaignIDs := make([]int, 88)
	for i := range campaignIDs {
		campaignIDs[i] = i + 1
	}
	_, err := client.GetCampaignStats(context.Background(), "token", campaignIDs, "2025-01-01", "2025-01-31")

	require.NoError(t, err)
	require.Len(t, batches, 2)
	assert.Len(t, batches[0], 50)
	assert.Len(t, batches[1], 38)
}

func TestListSearchClusters_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/advert/v2/adverts":
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"adverts":[{"advertId":42,"type":9,"status":9,"settings":{"name":"Auction Test","payment_type":"cpm"},"nm_settings":[{"nm_id":111}]}]}`))
		case "/adv/v1/normquery/stats":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[{"advertId":42,"nmId":111,"dailyStats":[{"date":"2025-05-06","stat":{"normQuery":"shoes","views":500,"clicks":25,"cpc":120}}]}]}`))
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
	assert.Nil(t, result[0].ClusterID)
	assert.Equal(t, "shoes", result[0].NormQuery)
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
		case "/adv/v1/normquery/stats":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[{"advertId":42,"nmId":111,"dailyStats":[{"date":"2025-05-06","stat":{"normQuery":"shoes","views":200,"clicks":20,"spend":30}}]}]}`))
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

func TestGetSearchClusterStats_ParsesSnakeCaseDailyStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/advert/v2/adverts":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"adverts":[{"advertId":42,"type":9,"status":9,"settings":{"name":"Auction Test","payment_type":"cpc"},"nm_settings":[{"nm_id":111}]}]}`))
		case "/adv/v1/normquery/stats":
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[{"advert_id":42,"nm_id":111,"daily_stats":[{"date":"2025-05-06T00:00:00+03:00","app_type_stats":[{"app_type":1,"stats":[{"norm_query":"pouchman backpack","views":200,"clicks":20,"orders":3,"spend":30,"avg_pos":7.1}]}]}]}]}`))
		case "/adv/v0/normquery/get-bids":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"bids":[{"advert_id":42,"nm_id":111,"bid":150,"norm_query":"pouchman backpack"}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSearchClusterStats(context.Background(), "token", 42)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "pouchman backpack", result[0].NormQuery)
	assert.Equal(t, int64(200), result[0].Views)
	assert.Equal(t, int64(20), result[0].Clicks)
	assert.Equal(t, 30.0, result[0].Sum)
}

func TestGetSearchClusterStatsWithNMIDs_ChunksNormQueryStatsItems(t *testing.T) {
	var batches []normQueryStatsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/adv/v1/normquery/stats":
			assert.Equal(t, http.MethodPost, r.Method)
			var req normQueryStatsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			batches = append(batches, req)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[]}`))
		case "/adv/v0/normquery/get-bids":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"bids":[]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	nmIDs := make([]int64, 101)
	for i := range nmIDs {
		nmIDs[i] = int64(1000 + i)
	}
	_, err := client.GetSearchClusterStatsWithNMIDs(context.Background(), "token", 42, nmIDs)

	require.NoError(t, err)
	require.Len(t, batches, 2)
	require.Len(t, batches[0].Items, 100)
	require.Len(t, batches[1].Items, 1)
	assert.Equal(t, int64(42), batches[0].Items[0].AdvertID)
	assert.Equal(t, int64(1000), batches[0].Items[0].NMID)
	assert.Equal(t, int64(1100), batches[1].Items[0].NMID)
}

func TestGetRecommendedBids_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/advert/v0/bids/recommendations", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "1", r.URL.Query().Get("advertId"))
		assert.Equal(t, "111", r.URL.Query().Get("nmId"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"advertId":1,"nmId":111,"base":{"competitiveBid":{"bidKopecks":50000},"leadersBid":{"bidKopecks":80000}}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetRecommendedBids(context.Background(), "token", 1, []int{111})

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(50000), result[0].CompetitiveBid)
	assert.Equal(t, int64(80000), result[0].LeadershipBid)
}

func TestSetClusterBids_UsesCurrentEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v0/normquery/bids", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.JSONEq(t, `{"bids":[{"advert_id":42,"nm_id":111,"norm_query":"shoes","bid":150}]}`, readRequestBody(t, r))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.SetClusterBids(context.Background(), "token", 42, []ClusterBidItem{
		{NMID: 111, NormQuery: "shoes", Bid: 150},
	})

	require.NoError(t, err)
}

func TestDeleteClusterBids_UsesCurrentEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v0/normquery/bids", r.URL.Path)
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.JSONEq(t, `{"bids":[{"advert_id":42,"nm_id":111,"norm_query":"shoes","bid":150}]}`, readRequestBody(t, r))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.DeleteClusterBids(context.Background(), "token", 42, []ClusterBidItem{
		{NMID: 111, NormQuery: "shoes", Bid: 150},
	})

	require.NoError(t, err)
}

func TestGetClusterMinus_UsesCurrentEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v0/normquery/get-minus", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.JSONEq(t, `{"items":[{"advert_id":42,"nm_id":111}]}`, readRequestBody(t, r))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items":[{"advert_id":42,"nm_id":111,"norm_queries":["cheap shoes","used shoes"]}]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetClusterMinus(context.Background(), "token", 42, 111)

	require.NoError(t, err)
	assert.Equal(t, []string{"cheap shoes", "used shoes"}, result)
}

func TestUpdateCampaignBid_UsesCurrentEndpointWithExplicitNMID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/advert/v1/bids", r.URL.Path)
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.JSONEq(t, `{"bids":[{"advert_id":42,"nm_bids":[{"nm_id":111,"bid_kopecks":250,"placement":"search"}]}]}`, readRequestBody(t, r))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.UpdateCampaignBid(context.Background(), "token", 42, 9, 111, "search", 250)

	require.NoError(t, err)
}

func TestUpdateCampaignBid_ResolvesCampaignNMIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/advert/v2/adverts":
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"adverts":[{"advertId":42,"type":9,"status":9,"settings":{"name":"Auction Test","payment_type":"cpm"},"nm_settings":[{"nm_id":111},{"nm_id":222}]}]}`))
		case "/api/advert/v1/bids":
			assert.Equal(t, http.MethodPatch, r.Method)
			assert.JSONEq(t, `{"bids":[{"advert_id":42,"nm_bids":[{"nm_id":111,"bid_kopecks":250,"placement":"recommendations"},{"nm_id":222,"bid_kopecks":250,"placement":"recommendations"}]}]}`, readRequestBody(t, r))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.UpdateCampaignBid(context.Background(), "token", 42, 9, 0, "recommendations", 250)

	require.NoError(t, err)
}

func TestRenameCampaign_UsesCurrentEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v0/rename", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.JSONEq(t, `{"advertId":42,"name":"Growth Search"}`, readRequestBody(t, r))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.RenameCampaign(context.Background(), "token", 42, "Growth Search")

	require.NoError(t, err)
}

func TestDeleteCampaign_UsesCurrentEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/adv/v0/delete", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "42", r.URL.Query().Get("id"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.DeleteCampaign(context.Background(), "token", 42)

	require.NoError(t, err)
}

func readRequestBody(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	return string(body)
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

func TestGetSalesFunnelProductsV3_MapsOpenCartAndOrderCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/analytics/v3/sales-funnel/products", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"products":[{"product":{"nmId":268913787},"statistic":{"selected":{"openCount":45,"cartCount":34,"orderCount":19}}}]}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	result, err := client.GetSalesFunnelProductsV3(context.Background(), "token", SalesFunnelParams{
		DateFrom: "2026-05-21",
		DateTo:   "2026-05-28",
		NmIDs:    []int64{268913787},
	})

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, int64(268913787), result[0].NmID)
	assert.Equal(t, int64(45), result[0].OpenCount)
	assert.Equal(t, int64(34), result[0].CartCount)
	assert.Equal(t, int64(19), result[0].OrderCount)
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
