package adguard

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdvertisingReadsAreNotMutationsButLifecycleGETsAre(t *testing.T) {
	for _, path := range []string{"/api/advert/v2/budget", "/api/advert/v1/bids/min", "/adv/v1/promotion/adverts", "/adv/v0/normquery/get-minus", "/adv/v0/normquery/list", "/adv/v1/normquery/stats", "/adv/v0/normquery/get-bids"} {
		require.False(t, IsWBMutation("POST", path), path)
	}
	for _, path := range []string{"/adv/v0/start?id=101", "/adv/v0/pause?id=101", "/adv/v0/stop?id=101", "/adv/v0/delete?id=101"} {
		require.True(t, IsWBMutation("GET", path), path)
	}
	for _, path := range []string{"/api/client/statistics", "/api/client/statistics/phrases", "/api/client/min/sku", "/api/client/campaign/search_promo/v2/products", "/api/client/search_promo/get_cpo_min_bids"} {
		require.False(t, IsOzonMutation("POST", path), path)
	}
	require.True(t, IsWBMutation("POST", "/unknown"))
	require.True(t, IsOzonMutation("GET", "/api/client/campaign/all_sku_promo/set_bid"))
	require.True(t, IsOzonMutation("GET", "/api/client/campaign/all_sku_promo/deactivate"))
	require.True(t, IsOzonMutation("POST", "/unknown"))
	require.False(t, IsOzonMutation("GET", "/api/client/campaign"))
}

func TestRequestIdentityIncludesExactPayloadOriginAndCanonicalSeller(t *testing.T) {
	var first Request
	for i, expiry := range []int{100, 200} {
		p, _ := json.Marshal(map[string]any{"sid": "seller-test-identity", "exp": expiry})
		token := "test." + base64.RawURLEncoding.EncodeToString(p) + ".test"
		r, err := NewWBRequest("PATCH", "https://wb.test/api/advert/v1/bids", token, []byte(`{"bids":[{"advert_id":101,"nm_bids":[{"nm_id":501,"bid_kopecks":1201,"placement":"search"}]}]}`))
		require.NoError(t, err)
		require.NoError(t, r.Validate())
		require.Equal(t, []string{"101"}, r.Resources)
		if i == 0 {
			first = r
		} else {
			require.Equal(t, first.AccountKey, r.AccountKey)
			require.Equal(t, first.Fingerprint, r.Fingerprint)
		}
	}
	changed, err := NewRequest(first.Marketplace, first.AccountKey, first.Method, first.URL, first.Resources, []byte(`{"bids":[]}`))
	require.NoError(t, err)
	require.NotEqual(t, first.Fingerprint, changed.Fingerprint)
	_, err = WBAccountKey("unknown-opaque-token")
	require.Error(t, err)
	a, err := NewOzonRequest("PUT", "https://ozon.test/api/client/campaign/101/products", "performance-test", []byte(`{"bids":[{"sku":"501","bid":"12010000"}]}`))
	require.NoError(t, err)
	require.Equal(t, first.ResourceKeys(), []string{"wb:campaign:101"})
	require.Equal(t, a.ResourceKeys(), []string{"ozon:campaign:101"})
}

func TestUncertaintyAndRejectedBeforeSendAreDifferentOutcomes(t *testing.T) {
	for _, code := range []int{0, 302, 408, 409, 500, 503} {
		err := AfterSendError(errors.New("native request outcome"), code)
		require.True(t, IsUnknown(err))
		require.Equal(t, "unknown", Result(code, nil, err).State)
	}
	for _, code := range []int{400, 401, 403, 404, 422, 429} {
		err := AfterSendError(errors.New("vendor refusal"), code)
		require.False(t, IsUnknown(err))
		require.Equal(t, "rejected", Result(code, nil, err).State)
	}
	err := BeforeSendError(errors.New("another command holds the article"))
	require.True(t, WasNotSent(err))
	require.False(t, IsUnknown(err))
	require.Equal(t, "acknowledged", Result(200, []byte(`{}`), nil).State)
}

func TestWBDailyLimitUsesCampaignResourcesAndStrictIntegerIDs(t *testing.T) {
	p, _ := json.Marshal(map[string]any{"sid": "daily-budget-account"})
	token := "test." + base64.RawURLEncoding.EncodeToString(p) + ".test"
	r, err := NewWBRequest("PUT", "https://wb.test/api/advert/v0/daily-limits", token, []byte(`{"advertIds":[101,102],"enabled":false}`))
	require.NoError(t, err)
	require.Equal(t, []string{"101", "102"}, r.Resources)
	require.Equal(t, []string{"wb:campaign:101", "wb:campaign:102"}, r.ResourceKeys())
	require.False(t, IsWBMutation("GET", "/api/advert/v0/daily-limits?advertIds=101"))
	require.True(t, IsWBMutation("PUT", "/api/advert/v0/daily-limits"))
	for _, ids := range []string{`[]`, `["101"]`, `[1.5]`, `[-1]`, `[0]`, `[9223372036854775808]`, `null`} {
		_, err := NewWBRequest("PUT", "https://wb.test/api/advert/v0/daily-limits", token, []byte(`{"advertIds":`+ids+`,"enabled":false}`))
		require.Error(t, err, ids)
	}
}
