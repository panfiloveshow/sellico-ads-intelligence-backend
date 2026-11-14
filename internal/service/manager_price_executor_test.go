package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestManagerPriceApprovalsEmittedByRealProductsOwnerHaveTheExactNativeContract(t *testing.T) {
	for _, mp := range []string{"wb", "ozon"} {
		t.Run(mp, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/manager_price_approval_" + mp + ".json")
			require.NoError(t, err)
			var c ApprovedPriceCommand
			require.NoError(t, json.Unmarshal(raw, &c))
			require.NoError(t, c.validate(mp, false))
			client := ""
			if mp == "ozon" {
				client = "555"
			}
			require.Equal(t, ManagerPriceCredentialsDigest(mp, client, "vendor-secret"), c.ExecutorCredentialsDigest)
			record, _, err := managerCommandRecord(c, uuid.New(), uuid.New(), uuid.New())
			require.NoError(t, err)
			var decoded ApprovedPriceCommand
			require.NoError(t, json.Unmarshal(record.Approval, &decoded))
			require.NoError(t, decoded.validate(mp, false))
		})
	}
}

func TestManagerPriceFinancialValidatorRequiresExactLivePersonalVerdict(t *testing.T) {
	c := managerApproval("wb")
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		code   int
		pass   bool
	}{
		{"matching", func(map[string]any) {}, 200, true},
		{"other_operation", func(v map[string]any) { v["operation_id"] = uuid.NewString() }, 200, false},
		{"other_proposal", func(v map[string]any) { v["proposal_fingerprint"] = "invalid" }, 200, false},
		{"other_review", func(v map[string]any) { v["financial_review_hash"] = "invalid" }, 200, false},
		{"other_expiry", func(v map[string]any) { v["expires_at"] = c.ExpiresAt.Add(time.Second) }, 200, false},
		{"refused", func(v map[string]any) { v["execution_allowed"] = false }, 200, false},
		{"unavailable", func(map[string]any) {}, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "Bearer personal-price-session", r.Header.Get("Authorization"))
				require.Equal(t, "3", r.Header.Get("X-Sellico-Workspace"))
				require.Equal(t, "/manager/price-proposals/"+c.OperationID.String()+"/validate-execution", r.URL.Path)
				var sent ApprovedPriceCommand
				require.NoError(t, json.NewDecoder(r.Body).Decode(&sent))
				require.Equal(t, c.OperationID, sent.OperationID)
				v := map[string]any{"version": "manager_price_validation_v1", "operation_id": c.OperationID, "proposal_fingerprint": c.ProposalFingerprint, "financial_review_hash": c.FinancialReviewHash, "execution_allowed": true, "expires_at": c.ExpiresAt}
				tc.mutate(v)
				w.WriteHeader(tc.code)
				require.NoError(t, json.NewEncoder(w).Encode(v))
			}))
			defer server.Close()
			validate := NewManagerPriceFinancialValidator(server.URL)
			require.Error(t, validate(context.Background(), c))
			require.Zero(t, calls)
			err := validate(context.WithValue(context.Background(), managerPriceSessionKey{}, "personal-price-session"), c)
			if tc.pass {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, 1, calls)
		})
	}
}

type managerPriceCRM struct{ integration *domain.ManagerIntegration }

func (v managerPriceCRM) VerifyManagerIntegration(context.Context, string, string, string) (*domain.ManagerIntegration, error) {
	return v.integration, nil
}

func TestManagerPriceExecutorChecksScopeAndReconcilesWBByReadingOneNativeTask(t *testing.T) {
	f, c, actor := newManagerWBFixture(t)
	access := domain.ManagerAccess{ExternalUserID: "7", ExternalWorkspaceID: "3", Permissions: []string{"products.view", "marketing.view", "marketing.manage", "unit_economics.view", "unit_economics.calculate", "integrations.view"}}
	crm := managerPriceCRM{&domain.ManagerIntegration{ID: c.IntegrationID, WorkspaceID: "3", Marketplace: "wb"}}
	executor := NewManagerPriceExecutor(f.q, crm, f.svc, &OzonRepricerService{}, false)
	_, err := executor.Apply(context.Background(), access, f.ws, actor, "session", c)
	require.Error(t, err)
	require.Zero(t, f.uploads.Load())
	executor.enabled = true
	wrong := c
	wrong.ExternalUserID = "8"
	_, err = executor.Apply(context.Background(), access, f.ws, actor, "session", wrong)
	require.Error(t, err)
	require.Zero(t, f.uploads.Load())
	f.onUpload.Store(func() { f.prices.Store(managerWBBody(1200, 17)) })
	r, err := executor.Apply(context.Background(), access, f.ws, actor, "session", c)
	require.NoError(t, err)
	require.Equal(t, "verifying", r.Status)
	executor.enabled = false // A stop must leave read-only recovery available.
	r, err = executor.Receipt(context.Background(), access, f.ws, actor, c.OperationID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", r.Status)
	require.Equal(t, int32(1), f.uploads.Load())
}
