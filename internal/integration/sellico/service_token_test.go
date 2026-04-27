package sellico

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceTokenManager_StaticToken(t *testing.T) {
	mgr := NewServiceTokenManager(nil, ServiceTokenConfig{StaticToken: "static-abc"})
	if !mgr.IsConfigured() {
		t.Fatal("expected configured")
	}
	tok, err := mgr.Get(context.Background())
	if err != nil || tok != "static-abc" {
		t.Fatalf("static path: tok=%q err=%v", tok, err)
	}
	// Invalidate is a no-op for static; subsequent Get returns same value.
	mgr.Invalidate()
	tok2, err := mgr.Get(context.Background())
	if err != nil || tok2 != "static-abc" {
		t.Fatalf("after invalidate: tok=%q err=%v", tok2, err)
	}
}

func TestServiceTokenManager_LoginPath(t *testing.T) {
	var loginCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/login") {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&loginCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"login-tok-` + atomicSnap(&loginCalls) + `","token_type":"Bearer","user":{"id":"42"}}`))
	}))
	defer srv.Close()

	cli := NewClient(srv.URL, time.Second)
	mgr := NewServiceTokenManager(cli, ServiceTokenConfig{
		Email: "ops@sellico.local", Password: "x", TTL: 10 * time.Millisecond,
	})

	tok, err := mgr.Get(context.Background())
	if err != nil || !strings.HasPrefix(tok, "login-tok-") {
		t.Fatalf("first get: tok=%q err=%v", tok, err)
	}
	if got := atomic.LoadInt32(&loginCalls); got != 1 {
		t.Fatalf("expected 1 login, got %d", got)
	}
	// Cache hit — no extra login.
	_, _ = mgr.Get(context.Background())
	if got := atomic.LoadInt32(&loginCalls); got != 1 {
		t.Fatalf("expected still 1 login (cache hit), got %d", got)
	}
	// Invalidate forces re-login.
	mgr.Invalidate()
	if _, err := mgr.Get(context.Background()); err != nil {
		t.Fatalf("post-invalidate Get: %v", err)
	}
	if got := atomic.LoadInt32(&loginCalls); got != 2 {
		t.Fatalf("expected 2 logins after invalidate, got %d", got)
	}
}

func TestServiceTokenManager_NoCreds(t *testing.T) {
	mgr := NewServiceTokenManager(nil, ServiceTokenConfig{})
	if mgr.IsConfigured() {
		t.Fatal("expected not configured")
	}
	_, err := mgr.Get(context.Background())
	if err != ErrNoServiceAccount {
		t.Fatalf("expected ErrNoServiceAccount, got %v", err)
	}
}

// atomicSnap is a tiny helper to convert an int32 read into a unique suffix
// for the fake login responses, so cached tokens are visibly different.
func atomicSnap(p *int32) string {
	switch atomic.LoadInt32(p) {
	case 1:
		return "1"
	case 2:
		return "2"
	default:
		return "n"
	}
}
