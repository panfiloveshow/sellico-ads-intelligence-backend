package wb

import (
	"context"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWBStateGetIsASingleMutation(t *testing.T) {
	for _, name := range []string{"start", "pause", "stop", "delete"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				if calls.Add(1) == 1 {
					w.WriteHeader(503)
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			c := newTestClient(server.URL)
			var err error
			switch name {
			case "start":
				err = c.StartCampaign(context.Background(), "test", 101)
			case "pause":
				err = c.PauseCampaign(context.Background(), "test", 101)
			case "stop":
				err = c.StopCampaign(context.Background(), "test", 101)
			case "delete":
				err = c.DeleteCampaign(context.Background(), "test", 101)
			}
			require.Error(t, err)
			require.Equal(t, int32(1), calls.Load(), "GET does not make a state-changing API call safe to replay")
		})
	}
}

func TestWBMutatingGETCannotBeReplayedByAReusedHTTPConnection(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/warm" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		require.NoError(t, err)
		_ = conn.Close()
	}))
	defer server.Close()
	c := newTestClient(server.URL)
	_, _, err := c.doRequest(context.Background(), "GET", "/warm", "test", nil)
	require.NoError(t, err)
	require.Error(t, c.PauseCampaign(context.Background(), "test", 101))
	require.Equal(t, int32(1), calls.Load())
}

func TestWBApprovedMutationCannotBypassMissingJournal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	err := newTestClient(server.URL).PauseCampaign(adguard.WithApprovedCommand(context.Background(), uuid.New()), "test", 101)
	require.Error(t, err)
	require.True(t, adguard.WasNotSent(err))
	require.Zero(t, calls.Load())
}
