package sellico

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetIntegrations_NoWorkspace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get-integrations" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer svc-tok" {
			t.Errorf("missing/wrong service-account bearer")
		}
		_, _ = w.Write([]byte(`[
			{"id":1,"work_space_id":42,"name":"WB Store","type":"WildBerries","api_key":"key-a","account_status":"confirmed"},
			{"id":2,"work_space_id":42,"name":"OZ Store","type":"OZON","api_key":"key-b","client_id":"cli-1"}
		]`))
	}))
	defer srv.Close()

	cli := NewClient(srv.URL, time.Second)
	out, err := cli.GetIntegrations(context.Background(), "svc-tok", "")
	if err != nil {
		t.Fatalf("GetIntegrations: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 integrations, got %d", len(out))
	}
	if out[0].Type != "WildBerries" || out[0].APIKey != "key-a" || out[0].WorkspaceID != "42" {
		t.Errorf("first integration mis-parsed: %+v", out[0])
	}
}

func TestGetIntegrations_WithWorkspace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get-integrations/123" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cli := NewClient(srv.URL, time.Second)
	out, err := cli.GetIntegrations(context.Background(), "svc-tok", "123")
	if err != nil {
		t.Fatalf("GetIntegrations: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0, got %d", len(out))
	}
}

func TestGetIntegration_FullCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/get-integration/77" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":77,"work_space_id":42,"name":"WB Premium","type":"WildBerries","api_key":"wb-key","performance_api_key":"perf-k","performance_client_secret":"perf-s","is_premium":true,"status":"active"}`))
	}))
	defer srv.Close()

	cli := NewClient(srv.URL, time.Second)
	out, err := cli.GetIntegration(context.Background(), "svc-tok", "77")
	if err != nil {
		t.Fatalf("GetIntegration: %v", err)
	}
	if out.APIKey != "wb-key" || out.PerformanceAPIKey != "perf-k" || !out.IsPremium {
		t.Errorf("rich fields mis-parsed: %+v", out)
	}
}

func TestCheckPermission_Valid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/check-permission" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var got map[string]string
		_ = json.Unmarshal(body, &got)
		if got["permission"] != "integrations.view" || got["user"] != "u1" {
			t.Errorf("body wrong: %v", got)
		}
		_, _ = w.Write([]byte(`{"valid":true}`))
	}))
	defer srv.Close()

	cli := NewClient(srv.URL, time.Second)
	ok, err := cli.CheckPermission(context.Background(), "svc", CheckPermissionParams{
		UserToken: "user-tok", UserID: "u1", WorkspaceID: "42", Permission: "integrations.view",
	})
	if err != nil || !ok {
		t.Errorf("expected (true,nil), got (%v,%v)", ok, err)
	}
}

func TestCheckPermission_FailClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	cli := NewClient(srv.URL, time.Second)
	ok, err := cli.CheckPermission(context.Background(), "svc", CheckPermissionParams{
		UserToken: "u", UserID: "1", WorkspaceID: "2", Permission: "x",
	})
	if ok || err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Errorf("expected fail-closed; got (%v,%v)", ok, err)
	}
}

func TestLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]string
		_ = json.Unmarshal(body, &got)
		if got["email"] != "ops@x" || got["password"] != "p" {
			t.Errorf("body wrong: %v", got)
		}
		_, _ = w.Write([]byte(`{"access_token":"tk-1","token_type":"Bearer","user":{"id":"99","is_service_account":true}}`))
	}))
	defer srv.Close()
	cli := NewClient(srv.URL, time.Second)
	out, err := cli.Login(context.Background(), "ops@x", "p")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if out.AccessToken != "tk-1" || out.User["id"] != "99" {
		t.Errorf("login parsed wrong: %+v", out)
	}
}

func TestCreateActivity_OK(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()
	cli := NewClient(srv.URL, time.Second)
	err := cli.CreateActivity(context.Background(), "user-tok", "42", ActivityPayload{
		Action: "integrations.view", Title: "X", Description: "Y", Meta: map[string]any{"a": 1},
	})
	if err != nil {
		t.Fatalf("CreateActivity: %v", err)
	}
	if seen != "/workspaces/42/activities" {
		t.Errorf("wrong path: %q", seen)
	}
}
