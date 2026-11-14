package service

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func priceRepositoryControl(t *testing.T, q *sqlcgen.Queries, account, mode string, lifetime time.Duration) domain.PriceExecutionControlOperation {
	t.Helper()
	ctx := context.Background()
	before, err := q.PriceExecutionControl(ctx, account, "wb")
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := domain.PriceExecutionControlOperation{ID: uuid.New(), WorkspaceID: uuid.New(), ActorID: uuid.New(), ExternalUserID: "7", ExternalWorkspaceID: "3", IntegrationID: "28", IntegrationName: "Isolated price control", CabinetID: uuid.New(), Marketplace: "wb", AccountKey: account, CredentialDigest: strings.Repeat("a", 64), Scope: priceAccountControlScope, TargetMode: mode, Before: before, Proof: domain.PriceExecutionAccountProof{ResourceID: "101", Source: "WB Prices API", ObservedAt: now}, Reason: "Explicit isolated control check", Fingerprint: strings.Repeat("b", 64), Status: "awaiting_confirmation", CreatedAt: now, ExpiresAt: now.Add(lifetime)}
	require.NoError(t, q.CreatePriceExecutionControlOperation(ctx, o))
	return o
}

func TestPriceAccountConcurrentDecisionsCannotOverwriteEachOthersExpectedVersion(t *testing.T) {
	q := sqlcgen.New(testdb.New(t))
	ctx := context.Background()
	account := "wb:" + strings.Repeat("a", 64)
	stop := priceRepositoryControl(t, q, account, "stopped", 5*time.Minute)
	other := priceRepositoryControl(t, q, account, "manual_only", 5*time.Minute)
	var group sync.WaitGroup
	result := make(chan error, 2)
	for _, o := range []domain.PriceExecutionControlOperation{stop, other} {
		group.Add(1)
		go func(o domain.PriceExecutionControlOperation) {
			defer group.Done()
			result <- q.ApplyPriceExecutionControl(ctx, o, func(context.Context) error { return nil })
		}(o)
	}
	group.Wait()
	close(result)
	successes := 0
	for err := range result {
		if err == nil {
			successes++
		} else {
			require.True(t, errors.Is(err, sqlcgen.ErrPriceExecutionAccountHeld) || errors.Is(err, sqlcgen.ErrPriceExecutionControlChanged), err)
		}
	}
	require.Equal(t, 1, successes)
	state, err := q.PriceExecutionControl(ctx, account, "wb")
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Version)
}

func TestPriceAccountExpiryDuringFinalAccessCheckRollsBackBothControlAndReceipt(t *testing.T) {
	q := sqlcgen.New(testdb.New(t))
	ctx := context.Background()
	account := "wb:" + strings.Repeat("b", 64)
	o := priceRepositoryControl(t, q, account, "stopped", 2*time.Second)
	err := q.ApplyPriceExecutionControl(ctx, o, func(context.Context) error {
		require.True(t, time.Now().Before(o.ExpiresAt))
		time.Sleep(time.Until(o.ExpiresAt) + 20*time.Millisecond)
		return nil
	})
	require.ErrorIs(t, err, sqlcgen.ErrPriceExecutionControlChanged)
	state, err := q.PriceExecutionControl(ctx, account, "wb")
	require.NoError(t, err)
	require.Zero(t, state.Version)
	saved, err := q.GetPriceExecutionControlOperation(ctx, o.ID, o.WorkspaceID, o.ActorID)
	require.NoError(t, err)
	require.Equal(t, "awaiting_confirmation", saved.Status)
	require.Nil(t, saved.ResultControl)
	require.NoError(t, q.ExpirePriceExecutionControlOperation(ctx, saved))
	saved, err = q.GetPriceExecutionControlOperation(ctx, o.ID, o.WorkspaceID, o.ActorID)
	require.NoError(t, err)
	require.Equal(t, "expired", saved.Status)
}

func TestPriceAccountCommittedDecisionIsNotReportedRejectedByLaterAuthFailure(t *testing.T) {
	f, _, actor, _ := priceControlWBFixture(t, testAdvertisingToken())
	a := &priceControlAuthority{pauseAuthority: pauseAuthority{market: "wb", personal: true}}
	s := NewManagerPriceExecutionControlService(f.q, a, f.svc.wbClient, nil, []byte(testdb.EncryptionKey), true)
	_, o := preparePersonalPriceControl(t, s, f.ws, actor, "stopped")
	a.denyAt = a.calls.Load() + 4
	result, err := s.Approve(context.Background(), priceControlIdentity(), f.ws, actor, o.ID, "personal-token", o.Fingerprint, priceControlChecks())
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
	_, err = s.Receipt(context.Background(), priceControlIdentity(), f.ws, actor, o.ID, "personal-token")
	require.Error(t, err)
	saved, err := f.q.GetPriceExecutionControlOperation(context.Background(), o.ID, f.ws, actor)
	require.NoError(t, err)
	require.Equal(t, "succeeded", saved.Status)
	require.Equal(t, "stopped", saved.ResultControl.Mode)
}

func TestPriceAccountMissingControlStorageNeverFallsBackToAnUnrestrictedWriter(t *testing.T) {
	f, c, actor, _ := priceControlWBFixture(t, testAdvertisingToken())
	_, err := f.pool.Exec(context.Background(), `DROP TABLE price_account_execution_controls`)
	require.NoError(t, err)
	_, err = f.svc.ApplyApprovedManagerPrice(context.Background(), f.ws, f.cab, actor, c)
	require.Error(t, err)
	require.Zero(t, f.uploads.Load())
}

func TestPriceAccountDowngradePreservesVersionedPriceCommandEvenWithoutAnyControlProposal(t *testing.T) {
	f, c, actor, _ := priceControlWBFixture(t, testAdvertisingToken())
	version := int64(0)
	c.ExecutionControlVersion = &version
	record, decision, err := managerCommandRecord(c, f.ws, f.cab, actor)
	require.NoError(t, err)
	before := c.BeforeWB
	_, _, created, err := f.q.CreateManagerWBPriceCommand(context.Background(), record, sqlcgen.CreatePriceChangeParams{WorkspaceID: record.WorkspaceID, SellerCabinetID: record.SellerCabinetID, WbProductID: before.NmID, OldPriceRub: before.Sizes[0].PriceCents / 100, NewPriceRub: c.WBBasePriceRub, OldDiscountPercent: int32(before.DiscountPercent), NewDiscountPercent: int32(before.DiscountPercent), Source: domain.PriceSourceManual, WbStatus: domain.PriceStatusPending, Reason: "isolated versioned command", DecisionContext: decision, CreatedBy: record.ActorID}, uuidToPgtype(uuid.New()))
	require.NoError(t, err)
	require.True(t, created)
	var count int
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT count(*) FROM manager_price_execution_control_operations`).Scan(&count))
	require.Zero(t, count)
	migration, err := os.ReadFile("../../migrations/000077_price_account_execution_control.down.sql")
	require.NoError(t, err)
	_, err = f.pool.Exec(context.Background(), string(migration))
	require.ErrorContains(t, err, "cannot discard personal price account execution controls")
	require.Zero(t, f.uploads.Load())
}
