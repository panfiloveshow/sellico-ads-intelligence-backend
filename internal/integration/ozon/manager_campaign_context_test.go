package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOzonManagerContextReadsExactBudgetThroughCampaignPagination(t *testing.T) {
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "test-only", 1800)
			return
		}
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/api/client/campaign", r.URL.Path)
		reads.Add(1)
		rows := []map[string]any{}
		if r.URL.Query().Get("page") == "1" {
			for i := 0; i < perfPageSize; i++ {
				rows = append(rows, map[string]any{"id": 1000 + i, "state": "CAMPAIGN_STATE_RUNNING"})
			}
		} else {
			require.Equal(t, "2", r.URL.Query().Get("page"))
			rows = append(rows, map[string]any{"id": "101", "state": "CAMPAIGN_STATE_INACTIVE", "title": "Native Ozon", "dailyBudget": "0", "weeklyBudget": "100000001"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"list": rows})
	}))
	defer srv.Close()
	row, err := newTestPerfClient(srv.URL).ReadCampaignContext(context.Background(), testCreds, 101)
	require.NoError(t, err)
	require.Equal(t, "CAMPAIGN_STATE_INACTIVE", row.Status)
	require.Equal(t, "100.000001", *row.WeeklyBudgetRub)
	require.Equal(t, "0", *row.DailyBudgetRub)
	require.Nil(t, row.CampaignType)
	require.Equal(t, int32(2), reads.Load())
}

func TestOzonManagerContextDoesNotInventAmountsOrCampaigns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "test-only", 1800)
			return
		}
		_, _ = w.Write([]byte(`{"list":[{"id":"101","state":"CAMPAIGN_STATE_RUNNING","weeklyBudget":"bad"}]}`))
	}))
	defer srv.Close()
	c := newTestPerfClient(srv.URL)
	row, err := c.ReadCampaignContext(context.Background(), testCreds, 101)
	require.NoError(t, err)
	require.Nil(t, row.DailyBudgetRub)
	require.Nil(t, row.WeeklyBudgetRub)
	row, err = c.ReadCampaignContext(context.Background(), testCreds, 102)
	require.Error(t, err)
	require.Nil(t, row)
	for _, invalid := range []string{"", "-1", "1.1", "not-amount", "9223372036854775808"} {
		require.Nil(t, nativeMicroBudget(invalid))
	}
}

func TestOzonManagerContextRejectsDuplicateAndIncompleteState(t *testing.T) {
	for _, body := range []string{`{"list":[{"id":101}]}`, `{"list":[{"id":101,"state":"one"},{"id":101,"state":"two"}]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/client/token" {
				writeToken(w, "test-only", 1800)
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		row, err := newTestPerfClient(srv.URL).ReadCampaignContext(context.Background(), testCreds, 101)
		srv.Close()
		require.Error(t, err)
		require.Nil(t, row)
	}
}

func TestOzonManagerContextBoundsARepeatingFullInventory(t *testing.T) {
	var reads atomic.Int32
	rows := make([]map[string]any, perfPageSize)
	for i := range rows {
		rows[i] = map[string]any{"id": 1000 + i, "state": "CAMPAIGN_STATE_RUNNING"}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "test-only", 1800)
			return
		}
		reads.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"list": rows})
	}))
	defer srv.Close()
	row, err := newTestPerfClient(srv.URL).ReadCampaignContext(context.Background(), testCreds, 101)
	require.Error(t, err)
	require.Nil(t, row)
	require.Equal(t, int32(100), reads.Load())
}
