package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWBManagerContextIsOneTargetedReadWithExplicitUnknowns(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/api/advert/v2/adverts", r.URL.Path)
		require.Equal(t, "101", r.URL.Query().Get("ids"))
		_, _ = w.Write([]byte(`{"adverts":[{"id":102,"status":4},{"id":101,"status":9,"dailyBudget":0,"settings":{"name":"Native WB"}}]}`))
	}))
	defer srv.Close()
	row, err := newTestClient(srv.URL).ReadCampaignContext(context.Background(), "test-only", 101)
	require.NoError(t, err)
	require.Equal(t, "101", row.ExternalID)
	require.Equal(t, "9", row.Status)
	require.Equal(t, "Native WB", row.Name)
	require.NotNil(t, row.DailyBudgetRub)
	require.Equal(t, "0", *row.DailyBudgetRub)
	require.Nil(t, row.CampaignType)
	require.Nil(t, row.WeeklyBudgetRub)
	require.Nil(t, row.BidType)
	require.Equal(t, int32(1), calls.Load(), "no whole-inventory fallback or bid enrichment")
}

func TestWBManagerContextRejectsMissingOrConflictingIdentityAndStatus(t *testing.T) {
	for _, body := range []string{`{"adverts":[{"id":102,"status":9}]}`, `{"adverts":[{"id":101}]}`, `{"adverts":[{"id":101,"advertId":102,"status":9}]}`, `{"adverts":[{"id":101,"status":9},{"id":101,"status":11}]}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			row, err := newTestClient(srv.URL).ReadCampaignContext(context.Background(), "test-only", 101)
			require.Error(t, err)
			require.Nil(t, row)
		})
	}
}
