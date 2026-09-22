package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type aiApprovalFixture struct {
	*ozonFixture
	campaign   sqlcgen.OzonCampaign
	strategyID uuid.UUID
	decision   sqlcgen.AiDecision
}

func newAIApprovalFixture(t *testing.T) *aiApprovalFixture {
	t.Helper()
	fx := newAIExecutionFixture(t)
	ctx := context.Background()
	weekly := int64(7000)
	campaign := seedOzonCampaign(t, fx.db, fx.cabinetID, 8900, "CAMPAIGN_STATE_RUNNING", nil, &weekly)
	seedOzonCampaignStat(t, fx.db, campaign.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)
	strategyID := seedAIStrategy(t, fx, 2)
	_, err := fx.db.Pool.Exec(ctx, `INSERT INTO strategy_bindings(strategy_id,ozon_campaign_id) VALUES($1,$2)`, strategyID, campaign.ID)
	require.NoError(t, err)
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	decision, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBudgetChange, Target: []byte(`{"ozon_campaign_id":8900}`),
		Proposal: []byte(`{"new_value":6000}`), GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
	})
	require.NoError(t, err)
	return &aiApprovalFixture{ozonFixture: fx, campaign: campaign, strategyID: strategyID, decision: decision}
}

func (fx *aiApprovalFixture) decisionStatus(t *testing.T) string {
	t.Helper()
	row, err := fx.db.Queries.GetAIDecisionByID(context.Background(), sqlcgen.GetAIDecisionByIDParams{ID: fx.decision.ID, WorkspaceID: uuidToPgtype(fx.workspaceID)})
	require.NoError(t, err)
	return row.Status
}

type approvalPerfClient struct {
	*fakePerfClient
	calls   atomic.Int32
	onWrite func(context.Context) error
}

func (f *approvalPerfClient) UpdateCampaign(ctx context.Context, _ ozon.Credentials, _ int64, _ ozon.CampaignPatch) error {
	f.calls.Add(1)
	if f.onWrite != nil {
		return f.onWrite(ctx)
	}
	return nil
}

func TestAIApproval_FreshProposalDoesNotConsumeOwnCooldown(t *testing.T) {
	fx := newAIApprovalFixture(t)
	perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}}
	perf.onWrite = func(ctx context.Context) error {
		// A separate database read at the external boundary proves the claim
		// has already committed, rather than being held in a rollbackable tx.
		row, err := fx.db.Queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{ID: fx.decision.ID, WorkspaceID: uuidToPgtype(fx.workspaceID)})
		if err != nil {
			return err
		}
		if row.Status != domain.AIDecisionStatusApproved {
			return fmt.Errorf("external call saw status %s instead of committed approved", row.Status)
		}
		return nil
	}
	mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
	updated, err := mgr.ApproveDecision(context.Background(), fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, domain.AIDecisionStatusApplied, updated.Status)
	assert.EqualValues(t, 1, perf.calls.Load())
}

func TestAIApproval_ConcurrentReviewCannotApplyTwiceOrOverwriteResult(t *testing.T) {
	for _, rejectSecond := range []bool{false, true} {
		t.Run(fmt.Sprintf("reject_second_%t", rejectSecond), func(t *testing.T) {
			fx := newAIApprovalFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			arrived, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}, onWrite: func(ctx context.Context) error {
				close(arrived)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
			first, second := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := mgr.ApproveDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
				first <- err
			}()
			select {
			case <-arrived:
			case <-ctx.Done():
				t.Fatal("first approval did not reach external call: ", ctx.Err())
			}
			assert.Equal(t, domain.AIDecisionStatusApproved, fx.decisionStatus(t))
			go func() {
				var err error
				if rejectSecond {
					_, err = mgr.RejectDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
				} else {
					_, err = mgr.ApproveDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
				}
				second <- err
			}()
			unblock()
			require.NoError(t, <-first)
			require.Error(t, <-second)
			assert.EqualValues(t, 1, perf.calls.Load())
			assert.Equal(t, domain.AIDecisionStatusApplied, fx.decisionStatus(t))
		})
	}
}

func TestAIApproval_CurrentAuthorityAndScopeAreRechecked(t *testing.T) {
	for _, scenario := range []string{"observation", "inactive", "scope_changed", "strategy_replaced"} {
		t.Run(scenario, func(t *testing.T) {
			fx := newAIApprovalFixture(t)
			ctx := context.Background()
			var err error
			switch scenario {
			case "observation":
				_, err = fx.db.Pool.Exec(ctx, `UPDATE strategies SET params=jsonb_set(params,'{automation_level}','1') WHERE id=$1`, fx.strategyID)
			case "inactive", "strategy_replaced":
				_, err = fx.db.Pool.Exec(ctx, `UPDATE strategies SET is_active=false WHERE id=$1`, fx.strategyID)
				if scenario == "strategy_replaced" {
					seedAIStrategy(t, fx.ozonFixture, 2)
				}
			case "scope_changed":
				other := seedOzonCampaign(t, fx.db, fx.cabinetID, 8901, "CAMPAIGN_STATE_RUNNING", nil, nil)
				_, err = fx.db.Pool.Exec(ctx, `UPDATE strategy_bindings SET ozon_campaign_id=$2 WHERE strategy_id=$1`, fx.strategyID, other.ID)
			}
			require.NoError(t, err)
			perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}}
			mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
			_, err = mgr.ApproveDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
			require.Error(t, err)
			assert.Zero(t, perf.calls.Load())
			assert.NotEqual(t, domain.AIDecisionStatusApplied, fx.decisionStatus(t))
		})
	}
}

func TestAIApproval_TemporaryCooldownKeepsProposalReviewable(t *testing.T) {
	fx := newAIApprovalFixture(t)
	_, err := fx.db.Queries.InsertAIDecision(context.Background(), sqlcgen.InsertAIDecisionParams{
		RunID: fx.decision.RunID, WorkspaceID: fx.decision.WorkspaceID, SellerCabinetID: fx.decision.SellerCabinetID,
		ActionType: fx.decision.ActionType, Target: fx.decision.Target, Proposal: []byte(`{"new_value":7000}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusApplied,
	})
	require.NoError(t, err)
	perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}}
	mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
	_, err = mgr.ApproveDecision(context.Background(), fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
	require.ErrorContains(t, err, "cooldown")
	assert.Zero(t, perf.calls.Load())
	assert.Equal(t, domain.AIDecisionStatusProposed, fx.decisionStatus(t))
}

func TestAIApproval_ExternalAndFinalizeFailuresDoNotResurrectProposal(t *testing.T) {
	for _, finalizeFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("finalize_failure_%t", finalizeFailure), func(t *testing.T) {
			fx := newAIApprovalFixture(t)
			ctx := context.Background()
			perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}}
			expectedStatus := domain.AIDecisionStatusFailed
			if finalizeFailure {
				_, err := fx.db.Pool.Exec(ctx, `CREATE FUNCTION test_reject_ai_finalize() RETURNS trigger LANGUAGE plpgsql AS $$
				 BEGIN IF NEW.status='applied' THEN RAISE EXCEPTION 'injected ledger failure'; END IF; RETURN NEW; END $$;
				 CREATE TRIGGER test_ai_finalize BEFORE UPDATE ON ai_decisions FOR EACH ROW EXECUTE FUNCTION test_reject_ai_finalize()`)
				require.NoError(t, err)
				expectedStatus = domain.AIDecisionStatusApproved
			} else {
				perf.onWrite = func(context.Context) error { return errors.New("injected Ozon failure") }
			}
			mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
			_, err := mgr.ApproveDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
			require.Error(t, err)
			assert.Equal(t, expectedStatus, fx.decisionStatus(t))
			_, err = mgr.ApproveDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
			require.Error(t, err)
			assert.EqualValues(t, 1, perf.calls.Load())
		})
	}
}

func TestAIApproval_QueuedLocksDoNotExhaustPool(t *testing.T) {
	fx := newAIApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := fx.db.Pool.Config()
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	q := sqlcgen.New(pool)
	arrived, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	first := make(chan error, 1)
	go func() {
		first <- q.WithOzonAIExecutionLock(ctx, fx.decision.SellerCabinetID, func(q *sqlcgen.Queries) error {
			close(arrived)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := q.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{ID: fx.decision.ID, WorkspaceID: fx.decision.WorkspaceID})
			return err
		})
	}()
	<-arrived
	var queued sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		queued.Add(1)
		go func() {
			defer queued.Done()
			errs <- q.WithOzonAIExecutionLock(ctx, fx.decision.SellerCabinetID, func(q *sqlcgen.Queries) error {
				_, err := q.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{ID: fx.decision.ID, WorkspaceID: fx.decision.WorkspaceID})
				return err
			})
		}()
	}
	unblock()
	require.NoError(t, <-first)
	queued.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestAIApproval_FreshContextEnforcesDRRCeiling(t *testing.T) {
	fx := newAIApprovalFixture(t)
	ctx := context.Background()
	_, err := fx.db.Pool.Exec(ctx, `UPDATE strategies SET params=jsonb_set(params,'{max_total_drr_percent}','10') WHERE id=$1`, fx.strategyID)
	require.NoError(t, err)
	_, err = fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET proposal='{"new_value":8000}' WHERE id=$1`, fx.decision.ID)
	require.NoError(t, err)
	// There is fresh campaign activity but no complete cabinet turnover:
	// approval must use the same unknown-DRR restriction as the autopilot.
	perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}}
	mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
	_, err = mgr.ApproveDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
	require.ErrorContains(t, err, "ДРР")
	assert.Zero(t, perf.calls.Load())
}

func TestAIApproval_CPOUsesCurrentScopeAndMirror(t *testing.T) {
	for _, campaignBound := range []bool{false, true} {
		t.Run(fmt.Sprintf("campaign_bound_%t", campaignBound), func(t *testing.T) {
			fx := newAIApprovalFixture(t)
			ctx := context.Background()
			if !campaignBound {
				_, err := fx.db.Pool.Exec(ctx, `DELETE FROM strategy_bindings WHERE strategy_id=$1`, fx.strategyID)
				require.NoError(t, err)
			}
			_, err := fx.db.Pool.Exec(ctx, `INSERT INTO ozon_cpo_products(seller_cabinet_id,sku,enabled,bid) VALUES($1,777,true,5)`, fx.cabinetID)
			require.NoError(t, err)
			_, err = fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET action_type='cpo_disable',target='{"sku":777}',proposal='{}' WHERE id=$1`, fx.decision.ID)
			require.NoError(t, err)
			perf := &fakePerfClient{}
			mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
			updated, err := mgr.ApproveDecision(ctx, fx.workspaceID, uuidFromPgtype(fx.decision.ID), uuid.New())
			if campaignBound {
				require.ErrorContains(t, err, "scope")
				assert.Zero(t, perf.disableCalls)
			} else {
				require.NoError(t, err)
				assert.Equal(t, domain.AIDecisionStatusApplied, updated.Status)
				assert.Equal(t, 1, perf.disableCalls)
			}
		})
	}
}

func TestAIApproval_LockRejectsNonDurableDatabaseConfiguration(t *testing.T) {
	fx := newAIApprovalFixture(t)
	ctx := context.Background()
	cfg := fx.db.Pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	called := false
	err = sqlcgen.New(pool).WithOzonAIExecutionLock(ctx, fx.decision.SellerCabinetID, func(*sqlcgen.Queries) error { called = true; return nil })
	require.ErrorContains(t, err, "at least two connections")
	assert.False(t, called)
	tx, err := fx.db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	err = sqlcgen.New(tx).WithOzonAIExecutionLock(ctx, fx.decision.SellerCabinetID, func(*sqlcgen.Queries) error { called = true; return nil })
	require.ErrorContains(t, err, "durable writes")
	assert.False(t, called)
}

func TestAIApproval_DifferentCabinetLocksLeaveConnectionForDurableWrites(t *testing.T) {
	fx := newAIApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := fx.db.Pool.Config()
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	q := sqlcgen.New(pool)
	start := make(chan struct{})
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			<-start
			errs <- q.WithOzonAIExecutionLock(ctx, uuidToPgtype(uuid.New()), func(q *sqlcgen.Queries) error {
				_, err := q.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{ID: fx.decision.ID, WorkspaceID: fx.decision.WorkspaceID})
				return err
			})
		}()
	}
	close(start)
	for i := 0; i < 8; i++ {
		require.NoError(t, <-errs)
	}
}

func TestAIApproval_BatchPrerequisitesRunBeforeLifecycleAndFailureStopsRemainder(t *testing.T) {
	for _, prerequisiteFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("prerequisite_fails_%t", prerequisiteFails), func(t *testing.T) {
			fx := newAIApprovalFixture(t)
			ctx := context.Background()
			pause, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
				RunID: fx.decision.RunID, WorkspaceID: fx.decision.WorkspaceID, SellerCabinetID: fx.decision.SellerCabinetID,
				ActionType: domain.AIActionCampaignPause, Target: fx.decision.Target, Proposal: []byte(`{}`),
				GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
			})
			require.NoError(t, err)
			perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}}
			perf.onWrite = func(ctx context.Context) error {
				row, err := fx.db.Queries.GetOzonCampaignByID(ctx, fx.campaign.ID)
				if err != nil {
					return err
				}
				if row.State.String != "CAMPAIGN_STATE_RUNNING" {
					return errors.New("campaign paused before its prerequisite completed")
				}
				if prerequisiteFails {
					return errors.New("injected prerequisite failure")
				}
				return nil
			}
			mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
			// UI order must not determine execution order: lifecycle is first.
			ids := []uuid.UUID{uuidFromPgtype(pause.ID), uuidFromPgtype(fx.decision.ID)}
			results := mgr.ApproveDecisionsBatch(ctx, fx.workspaceID, ids, uuid.New())
			require.Len(t, results, 2)
			assert.Equal(t, ids[0], results[0].ID)
			assert.Equal(t, ids[1], results[1].ID)
			assert.EqualValues(t, 1, perf.calls.Load())
			if prerequisiteFails {
				assert.False(t, results[0].OK)
				assert.Contains(t, results[0].Error, "earlier batch decision")
				assert.Contains(t, results[1].Error, "injected prerequisite failure")
				assert.Empty(t, perf.deactivateCalls)
				row, err := fx.db.Queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{ID: pause.ID, WorkspaceID: pause.WorkspaceID})
				require.NoError(t, err)
				assert.Equal(t, domain.AIDecisionStatusProposed, row.Status)
			} else {
				assert.True(t, results[0].OK, results[0].Error)
				assert.True(t, results[1].OK, results[1].Error)
				assert.Equal(t, []int64{8900}, perf.deactivateCalls)
			}
		})
	}
}

func TestAIApproval_BatchInvalidFirstIDStopsRemainingWrites(t *testing.T) {
	fx := newAIApprovalFixture(t)
	perf := &approvalPerfClient{fakePerfClient: &fakePerfClient{}}
	mgr := newAIManagerWithPerf(fx.db, &fakeLLM{}, perf, perf)
	results := mgr.ApproveDecisionsBatch(context.Background(), fx.workspaceID, []uuid.UUID{uuid.Nil, uuidFromPgtype(fx.decision.ID)}, uuid.New())
	require.Len(t, results, 2)
	assert.False(t, results[0].OK)
	assert.Contains(t, results[1].Error, "earlier batch decision")
	assert.Zero(t, perf.calls.Load())
	assert.Equal(t, domain.AIDecisionStatusProposed, fx.decisionStatus(t))
}
