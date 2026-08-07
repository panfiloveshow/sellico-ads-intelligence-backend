package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
)

// fakeOzonAPI is an httptest-backed stand-in for BOTH Ozon APIs (Seller +
// Performance) — the two path spaces never overlap, so one server serves
// both base URLs. Responses are registered per exact path; the token endpoint
// answers by default; any unknown path returns "{}" so write endpoints
// succeed without setup.
type fakeOzonAPI struct {
	mu      sync.Mutex
	jsonRes map[string]any    // path → payload marshaled as JSON
	rawRes  map[string]string // path → verbatim body (CSV etc.)
	status  map[string]int    // path → forced HTTP status (error injection)
	calls   map[string]int
	bodies  map[string][]string // path → captured request bodies
}

func newFakeOzonAPI() *fakeOzonAPI {
	return &fakeOzonAPI{
		jsonRes: map[string]any{},
		rawRes:  map[string]string{},
		status:  map[string]int{},
		calls:   map[string]int{},
		bodies:  map[string][]string{},
	}
}

func (f *fakeOzonAPI) setJSON(path string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jsonRes[path] = payload
}

func (f *fakeOzonAPI) setRaw(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rawRes[path] = body
}

// fail makes the path answer the given status. Use 4xx in tests: the clients
// retry 5xx with real backoff sleeps.
func (f *fakeOzonAPI) fail(path string, statusCode int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[path] = statusCode
}

func (f *fakeOzonAPI) callCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

func (f *fakeOzonAPI) lastBody(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	captured := f.bodies[path]
	if len(captured) == 0 {
		return ""
	}
	return captured[len(captured)-1]
}

func (f *fakeOzonAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	requestBody, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	f.calls[path]++
	if len(requestBody) > 0 {
		f.bodies[path] = append(f.bodies[path], string(requestBody))
	}
	forcedStatus, forced := f.status[path]
	raw, hasRaw := f.rawRes[path]
	payload, hasJSON := f.jsonRes[path]
	f.mu.Unlock()

	if forced {
		w.WriteHeader(forcedStatus)
		_, _ = w.Write([]byte(`{"error":"forced failure"}`))
		return
	}
	if path == "/api/client/token" && !hasJSON {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":1800}`))
		return
	}
	if hasRaw {
		_, _ = w.Write([]byte(raw))
		return
	}
	if hasJSON {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

// ozonTestEnv wires one test's world: its own database, its own fake Ozon
// server and real services on top of both.
type ozonTestEnv struct {
	pool        *pgxpool.Pool
	queries     *sqlcgen.Queries
	fake        *fakeOzonAPI
	syncSvc     *OzonSyncService
	actionsSvc  *OzonCampaignActionsService
	workspaceID uuid.UUID
	cabinetID   uuid.UUID
	creds       domain.OzonCredentials
}

// newOzonTestEnv builds the environment with a workspace + one Ozon cabinet
// carrying the given credentials. Skips when local postgres is unavailable.
func newOzonTestEnv(t *testing.T, creds domain.OzonCredentials) *ozonTestEnv {
	t.Helper()
	pool := testdb.New(t)

	fake := newFakeOzonAPI()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	cfg := &config.Config{
		OzonSellerAPIBaseURL: server.URL,
		OzonPerfAPIBaseURL:   server.URL,
	}
	logger := zerolog.Nop()
	queries := sqlcgen.New(pool)
	sellerClient := ozon.NewSellerClient(cfg, logger)
	perfClient := ozon.NewPerfClient(cfg, logger)

	workspaceID := testdb.Workspace(t, pool)
	cabinetID := testdb.OzonCabinet(t, pool, workspaceID, creds)

	return &ozonTestEnv{
		pool:        pool,
		queries:     queries,
		fake:        fake,
		syncSvc:     NewOzonSyncService(queries, sellerClient, perfClient, testdb.EncryptionKey, logger),
		actionsSvc:  NewOzonCampaignActionsService(queries, perfClient, testdb.EncryptionKey, logger),
		workspaceID: workspaceID,
		cabinetID:   cabinetID,
		creds:       creds,
	}
}

// cabinet loads the env cabinet as the domain object services expect.
func (e *ozonTestEnv) cabinet(t *testing.T) domain.SellerCabinet {
	t.Helper()
	row, err := e.queries.GetSellerCabinetByID(context.Background(), uuidToPgtype(e.cabinetID))
	if err != nil {
		t.Fatalf("load env cabinet: %v", err)
	}
	return sellerCabinetFromSqlc(row)
}

// countRows is a tiny assertion helper for raw table counts.
func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}
