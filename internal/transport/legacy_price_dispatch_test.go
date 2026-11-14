package transport

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/require"
)

const isolatedPriceDispatchSecret = "test-only-price-key-test-only-price-key-test-only-price-key-"

type legacyPriceHTTPAuthorizer struct {
	calls    int
	received domain.LegacyPriceDispatchRequest
	err      error
}

func (s *legacyPriceHTTPAuthorizer) Authorize(_ context.Context, r domain.LegacyPriceDispatchRequest) (domain.LegacyPriceDispatchGrant, error) {
	s.calls++
	s.received = r
	if s.err != nil {
		return domain.LegacyPriceDispatchGrant{}, s.err
	}
	now := time.Now().UTC()
	return domain.LegacyPriceDispatchGrant{Version: "legacy_price_dispatch_grant_v1", Origin: r.Origin, CommandID: r.CommandID, Marketplace: r.Marketplace, Method: r.Method, Endpoint: r.Endpoint, PayloadSHA256: r.PayloadSHA256, Authorized: true, Scope: "one_legacy_price_request", IssuedAt: now, ExpiresAt: now.Add(5 * time.Second), ValidForMS: 5000}, nil
}

func TestLegacyPriceDispatchHTTPReportsActiveOrUnboundHoldWithoutPrivateBinding(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"active", sqlcgen.ErrLegacyPriceActiveWrite, "price_dispatch_blocked_by_active_write"},
		{"unbound", sqlcgen.ErrLegacyPriceBindingUnknown, "price_dispatch_binding_unknown"},
		{"retired", service.ErrLegacyPriceDispatchRetired, "price_dispatch_retired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &legacyPriceHTTPAuthorizer{err: tc.err}
			request := domain.LegacyPriceDispatchRequest{Version: "legacy_price_dispatch_v1", Origin: "products", CommandID: uuid.NewString(), SentAt: time.Now().Unix(), Marketplace: "wb", Method: "POST", AccountKey: "wb:" + strings.Repeat("a", 64), Endpoint: "/api/v2/upload/task", PayloadSHA256: strings.Repeat("b", 64)}
			body, err := json.Marshal(request)
			require.NoError(t, err)
			r := httptest.NewRequest(http.MethodPost, "/internal/v1/prices/dispatch-authorizations", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Sellico-Price-Authorization", priceDispatchSignature(body))
			w := httptest.NewRecorder()
			handler.NewLegacyPriceDispatchHandler(s, isolatedPriceDispatchSecret).Authorize(w, r)
			require.Equal(t, 409, w.Code)
			require.JSONEq(t, `{"data":null,"error":"`+tc.code+`"}`, w.Body.String())
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NotContains(t, w.Body.String(), request.AccountKey)
			require.Equal(t, 1, s.calls)
		})
	}
}

func priceDispatchSignature(body []byte) string {
	m := hmac.New(sha256.New, []byte(isolatedPriceDispatchSecret))
	m.Write([]byte("legacy-price-dispatch-v1\n"))
	m.Write(body)
	return "v1=" + hex.EncodeToString(m.Sum(nil))
}

func TestLegacyPriceDispatchHTTPRequiresExactSeparateServiceSignature(t *testing.T) {
	s := &legacyPriceHTTPAuthorizer{}
	router := NewRouter(RouterDeps{LegacyPriceDispatchHandler: handler.NewLegacyPriceDispatchHandler(s, isolatedPriceDispatchSecret)})
	request := domain.LegacyPriceDispatchRequest{Version: "legacy_price_dispatch_v1", Origin: "products", CommandID: uuid.NewString(), SentAt: time.Now().Unix(), Marketplace: "wb", Method: "POST", AccountKey: "wb:" + strings.Repeat("a", 64), Endpoint: "/api/v2/upload/task", PayloadSHA256: strings.Repeat("b", 64)}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, body, signature, query, contentType string
		status                                    int
	}{
		{"personal_bearer", string(body), "", "", "application/json", 401},
		{"wrong_signature", string(body), "v1=" + strings.Repeat("0", 64), "", "application/json", 401},
		{"changed_body", string(body) + " ", priceDispatchSignature(body), "", "application/json", 401},
		{"query", string(body), priceDispatchSignature(body), "?workspace=3", "application/json", 400},
		{"unknown_key", strings.TrimSuffix(string(body), "}") + `,"manager_approval":true}`, "", "", "application/json", 400},
		{"second_json", string(body) + " {}", "", "", "application/json", 400},
		{"wrong_type", string(body), priceDispatchSignature(body), "", "text/plain", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.signature == "" && tc.name != "personal_bearer" {
				tc.signature = priceDispatchSignature([]byte(tc.body))
			}
			r := httptest.NewRequest(http.MethodPost, "/internal/v1/prices/dispatch-authorizations"+tc.query, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set("Authorization", "Bearer personal-user-token")
			r.Header.Set("X-Sellico-Price-Authorization", tc.signature)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.Zero(t, s.calls)
		})
	}
	r := httptest.NewRequest(http.MethodPost, "/internal/v1/prices/dispatch-authorizations", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Sellico-Price-Authorization", priceDispatchSignature(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 1, s.calls)
	require.Equal(t, request, s.received)
	require.NotContains(t, w.Body.String(), "account_key")
	require.NotContains(t, w.Body.String(), "Bearer")
	var response struct {
		Data domain.LegacyPriceDispatchGrant `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, request.PayloadSHA256, response.Data.PayloadSHA256)
}

func TestLegacyPriceDispatchHTTPFailsClosedWithoutItsOwnCredential(t *testing.T) {
	s := &legacyPriceHTTPAuthorizer{}
	h := handler.NewLegacyPriceDispatchHandler(s, "")
	r := httptest.NewRequest(http.MethodPost, "/internal/v1/prices/dispatch-authorizations", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	h.Authorize(w, r)
	require.Equal(t, 503, w.Code)
	require.Zero(t, s.calls)
}

func TestLegacyPriceDispatchAcceptsTheExactSignedBodyEmittedByProducts(t *testing.T) {
	dir := os.Getenv("SELYA_LEGACY_PRICE_PHP_CONTRACT_DIR")
	if dir == "" {
		dir = "testdata"
	}
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, "legacy_price_dispatch_"+market+".json"))
			require.NoError(t, err)
			var fixture struct {
				Body      string `json:"body"`
				Signature string `json:"signature"`
			}
			require.NoError(t, json.Unmarshal(raw, &fixture))
			s := &legacyPriceHTTPAuthorizer{}
			h := handler.NewLegacyPriceDispatchHandler(s, isolatedPriceDispatchSecret)
			r := httptest.NewRequest(http.MethodPost, "/internal/v1/prices/dispatch-authorizations", strings.NewReader(fixture.Body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Sellico-Price-Authorization", fixture.Signature)
			w := httptest.NewRecorder()
			h.Authorize(w, r)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, 1, s.calls)
			require.Equal(t, market, s.received.Marketplace)
		})
	}
}
