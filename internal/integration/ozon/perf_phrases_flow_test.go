package ozon

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetPhrasesReport_FullAsyncFlow exercises submit → poll → download →
// parse, including the campaign chunking (>10 campaigns → two reports) and the
// ZIP-of-CSVs download shape.
func TestGetPhrasesReport_FullAsyncFlow(t *testing.T) {
	var submitCount, pollCount int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/client/token":
			writeToken(w, "tok-1", 1800)
		case r.URL.Path == "/api/client/statistics/phrases":
			mu.Lock()
			submitCount++
			id := submitCount
			mu.Unlock()
			var body struct {
				Campaigns []string `json:"campaigns"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.LessOrEqual(t, len(body.Campaigns), phrasesCampaignChunk)
			// Alternate UUID casing to cover both branches.
			if id == 1 {
				w.Write([]byte(`{"UUID":"uuid-1"}`))
			} else {
				w.Write([]byte(`{"uuid":"uuid-2"}`))
			}
		case r.URL.Path == "/api/client/statistics/uuid-1" || r.URL.Path == "/api/client/statistics/uuid-2":
			mu.Lock()
			pollCount++
			n := pollCount
			mu.Unlock()
			// First poll of report 1 returns a non-terminal state once.
			if n == 1 {
				w.Write([]byte(`{"state":"IN_PROGRESS"}`))
				return
			}
			w.Write([]byte(`{"state":"OK"}`))
		case r.URL.Path == "/api/client/statistics/report":
			uuid := r.URL.Query().Get("UUID")
			if uuid == "uuid-1" {
				// ZIP of one CSV.
				var buf bytes.Buffer
				zw := zip.NewWriter(&buf)
				e, _ := zw.Create("c1.csv")
				e.Write([]byte("Поисковый запрос;Показы;Клики;Расход;Заказы\nтермос;300;12;25,00;1\n"))
				zw.Close()
				w.Write(buf.Bytes())
				return
			}
			// JSON for report 2.
			w.Write([]byte(`{"rows":[{"phrase":"кружка","views":5,"clicks":1,"moneySpent":2,"orders":0,"date":"2026-08-02"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	// Shorten the poll interval so the one non-terminal poll doesn't add 5s.
	oldInterval := phrasesPollInterval
	phrasesPollInterval = 10 * time.Millisecond
	defer func() { phrasesPollInterval = oldInterval }()

	c := newTestPerfClient(srv.URL)
	campaigns := make([]int64, phrasesCampaignChunk+1) // → 2 chunks/reports
	for i := range campaigns {
		campaigns[i] = int64(i + 1)
	}
	from := mustDate(2026, 8, 1)
	to := mustDate(2026, 8, 2)
	rows, err := c.GetPhrasesReport(context.Background(), testCreds, campaigns, from, to)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	queries := []string{rows[0].Query, rows[1].Query}
	assert.ElementsMatch(t, []string{"термос", "кружка"}, queries)
	assert.Equal(t, 2, submitCount)
}

func TestGetPhrasesReport_SubmitNoUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.Write([]byte(`{}`)) // no UUID
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	from := mustDate(2026, 8, 1)
	_, err := c.GetPhrasesReport(context.Background(), testCreds, []int64{1}, from, from)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no UUID")
}

func TestWaitStatisticsReport_ErrorState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "tok-1", 1800)
			return
		}
		w.Write([]byte(`{"state":"ERROR","error":"boom"}`))
	}))
	defer srv.Close()

	c := newTestPerfClient(srv.URL)
	err := c.waitStatisticsReport(context.Background(), testCreds, "uuid-x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestGetPhrasesReport_EmptyCampaignsNoRequests(t *testing.T) {
	c := newTestPerfClient("http://unused")
	from := mustDate(2026, 8, 1)
	rows, err := c.GetPhrasesReport(context.Background(), testCreds, nil, from, from)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
