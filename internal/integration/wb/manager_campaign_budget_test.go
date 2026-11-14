package wb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func budgetTestToken(expiry string) string {
	return "test." + base64.RawURLEncoding.EncodeToString([]byte(`{"sid":"budget-test-seller","exp":`+expiry+`}`)) + ".test"
}

func TestManagerCampaignBudgetWBQuotaSurvivesTokenRotationAndConcurrentFirstUse(t *testing.T) {
	c := newTestClient("https://wb.test")
	values := make(chan *rate.Limiter, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			values <- c.managerBudgetLimiter("read", budgetTestToken(strconv.Itoa(i+100)))
		}(i)
	}
	wg.Wait()
	close(values)
	var first *rate.Limiter
	for value := range values {
		if first == nil {
			first = value
		}
		require.Same(t, first, value)
	}
	now := time.Now()
	require.True(t, first.AllowN(now, 5))
	require.False(t, c.managerBudgetLimiter("read", budgetTestToken("999")).AllowN(now, 1))
	require.True(t, c.managerBudgetLimiter("write", budgetTestToken("100")).AllowN(now, 5))
	require.True(t, c.managerBudgetLimiter("config", budgetTestToken("100")).AllowN(now, 10))
	require.False(t, c.managerBudgetLimiter("config", budgetTestToken("200")).AllowN(now, 1))
}

func TestManagerCampaignBudgetWBReadsCPCDailyKopecksAndExplicitFlags(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		switch r.URL.Path {
		case "/api/advert/v2/adverts":
			require.Equal(t, "101", r.URL.Query().Get("ids"))
			_, _ = w.Write([]byte(`{"adverts":[{"id":101,"status":9,"settings":{"name":"Native CPC","payment_type":"cpc"}}]}`))
		case "/api/advert/v0/daily-limits":
			require.Equal(t, "101", r.URL.Query().Get("advertIds"))
			_, _ = w.Write([]byte(`{"adverts":[{"advertId":101,"enabled":true,"dailyLimit":100001,"spentToday":35001,"currency":"RUB","carryOverEnabled":false,"valid":true,"requiredLimit":3001}]}`))
		case "/api/advert/v1/config":
			_, _ = w.Write([]byte(`{"currency":"RUB","currencyCode":643,"minDailyLimit":100000}`))
		default:
			t.Errorf("unexpected native budget path %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	ctx, err := newTestClient(srv.URL).ReadManagerCampaignBudget(context.Background(), budgetTestToken("100"), 101, "daily")
	require.NoError(t, err)
	require.Equal(t, "1000.01", *ctx.Budget.AmountRub)
	require.Equal(t, "350.01", *ctx.Budget.SpentTodayRub)
	require.Equal(t, "1000", *ctx.Budget.MinimumAmountRub)
	require.Equal(t, "30.01", *ctx.Budget.RecommendedMinimumRub)
	require.True(t, ctx.Budget.Enabled)
	require.False(t, *ctx.Budget.CarryOverEnabled)
	require.True(t, ctx.Budget.PeriodConfirmed)
	require.Nil(t, ctx.Budget.MaximumAmountRub)
	require.Equal(t, int32(0), writes.Load())
}

func TestManagerCampaignBudgetWBRejectsMissingForeignAndNonRUBEvidence(t *testing.T) {
	for _, bad := range []string{"missing_enabled", "missing_carry", "missing_amount", "missing_spend", "missing_valid", "missing_recommendation", "duplicate", "foreign", "currency", "config_currency", "missing_minimum", "cpm"} {
		t.Run(bad, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/advert/v2/adverts":
					payment := "cpc"
					if bad == "cpm" {
						payment = "cpm"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"adverts": []any{map[string]any{"id": 101, "status": 9, "settings": map[string]any{"payment_type": payment}}}})
				case "/api/advert/v1/config":
					cfg := map[string]any{"currency": "RUB", "currencyCode": 643, "minDailyLimit": 100000}
					if bad == "config_currency" {
						cfg["currency"] = "UZS"
					}
					if bad == "missing_minimum" {
						delete(cfg, "minDailyLimit")
					}
					_ = json.NewEncoder(w).Encode(cfg)
				case "/api/advert/v0/daily-limits":
					row := map[string]any{"advertId": 101, "enabled": false, "dailyLimit": 0, "spentToday": 0, "currency": "RUB", "carryOverEnabled": false, "valid": false, "requiredLimit": 0}
					keys := map[string]string{"missing_enabled": "enabled", "missing_carry": "carryOverEnabled", "missing_amount": "dailyLimit", "missing_spend": "spentToday", "missing_valid": "valid", "missing_recommendation": "requiredLimit"}
					if key := keys[bad]; key != "" {
						delete(row, key)
					}
					if bad == "foreign" {
						row["advertId"] = 102
					}
					if bad == "currency" {
						row["currency"] = "UZS"
					}
					rows := []any{row}
					if bad == "duplicate" {
						rows = append(rows, row)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"adverts": rows})
				default:
					w.WriteHeader(400)
				}
			}))
			defer srv.Close()
			_, err := newTestClient(srv.URL).ReadManagerCampaignBudget(context.Background(), budgetTestToken("100"), 101, "daily")
			require.Error(t, err, bad)
		})
	}
}

func TestManagerCampaignBudgetWBExactEnableAndDisableBodiesAreScoped(t *testing.T) {
	c := newTestClient("https://wb.test")
	amount, carry := "1000.01", false
	r, err := c.AdvertisingBudgetRequest(budgetTestToken("100"), 101, "daily", true, &amount, &carry)
	require.NoError(t, err)
	require.Equal(t, []string{"101"}, r.Resources)
	body, err := exactManagerCampaignBudgetBody(101, "daily", true, &amount, &carry)
	require.NoError(t, err)
	require.JSONEq(t, `{"advertIds":[101],"enabled":true,"dailyLimit":100001,"carryOverEnabled":false}`, string(body))
	expected, err := adguard.NewWBRequest("PUT", "https://wb.test/api/advert/v0/daily-limits", budgetTestToken("100"), body)
	require.NoError(t, err)
	require.Equal(t, expected.Fingerprint, r.Fingerprint)
	disabled, err := c.AdvertisingBudgetRequest(budgetTestToken("200"), 101, "daily", false, nil, nil)
	require.NoError(t, err)
	body, err = exactManagerCampaignBudgetBody(101, "daily", false, nil, nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"advertIds":[101],"enabled":false}`, string(body))
	require.Equal(t, r.AccountKey, disabled.AccountKey)
	require.NotEqual(t, r.Fingerprint, disabled.Fingerprint)
	for _, value := range []string{"0", "-1", "1e3", "1000.001", "01", "999999999999999999"} {
		_, err := c.AdvertisingBudgetRequest(budgetTestToken("100"), 101, "daily", true, &value, &carry)
		require.Error(t, err)
	}
	_, err = c.AdvertisingBudgetRequest(budgetTestToken("100"), 101, "daily", true, &amount, nil)
	require.Error(t, err)
	_, err = c.AdvertisingBudgetRequest(budgetTestToken("100"), 101, "daily", false, &amount, &carry)
	require.Error(t, err)
}

func TestManagerCampaignBudgetWBWriteDoesNotRetryOrRedirect(t *testing.T) {
	for _, status := range []int{302, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls, destinationCalls atomic.Int32
			dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1); w.WriteHeader(200) }))
			defer dst.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", dst.URL)
				w.WriteHeader(status)
			}))
			defer srv.Close()
			amount, carry := "1000.01", false
			err := newTestClient(srv.URL).SetExactCampaignBudget(context.Background(), budgetTestToken("100"), 101, "daily", true, &amount, &carry)
			require.Error(t, err)
			require.Equal(t, int32(1), calls.Load())
			require.Equal(t, int32(0), destinationCalls.Load())
		})
	}
}
