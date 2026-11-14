package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/adguard"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type pauseAuthority struct {
	market   string
	calls    atomic.Int32
	revokeAt int32
	denyAt   int32
	personal bool
	hook     func(int32)
}

func (a *pauseAuthority) VerifyManagerIntegration(_ context.Context, token, workspace, integration string) (*domain.ManagerIntegration, error) {
	if token != "personal-token" || workspace != "3" || integration != "28" {
		return nil, sqlcgen.ErrManagerAdOperationChanged
	}
	return &domain.ManagerIntegration{ID: integration, WorkspaceID: workspace, Marketplace: a.market, Name: "Test shop"}, nil
}
func (a *pauseAuthority) VerifyManagerAccess(_ context.Context, token, workspace string) (*domain.ManagerAccess, error) {
	n := a.calls.Add(1)
	if a.denyAt > 0 && n >= a.denyAt {
		return nil, sellico.ErrUnauthorized
	}
	if a.hook != nil {
		a.hook(n)
	}
	access := managerAdsIdentity()
	access.PersonalAccountVerified = a.personal
	if a.revokeAt == 0 || n < a.revokeAt {
		access.Permissions = append(access.Permissions, "marketing.manage")
	}
	return &access, nil
}

type pauseNativeAPI struct {
	market                   string
	mu                       sync.Mutex
	paused                   bool
	ready                    bool
	emptyProducts            bool
	duplicateProduct         bool
	productPagination        bool
	name                     string
	mode                     string
	budget                   int64
	budgetCurrency           string
	budgetMissing            bool
	weeklyBudget             string
	bidWB                    int64
	bidRecommendations       int64
	bidOzon                  string
	minimumWB                int64
	minimumOzon              string
	paymentType              string
	dailyLimitEnabled        bool
	dailyLimitKopecks        int64
	spentTodayKopecks        int64
	carryOver                bool
	minimumDailyLimitKopecks int64
	dailyLimitCurrency       string
	dailyBudgetOzon          string
	writes                   atomic.Int32
}

func (a *pauseNativeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/client/token" {
		_, _ = w.Write([]byte(`{"access_token":"test-only-token","expires_in":1800}`))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.URL.Path == "/api/advert/v2/adverts" {
		status := 9
		if a.paused {
			status = 11
		}
		if a.ready {
			status = 4
		}
		payment := a.paymentType
		if payment == "" {
			payment = "cpm"
		}
		products := []any{map[string]any{"nm_id": 202, "bids_kopecks": map[string]any{"search": a.bidWB, "recommendations": a.bidRecommendations}}}
		if a.emptyProducts {
			products = []any{}
		}
		if a.duplicateProduct {
			products = append(products, products[0])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"adverts": []any{map[string]any{"id": 101, "status": status, "name": a.name, "type": 9, "dailyBudget": 1000, "bid_type": "manual", "settings": map[string]any{"name": a.name, "payment_type": payment, "placements": map[string]bool{"search": true, "recommendations": true}}, "nm_settings": products}}})
		return
	}
	if r.URL.Path == "/api/advert/v0/daily-limits" && r.Method == "GET" {
		_ = json.NewEncoder(w).Encode(map[string]any{"adverts": []any{map[string]any{"advertId": 101, "enabled": a.dailyLimitEnabled, "dailyLimit": a.dailyLimitKopecks, "spentToday": a.spentTodayKopecks, "carryOverEnabled": a.carryOver, "valid": true, "requiredLimit": 3001, "currency": a.dailyLimitCurrency}}})
		return
	}
	if r.URL.Path == "/api/advert/v1/config" && r.Method == "GET" {
		_ = json.NewEncoder(w).Encode(map[string]any{"currency": a.dailyLimitCurrency, "currencyCode": 643, "minDailyLimit": a.minimumDailyLimitKopecks})
		return
	}
	if r.URL.Path == "/api/client/campaign" {
		status := "CAMPAIGN_STATE_RUNNING"
		if a.paused {
			status = "CAMPAIGN_STATE_INACTIVE"
		}
		row := map[string]any{"id": "101", "state": status, "title": a.name, "advObjectType": "SKU", "weeklyBudget": a.weeklyBudget, "productAutopilotStrategy": "TARGET_BIDS", "placement": "PLACEMENT_SEARCH"}
		if a.dailyBudgetOzon != "" {
			row["dailyBudget"] = a.dailyBudgetOzon
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"list": []any{row}})
		return
	}
	if r.URL.Path == "/api/advert/v2/budget" {
		if r.Method != "POST" {
			w.WriteHeader(400)
			return
		}
		var ids struct {
			IDs []int64 `json:"advertIds"`
		}
		if json.NewDecoder(r.Body).Decode(&ids) != nil || len(ids.IDs) != 1 || ids.IDs[0] != 101 {
			w.WriteHeader(400)
			return
		}
		row := map[string]any{"advertId": 101, "currency": a.budgetCurrency}
		if !a.budgetMissing {
			row["total"] = a.budget
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"adverts": []any{row}})
		return
	}
	if r.URL.Path == "/api/client/campaign/101/v2/products" {
		if a.productPagination {
			_ = json.NewEncoder(w).Encode(map[string]any{"products": []any{map[string]any{"sku": "202", "bid": a.bidOzon}}, "hasNext": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"products": func() []any {
			p := []any{map[string]any{"sku": "202", "bid": a.bidOzon}}
			if a.emptyProducts {
				return []any{}
			}
			if a.duplicateProduct {
				return append(p, p[0])
			}
			return p
		}()})
		return
	}
	if r.URL.Path == "/api/advert/v1/bids/min" {
		_ = json.NewEncoder(w).Encode(map[string]any{"bids": []any{map[string]any{"nm_id": 202, "bids": []any{map[string]any{"type": "search", "value": a.minimumWB}, map[string]any{"type": "recommendation", "value": a.minimumWB}}}}})
		return
	}
	if r.URL.Path == "/api/client/min/sku" {
		_ = json.NewEncoder(w).Encode(map[string]any{"minBids": []any{map[string]any{"sku": "202", "bid": a.minimumOzon}}})
		return
	}
	a.writes.Add(1)
	var proposedWB *int64
	var proposedOzon string
	var bidPlacement string
	var budgetWB *struct {
		IDs     []int64 `json:"advertIds"`
		Enabled *bool   `json:"enabled"`
		Amount  *int64  `json:"dailyLimit"`
		Carry   *bool   `json:"carryOverEnabled"`
	}
	var budgetOzon map[string]string
	if a.market == "wb" && r.URL.Path == "/api/advert/v0/daily-limits" {
		budgetWB = &struct {
			IDs     []int64 `json:"advertIds"`
			Enabled *bool   `json:"enabled"`
			Amount  *int64  `json:"dailyLimit"`
			Carry   *bool   `json:"carryOverEnabled"`
		}{}
		if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(budgetWB) != nil || len(budgetWB.IDs) != 1 || budgetWB.IDs[0] != 101 || budgetWB.Enabled == nil {
			w.WriteHeader(400)
			return
		}
		if *budgetWB.Enabled {
			if budgetWB.Amount == nil || *budgetWB.Amount <= 0 || budgetWB.Carry == nil {
				w.WriteHeader(400)
				return
			}
		} else if budgetWB.Amount != nil || budgetWB.Carry != nil {
			w.WriteHeader(400)
			return
		}
	}
	if a.market == "ozon" && r.URL.Path == "/api/client/campaign/101" {
		if r.Method != "PATCH" || json.NewDecoder(r.Body).Decode(&budgetOzon) != nil || len(budgetOzon) != 1 {
			w.WriteHeader(400)
			return
		}
		for key, value := range budgetOzon {
			amount, err := strconv.ParseInt(value, 10, 64)
			if key != "dailyBudget" && key != "weeklyBudget" || err != nil || amount < 0 {
				w.WriteHeader(400)
				return
			}
		}
	}
	if a.market == "wb" && r.URL.Path == "/api/advert/v1/bids" {
		var payload struct {
			Bids []struct {
				Advert int64 `json:"advert_id"`
				Items  []struct {
					ID        int64  `json:"nm_id"`
					Bid       int64  `json:"bid_kopecks"`
					Placement string `json:"placement"`
				} `json:"nm_bids"`
			} `json:"bids"`
		}
		if r.Method != "PATCH" || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Bids) != 1 || payload.Bids[0].Advert != 101 || len(payload.Bids[0].Items) != 1 || payload.Bids[0].Items[0].ID != 202 || payload.Bids[0].Items[0].Bid <= 0 {
			w.WriteHeader(400)
			return
		}
		proposedWB = &payload.Bids[0].Items[0].Bid
		bidPlacement = payload.Bids[0].Items[0].Placement
		if bidPlacement != "search" && bidPlacement != "recommendations" {
			w.WriteHeader(400)
			return
		}
	}
	if a.market == "ozon" && r.URL.Path == "/api/client/campaign/101/products" {
		var payload struct {
			Bids []struct {
				SKU string `json:"sku"`
				Bid string `json:"bid"`
			} `json:"bids"`
		}
		if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Bids) != 1 || payload.Bids[0].SKU != "202" {
			w.WriteHeader(400)
			return
		}
		value, err := strconv.ParseInt(payload.Bids[0].Bid, 10, 64)
		if err != nil || value <= 0 {
			w.WriteHeader(400)
			return
		}
		proposedOzon = payload.Bids[0].Bid
	}
	if proposedWB == nil && budgetWB == nil && a.market == "wb" && (r.Method != "GET" || (r.URL.Path != "/adv/v0/pause" && r.URL.Path != "/adv/v0/start") || r.URL.Query().Get("id") != "101") {
		w.WriteHeader(400)
		return
	}
	if proposedOzon == "" && budgetOzon == nil && a.market == "ozon" && (r.Method != "POST" || (r.URL.Path != "/api/client/campaign/101/deactivate" && r.URL.Path != "/api/client/campaign/101/activate")) {
		w.WriteHeader(400)
		return
	}
	if a.mode == "reject" {
		w.WriteHeader(400)
		return
	}
	if a.mode == "unknown" {
		w.WriteHeader(503)
		return
	}
	if a.mode != "pending" {
		if budgetWB != nil {
			a.dailyLimitEnabled = *budgetWB.Enabled
			if *budgetWB.Enabled {
				a.dailyLimitKopecks = *budgetWB.Amount
				a.carryOver = *budgetWB.Carry
			}
		} else if budgetOzon != nil {
			if value, ok := budgetOzon["weeklyBudget"]; ok {
				a.weeklyBudget = value
			}
			if value, ok := budgetOzon["dailyBudget"]; ok {
				a.dailyBudgetOzon = value
			}
		} else if proposedWB != nil {
			if bidPlacement == "search" {
				a.bidWB = *proposedWB
			} else {
				a.bidRecommendations = *proposedWB
			}
		} else if proposedOzon != "" {
			a.bidOzon = proposedOzon
		} else {
			a.ready = false
			a.paused = r.URL.Path == "/adv/v0/pause" || r.URL.Path == "/api/client/campaign/101/deactivate"
		}
	}
	if a.mode == "lost" {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	_, _ = w.Write([]byte(`{}`))
}

type pauseEnv struct {
	svc                                 *ManagerAdOperationsService
	q                                   *sqlcgen.Queries
	pool                                *pgxpool.Pool
	workspace, actor, cabinet, campaign uuid.UUID
	authority                           *pauseAuthority
	api                                 *pauseNativeAPI
	wb                                  *wb.Client
	perf                                *ozon.PerfClient
	token                               string
}

func newPauseEnv(t *testing.T, market, mode string) *pauseEnv {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	q := sqlcgen.New(pool)
	workspace := testdb.Workspace(t, pool)
	actor := uuid.New()
	var cabinet, campaign uuid.UUID
	api := &pauseNativeAPI{market: market, name: "Native campaign", mode: mode, budget: 1234, budgetCurrency: "RUB", weeklyBudget: "100000001", bidWB: 1234, bidRecommendations: 5678, bidOzon: "12340000", minimumWB: 100, minimumOzon: "5000000"}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	token := testAdvertisingToken()
	if market == "wb" {
		cabinet = testdb.WBCabinet(t, pool, workspace)
		encrypted, err := crypto.Encrypt(token, testdb.EncryptionKey)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, cabinet, encrypted)
		require.NoError(t, err)
		err = pool.QueryRow(ctx, `INSERT INTO campaigns(workspace_id,seller_cabinet_id,wb_campaign_id,name,status,bid_type,payment_type) VALUES($1,$2,101,'Old campaign','active','manual','cpm') RETURNING id`, workspace, cabinet).Scan(&campaign)
		require.NoError(t, err)
	} else {
		cabinet = testdb.OzonCabinet(t, pool, workspace, testdb.OzonCredentials())
		campaign = testdb.OzonCampaign(t, pool, cabinet, 101, "Old campaign", "CAMPAIGN_STATE_RUNNING")
	}
	_, err := pool.Exec(ctx, `UPDATE seller_cabinets SET external_integration_id='28' WHERE id=$1`, cabinet)
	require.NoError(t, err)
	cfg := &config.Config{WBAPIBaseURL: srv.URL, WBAPIRateLimit: 10000, OzonPerfAPIBaseURL: srv.URL}
	wbClient := wb.NewClient(cfg, zerolog.Nop()).WithAdGuard(q)
	perf := ozon.NewPerfClient(cfg, zerolog.Nop()).WithAdGuard(q)
	auth := &pauseAuthority{market: market, personal: true}
	reader := NewManagerAdContextService(q, auth, wbClient, perf, testdb.EncryptionKey)
	return &pauseEnv{NewManagerAdOperationsService(q, auth, reader, true), q, pool, workspace, actor, cabinet, campaign, auth, api, wbClient, perf, token}
}
func (e *pauseEnv) prepare(t *testing.T) *domain.ManagerAdOperation {
	t.Helper()
	o, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "pause")
	require.NoError(t, err)
	require.True(t, o.ApprovalAvailable)
	return o
}
func (e *pauseEnv) approve(o *domain.ManagerAdOperation) (*domain.ManagerAdOperation, error) {
	return e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint, map[string]bool{"campaign_checked": true, "pause_impact_checked": true})
}

func TestManagerAdPausePersonalApprovalObservesBothMarketplacesAndKeepsIntent(t *testing.T) {
	for _, market := range []string{"wb", "ozon"} {
		t.Run(market, func(t *testing.T) {
			e := newPauseEnv(t, market, "ok")
			o := e.prepare(t)
			require.Equal(t, int32(0), e.api.writes.Load())
			raw, err := json.Marshal(o)
			require.NoError(t, err)
			require.NotContains(t, string(raw), e.token)
			require.NotContains(t, string(raw), "credential_digest")
			require.NotContains(t, string(raw), "AccountKey")
			_, err = e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint, nil)
			require.Error(t, err)
			require.Zero(t, e.api.writes.Load())
			result, err := e.approve(o)
			require.NoError(t, err)
			require.Equal(t, "succeeded", result.Status)
			require.True(t, result.Result.FieldsVerified)
			require.True(t, result.Result.SubmissionAcknowledged)
			require.True(t, result.Result.ControlHoldActive)
			require.Equal(t, int32(1), e.api.writes.Load())
			result, err = e.approve(o)
			require.NoError(t, err)
			require.Equal(t, "succeeded", result.Status)
			require.Equal(t, int32(1), e.api.writes.Load())
			if market == "wb" {
				err = e.wb.StartCampaign(context.Background(), e.token, 101)
			} else {
				err = e.perf.ActivateCampaign(context.Background(), ozonClientCreds(testdb.OzonCredentials()), 101)
			}
			require.True(t, adguard.WasNotSent(err))
			require.Equal(t, int32(1), e.api.writes.Load(), "legacy and autopilot clients cannot undo the managed pause")
			e.svc.enabled = false
			result, err = e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
			require.NoError(t, err)
			require.Equal(t, "succeeded", result.Status)
		})
	}
}
func TestManagerAdPauseLostResponseAndPendingObservationNeverReplay(t *testing.T) {
	for _, mode := range []string{"lost", "pending", "unknown", "reject"} {
		t.Run(mode, func(t *testing.T) {
			e := newPauseEnv(t, "wb", mode)
			o := e.prepare(t)
			result, err := e.approve(o)
			require.NoError(t, err)
			want := map[string]string{"lost": "succeeded", "pending": "verifying", "unknown": "unknown", "reject": "failed"}[mode]
			require.Equal(t, want, result.Status)
			require.Equal(t, int32(1), e.api.writes.Load())
			if mode == "lost" {
				require.False(t, result.Result.SubmissionAcknowledged)
				require.True(t, result.Result.FieldsVerified)
			}
			if mode == "pending" {
				e.api.mu.Lock()
				e.api.paused = true
				e.api.mu.Unlock()
			}
			for i := 0; i < 2; i++ {
				result, err = e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
				require.NoError(t, err)
			}
			if mode == "pending" {
				require.Equal(t, "succeeded", result.Status)
			}
			require.Equal(t, int32(1), e.api.writes.Load())
		})
	}
}
func TestManagerAdPauseRechecksRightsAndCredentialsUnderItsHold(t *testing.T) {
	for _, change := range []string{"rights", "snapshot", "credentials"} {
		t.Run(change, func(t *testing.T) {
			e := newPauseEnv(t, "wb", "ok")
			o := e.prepare(t)
			if change == "rights" {
				e.authority.revokeAt = 3
			} else {
				e.authority.hook = func(n int32) {
					if n != 3 {
						return
					}
					if change == "snapshot" {
						e.api.mu.Lock()
						e.api.name = "Changed campaign"
						e.api.mu.Unlock()
					} else {
						encrypted, err := crypto.Encrypt(strings.TrimSuffix(e.token, ".test")+".rotated", testdb.EncryptionKey)
						require.NoError(t, err)
						_, err = e.pool.Exec(context.Background(), `UPDATE seller_cabinets SET encrypted_token=$2 WHERE id=$1`, e.cabinet, encrypted)
						require.NoError(t, err)
					}
				}
			}
			result, err := e.approve(o)
			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Zero(t, e.api.writes.Load())
			native, err := e.q.ReadNativeAdCommand(context.Background(), o.ID)
			require.NoError(t, err)
			require.False(t, native.StartedAt.Valid)
			require.Equal(t, "rejected", native.Status)
		})
	}
}
func TestManagerAdPauseRequiresAnExplicitPersonalProfileAndExactOwner(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	e.authority.personal = false
	_, err := e.svc.Prepare(context.Background(), managerAdsIdentity(), e.workspace, e.actor, "personal-token", "28", e.campaign, "pause")
	require.Error(t, err)
	require.Zero(t, e.api.writes.Load())
	e.authority.personal = true
	o := e.prepare(t)
	_, err = e.svc.Approve(context.Background(), managerAdsIdentity(), e.workspace, uuid.New(), o.ID, "personal-token", o.Fingerprint, map[string]bool{"campaign_checked": true, "pause_impact_checked": true})
	require.Error(t, err)
	require.Zero(t, e.api.writes.Load())
}

func TestManagerAdProposalFingerprintSurvivesStorage(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	original := e.prepare(t)
	stored, err := e.q.GetManagerAdOperation(context.Background(), original.ID, e.workspace, e.actor)
	require.NoError(t, err)
	require.Equal(t, original.CreatedAt.UTC(), stored.CreatedAt.UTC())
	require.Equal(t, original.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), stored.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	require.Equal(t, original.Snapshot, stored.Snapshot)
	require.Equal(t, original.Request, stored.Request)
	require.Equal(t, original.Fingerprint, adOperationFingerprint(stored))
}

func TestManagerAdPauseConcurrentApprovalsDispatchOnce(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	o := e.prepare(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = e.approve(o) }()
	}
	wg.Wait()
	require.Equal(t, int32(1), e.api.writes.Load())
	result, err := e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
}

func TestManagerAdPauseCancelledOrExpiredProposalCannotSend(t *testing.T) {
	for _, mode := range []string{"cancelled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			e := newPauseEnv(t, "wb", "ok")
			o := e.prepare(t)
			if mode == "cancelled" {
				_, err := e.svc.Cancel(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token", o.Fingerprint)
				require.NoError(t, err)
			} else {
				// Expiry must remain part of the immutable stored proposal.
				o.CreatedAt = o.CreatedAt.Add(-10 * 60 * 1e9)
				o.ExpiresAt = o.ExpiresAt.Add(-10 * 60 * 1e9)
				o.Fingerprint = adOperationFingerprint(*o)
				_, err := e.pool.Exec(context.Background(), `UPDATE manager_ad_operations SET created_at=$2,expires_at=$3,fingerprint=$4 WHERE id=$1`, o.ID, o.CreatedAt, o.ExpiresAt, o.Fingerprint)
				require.NoError(t, err)
			}
			_, _ = e.approve(o)
			require.Zero(t, e.api.writes.Load())
		})
	}
}

func TestManagerAdPauseAuthFailureAfterSendCannotBeReportedAsRejected(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	o := e.prepare(t)
	e.authority.denyAt = 4
	result, err := e.approve(o)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEqual(t, "failed", result.Status)
	require.Equal(t, int32(1), e.api.writes.Load())
	e.authority.denyAt = 0
	result, err = e.svc.Receipt(context.Background(), managerAdsIdentity(), e.workspace, e.actor, o.ID, "personal-token")
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
	require.Equal(t, int32(1), e.api.writes.Load())
}

// Optional cross-language contract capture is isolated to this test and /tmp.
func TestManagerAdPausePublicWireContract(t *testing.T) {
	e := newPauseEnv(t, "wb", "ok")
	prepared := e.prepare(t)
	finished, err := e.approve(prepared)
	require.NoError(t, err)
	require.Equal(t, "succeeded", finished.Status)
	if dir := os.Getenv("SELYA_AD_TEST_CONTRACT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		for name, value := range map[string]*domain.ManagerAdOperation{"prepared": prepared, "finished": finished} {
			raw, err := json.Marshal(map[string]any{"data": value})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, name+".json"), raw, 0600))
		}
	}
}

func TestManagerAdPauseMigrationDowngradePreservesManagedControls(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000071_manager_ad_operations.down.sql")
	require.NoError(t, err)
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "managed_pause"}[managed], func(t *testing.T) {
			e := newPauseEnv(t, "wb", "ok")
			o := e.prepare(t)
			if managed {
				result, err := e.approve(o)
				require.NoError(t, err)
				require.Equal(t, "succeeded", result.Status)
			}
			tx, err := e.pool.Begin(context.Background())
			require.NoError(t, err)
			defer tx.Rollback(context.Background())
			_, err = tx.Exec(context.Background(), string(raw))
			if managed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
