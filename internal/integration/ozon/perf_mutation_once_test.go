package ozon

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

func TestPerformanceMutationsAreNotRetried(t *testing.T) {
	for _, action := range []struct {
		name string
		send func(*PerfClient) error
	}{
		{"activate", func(c *PerfClient) error { return c.ActivateCampaign(context.Background(), testCreds, 101) }},
		{"deactivate", func(c *PerfClient) error { return c.DeactivateCampaign(context.Background(), testCreds, 101) }},
		{"cpo_activate", func(c *PerfClient) error {
			_, err := c.ActivateAllSKUPromo(context.Background(), testCreds)
			return err
		}},
		{"cpo_rate", func(c *PerfClient) error { return c.SetAllSKUPromoRate(context.Background(), testCreds, 7) }},
		{"cpo_deactivate", func(c *PerfClient) error { return c.DeactivateAllSKUPromo(context.Background(), testCreds) }},
		{"budget", func(c *PerfClient) error {
			v := int64(1000)
			return c.UpdateCampaign(context.Background(), testCreds, 101, CampaignPatch{WeeklyBudgetRub: &v})
		}},
		{"bid", func(c *PerfClient) error {
			return c.SetCampaignProductBids(context.Background(), testCreds, 101, []ProductBid{{SKU: 501, BidRub: 12.01}})
		}},
	} {
		t.Run(action.name, func(t *testing.T) {
			for _, failure := range []string{"uncertain_5xx", "lost_response", "rate_limit", "unauthorized"} {
				t.Run(failure, func(t *testing.T) {
					var writes atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/api/client/token" {
							writeToken(w, "test-token", 1800)
							return
						}
						if writes.Add(1) == 1 {
							switch failure {
							case "uncertain_5xx":
								w.WriteHeader(503)
							case "rate_limit":
								w.Header().Set("Retry-After", "0")
								w.WriteHeader(429)
							case "unauthorized":
								w.WriteHeader(401)
							case "lost_response":
								conn, _, err := w.(http.Hijacker).Hijack()
								require.NoError(t, err)
								_ = conn.Close()
							}
							return
						}
						_, _ = w.Write([]byte(`{}`))
					}))
					defer server.Close()
					require.Error(t, action.send(newTestPerfClient(server.URL)))
					require.Equal(t, int32(1), writes.Load(), "one call must not replay a possibly accepted advertising mutation")
				})
			}
		})
	}
}

func TestPerformanceMutationDoesNotFollowRedirect(t *testing.T) {
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "test-token", 1800)
			return
		}
		http.Redirect(w, r, destination.URL+"/unexpected", 307)
	}))
	defer origin.Close()
	require.Error(t, newTestPerfClient(origin.URL).ActivateCampaign(context.Background(), testCreds, 101))
	require.Zero(t, forwarded.Load())
}

func TestOzonApprovedMutationCannotBypassMissingJournal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	err := newTestPerfClient(server.URL).ActivateCampaign(adguard.WithApprovedCommand(context.Background(), uuid.New()), testCreds, 101)
	require.Error(t, err)
	require.True(t, adguard.WasNotSent(err))
	require.Zero(t, calls.Load())
}

func TestOzonCPOMutatingGETCannotBeReplayedByAReusedHTTPConnection(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/client/token" {
			writeToken(w, "test-token", 1800)
			return
		}
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
	c := newTestPerfClient(server.URL)
	_, err := c.doJSON(context.Background(), testCreds, http.MethodGet, "/warm", nil, nil)
	require.NoError(t, err)
	require.Error(t, c.DeactivateAllSKUPromo(context.Background(), testCreds))
	require.Equal(t, int32(1), calls.Load())
}
