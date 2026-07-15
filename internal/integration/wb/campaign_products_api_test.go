package wb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestUpdateCampaignProducts_UsesOfficialContractAndNormalizesNMIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/adv/v0/auction/nms", r.URL.Path)
		assert.Equal(t, "token", r.Header.Get("Authorization"))
		assert.JSONEq(t, `{"nms":[{"advert_id":12345,"nms":{"add":[11111111,44444444],"delete":[55555555]}}]}`, readRequestBody(t, r))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nms":[{"advert_id":12345,"nms":{"added":[11111111,44444444],"deleted":[55555555]}}]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	response, err := client.UpdateCampaignProducts(context.Background(), "token", CampaignProductUpdatesRequest{
		NMs: []CampaignProductUpdate{{
			AdvertID: 12345,
			NMs: CampaignProductNMChanges{
				Add:    []int64{11111111, 11111111, 44444444},
				Delete: []int64{55555555, 55555555},
			},
		}},
	})

	require.NoError(t, err)
	require.Len(t, response.NMs, 1)
	assert.Equal(t, int64(12345), response.NMs[0].AdvertID)
	assert.Equal(t, []int64{11111111, 44444444}, response.NMs[0].NMs.Added)
	assert.Equal(t, []int64{55555555}, response.NMs[0].NMs.Deleted)
}

func TestUpdateCampaignProducts_RejectsInvalidInputBeforeHTTP(t *testing.T) {
	tests := []struct {
		name    string
		request CampaignProductUpdatesRequest
		message string
	}{
		{name: "empty request", request: CampaignProductUpdatesRequest{}, message: "at least one"},
		{name: "invalid campaign", request: singleCampaignProductUpdate(0, []int64{1}, nil), message: "advert_id"},
		{name: "empty change", request: singleCampaignProductUpdate(1, nil, nil), message: "no product changes"},
		{name: "invalid add", request: singleCampaignProductUpdate(1, []int64{-1}, nil), message: "positive"},
		{name: "invalid delete", request: singleCampaignProductUpdate(1, nil, []int64{0}), message: "positive"},
		{name: "conflict", request: singleCampaignProductUpdate(1, []int64{5}, []int64{5}), message: "both added and deleted"},
		{name: "duplicate campaign", request: CampaignProductUpdatesRequest{NMs: []CampaignProductUpdate{
			{AdvertID: 1, NMs: CampaignProductNMChanges{Add: []int64{2}}},
			{AdvertID: 1, NMs: CampaignProductNMChanges{Delete: []int64{3}}},
		}}, message: "duplicate advert_id"},
	}

	client := newTestClient("http://127.0.0.1:1")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.UpdateCampaignProducts(context.Background(), "token", tt.request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestUpdateCampaignProducts_RejectsMoreThanTwentyCampaigns(t *testing.T) {
	request := CampaignProductUpdatesRequest{NMs: make([]CampaignProductUpdate, 21)}
	for i := range request.NMs {
		request.NMs[i] = CampaignProductUpdate{
			AdvertID: int64(i + 1),
			NMs:      CampaignProductNMChanges{Add: []int64{int64(i + 100)}},
		}
	}

	_, err := newTestClient("http://127.0.0.1:1").UpdateCampaignProducts(context.Background(), "token", request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most 20 campaigns")
}

func TestUpdateCampaignProducts_MapsWBError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"title":"invalid payload"}`))
	}))
	defer server.Close()

	_, err := newTestClient(server.URL).UpdateCampaignProducts(context.Background(), "token", singleCampaignProductUpdate(1, []int64{2}, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client error (400)")
	assert.False(t, CampaignProductUpdateOutcomeUnknown(err))
}

func TestUpdateCampaignProducts_RejectsMalformedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	_, err := newTestClient(server.URL).UpdateCampaignProducts(context.Background(), "token", singleCampaignProductUpdate(1, []int64{2}, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal campaign product updates response")
	assert.True(t, CampaignProductUpdateOutcomeUnknown(err))
}

func TestUpdateCampaignProducts_RejectsSuccessResponseWithoutRequestedCampaign(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nms":[{"advert_id":99,"nms":{"added":[2],"deleted":[]}}]}`))
	}))
	defer server.Close()

	_, err := newTestClient(server.URL).UpdateCampaignProducts(context.Background(), "token", singleCampaignProductUpdate(1, []int64{2}, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing advert_id 1")
	assert.True(t, CampaignProductUpdateOutcomeUnknown(err))
}

func TestUpdateCampaignProducts_UsesDedicatedRateLimiter(t *testing.T) {
	client := newTestClient("http://127.0.0.1:1")
	limiter := rate.NewLimiter(rate.Every(time.Hour), 1)
	require.True(t, limiter.Allow()) // consume the only token
	client.campaignProductLimiters.Set("token", limiter)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.UpdateCampaignProducts(ctx, "token", singleCampaignProductUpdate(1, []int64{2}, nil))
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Contains(t, err.Error(), "campaign products rate limiter wait")
}

func singleCampaignProductUpdate(advertID int64, add, remove []int64) CampaignProductUpdatesRequest {
	return CampaignProductUpdatesRequest{NMs: []CampaignProductUpdate{{
		AdvertID: advertID,
		NMs:      CampaignProductNMChanges{Add: add, Delete: remove},
	}}}
}
