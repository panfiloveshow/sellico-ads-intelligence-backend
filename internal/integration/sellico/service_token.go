package sellico

// ServiceTokenManager hides the difference between two ways of supplying the
// service-account bearer that the backend uses for /get-integrations and
// /check-permission:
//
//  1. A static token in env (SELLICO_API_TOKEN) — preferred in production.
//     Cached forever, never re-fetched, only re-read at process restart.
//
//  2. Email + password (SELLICO_EMAIL / SELLICO_PASSWORD) — bootstrap fallback.
//     The manager calls Client.Login on first use, caches the token for 23h
//     (matching the upstream token TTL of 24h with a safety margin), and
//     re-logs-in transparently on Invalidate() (called when a 401 surfaces).
//
// Concurrency: a single sync.Mutex serialises Login attempts so a request
// burst at startup doesn't spam /login. After the first success the hot path
// is just a read of the cached value under the same mutex.
//
// Failure mode: if neither static token nor credentials are configured,
// Get() returns ErrNoServiceAccount without trying the network. The auth
// middleware uses this signal to fall back to local-JWT auth (or fail-closed,
// depending on configuration) so the system stays bootable even when the
// Sellico integration isn't wired up yet.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNoServiceAccount is returned by ServiceTokenManager.Get when neither a
// static SELLICO_API_TOKEN nor SELLICO_EMAIL/SELLICO_PASSWORD are configured.
var ErrNoServiceAccount = errors.New("sellico: service-account credentials not configured")

// ServiceTokenManager Config — populate from internal/config.Config.
type ServiceTokenConfig struct {
	StaticToken string        // SELLICO_API_TOKEN — preferred when set
	Email       string        // SELLICO_EMAIL — login fallback
	Password    string        // SELLICO_PASSWORD — login fallback
	TTL         time.Duration // how long a fetched token is trusted; 0 → 23h
}

type ServiceTokenManager struct {
	client *Client
	cfg    ServiceTokenConfig

	mu        sync.Mutex
	cached    string
	cachedExp time.Time
}

func NewServiceTokenManager(client *Client, cfg ServiceTokenConfig) *ServiceTokenManager {
	if cfg.TTL <= 0 {
		cfg.TTL = 23 * time.Hour
	}
	return &ServiceTokenManager{client: client, cfg: cfg}
}

// IsConfigured reports whether either auth path (static token or login creds)
// is wired up. Useful for /health/ready or for the auth middleware to decide
// at startup whether to require service-account presence.
func (m *ServiceTokenManager) IsConfigured() bool {
	return m.cfg.StaticToken != "" || (m.cfg.Email != "" && m.cfg.Password != "")
}

// Get returns a valid service-account token. Cheap on the cache hit (one
// mutex acquisition + a time comparison); on a miss it either returns the
// static token (and primes the cache) or performs a /login round-trip.
//
// Callers should treat ErrNoServiceAccount as a hard configuration error —
// it will not become valid until the process is restarted with the right env.
func (m *ServiceTokenManager) Get(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cached != "" && time.Now().Before(m.cachedExp) {
		return m.cached, nil
	}

	if m.cfg.StaticToken != "" {
		// Static tokens never expire from our side. Cache anyway so we don't
		// re-enter this branch on every call (cosmetic — the branch is cheap).
		m.cached = m.cfg.StaticToken
		m.cachedExp = time.Now().Add(m.cfg.TTL)
		return m.cached, nil
	}

	if m.cfg.Email == "" || m.cfg.Password == "" {
		return "", ErrNoServiceAccount
	}

	resp, err := m.client.Login(ctx, m.cfg.Email, m.cfg.Password)
	if err != nil {
		return "", err
	}
	m.cached = resp.AccessToken
	m.cachedExp = time.Now().Add(m.cfg.TTL)
	return m.cached, nil
}

// Invalidate forces the next Get() to re-fetch. Call this when a downstream
// API returns ErrUnauthorized so the manager re-logs-in on retry.
//
// For static-token mode this is a no-op for retries (the same value comes
// back) — the operator must rotate SELLICO_API_TOKEN in env if it's revoked.
func (m *ServiceTokenManager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.StaticToken != "" {
		// Re-priming with the same value is pointless; keep cache as-is so
		// retries don't loop forever. Operator must restart with new env.
		return
	}
	m.cached = ""
	m.cachedExp = time.Time{}
}
