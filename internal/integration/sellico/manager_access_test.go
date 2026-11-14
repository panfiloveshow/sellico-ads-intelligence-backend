package sellico

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerAccessCanonicalAndUncached(t *testing.T) {
	var reads atomic.Int32
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		assert.Equal(t, "Bearer personal-token", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/user":
			fmt.Fprint(w, `{"id":42,"is_service_account":false,"role":"admin"}`)
		case "/api/workspaces/3/user-permissions":
			if revoked.Load() {
				fmt.Fprint(w, `{"user_id":42,"permissions":["marketing.view"]}`)
			} else {
				fmt.Fprint(w, `{"user_id":42,"permissions":["marketing.view",{"slug":"marketing.manage"}]}`)
			}
		default:
			t.Errorf("unexpected identity path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL+"/api", time.Second)
	first, err := client.VerifyManagerAccess(context.Background(), "personal-token", "3")
	require.NoError(t, err)
	assert.Equal(t, "42", first.ExternalUserID)
	assert.Equal(t, "3", first.ExternalWorkspaceID)
	assert.True(t, first.Allows("marketing.manage"))
	assert.True(t, first.PersonalAccountVerified)
	revoked.Store(true)
	second, err := client.VerifyManagerAccess(context.Background(), "personal-token", "3")
	require.NoError(t, err)
	assert.False(t, second.Allows("marketing.manage"))
	assert.EqualValues(t, 4, reads.Load())
}

func TestManagerAccessRejectsUnverifiedIdentityAndMembership(t *testing.T) {
	cases := []struct {
		name, profile, membership string
	}{
		{"service account", `{"id":42,"is_service_account":true}`, `{"user_id":42,"permissions":["marketing.manage"]}`},
		{"missing account type", `{"id":42}`, `{"user_id":42,"permissions":["marketing.manage"]}`},
		{"null account type", `{"id":42,"is_service_account":null}`, `{"user_id":42,"permissions":["marketing.manage"]}`},
		{"numeric account type", `{"id":42,"is_service_account":0}`, `{"user_id":42,"permissions":["marketing.manage"]}`},
		{"invalid user id", `{"id":"admin","is_service_account":false}`, `{"user_id":"admin","permissions":["marketing.manage"]}`},
		{"another user", `{"id":42,"is_service_account":false}`, `{"user_id":99,"permissions":["marketing.manage"]}`},
		{"another workspace", `{"id":42,"is_service_account":false}`, `{"user_id":42,"workspace_id":4,"permissions":["marketing.manage"]}`},
		{"role without permissions", `{"id":42,"is_service_account":false}`, `{"user_id":42,"role":"admin"}`},
		{"object instead of permission list", `{"id":42,"is_service_account":false}`, `{"user_id":42,"permissions":{"marketing.manage":true}}`},
		{"malformed permission", `{"id":42,"is_service_account":false}`, `{"user_id":42,"permissions":["marketing.view",{"role":"admin"}]}`},
		{"empty permission", `{"id":42,"is_service_account":false}`, `{"user_id":42,"permissions":["marketing.view",""]}`},
		{"padded permission", `{"id":42,"is_service_account":false}`, `{"user_id":42,"permissions":["marketing.view"," marketing.manage "]}`},
		{"trailing json", `{"id":42,"is_service_account":false} {"id":99}`, `{"user_id":42,"permissions":["marketing.manage"]}`},
		{"invalid account type", `{"id":42,"is_service_account":"false"}`, `{"user_id":42,"permissions":["marketing.manage"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/user" {
					fmt.Fprint(w, tc.profile)
				} else {
					fmt.Fprint(w, tc.membership)
				}
			}))
			defer server.Close()
			access, err := NewClient(server.URL, time.Second).VerifyManagerAccess(context.Background(), "personal", "3")
			require.Error(t, err)
			assert.Nil(t, access)
		})
	}
}

func TestManagerAccessIncompleteFreshProfileCannotReuseEarlierVerification(t *testing.T) {
	var incomplete atomic.Bool
	var membershipReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			if incomplete.Load() {
				fmt.Fprint(w, `{"id":42}`)
			} else {
				fmt.Fprint(w, `{"id":42,"is_service_account":false}`)
			}
			return
		}
		membershipReads.Add(1)
		fmt.Fprint(w, `{"user_id":42,"permissions":["marketing.manage",{"slug":"marketing.manage"}]}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, time.Second)
	first, err := client.VerifyManagerAccess(context.Background(), "personal", "3")
	require.NoError(t, err)
	assert.True(t, first.PersonalAccountVerified)
	assert.Equal(t, []string{"marketing.manage"}, first.Permissions)
	incomplete.Store(true)
	fresh, err := client.VerifyManagerAccess(context.Background(), "personal", "3")
	require.Error(t, err)
	assert.Nil(t, fresh)
	assert.EqualValues(t, 1, membershipReads.Load())
}

func TestManagerAccessDoesNotFollowRedirectsOrExposeUpstreamBodies(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusInternalServerError, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(code)
				fmt.Fprint(w, "PRIVATE_UPSTREAM_BODY_WITH_CREDENTIALS")
			}))
			defer server.Close()
			_, err := NewClient(server.URL, time.Second).VerifyManagerAccess(context.Background(), "personal", "3")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "PRIVATE_UPSTREAM")
			if code == http.StatusUnauthorized {
				assert.True(t, errors.Is(err, ErrUnauthorized))
			}
		})
	}
	assert.Zero(t, hits.Load())
}

func TestManagerAccessRejectsCallerPathsWithoutARequest(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer server.Close()
	client := NewClient(server.URL, time.Second)
	for _, workspace := range []string{"", "0", "-1", "3/../4", "3?user_id=99"} {
		_, err := client.VerifyManagerAccess(context.Background(), "personal", workspace)
		require.Error(t, err)
	}
	_, err := client.VerifyManagerAccess(context.Background(), strings.Repeat("x", 8193), "3")
	require.Error(t, err)
	assert.Zero(t, hits.Load())
}

func TestManagerAccessUnconfiguredClientFailsClosed(t *testing.T) {
	for _, client := range []*Client{nil, {}} {
		access, err := client.VerifyManagerAccess(context.Background(), "personal", "3")
		require.Error(t, err)
		assert.Nil(t, access)
	}
}
