package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/llm"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
)

// fakeLLM implements aiChatClient. enabled toggles Enabled(); responses is a
// queue of canned responses returned by ChatCompletion in order.
type fakeLLM struct {
	enabled   bool
	responses []*llm.ChatResponse
	err       error
	calls     int
}

var assertAnErr = errors.New("llm boom")

func (f *fakeLLM) Enabled() bool { return f.enabled }
func (f *fakeLLM) Model() string { return "fake-model" }
func (f *fakeLLM) ChatCompletion(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	i := f.calls
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if i < len(f.responses) {
		return f.responses[i], nil
	}
	return &llm.ChatResponse{}, nil
}

func newAIManager(db *testutil.TestDB, llmClient aiChatClient) *OzonAIManagerService {
	return newAIManagerWithPerf(db, llmClient, &fakePerfClient{}, &fakePerfClient{})
}

func newAIManagerWithPerf(db *testutil.TestDB, llmClient aiChatClient, managerPerf aiManagerPerfClient, actionsPerf ozonCampaignPerfClient) *OzonAIManagerService {
	return &OzonAIManagerService{
		queries:       db.Queries,
		perfClient:    managerPerf,
		actions:       newCampaignActions(db, actionsPerf),
		llm:           llmClient,
		encryptionKey: ozonTestKey(),
		logger:        ozonTestLogger(),
	}
}

func submitProposalsResponse(summary string, proposals []map[string]any) *llm.ChatResponse {
	args, _ := json.Marshal(map[string]any{"summary": summary, "proposals": proposals})
	return &llm.ChatResponse{
		Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: "call_1", Type: "function",
				Function: llm.ToolCallFunction{Name: "submit_proposals", Arguments: string(args)},
			}},
		},
	}
}

func seedAIStrategy(t *testing.T, fx *ozonFixture, level int) uuid.UUID {
	t.Helper()
	params := domain.StrategyParams{
		AutomationLevel: level, TargetACoS: 15, MaxChangePercent: 50,
		MinBid: 10, MaxBid: 5000, MaxChangesPerDay: 5,
	}
	return seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID, domain.StrategyTypeOzonAIAutopilot, params, true)
}

func TestOzonAI_ListRunsAndDecisions(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-list")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	strategyID := seedAIStrategy(t, fx, 1)

	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusRunning, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	target, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 1, SKU: 5})
	_, err = fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBidChange, Target: target, Proposal: []byte(`{"new_value":20}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
	})
	require.NoError(t, err)

	runs, total, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, runs, 1)

	decisions, dtotal, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, "", nil, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, dtotal)
	require.Len(t, decisions, 1)

	filtered, ftotal, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, domain.AIDecisionStatusProposed, nil, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, ftotal)
	require.Len(t, filtered, 1)

	t.Run("tenancy", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-ai-list-other")
		_, _, err := mgr.ListRuns(ctx, other, fx.cabinetID, 50, 0)
		require.Error(t, err)
	})
}

func TestOzonAI_CheckManualRunAllowed(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-manual-gate")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})

	t.Run("no strategy rejected", func(t *testing.T) {
		err := mgr.CheckManualRunAllowed(ctx, fx.workspaceID, fx.cabinetID)
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrValidation))
	})

	seedAIStrategy(t, fx, 3)
	t.Run("allowed with active strategy", func(t *testing.T) {
		require.NoError(t, mgr.CheckManualRunAllowed(ctx, fx.workspaceID, fx.cabinetID))
	})
}

func TestOzonAI_RunForCabinetDisabledLLM(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-disabled")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{enabled: false})
	strategyID := seedAIStrategy(t, fx, 1)
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	require.False(t, mgr.Enabled())
	err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
	require.NoError(t, err)

	runs, total, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, runs, 1)
	assert.Equal(t, domain.AIRunStatusSkipped, runs[0].Status)
}

func TestOzonAI_RunForCabinetShadowLevel(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-shadow")
	defer cleanup()
	ctx := context.Background()

	// Seed a running campaign + active bid so the bid_change proposal validates.
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8001, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 55, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)

	strategyID := seedAIStrategy(t, fx, 1) // shadow: never applies
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		submitProposalsResponse("all good", []map[string]any{
			{"action_type": "bid_change", "target": map[string]any{"ozon_campaign_id": 8001, "sku": 55},
				"new_value": 25.0, "rationale": "raise a bit", "expected_effect": "more clicks"},
		}),
	}}
	mgr := newAIManager(fx.db, llmClient)

	err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
	require.NoError(t, err)
	assert.Equal(t, 1, llmClient.calls)

	// One completed run + one shadow decision.
	runs, _, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, domain.AIRunStatusCompleted, runs[0].Status)

	decisions, _, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, "", nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, domain.AIDecisionStatusShadow, decisions[0].Status)
}

func requestDataResponse(what string, skus []int64) *llm.ChatResponse {
	args, _ := json.Marshal(map[string]any{"what": what, "skus": skus})
	return &llm.ChatResponse{
		Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{{
				ID: "call_data", Type: "function",
				Function: llm.ToolCallFunction{Name: "request_data", Arguments: string(args)},
			}},
		},
	}
}

func TestOzonAI_RunWithDataRequestAndMultiProposals(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-datareq")
	defer cleanup()
	ctx := context.Background()

	weekly := int64(7000)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8500, "CAMPAIGN_STATE_RUNNING", nil, &weekly)
	seedOzonCampaignProduct(t, fx.db, c.ID, 55, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)
	// a phrases row so the search_queries data request returns content
	seedOzonProduct(t, fx.db, fx.cabinetID, 55, 55, "ART-55", "Q")
	require.NoError(t, fx.db.Queries.UpsertOzonSearchQuery(ctx,
		ozonSearchQueryRow(fx.cabinetID, 55, "query one", time.Now().UTC(), 100, 10, 1, 20)))

	strategyID := seedAIStrategy(t, fx, 1) // shadow
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		requestDataResponse("search_queries", []int64{55}),
		submitProposalsResponse("done", []map[string]any{
			{"action_type": "bid_change", "target": map[string]any{"ozon_campaign_id": 8500, "sku": 55},
				"new_value": 25.0, "rationale": "r", "expected_effect": "e"},
			{"action_type": "budget_change", "target": map[string]any{"ozon_campaign_id": 8500},
				"new_value": 8000.0, "rationale": "r", "expected_effect": "e"},
			{"action_type": "cpo_bid", "target": map[string]any{"sku": 55},
				"new_value": 12.0, "rationale": "r", "expected_effect": "e"},
			{"action_type": "cpo_enable", "target": map[string]any{"sku": 55},
				"rationale": "r", "expected_effect": "e"},
		}),
	}}
	mgr := newAIManager(fx.db, llmClient)

	err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
	require.NoError(t, err)
	assert.Equal(t, 2, llmClient.calls) // open call + forced submit

	decisions, total, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, "", nil, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 4, total)
	for _, d := range decisions {
		assert.Equal(t, domain.AIDecisionStatusShadow, d.Status)
	}
}

func TestOzonAI_RunForCabinetAutopilotApplies(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-autopilot")
	defer cleanup()
	ctx := context.Background()

	// weekly-budgeted running campaign so budget_change resolves + a CPO product.
	weekly := int64(7000)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8700, "CAMPAIGN_STATE_RUNNING", nil, &weekly)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)
	seedOzonProduct(t, fx.db, fx.cabinetID, 66, 66, "ART-66", "CPO")

	strategyID := seedAIStrategy(t, fx, 3) // autopilot
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		submitProposalsResponse("apply", []map[string]any{
			{"action_type": "budget_change", "target": map[string]any{"ozon_campaign_id": 8700},
				"new_value": 8000.0, "rationale": "scale", "expected_effect": "more"},
			{"action_type": "campaign_pause", "target": map[string]any{"ozon_campaign_id": 8700},
				"rationale": "pause", "expected_effect": "less"},
			{"action_type": "cpo_enable", "target": map[string]any{"sku": 66},
				"rationale": "promo", "expected_effect": "orders"},
		}),
	}}
	mgr := newAIManager(fx.db, llmClient)

	err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
	require.NoError(t, err)

	decisions, total, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, "", nil, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	// At least one auto_applied decision (the guardrails passed and s.actions wrote).
	applied := 0
	for _, d := range decisions {
		if d.Status == domain.AIDecisionStatusAutoApplied {
			applied++
		}
	}
	assert.GreaterOrEqual(t, applied, 1)
}

func TestOzonAI_AutopilotBidAndCPOApply(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-autopilot-bid")
	defer cleanup()
	ctx := context.Background()

	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8800, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 77, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)
	seedOzonProduct(t, fx.db, fx.cabinetID, 77, 77, "ART-77", "Ad")

	strategyID := seedAIStrategy(t, fx, 3) // autopilot
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	// Manager perf returns Ozon minimums (bid 25 >= min 10 passes; cpo below min rejected).
	managerPerf := &fakePerfClient{minBids: map[int64]float64{77: 10}, cpoMinBids: map[int64]float64{77: 3}}
	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		submitProposalsResponse("apply", []map[string]any{
			{"action_type": "bid_change", "target": map[string]any{"ozon_campaign_id": 8800, "sku": 77},
				"new_value": 25.0, "rationale": "raise", "expected_effect": "clicks"},
			{"action_type": "cpo_bid", "target": map[string]any{"sku": 77},
				"new_value": 5.0, "rationale": "cpo", "expected_effect": "orders"},
		}),
	}}
	mgr := newAIManagerWithPerf(fx.db, llmClient, managerPerf, &fakePerfClient{})

	err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
	require.NoError(t, err)

	decisions, total, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, "", nil, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	applied := 0
	for _, d := range decisions {
		if d.Status == domain.AIDecisionStatusAutoApplied {
			applied++
		}
	}
	assert.GreaterOrEqual(t, applied, 1)
}

func TestOzonAI_ApproveDecisionSuccess(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-approve-ok")
	defer cleanup()
	ctx := context.Background()

	weekly := int64(7000)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8900, "CAMPAIGN_STATE_RUNNING", nil, &weekly)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)

	strategyID := seedAIStrategy(t, fx, 2) // copilot
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	target, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 8900})
	dec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBudgetChange, Target: target, Proposal: []byte(`{"new_value":8000}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
	})
	require.NoError(t, err)
	// Backdate so the decision's own row is outside the guardrail cooldown window.
	_, err = fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET created_at = now() - interval '3 hours' WHERE id = $1`, dec.ID)
	require.NoError(t, err)

	mgr := newAIManager(fx.db, &fakeLLM{})
	updated, err := mgr.ApproveDecision(ctx, fx.workspaceID, uuid.UUID(dec.ID.Bytes), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, domain.AIDecisionStatusApplied, updated.Status)

	t.Run("cannot approve non-proposed", func(t *testing.T) {
		_, err := mgr.ApproveDecision(ctx, fx.workspaceID, uuid.UUID(dec.ID.Bytes), uuid.New())
		require.Error(t, err)
	})
}

func TestOzonAI_DataRequestCompetitiveBids(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-datareq-comp")
	defer cleanup()
	ctx := context.Background()

	wk := int64(7000)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8600, "CAMPAIGN_STATE_RUNNING", nil, &wk)
	seedOzonCampaignProduct(t, fx.db, c.ID, 88, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)

	strategyID := seedAIStrategy(t, fx, 1)
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	compArgs, _ := json.Marshal(map[string]any{"what": "competitive_bids", "campaign_ids": []int64{8600}})
	compResp := &llm.ChatResponse{Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{
		ID: "c", Type: "function", Function: llm.ToolCallFunction{Name: "request_data", Arguments: string(compArgs)},
	}}}}
	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		compResp,
		submitProposalsResponse("done", nil),
	}}
	// actions perf returns competitive bids for the data request.
	actionsPerf := &fakePerfClient{competitive: []ozon.CompetitiveBid{{SKU: 88, BidRub: 15}}}
	mgr := newAIManagerWithPerf(fx.db, llmClient, &fakePerfClient{}, actionsPerf)

	err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
	require.NoError(t, err)
	assert.Equal(t, 2, llmClient.calls)
}

func TestOzonAI_RunSkipsAndErrors(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-skips")
	defer cleanup()
	ctx := context.Background()
	strategyID := seedAIStrategy(t, fx, 1)
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)
	strat := strategyFromSqlc(strategy)

	t.Run("already running skips", func(t *testing.T) {
		mgr := newAIManager(fx.db, &fakeLLM{enabled: true})
		_, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
			WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
			StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusRunning, Trigger: domain.AIRunTriggerManual,
		})
		require.NoError(t, err)
		require.NoError(t, mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strat, domain.AIRunTriggerCron))
	})

	t.Run("llm error records failed run", func(t *testing.T) {
		fx2, cleanup2 := newOzonFixture(t, "ozon-ai-skips-err")
		defer cleanup2()
		sID := seedAIStrategy(t, fx2, 1)
		s2, err := fx2.db.Queries.GetStrategyByID(ctx, uuidToPgtype(sID))
		require.NoError(t, err)
		mgr := newAIManager(fx2.db, &fakeLLM{enabled: true, err: assertAnErr})
		err = mgr.RunForCabinet(ctx, fx2.workspaceID, fx2.cabinetID, strategyFromSqlc(s2), domain.AIRunTriggerManual)
		require.Error(t, err)
		runs, _, err := mgr.ListRuns(ctx, fx2.workspaceID, fx2.cabinetID, 50, 0)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assert.Equal(t, domain.AIRunStatusFailed, runs[0].Status)
	})
}

func TestOzonAI_RunForCabinetIDSuccessAndMinGap(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-runid")
	defer cleanup()
	ctx := context.Background()
	seedAIStrategy(t, fx, 1)
	// Enabled LLM that submits an empty proposal set -> completed run.
	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		submitProposalsResponse("nothing to do", nil),
	}}
	mgr := newAIManager(fx.db, llmClient)

	require.NoError(t, mgr.RunForCabinetID(ctx, fx.cabinetID, domain.AIRunTriggerManual))
	runs, total, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, domain.AIRunStatusCompleted, runs[0].Status)

	// A cron trigger within 2h of the last completed run is skipped (min-gap).
	require.NoError(t, mgr.RunForCabinetID(ctx, fx.cabinetID, domain.AIRunTriggerCron))
	_, total2, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total2, "min-gap must prevent a second run")

	t.Run("non-ozon cabinet rejected", func(t *testing.T) {
		wb := seedWBCabinet(t, fx.db, fx.workspaceID)
		err := mgr.RunForCabinetID(ctx, wb, domain.AIRunTriggerManual)
		require.Error(t, err)
	})
}

func TestOzonAI_ExecuteFreeTextForcesSubmit(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-freetext")
	defer cleanup()
	ctx := context.Background()
	strategyID := seedAIStrategy(t, fx, 1)
	strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	// First response is free text (no tool call) -> manager forces a second
	// submit_proposals call.
	freeText := &llm.ChatResponse{Message: llm.Message{Role: "assistant", Content: "let me think"}}
	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		freeText,
		submitProposalsResponse("done", nil),
	}}
	mgr := newAIManager(fx.db, llmClient)

	err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
	require.NoError(t, err)
	assert.Equal(t, 2, llmClient.calls)
	runs, _, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 50, 0)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, domain.AIRunStatusCompleted, runs[0].Status)
}

func TestOzonAI_RunForCabinetIDNoStrategyNoop(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-noop")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})

	// No active AI strategy -> silent no-op.
	require.NoError(t, mgr.RunForCabinetID(ctx, fx.cabinetID, domain.AIRunTriggerManual))

	ids, err := mgr.ListAICabinetIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, ids)

	seedAIStrategy(t, fx, 1)
	ids, err = mgr.ListAICabinetIDs(ctx)
	require.NoError(t, err)
	require.Len(t, ids, 1)
}

func TestOzonAI_RejectDecision(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-reject")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	strategyID := seedAIStrategy(t, fx, 2)
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	target, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 1, SKU: 5})
	dec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBidChange, Target: target, Proposal: []byte(`{"new_value":20}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
	})
	require.NoError(t, err)

	updated, err := mgr.RejectDecision(ctx, fx.workspaceID, uuid.UUID(dec.ID.Bytes), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, domain.AIDecisionStatusRejectedByUser, updated.Status)

	t.Run("cannot reject twice", func(t *testing.T) {
		_, err := mgr.RejectDecision(ctx, fx.workspaceID, uuid.UUID(dec.ID.Bytes), uuid.New())
		require.Error(t, err)
	})

	t.Run("missing decision", func(t *testing.T) {
		_, err := mgr.RejectDecision(ctx, fx.workspaceID, uuid.New(), uuid.New())
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.ErrNotFound))
	})
}

func TestOzonAI_ApproveDecisionGuardrailReject(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-approve")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})
	strategyID := seedAIStrategy(t, fx, 2)
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	// Target a campaign that does not exist in the mirror -> guardrail rejects.
	target, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 999999, SKU: 5})
	dec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBidChange, Target: target, Proposal: []byte(`{"new_value":20}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
	})
	require.NoError(t, err)

	_, err = mgr.ApproveDecision(ctx, fx.workspaceID, uuid.UUID(dec.ID.Bytes), uuid.New())
	require.Error(t, err) // guardrail rejects (campaign not found)
	fresh, err := fx.db.Queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{
		ID: dec.ID, WorkspaceID: uuidToPgtype(fx.workspaceID),
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AIDecisionStatusRejectedByGuardrail, fresh.Status)
}

func TestOzonAI_Impact(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-impact")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})

	// Empty impact aggregate.
	summary, err := mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.EqualValues(t, 0, summary.DecisionsApplied)

	// Seed an applied campaign-targeted decision + before/after stats, then sweep.
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8100, "CAMPAIGN_STATE_RUNNING", nil, nil)
	appliedAt := time.Now().UTC().AddDate(0, 0, -4) // recent enough for after-window data
	for i := 1; i <= aiImpactWindowDays; i++ {
		// before window
		seedOzonCampaignStat(t, fx.db, c.ID, appliedAt.AddDate(0, 0, -i), 100, 10, 1, 50, 100)
	}
	for i := 1; i <= 3; i++ {
		// after window (>= aiImpactMinAfterDays days present)
		seedOzonCampaignStat(t, fx.db, c.ID, appliedAt.AddDate(0, 0, i), 100, 10, 2, 40, 200)
	}

	strategyID := seedAIStrategy(t, fx, 3)
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	target, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 8100})
	dec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBudgetChange, Target: target, Proposal: []byte(`{"new_value":100}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusApplied,
	})
	require.NoError(t, err)
	// Mark applied_at in the past so windows resolve.
	_, err = fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET applied_at = $2 WHERE id = $1`,
		dec.ID, pgtype.Timestamptz{Time: appliedAt, Valid: true})
	require.NoError(t, err)

	require.NoError(t, mgr.EvaluateImpactSweep(ctx))

	fresh, err := fx.db.Queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{
		ID: dec.ID, WorkspaceID: uuidToPgtype(fx.workspaceID),
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AIOutcomeEvaluated, pgTextValue(fresh.OutcomeStatus))

	summary, err = mgr.GetImpact(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, summary.DecisionsApplied)
	assert.EqualValues(t, 1, summary.DecisionsEvaluated)
}
