package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/stretchr/testify/require"
)

func managerApproval(marketplace string) ApprovedPriceCommand {
	return ApprovedPriceCommand{Version: "manager_price_approval_v1", OperationID: uuid.New(), ProductID: uuid.New(), ExternalWorkspaceID: "3", ExternalUserID: "7", IntegrationID: "28",
		ProposalFingerprint: strings.Repeat("a", 64), FinancialReviewHash: strings.Repeat("b", 64), CredentialBinding: strings.Repeat("c", 64), Marketplace: marketplace,
		IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(4 * time.Minute)}
}

func managerWBBody(base int64, discount int) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(wbObservedPriceBody(101, base, discount, false)), &body); err != nil {
		panic(err)
	}
	goods := body["data"].(map[string]any)["listGoods"].([]any)
	goods[0].(map[string]any)["wholesaleDiscountThreshold"] = []any{}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func newManagerWBFixture(t *testing.T) (*wbPriceReadbackFixture, ApprovedPriceCommand, uuid.UUID) {
	t.Helper()
	f := newWBPriceReadbackFixture(t)
	f.prices.Store(managerWBBody(1000, 17))
	snapshots, err := f.svc.wbClient.ReadPriceSnapshots(context.Background(), "test", []int64{101})
	require.NoError(t, err)
	before := snapshots[101]
	c := managerApproval("wb")
	c.BeforeWB = &before
	c.WBBasePriceRub = 1200
	c.SellerPriceCents = 99600
	f.svc.managerPriceValidator = func(context.Context, ApprovedPriceCommand) error { return nil }
	c.ExecutorCredentialsDigest = ManagerPriceCredentialsDigest("wb", "", testAdvertisingToken())
	_, err = f.pool.Exec(context.Background(), `UPDATE seller_cabinets SET external_integration_id=$2 WHERE id=$1`, f.cab, c.IntegrationID)
	require.NoError(t, err)
	u, err := f.q.CreateUser(context.Background(), sqlcgen.CreateUserParams{Email: uuid.NewString() + "@test.invalid", PasswordHash: "test-only-unused", Name: "Manager test"})
	require.NoError(t, err)
	return f, c, uuidFromPgtype(u.ID)
}

func TestManagerWBExactPriceUsesSharedClaimAndOneIntentWithoutChangingTheConfirmedTarget(t *testing.T) {
	f, c, actor := newManagerWBFixture(t)
	ctx := context.Background()
	calls := 0
	f.svc.managerPriceValidator = func(_ context.Context, approval ApprovedPriceCommand) error {
		calls++
		require.Equal(t, c.OperationID, approval.OperationID)
		return nil
	}
	f.onUpload.Store(func() { f.prices.Store(managerWBBody(1200, 17)) })
	receipt, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, "verifying", receipt.Status)
	require.Equal(t, int32(1), f.uploads.Load())
	require.GreaterOrEqual(t, calls, 2)
	row, err := f.q.GetPriceChange(ctx, uuidToPgtype(receipt.NativeChangeID))
	require.NoError(t, err)
	require.Equal(t, int64(1200), row.NewPriceRub)
	require.Equal(t, int32(17), row.NewDiscountPercent)
	for i := 0; i < 3; i++ {
		again, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
		require.NoError(t, err)
		require.Equal(t, receipt.NativeChangeID, again.NativeChangeID)
	}
	require.Equal(t, int32(1), f.uploads.Load())
	_, err = f.svc.PollUploadTasks(ctx, f.ws)
	require.NoError(t, err)
	final, err := f.svc.ManagerPriceReceipt(ctx, f.ws, actor, c.OperationID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	changed := c
	changed.WBBasePriceRub = 1300
	changed.SellerPriceCents = 107900
	_, err = f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, changed)
	require.ErrorIs(t, err, sqlcgen.ErrManagerPriceCommandConflict)
	_, err = f.svc.ManagerPriceReceipt(ctx, f.ws, uuid.New(), c.OperationID)
	require.Error(t, err)
	_, err = f.svc.ManagerPriceReceipt(ctx, uuid.New(), actor, c.OperationID)
	require.Error(t, err)
	require.Equal(t, int32(1), f.uploads.Load())
}

func TestManagerWBApprovalIsRecheckedUnderItsPhysicalHoldAndCannotFallbackToAnOrdinaryQueue(t *testing.T) {
	f, c, actor := newManagerWBFixture(t)
	ctx := context.Background()
	calls := 0
	f.svc.managerPriceValidator = func(context.Context, ApprovedPriceCommand) error {
		calls++
		if calls >= 2 {
			return errors.New("financial settings changed")
		}
		return nil
	}
	receipt, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
	require.Error(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, "failed", receipt.Status)
	require.Zero(t, f.uploads.Load())
	f.svc.managerPriceValidator = nil
	again, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
	require.NoError(t, err)
	require.Equal(t, "failed", again.Status)
	_, err = f.svc.drainPendingPriceChanges(ctx, f.ws)
	require.NoError(t, err)
	require.Zero(t, f.uploads.Load())
}

func TestManagerWBUnknownNativeConditionsAndUnrepresentableTargetsNeverReachUpload(t *testing.T) {
	f, c, actor := newManagerWBFixture(t)
	ctx := context.Background()
	c.SellerPriceCents++
	_, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
	require.Error(t, err)
	require.Zero(t, f.uploads.Load())
	c.SellerPriceCents--
	f.prices.Store(wbObservedPriceBody(101, 1000, 17, false)) // no wholesale observation
	receipt, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c)
	require.Error(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, "failed", receipt.Status)
	require.Zero(t, f.uploads.Load())
}

// Only the native boundary is replaced. It updates observed marketing price
// with the seller price, independently of any database expectation.
type managerObservedOzonWriter struct{ *observedPriceWriter }

func (v *managerObservedOzonWriter) UpdatePrices(ctx context.Context, creds ozon.Credentials, items []ozon.PriceUpdate) ([]ozon.PriceUpdateResult, error) {
	rows, err := v.observedPriceWriter.UpdatePrices(ctx, creds, items)
	v.mu.Lock()
	defer v.mu.Unlock()
	for id, p := range v.prices {
		if p.Conditions != nil {
			condition := *p.Conditions
			condition.MarketingPriceCents = p.PriceCents
			p.Conditions = &condition
			v.prices[id] = p
		}
	}
	return rows, err
}

func newManagerOzonFixture(t *testing.T) (*OzonRepricerService, *observedPriceWriter, uuid.UUID, uuid.UUID, uuid.UUID, ApprovedPriceCommand) {
	t.Helper()
	old, min := int64(200000), int64(50000)
	v := &observedPriceWriter{prices: map[int64]ozon.PriceSnapshot{101: {ProductID: 101, OfferID: "TEST-101", PriceCents: 100000, OldPriceCents: &old, MinPriceCents: &min, Conditions: &ozon.PriceConditions{MarketingPriceCents: 100000}}}}
	s, _, ws, cab := newObservedRepricer(t, v)
	s.sellerClient = &managerObservedOzonWriter{v}
	s.managerPriceValidator = func(context.Context, ApprovedPriceCommand) error { return nil }
	c := managerApproval("ozon")
	p := v.prices[101]
	c.BeforeOzon = &p
	c.SellerPriceCents = 120001
	c.ExecutorCredentialsDigest = ManagerPriceCredentialsDigest("ozon", v.creds.ClientID, v.creds.APIKey)
	_, err := v.pool.Exec(context.Background(), `UPDATE seller_cabinets SET external_integration_id=$2 WHERE id=$1`, cab, c.IntegrationID)
	require.NoError(t, err)
	return s, v, ws, cab, uuid.New(), c
}

func TestManagerOzonExactKopecksAndPreservedFieldsUseOneDurableNativeCommand(t *testing.T) {
	s, v, ws, cab, actor, c := newManagerOzonFixture(t)
	ctx := context.Background()
	calls := 0
	s.managerPriceValidator = func(context.Context, ApprovedPriceCommand) error { calls++; return nil }
	receipt, err := s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, "succeeded", receipt.Status)
	require.Equal(t, 1, v.writes)
	require.GreaterOrEqual(t, calls, 3)
	require.Equal(t, int64(120001), v.prices[101].PriceCents)
	require.Equal(t, int64(200000), *v.prices[101].OldPriceCents)
	require.Equal(t, int64(50000), *v.prices[101].MinPriceCents)
	for i := 0; i < 3; i++ {
		again, err := s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
		require.NoError(t, err)
		require.Equal(t, receipt.NativeChangeID, again.NativeChangeID)
	}
	require.Equal(t, 1, v.writes)
	changed := c
	changed.SellerPriceCents = 125001
	_, err = s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, changed)
	require.ErrorIs(t, err, sqlcgen.ErrManagerPriceCommandConflict)
}

func TestManagerOzonLostReceiptAndRestartOnlyReadTheOriginalCommand(t *testing.T) {
	s, v, ws, cab, actor, c := newManagerOzonFixture(t)
	ctx := context.Background()
	v.lost = true
	receipt, err := s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
	require.Error(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, "unknown", receipt.Status)
	require.Equal(t, 1, v.writes)
	restarted := &OzonRepricerService{queries: s.queries, sellerClient: s.sellerClient, encryptionKey: s.encryptionKey, logger: s.logger}
	again, err := restarted.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
	require.NoError(t, err)
	require.Equal(t, receipt.NativeChangeID, again.NativeChangeID)
	require.Equal(t, "unknown", again.Status)
	require.Equal(t, 1, v.writes)
}

func TestManagerOzonChangedConditionsOrFinancialApprovalUnderHoldPreventTransport(t *testing.T) {
	for _, financial := range []bool{false, true} {
		t.Run(map[bool]string{false: "promotions", true: "financial_inputs"}[financial], func(t *testing.T) {
			s, v, ws, cab, actor, c := newManagerOzonFixture(t)
			calls := 0
			if financial {
				s.managerPriceValidator = func(context.Context, ApprovedPriceCommand) error {
					calls++
					if calls >= 3 {
						return errors.New("inputs changed")
					}
					return nil
				}
			} else {
				v.onRead = func(v *observedPriceWriter) {
					if v.reads == 2 {
						p := v.prices[101]
						condition := *p.Conditions
						condition.AutoActionEnabled = true
						p.Conditions = &condition
						v.prices[101] = p
					}
				}
			}
			receipt, err := s.ApplyApprovedManagerPrice(context.Background(), ws, cab, actor, c)
			require.Error(t, err)
			require.NotNil(t, receipt)
			require.Equal(t, "failed", receipt.Status)
			require.Zero(t, v.writes)
		})
	}
}

func TestManagerPriceCommandsRequireAnExplicitFinancialValidatorAndFreshGrant(t *testing.T) {
	s, v, ws, cab, actor, c := newManagerOzonFixture(t)
	s.managerPriceValidator = nil
	_, err := s.ApplyApprovedManagerPrice(context.Background(), ws, cab, actor, c)
	require.Error(t, err)
	require.Zero(t, v.writes)
	s.managerPriceValidator = func(context.Context, ApprovedPriceCommand) error { return nil }
	c.IssuedAt = time.Now().Add(-10 * time.Minute)
	c.ExpiresAt = c.IssuedAt.Add(4 * time.Minute)
	_, err = s.ApplyApprovedManagerPrice(context.Background(), ws, cab, actor, c)
	require.Error(t, err)
	require.Zero(t, v.writes)
}

func TestManagerWBConcurrentApprovalCreatesExactlyOneNativeCommand(t *testing.T) {
	f, c, actor := newManagerWBFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() { _, err := f.svc.ApplyApprovedManagerPrice(ctx, f.ws, f.cab, actor, c); errCh <- err })
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), f.uploads.Load())
	var count int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM price_changes WHERE seller_cabinet_id=$1`, f.cab).Scan(&count))
	require.Equal(t, 1, count)
}

func TestManagerPriceCabinetBindingAndCredentialRotationPreventWrites(t *testing.T) {
	for _, change := range []string{"integration", "credentials"} {
		t.Run("wb_"+change, func(t *testing.T) {
			f, c, actor := newManagerWBFixture(t)
			if change == "integration" {
				c.IntegrationID = "29"
			} else {
				c.ExecutorCredentialsDigest = strings.Repeat("0", 64)
			}
			_, err := f.svc.ApplyApprovedManagerPrice(context.Background(), f.ws, f.cab, actor, c)
			require.Error(t, err)
			require.Zero(t, f.uploads.Load())
		})
		t.Run("ozon_"+change, func(t *testing.T) {
			s, v, ws, cab, actor, c := newManagerOzonFixture(t)
			if change == "integration" {
				c.IntegrationID = "29"
			} else {
				c.ExecutorCredentialsDigest = strings.Repeat("0", 64)
			}
			_, err := s.ApplyApprovedManagerPrice(context.Background(), ws, cab, actor, c)
			require.Error(t, err)
			require.Zero(t, v.writes)
		})
	}
	t.Run("rotation_after_financial_check", func(t *testing.T) {
		s, v, ws, cab, actor, c := newManagerOzonFixture(t)
		calls := 0
		s.managerPriceValidator = func(context.Context, ApprovedPriceCommand) error {
			calls++
			if calls == 2 {
				_, err := v.pool.Exec(context.Background(), `UPDATE seller_cabinets SET external_integration_id='99' WHERE id=$1`, cab)
				return err
			}
			return nil
		}
		r, err := s.ApplyApprovedManagerPrice(context.Background(), ws, cab, actor, c)
		require.Error(t, err)
		require.NotNil(t, r)
		require.Equal(t, "failed", r.Status)
		require.Zero(t, v.writes)
	})
}

func TestManagerWBReadbackRequiresPreservedConditionsAndRiskGuardsRefuseExactTarget(t *testing.T) {
	t.Run("readback_missing_conditions", func(t *testing.T) {
		f, c, actor := newManagerWBFixture(t)
		f.onUpload.Store(func() { f.prices.Store(wbObservedPriceBody(101, 1200, 17, false)) })
		_, err := f.svc.ApplyApprovedManagerPrice(context.Background(), f.ws, f.cab, actor, c)
		require.NoError(t, err)
		_, err = f.svc.PollUploadTasks(context.Background(), f.ws)
		require.NoError(t, err)
		r, err := f.svc.ManagerPriceReceipt(context.Background(), f.ws, actor, c.OperationID)
		require.NoError(t, err)
		require.Equal(t, "verifying", r.Status)
		require.Equal(t, int32(1), f.uploads.Load())
	})
	for _, target := range []int64{300, 3000} {
		t.Run("unsafe_exact_target_"+time.Duration(target).String(), func(t *testing.T) {
			f, c, actor := newManagerWBFixture(t)
			c.WBBasePriceRub = target
			c.SellerPriceCents = target * 83
			_, err := f.svc.ApplyApprovedManagerPrice(context.Background(), f.ws, f.cab, actor, c)
			require.Error(t, err)
			require.Zero(t, f.uploads.Load())
		})
	}
}

func TestManagerPriceReceiptRejectsTamperedNativeApproval(t *testing.T) {
	s, v, ws, cab, actor, c := newManagerOzonFixture(t)
	r, err := s.ApplyApprovedManagerPrice(context.Background(), ws, cab, actor, c)
	require.NoError(t, err)
	_, err = v.pool.Exec(context.Background(), `UPDATE ozon_price_changes SET decision_context=jsonb_set(decision_context,'{manager_approval,seller_price_cents}','125001'::jsonb) WHERE id=$1`, r.NativeChangeID)
	require.NoError(t, err)
	_, err = s.ManagerPriceReceipt(context.Background(), ws, actor, c.OperationID)
	require.ErrorIs(t, err, sqlcgen.ErrManagerPriceCommandConflict)
	require.Equal(t, 1, v.writes)
}

func TestManagerOzonCrashBeforeClaimExpiresWithoutSendAndCannotBeRetried(t *testing.T) {
	s, v, ws, cab, actor, c := newManagerOzonFixture(t)
	ctx := context.Background()
	c.IssuedAt = time.Now().Add(-6 * time.Minute)
	c.ExpiresAt = c.IssuedAt.Add(4 * time.Minute)
	record, decision, err := managerCommandRecord(c, ws, cab, actor)
	require.NoError(t, err)
	_, created, err := s.queries.CreateManagerOzonPriceCommand(ctx, record, sqlcgen.InsertOzonPriceChangeParams{
		SellerCabinetID: record.SellerCabinetID, Sku: 101, OfferID: textToPgtype("TEST-101"),
		OldPriceRub: floatToPgNumeric(1000), NewPriceRub: floatToPgNumeric(1200.01), OldOldPriceRub: floatToPgNumeric(2000), NewOldPriceRub: floatToPgNumeric(2000), MinPriceRub: floatToPgNumeric(500), Source: "manual", Status: "pending", DecisionContext: decision})
	require.NoError(t, err)
	require.True(t, created)
	r, err := s.ManagerPriceReceipt(ctx, ws, actor, c.OperationID)
	require.NoError(t, err)
	require.Equal(t, "failed", r.Status)
	r, err = s.ApplyApprovedManagerPrice(ctx, ws, cab, actor, c)
	require.NoError(t, err)
	require.Equal(t, "failed", r.Status)
	require.Zero(t, v.writes)
}
