package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
)

// Sellico hides raw credentials from browser-facing (user-token) endpoints and
// exposes them only to service accounts. The cabinet sync must therefore read
// integrations via the service account, falling back to the user token only
// when no service account is configured.
func newSellicoStub(t *testing.T) *httptest.Server {
	t.Helper()
	full := map[string]any{
		"id": 90, "work_space_id": 3, "name": "Ozon shop", "type": "OZON", "status": "active",
		"client_id": "cid", "api_key": "seller-key",
		"performance_api_key": "perf-id", "performance_client_secret": "perf-secret",
	}
	masked := map[string]any{
		"id": 90, "work_space_id": 3, "name": "Ozon shop", "type": "OZON", "status": "active",
		"client_id": "cid", "has_performance_api_key": true, "has_performance_client_secret": true,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		isService := r.Header.Get("Authorization") == "Bearer svc-token"
		switch {
		case strings.HasPrefix(r.URL.Path, "/get-integrations/"):
			if !isService {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode([]any{full})
		case strings.HasPrefix(r.URL.Path, "/get-integration/"):
			if !isService {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(full)
		case strings.HasSuffix(r.URL.Path, "/integrations"), strings.Contains(r.URL.Path, "/integrations/"):
			_ = json.NewEncoder(w).Encode([]any{masked})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newCabinetServiceForStub(url string, withServiceAccount bool) *SellerCabinetService {
	client := sellico.NewClient(url, 5*time.Second)
	svc := NewSellerCabinetService(nil, []byte("0123456789abcdef0123456789abcdef"), nil, client)
	if withServiceAccount {
		svc.WithServiceAccount(sellico.NewServiceTokenManager(client, sellico.ServiceTokenConfig{StaticToken: "svc-token"}))
	}
	return svc
}

func TestFetchIntegrations_ServiceAccountSeesCredentials(t *testing.T) {
	stub := newSellicoStub(t)
	defer stub.Close()

	got, err := newCabinetServiceForStub(stub.URL, true).fetchIntegrations(context.Background(), "user-token", []string{"3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].APIKey != "seller-key" || got[0].PerformanceAPIKey != "perf-id" || got[0].PerformanceClientSecret != "perf-secret" {
		t.Fatalf("expected full credentials via service account, got %+v", got)
	}
}

func TestFetchIntegrations_UserTokenOnlySeesNoCredentials(t *testing.T) {
	stub := newSellicoStub(t)
	defer stub.Close()

	got, err := newCabinetServiceForStub(stub.URL, false).fetchIntegrations(context.Background(), "user-token", []string{"3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Documents the failure mode: masked payload carries no api_key, so the
	// integration is skipped — exactly why the service-account path exists.
	if len(got) != 0 {
		t.Fatalf("expected masked integration to be skipped, got %+v", got)
	}
}

func TestFetchIntegration_ServiceAccountIsWorkspaceScoped(t *testing.T) {
	stub := newSellicoStub(t)
	defer stub.Close()
	svc := newCabinetServiceForStub(stub.URL, true)

	got, err := svc.fetchIntegration(context.Background(), "", []string{"3"}, "90")
	if err != nil || got == nil || got.PerformanceAPIKey != "perf-id" {
		t.Fatalf("expected integration 90 for workspace 3, got %+v err=%v", got, err)
	}

	if _, err := svc.fetchIntegration(context.Background(), "", []string{"999"}, "90"); err == nil {
		t.Fatalf("integration of another workspace must not resolve")
	}
}
