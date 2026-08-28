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

// TestOzonAI_TotalDRRCeilingBlocksIncrease proves the total-ДРР ceiling is
// enforced on the AI branch too, not only in the deterministic strategy: an
// autopilot proposal to raise a bid must be rejected while the cabinet's whole
// turnover is already paying too much for advertising.
func TestOzonAI_TotalDRRCeilingBlocksIncrease(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-total-drr")
	defer cleanup()
	ctx := context.Background()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)

	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8300, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 77, 20)
	// The campaign itself looks healthy: 200 ₽ spent, 1000 ₽ attributed → 20%.
	seedOzonCampaignStat(t, fx.db, c.ID, yesterday, 1000, 100, 10, 200, 1000)
	// But the cabinet's WHOLE turnover is only 1000 ₽, so the total ДРР is
	// also 20% — well past the 5% ceiling below.
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 77, yesterday, 4, 1000)

	ceiling := 5.0
	params := domain.StrategyParams{
		AutomationLevel: 2, TargetACoS: 15, MaxChangePercent: 50,
		MinBid: 10, MaxBid: 5000, MaxChangesPerDay: 5,
		MaxTotalDRRPercent: &ceiling,
	}
	strategyID := seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID, domain.StrategyTypeOzonAIAutopilot, params, true)
	strategyRow, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		submitProposalsResponse("scaling up", []map[string]any{
			{"action_type": "bid_change", "target": map[string]any{"ozon_campaign_id": 8300, "sku": 77},
				"new_value": 25.0, "rationale": "raise", "expected_effect": "more clicks"},
		}),
	}}
	mgr := newAIManager(fx.db, llmClient)

	require.NoError(t, mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategyRow), domain.AIRunTriggerManual))

	decisions, _, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, "", nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, domain.AIDecisionStatusRejectedByGuardrail, decisions[0].Status)
	assert.Contains(t, decisions[0].GuardrailVerdict, "ДРР от общего оборота")
}

// A decrease must always get through — a ceiling that also blocked cuts would
// trap a cabinet at exactly the level it is trying to escape.
func TestOzonAI_TotalDRRCeilingAllowsDecrease(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-total-drr-down")
	defer cleanup()
	ctx := context.Background()
	yesterday := time.Now().UTC().AddDate(0, 0, -1)

	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 8301, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 78, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, yesterday, 1000, 100, 10, 200, 1000)
	seedOzonSalesDaily(t, fx.db, fx.cabinetID, 78, yesterday, 4, 1000)

	ceiling := 5.0
	params := domain.StrategyParams{
		AutomationLevel: 2, TargetACoS: 15, MaxChangePercent: 50,
		MinBid: 10, MaxBid: 5000, MaxChangesPerDay: 5,
		MaxTotalDRRPercent: &ceiling,
	}
	strategyID := seedOzonStrategy(t, fx.db, fx.workspaceID, fx.cabinetID, domain.StrategyTypeOzonAIAutopilot, params, true)
	strategyRow, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)

	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{
		submitProposalsResponse("cutting back", []map[string]any{
			{"action_type": "bid_change", "target": map[string]any{"ozon_campaign_id": 8301, "sku": 78},
				"new_value": 15.0, "rationale": "cut", "expected_effect": "lower spend"},
		}),
	}}
	mgr := newAIManager(fx.db, llmClient)

	require.NoError(t, mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategyRow), domain.AIRunTriggerManual))

	decisions, _, err := mgr.ListDecisions(ctx, fx.workspaceID, fx.cabinetID, "", nil, 50, 0)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.NotEqual(t, domain.AIDecisionStatusRejectedByGuardrail, decisions[0].Status)
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

// TestOzonAI_ContextFeedbackAndMargin verifies the feedback-loop section
// (recent applied decisions + measured outcome) and the per-SKU margin.
func TestOzonAI_ContextFeedbackAndMargin(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-feedback")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})

	// A campaign the recent decision targets + an active bid SKU so it lands in
	// the economics/skuSet.
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 9100, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 501, 20)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)

	// SKU 501: Ozon net_price present → margin from the price row (cost "ozon").
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 501, OfferID: "ART-501", Name: "P1", PriceRub: 1000,
		NetPriceRub: 400, CommissionFBOPct: 15, AcquiringPct: 20,
	})

	strategyID := seedAIStrategy(t, fx, 3)
	strategyRow, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)
	strategy := strategyFromSqlc(strategyRow)

	// Seed one applied+evaluated decision on campaign 9100 with impact numbers.
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)
	target, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 9100, SKU: 501})
	dec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBidChange, Target: target, Proposal: []byte(`{"new_value":18}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusApplied,
	})
	require.NoError(t, err)
	// Campaign ДРР improved (25 → 18) while the cabinet's total ДРР got worse
	// (4.0 → 5.5): the decision bought orders that were already converting.
	// The model can only tell this apart if both pairs reach the pack.
	_, err = fx.db.Pool.Exec(ctx, `
		UPDATE ai_decisions SET outcome_status='evaluated',
			drr_before=25.0, drr_after=18.0,
			total_drr_before=4.0, total_drr_after=5.5,
			spend_before_rub=500, spend_after_rub=450,
			revenue_before_rub=2000, revenue_after_rub=2500
		WHERE id=$1`, dec.ID)
	require.NoError(t, err)

	pack, _, err := mgr.buildAIContext(ctx, fx.workspaceID, fx.cabinetID, strategy, strategy.Params.Merged())
	require.NoError(t, err)

	// Feedback section present with measured outcome + deltas.
	require.Len(t, pack.RecentDecisions, 1)
	rd := pack.RecentDecisions[0]
	assert.Equal(t, domain.AIActionBidChange, rd.Action)
	assert.EqualValues(t, 501, rd.SKU)
	assert.Equal(t, domain.AIOutcomeEvaluated, rd.Outcome)
	require.NotNil(t, rd.DRRBefore)
	require.NotNil(t, rd.DRRAfter)
	assert.InDelta(t, 25.0, *rd.DRRBefore, 0.01)
	assert.InDelta(t, 18.0, *rd.DRRAfter, 0.01)
	require.NotNil(t, rd.TotalDRRBefore)
	require.NotNil(t, rd.TotalDRRAfter)
	assert.InDelta(t, 4.0, *rd.TotalDRRBefore, 0.01)
	assert.InDelta(t, 5.5, *rd.TotalDRRAfter, 0.01)
	require.NotNil(t, rd.SpendDelta)
	assert.InDelta(t, -50.0, *rd.SpendDelta, 0.01) // 450-500
	require.NotNil(t, rd.RevenueDelta)
	assert.InDelta(t, 500.0, *rd.RevenueDelta, 0.01) // 2500-2000

	// Margin: SKU 501 has Ozon net_price → cost source "ozon", margin 43%.
	var econ *aiPackEconomics
	for i := range pack.Economics {
		if pack.Economics[i].SKU == 501 {
			econ = &pack.Economics[i]
		}
	}
	require.NotNil(t, econ)
	assert.Equal(t, "ozon", econ.CostSource)
	require.NotNil(t, econ.MarginPct)
	assert.InDelta(t, 43.0, *econ.MarginPct, 0.1)
}

func TestOzonAI_ContextBoundCampaignScopeExcludesOtherCampaignsAndCabinetCPO(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-campaign-scope")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})

	selected := seedOzonCampaign(t, fx.db, fx.cabinetID, 9401, "CAMPAIGN_STATE_RUNNING", nil, nil)
	other := seedOzonCampaign(t, fx.db, fx.cabinetID, 9402, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, selected.ID, 501, 20)
	seedOzonCampaignProduct(t, fx.db, other.ID, 502, 20)
	_, err := fx.db.Pool.Exec(ctx, `
		INSERT INTO ozon_cpo_products (seller_cabinet_id, sku, enabled, bid)
		VALUES ($1, 777, true, 5)`, fx.cabinetID)
	require.NoError(t, err)

	strategyID := seedAIStrategy(t, fx, 1)
	_, err = fx.db.Pool.Exec(ctx, `
		INSERT INTO strategy_bindings (strategy_id, ozon_campaign_id)
		VALUES ($1, $2)`, strategyID, uuidFromPgtype(selected.ID))
	require.NoError(t, err)
	strategyRow, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)
	strategy := strategyFromSqlc(strategyRow)

	pack, data, err := mgr.buildAIContext(ctx, fx.workspaceID, fx.cabinetID, strategy, strategy.Params.Merged())
	require.NoError(t, err)
	require.True(t, pack.BoundCampaigns)
	require.Len(t, pack.Campaigns, 1)
	assert.EqualValues(t, 9401, pack.Campaigns[0].OzonCampaignID)
	assert.Empty(t, pack.CPO, "cabinet-wide CPO must not leak into a campaign-scoped run")
	assert.Empty(t, data.cpoBySKU)
	assert.Contains(t, data.campaignsByOzonID, int64(9401))
	assert.NotContains(t, data.campaignsByOzonID, int64(9402))
}

// TestOzonAI_ContextMarginSellicoFallback verifies the cost fallback path:
// no Ozon net_price, cost pulled from the Sellico unit-economics mirror.
func TestOzonAI_ContextMarginSellicoFallback(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-margin-fallback")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})

	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 9200, "CAMPAIGN_STATE_RUNNING", nil, nil)
	seedOzonCampaignProduct(t, fx.db, c.ID, 601, 20)
	// Price row without net_price (no Ozon cost), commission + acquiring present.
	seedOzonPrice(t, fx.db, fx.cabinetID, ozonPriceSeed{
		SKU: 601, OfferID: "ART-601", Name: "P2", PriceRub: 1000,
		CommissionFBOPct: 10, AcquiringPct: 0,
	})
	// Sellico economics: cost 300 + other 50 = 350.
	seedOzonEconomics(t, fx.db, fx.cabinetID, "ART-601", 601, 300, 50)

	strategyID := seedAIStrategy(t, fx, 3)
	strategyRow, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
	require.NoError(t, err)
	strategy := strategyFromSqlc(strategyRow)

	pack, _, err := mgr.buildAIContext(ctx, fx.workspaceID, fx.cabinetID, strategy, strategy.Params.Merged())
	require.NoError(t, err)

	var econ *aiPackEconomics
	for i := range pack.Economics {
		if pack.Economics[i].SKU == 601 {
			econ = &pack.Economics[i]
		}
	}
	require.NotNil(t, econ)
	assert.Equal(t, "sellico", econ.CostSource)
	require.NotNil(t, econ.MarginPct)
	// (1000 - 350 - 1000*10/100 - 0)/1000*100 = 55%.
	assert.InDelta(t, 55.0, *econ.MarginPct, 0.1)
}

// TestOzonAI_BatchApproveRejectTenancyAndPartial covers batch approve/reject:
// per-id results, partial failure, and tenancy isolation.
func TestOzonAI_BatchApproveRejectTenancyAndPartial(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-batch")
	defer cleanup()
	ctx := context.Background()
	mgr := newAIManager(fx.db, &fakeLLM{})

	weekly := int64(7000)
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 9300, "CAMPAIGN_STATE_RUNNING", nil, &weekly)
	seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 10, 200, 1000)

	strategyID := seedAIStrategy(t, fx, 2) // copilot
	run, err := fx.db.Queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		StrategyID: uuidToPgtype(strategyID), Status: domain.AIRunStatusCompleted, Trigger: domain.AIRunTriggerManual,
	})
	require.NoError(t, err)

	// One approvable budget_change on the real campaign.
	okTarget, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 9300})
	okDec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBudgetChange, Target: okTarget, Proposal: []byte(`{"new_value":8000}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
	})
	require.NoError(t, err)
	_, err = fx.db.Pool.Exec(ctx, `UPDATE ai_decisions SET created_at = now() - interval '3 hours' WHERE id = $1`, okDec.ID)
	require.NoError(t, err)

	// One that fails the guardrail (campaign not in cabinet).
	badTarget, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 999999})
	badDec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
		ActionType: domain.AIActionBudgetChange, Target: badTarget, Proposal: []byte(`{"new_value":8000}`),
		GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
	})
	require.NoError(t, err)

	okID := uuid.UUID(okDec.ID.Bytes)
	badID := uuid.UUID(badDec.ID.Bytes)
	results := mgr.ApproveDecisionsBatch(ctx, fx.workspaceID, []uuid.UUID{okID, badID}, uuid.New())
	require.Len(t, results, 2)
	byID := map[uuid.UUID]domain.AIDecisionBatchResult{}
	for _, r := range results {
		byID[r.ID] = r
	}
	assert.True(t, byID[okID].OK, "the valid decision should apply")
	assert.False(t, byID[badID].OK, "the bad decision should fail")
	assert.NotEmpty(t, byID[badID].Error)

	t.Run("tenancy: other workspace fails all", func(t *testing.T) {
		other := newOzonWorkspace(t, fx.db, "ozon-ai-batch-other")
		// A fresh proposed decision to reject under the wrong workspace.
		rejTarget, _ := json.Marshal(domain.AIDecisionTarget{OzonCampaignID: 9300})
		rejDec, err := fx.db.Queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
			RunID: run.ID, WorkspaceID: uuidToPgtype(fx.workspaceID), SellerCabinetID: uuidToPgtype(fx.cabinetID),
			ActionType: domain.AIActionBudgetChange, Target: rejTarget, Proposal: []byte(`{"new_value":8000}`),
			GuardrailVerdict: "passed", Status: domain.AIDecisionStatusProposed,
		})
		require.NoError(t, err)
		res := mgr.RejectDecisionsBatch(ctx, other, []uuid.UUID{uuid.UUID(rejDec.ID.Bytes)}, uuid.New())
		require.Len(t, res, 1)
		assert.False(t, res[0].OK, "cross-workspace reject must not succeed")
	})
}

// TestOzonAI_WeeklyReportGeneration verifies weekly report gen with a fake LLM,
// the endpoint, and the once-per-ISO-week guard.
func TestOzonAI_WeeklyReportGeneration(t *testing.T) {
	fx, cleanup := newOzonFixture(t, "ozon-ai-weekly")
	defer cleanup()
	ctx := context.Background()

	// Some campaign stats over the trailing week for the DRR trend.
	c := seedOzonCampaign(t, fx.db, fx.cabinetID, 9400, "CAMPAIGN_STATE_RUNNING", nil, nil)
	for i := 1; i <= 6; i++ {
		seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -i), 1000, 100, 10, 100, 1000)
	}
	seedAIStrategy(t, fx, 1)

	textResp := &llm.ChatResponse{Message: llm.Message{Role: "assistant", Content: "За неделю расход составил около 600 рублей, выручка держалась стабильно."}}
	llmClient := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{textResp}}
	mgr := newAIManager(fx.db, llmClient)

	// Endpoint returns nil before generation.
	before, err := mgr.GetLatestWeeklyReport(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	assert.Nil(t, before)

	generated, err := mgr.GenerateWeeklyReportForCabinetID(ctx, fx.cabinetID)
	require.NoError(t, err)
	assert.True(t, generated)
	assert.Equal(t, 1, llmClient.calls)

	report, err := mgr.GetLatestWeeklyReport(ctx, fx.workspaceID, fx.cabinetID)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Contains(t, report.Text, "расход")
	assert.False(t, report.PeriodStart.IsZero())

	// Second run in the same ISO week is a guarded no-op (no extra LLM call).
	generated2, err := mgr.GenerateWeeklyReportForCabinetID(ctx, fx.cabinetID)
	require.NoError(t, err)
	assert.False(t, generated2)
	assert.Equal(t, 1, llmClient.calls)

	t.Run("disabled llm is a graceful no-op", func(t *testing.T) {
		fx2, cleanup2 := newOzonFixture(t, "ozon-ai-weekly-off")
		defer cleanup2()
		seedAIStrategy(t, fx2, 1)
		mgrOff := newAIManager(fx2.db, &fakeLLM{enabled: false})
		gen, err := mgrOff.GenerateWeeklyReportForCabinetID(ctx, fx2.cabinetID)
		require.NoError(t, err)
		assert.False(t, gen)
	})
}
