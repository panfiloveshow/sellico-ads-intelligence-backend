//go:build crossruntime

package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/require"
)

func TestLegacyPriceDispatchRealPHPClientTLSAndSQLStop(t *testing.T) {
	root := os.Getenv("SELYA_PRODUCTS_ROOT")
	require.NotEmpty(t, root, "crossruntime requires the explicit local Products checkout")
	_, err := exec.LookPath("php")
	require.NoError(t, err, "this explicit crossruntime check requires PHP")
	ctx := context.Background()
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	secret := strings.Repeat("test-only-price-key-", 3)
	h := handler.NewLegacyPriceDispatchHandler(service.NewLegacyPriceDispatchService(q, true), secret)
	var mu sync.Mutex
	var bodies [][]byte
	var signatures []string
	var issued []json.RawMessage
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			http.Error(w, "read failed", 400)
			return
		}
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), raw...))
		signatures = append(signatures, r.Header.Get("X-Sellico-Price-Authorization"))
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		result := httptest.NewRecorder()
		h.Authorize(result, r)
		if result.Code == 200 {
			mu.Lock()
			issued = append(issued, append([]byte(nil), result.Body.Bytes()...))
			mu.Unlock()
		}
		for k, values := range result.Header() {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(result.Code)
		_, _ = w.Write(result.Body.Bytes())
	}))
	defer srv.Close()
	dir := t.TempDir()
	cert := filepath.Join(dir, "local-test-ca.pem")
	require.NoError(t, os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600))
	script, err := filepath.Abs("testdata/legacy_price_dispatch_client.php")
	require.NoError(t, err)
	run := func(mode, key string) map[string]any {
		t.Helper()
		cmd := exec.Command("php", script)
		cmd.Dir = root
		values := map[string]string{"HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "http_proxy": "", "https_proxy": "", "all_proxy": "", "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost", "APP_ENV": "testing", "DB_CONNECTION": "sqlite", "DB_DATABASE": ":memory:", "DB_URL": "", "DATABASE_URL": "", "CACHE_STORE": "array", "QUEUE_CONNECTION": "sync", "MAIL_MAILER": "array", "SESSION_DRIVER": "array", "APP_CONFIG_CACHE": filepath.Join(dir, "no-cached-config.php"), "SELYA_GATE_URL": srv.URL + "/internal/v1/prices/dispatch-authorizations", "SELYA_GATE_CERT": cert, "SELYA_GATE_SECRET": key, "SELYA_GATE_MODE": mode}
		for _, v := range os.Environ() {
			k, _, _ := strings.Cut(v, "=")
			if _, replace := values[k]; !replace {
				cmd.Env = append(cmd.Env, v)
			}
		}
		for k, v := range values {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		require.NoError(t, cmd.Run(), stderr.String()+out.String())
		var result map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String()+stderr.String())
		return result
	}
	allowed := run("write", secret)
	require.EqualValues(t, 2, allowed["native_requests_simulated"])
	require.Equal(t, "receipt_received", allowed["wb_status"])
	require.Equal(t, "receipt_received", allowed["ozon_status"])
	// A PHP receipt is not yet a durable Native-terminal proof. The next raw
	// command is fenced even before any Go physical hold has been created.
	permitHeld := run("write", secret)
	require.EqualValues(t, 0, permitHeld["native_requests_simulated"])
	require.Equal(t, "not_dispatched", permitHeld["wb_status"])
	require.Equal(t, "not_dispatched", permitHeld["ozon_status"])
	wb := adguard.AccountKey("wb", "11111111-2222-3333-4444-555555555555")
	sum := sha256.Sum256([]byte("ozon-seller-price:555"))
	ozon := "ozon:" + hex.EncodeToString(sum[:])
	// Actual PHP -> local TLS -> signed Go handler -> real SQL denial. Only
	// native marketplaces are faked, and no granted command is sent to them.
	ws := testdb.Workspace(t, pool)
	cab := testdb.WBCabinet(t, pool, ws)
	row, err := q.EnqueuePriceChange(ctx, sqlcgen.CreatePriceChangeParams{WorkspaceID: pgtype.UUID{Bytes: ws, Valid: true}, SellerCabinetID: pgtype.UUID{Bytes: cab, Valid: true}, WbProductID: 101, OldPriceRub: 1000, NewPriceRub: 900, Source: "manual"})
	require.NoError(t, err)
	bound, err := json.Marshal(map[string]any{"wb_dispatch": map[string]any{"account_key": wb}})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, "UPDATE price_changes SET wb_status='submit_unknown',decision_context=$2 WHERE id=$1", row.ID, bound)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO ozon_price_write_attempts(change_id,seller_cabinet_id,product_id,vendor_account_key,resource_key,status,price_cents,baseline)
 VALUES($1,$2,101,$3,$4,'submit_unknown',10000,'{}')`, uuid.New(), uuid.New(), strings.TrimPrefix(ozon, "ozon:"), uuid.NewString())
	require.NoError(t, err)
	for _, unbound := range []bool{false, true} {
		if unbound {
			_, err = pool.Exec(ctx, "UPDATE price_changes SET decision_context='{}' WHERE id=$1", row.ID)
			require.NoError(t, err)
		}
		held := run("write", secret)
		require.EqualValues(t, 0, held["native_requests_simulated"])
		require.Equal(t, "not_dispatched", held["wb_status"])
		require.Equal(t, "not_dispatched", held["ozon_status"])
	}
	set := func(market, account, mode string) {
		t.Helper()
		before, e := q.PriceExecutionControl(ctx, account, market)
		require.NoError(t, e)
		now := time.Now().UTC().Truncate(time.Microsecond)
		o := domain.PriceExecutionControlOperation{ID: uuid.New(), WorkspaceID: uuid.New(), ActorID: uuid.New(), ExternalUserID: "7", ExternalWorkspaceID: "3", IntegrationID: "28", IntegrationName: "Isolated cross-runtime", CabinetID: uuid.New(), Marketplace: market, AccountKey: account, CredentialDigest: strings.Repeat("a", 64), Scope: "configured_price_account", TargetMode: mode, Before: before, Proof: domain.PriceExecutionAccountProof{ResourceID: "101", Source: "WB Prices API", ObservedAt: now}, Reason: "Isolated cross-runtime stop", Fingerprint: strings.Repeat("b", 64), Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
		if market == "ozon" {
			o.Proof.Source = "Ozon Seller API"
		}
		require.NoError(t, q.CreatePriceExecutionControlOperation(ctx, o))
		require.NoError(t, q.ApplyPriceExecutionControl(ctx, o, func(context.Context) error { return nil }))
	}
	for _, mode := range []string{"stopped", "manual_only"} {
		set("wb", wb, mode)
		set("ozon", ozon, mode)
		result := run("write", secret)
		require.EqualValues(t, 0, result["native_requests_simulated"])
		require.Equal(t, "not_dispatched", result["wb_status"])
		require.Equal(t, "not_dispatched", result["ozon_status"])
	}
	reads := run("read", "")
	require.EqualValues(t, 2, reads["native_requests_simulated"])
	require.Equal(t, true, reads["read_arrays"])
	wrong := run("write", strings.Repeat("wrong-test-key-", 4))
	require.EqualValues(t, 0, wrong["native_requests_simulated"])
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM legacy_price_dispatch_authorizations").Scan(&count))
	require.Equal(t, 2, count)
	mu.Lock()
	first := append([]byte(nil), bodies[0]...)
	signature := signatures[0]
	grants := append([]json.RawMessage(nil), issued...)
	mu.Unlock()
	r := httptest.NewRequest(http.MethodPost, "/internal/v1/prices/dispatch-authorizations", bytes.NewReader(first))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Sellico-Price-Authorization", signature)
	w := httptest.NewRecorder()
	h.Authorize(w, r)
	require.Equal(t, 409, w.Code)
	if export := os.Getenv("SELYA_LEGACY_PRICE_GO_CONTRACT_DIR"); export != "" {
		require.NoError(t, os.MkdirAll(export, 0700))
		for _, grant := range grants {
			var result struct {
				Data domain.LegacyPriceDispatchGrant `json:"data"`
			}
			require.NoError(t, json.Unmarshal(grant, &result))
			require.NoError(t, os.WriteFile(filepath.Join(export, result.Data.Marketplace+"-grant.json"), grant, 0600))
		}
	}
}
