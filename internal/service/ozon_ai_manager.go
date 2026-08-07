package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/llm"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// aiMaxLLMCalls is the hard cap per cabinet run. The free-tier endpoint takes
// minutes per request at ~40 RPM globally — two calls is the whole budget:
// one open call, and at most one follow-up with submit_proposals forced.
const aiMaxLLMCalls = 2

// aiChatClient is the LLM surface the manager needs (mockable in tests).
type aiChatClient interface {
	ChatCompletion(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
	Enabled() bool
	Model() string
}

// OzonAIManagerService is the phase-3 AI autopilot: it packs cabinet context,
// asks the LLM for proposals, validates every proposal with deterministic
// guardrails, and applies the survivors through the same audited write paths
// the manual endpoints use. The LLM never touches Ozon directly.
type OzonAIManagerService struct {
	queries       *sqlcgen.Queries
	perfClient    *ozon.PerfClient
	actions       *OzonCampaignActionsService
	llm           aiChatClient
	encryptionKey []byte
	logger        zerolog.Logger
}

// NewOzonAIManagerService wires the AI manager. llmClient may be a disabled
// client (empty key): runs are then recorded as 'skipped'.
func NewOzonAIManagerService(queries *sqlcgen.Queries, perfClient *ozon.PerfClient, actions *OzonCampaignActionsService, llmClient aiChatClient, encryptionKey []byte, logger zerolog.Logger) *OzonAIManagerService {
	return &OzonAIManagerService{
		queries:       queries,
		perfClient:    perfClient,
		actions:       actions,
		llm:           llmClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "ozon_ai_manager").Logger(),
	}
}

// Enabled reports whether the LLM side is configured.
func (s *OzonAIManagerService) Enabled() bool {
	return s.llm != nil && s.llm.Enabled()
}

// ListAICabinetIDs enumerates cabinets with an active AI autopilot strategy
// (worker sweep fan-out).
func (s *OzonAIManagerService) ListAICabinetIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.queries.ListActiveOzonAICabinets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ai cabinets: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, uuidFromPgtype(row))
	}
	return ids, nil
}

// RunForCabinetID resolves the cabinet and its active AI strategy, then runs.
// A cabinet without an active ozon_ai_autopilot strategy is a silent no-op.
func (s *OzonAIManagerService) RunForCabinetID(ctx context.Context, cabinetID uuid.UUID, trigger string) error {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.Marketplace != domain.MarketplaceOzon {
		return apperror.New(apperror.ErrValidation, "seller cabinet is not an Ozon cabinet")
	}
	strategyRow, err := s.queries.GetActiveOzonAIStrategyForCabinet(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		s.logger.Debug().Str("cabinet_id", cabinetID.String()).Msg("no active ai strategy; skipping")
		return nil
	}
	if err != nil {
		return fmt.Errorf("load ai strategy: %w", err)
	}
	strategy := strategyFromSqlc(strategyRow)
	return s.RunForCabinet(ctx, cabinet.WorkspaceID, cabinetID, strategy, trigger)
}

// RunForCabinet executes one AI run. Every failure is recorded on the ai_runs
// row; the returned error is informational — callers must not crash on it.
func (s *OzonAIManagerService) RunForCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID, strategy domain.Strategy, trigger string) error {
	// Sequential cabinet processing: one live run per cabinet at a time.
	if _, err := s.queries.GetRunningAIRunForCabinet(ctx, uuidToPgtype(cabinetID)); err == nil {
		s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("ai run already in progress; skipping")
		return nil
	}

	run, err := s.queries.InsertAIRun(ctx, sqlcgen.InsertAIRunParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(cabinetID),
		StrategyID:      uuidToPgtype(strategy.ID),
		Status:          domain.AIRunStatusRunning,
		Trigger:         trigger,
	})
	if err != nil {
		return fmt.Errorf("insert ai run: %w", err)
	}

	if !s.Enabled() {
		s.finishRun(ctx, run.ID, domain.AIRunStatusSkipped, "ИИ отключён: LLM_API_KEY не настроен", "", llm.Usage{})
		return nil
	}

	summary, usage, execErr := s.execute(ctx, run, workspaceID, cabinetID, strategy)
	if execErr != nil {
		s.logger.Error().Err(execErr).
			Str("cabinet_id", cabinetID.String()).
			Str("run_id", uuidFromPgtype(run.ID).String()).
			Msg("ai run failed")
		s.finishRun(ctx, run.ID, domain.AIRunStatusFailed, "", truncateError(execErr.Error()), usage)
		return fmt.Errorf("ai run %s: %w", uuidFromPgtype(run.ID), execErr)
	}
	s.finishRun(ctx, run.ID, domain.AIRunStatusCompleted, summary, "", usage)
	return nil
}

func (s *OzonAIManagerService) finishRun(ctx context.Context, runID pgtype.UUID, status, summary, errText string, usage llm.Usage) {
	var summaryText, errPg pgtype.Text
	if summary != "" {
		summaryText = textToPgtype(summary)
	}
	if errText != "" {
		errPg = textToPgtype(errText)
	}
	if err := s.queries.FinishAIRun(ctx, sqlcgen.FinishAIRunParams{
		ID:               runID,
		Status:           status,
		Summary:          summaryText,
		Error:            errPg,
		PromptTokens:     pgtype.Int4{Int32: int32(usage.PromptTokens), Valid: true},
		CompletionTokens: pgtype.Int4{Int32: int32(usage.CompletionTokens), Valid: true},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to finalize ai run")
	}
}

// --- the agentic cycle (hard-capped at 2 LLM calls) ---

// aiProposal is one item of the submit_proposals tool call.
type aiProposal struct {
	ActionType     string                  `json:"action_type"`
	Target         domain.AIDecisionTarget `json:"target"`
	NewValue       *float64                `json:"new_value"`
	Rationale      string                  `json:"rationale"`
	ExpectedEffect string                  `json:"expected_effect"`
}

type aiSubmission struct {
	Summary   string       `json:"summary"`
	Proposals []aiProposal `json:"proposals"`
}

type aiDataRequest struct {
	What        string  `json:"what"`
	CampaignIDs []int64 `json:"campaign_ids"`
	SKUs        []int64 `json:"skus"`
}

func (s *OzonAIManagerService) execute(ctx context.Context, run sqlcgen.AiRun, workspaceID, cabinetID uuid.UUID, strategy domain.Strategy) (string, llm.Usage, error) {
	params := strategy.Params.Merged()
	usage := llm.Usage{}

	pack, data, err := s.buildAIContext(ctx, workspaceID, cabinetID, strategy, params)
	if err != nil {
		return "", usage, fmt.Errorf("build context pack: %w", err)
	}
	packJSON, err := marshalAIContextPack(pack, aiContextPackMaxBytes)
	if err != nil {
		return "", usage, err
	}

	messages := []llm.Message{
		{Role: "system", Content: aiSystemPrompt(params)},
		{Role: "user", Content: "Данные кабинета (JSON):\n" + string(packJSON)},
	}
	tools := aiTools()

	// Call 1: open — the model may ask for extra data once or submit directly.
	resp, err := s.llm.ChatCompletion(ctx, llm.ChatRequest{Messages: messages, Tools: tools, ToolChoice: "auto"})
	if err != nil {
		return "", usage, fmt.Errorf("llm call 1: %w", err)
	}
	usage = addUsage(usage, resp.Usage)

	var submission *aiSubmission
	call := resp.FirstToolCall()
	switch {
	case call != nil && call.Function.Name == "submit_proposals":
		submission, err = parseSubmission(call.Function.Arguments)
		if err != nil {
			return "", usage, err
		}
	default:
		// Either request_data or free text: both consume the second (and last)
		// call with submit_proposals forced.
		messages = append(messages, resp.Message)
		if call != nil && call.Function.Name == "request_data" {
			toolResult := s.handleDataRequest(ctx, workspaceID, call.Function.Arguments, data)
			messages = append(messages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: toolResult})
		} else {
			messages = append(messages, llm.Message{Role: "user", Content: "Сформируй финальные предложения: вызови submit_proposals."})
		}
		resp2, err2 := s.llm.ChatCompletion(ctx, llm.ChatRequest{
			Messages: messages, Tools: tools, ToolChoice: llm.ForceTool("submit_proposals"),
		})
		if err2 != nil {
			return "", usage, fmt.Errorf("llm call 2: %w", err2)
		}
		usage = addUsage(usage, resp2.Usage)
		call2 := resp2.FirstToolCall()
		if call2 == nil || call2.Function.Name != "submit_proposals" {
			return "", usage, fmt.Errorf("model did not call submit_proposals within %d calls", aiMaxLLMCalls)
		}
		submission, err = parseSubmission(call2.Function.Arguments)
		if err != nil {
			return "", usage, err
		}
	}

	applied, rejected := 0, 0
	for _, proposal := range submission.Proposals {
		status, procErr := s.processProposal(ctx, run, workspaceID, cabinetID, params, proposal, data)
		if procErr != nil {
			s.logger.Warn().Err(procErr).Str("action", proposal.ActionType).Msg("failed to process ai proposal")
			continue
		}
		switch status {
		case domain.AIDecisionStatusRejectedByGuardrail, domain.AIDecisionStatusFailed:
			rejected++
		case domain.AIDecisionStatusAutoApplied:
			applied++
		}
	}
	s.logger.Info().
		Str("cabinet_id", cabinetID.String()).
		Int("proposals", len(submission.Proposals)).
		Int("auto_applied", applied).
		Int("rejected", rejected).
		Int("prompt_tokens", usage.PromptTokens).
		Int("completion_tokens", usage.CompletionTokens).
		Msg("ai run completed")
	return submission.Summary, usage, nil
}

func parseSubmission(arguments string) (*aiSubmission, error) {
	var submission aiSubmission
	if err := json.Unmarshal([]byte(arguments), &submission); err != nil {
		return nil, fmt.Errorf("parse submit_proposals arguments: %w", err)
	}
	return &submission, nil
}

func addUsage(total, add llm.Usage) llm.Usage {
	total.PromptTokens += add.PromptTokens
	total.CompletionTokens += add.CompletionTokens
	total.TotalTokens += add.TotalTokens
	return total
}

// handleDataRequest serves the request_data tool. competitive_bids hits the
// Performance API (the one permitted call class); the other kinds have no
// backing tables yet and answer honestly so the model does not hallucinate.
func (s *OzonAIManagerService) handleDataRequest(ctx context.Context, workspaceID uuid.UUID, arguments string, data *aiCabinetData) string {
	var req aiDataRequest
	if err := json.Unmarshal([]byte(arguments), &req); err != nil {
		return `{"error":"не удалось разобрать запрос данных"}`
	}
	switch req.What {
	case "competitive_bids":
		type campaignBids struct {
			OzonCampaignID int64                 `json:"ozon_campaign_id"`
			Bids           []ozon.CompetitiveBid `json:"bids,omitempty"`
			Error          string                `json:"error,omitempty"`
		}
		out := make([]campaignBids, 0, len(req.CampaignIDs))
		// At most 3 campaigns per request keeps Ozon quota + latency bounded.
		ids := req.CampaignIDs
		if len(ids) > 3 {
			ids = ids[:3]
		}
		for _, ozonID := range ids {
			campaign, ok := data.campaignsByOzonID[ozonID]
			if !ok {
				out = append(out, campaignBids{OzonCampaignID: ozonID, Error: "кампания не найдена в кабинете"})
				continue
			}
			bids, err := s.actions.GetCompetitiveBids(ctx, workspaceID, uuidFromPgtype(campaign.ID))
			if err != nil {
				out = append(out, campaignBids{OzonCampaignID: ozonID, Error: truncateError(err.Error())})
				continue
			}
			if len(bids) > 100 {
				bids = bids[:100]
			}
			out = append(out, campaignBids{OzonCampaignID: ozonID, Bids: bids})
		}
		payload, _ := json.Marshal(map[string]any{"competitive_bids": out})
		return string(payload)
	case "search_queries", "price_history":
		return `{"error":"эти данные пока не собираются; работай с тем, что есть в контексте"}`
	default:
		return `{"error":"неизвестный тип данных"}`
	}
}

// --- proposal processing (guardrails → persistence → optional apply) ---

func (s *OzonAIManagerService) processProposal(ctx context.Context, run sqlcgen.AiRun, workspaceID, cabinetID uuid.UUID, params domain.StrategyParams, proposal aiProposal, data *aiCabinetData) (string, error) {
	verdict := s.evaluateProposal(ctx, cabinetID, params, proposal, data)

	status := domain.AIDecisionStatusShadow
	guardrailVerdict := "passed"
	if verdict != "" {
		status = domain.AIDecisionStatusRejectedByGuardrail
		guardrailVerdict = verdict
	} else {
		switch params.AutomationLevel {
		case 1:
			status = domain.AIDecisionStatusShadow
		case 2:
			status = domain.AIDecisionStatusProposed
		default: // 3 — autopilot
			status = domain.AIDecisionStatusAutoApplied
		}
	}

	targetJSON, _ := json.Marshal(proposal.Target)
	proposalJSON, _ := json.Marshal(map[string]any{
		"action_type": proposal.ActionType,
		"new_value":   proposal.NewValue,
	})

	// auto_applied rows are inserted as proposed first: the apply step can
	// still fail, and the audit row must reflect what actually happened.
	insertStatus := status
	if status == domain.AIDecisionStatusAutoApplied {
		insertStatus = domain.AIDecisionStatusProposed
	}
	decision, err := s.queries.InsertAIDecision(ctx, sqlcgen.InsertAIDecisionParams{
		RunID:            run.ID,
		WorkspaceID:      uuidToPgtype(workspaceID),
		SellerCabinetID:  uuidToPgtype(cabinetID),
		ActionType:       proposal.ActionType,
		Target:           targetJSON,
		Proposal:         proposalJSON,
		Rationale:        textToPgtype(proposal.Rationale),
		ExpectedEffect:   textToPgtype(proposal.ExpectedEffect),
		GuardrailVerdict: guardrailVerdict,
		Status:           insertStatus,
	})
	if err != nil {
		return "", fmt.Errorf("insert ai decision: %w", err)
	}

	if status != domain.AIDecisionStatusAutoApplied {
		return status, nil
	}

	applyVerdict, applyErr := s.applyProposal(ctx, workspaceID, cabinetID, proposal, data)
	final := domain.AIDecisionStatusAutoApplied
	var errText pgtype.Text
	switch {
	case applyVerdict != "":
		final = domain.AIDecisionStatusRejectedByGuardrail
		errText = textToPgtype(applyVerdict)
	case applyErr != nil:
		final = domain.AIDecisionStatusFailed
		errText = textToPgtype(truncateError(applyErr.Error()))
	}
	if markErr := s.queries.SetAIDecisionStatus(ctx, sqlcgen.SetAIDecisionStatusParams{
		ID: decision.ID, Status: final, Error: errText,
	}); markErr != nil {
		s.logger.Warn().Err(markErr).Msg("failed to finalize ai decision status")
	}
	return final, nil
}

// evaluateProposal runs the deterministic pre-checks: target resolution,
// range/percent clamps against mirrored data, campaign state gates, and the
// shared cooldown/daily-cap budget (ozon_bid_changes + ai_decisions history).
// The Ozon min-bid clamp needs a live API call and therefore runs at apply
// time only. Empty return = passed.
func (s *OzonAIManagerService) evaluateProposal(ctx context.Context, cabinetID uuid.UUID, params domain.StrategyParams, proposal aiProposal, data *aiCabinetData) string {
	now := time.Now().UTC()
	switch proposal.ActionType {
	case domain.AIActionBidChange:
		campaign, ok := data.campaignsByOzonID[proposal.Target.OzonCampaignID]
		if !ok {
			return fmt.Sprintf("campaign %d not found in this cabinet", proposal.Target.OzonCampaignID)
		}
		if proposal.Target.SKU <= 0 {
			return "bid_change requires target.sku"
		}
		if proposal.NewValue == nil {
			return "bid_change requires new_value"
		}
		currentBid := data.bidsByCampaignSKU[proposal.Target.OzonCampaignID][proposal.Target.SKU]
		if currentBid <= 0 {
			return fmt.Sprintf("sku %d has no active bid in campaign %d", proposal.Target.SKU, proposal.Target.OzonCampaignID)
		}
		if reason := ozonAIBidGuardReason(currentBid, *proposal.NewValue, 0, params); reason != "" {
			return reason
		}
		return s.bidCooldownReason(ctx, campaign.ID, cabinetID, proposal, params, now)
	case domain.AIActionBudgetChange:
		campaign, ok := data.campaignsByOzonID[proposal.Target.OzonCampaignID]
		if !ok {
			return fmt.Sprintf("campaign %d not found in this cabinet", proposal.Target.OzonCampaignID)
		}
		if proposal.NewValue == nil {
			return "budget_change requires new_value"
		}
		current, weekly := campaignBudget(campaign)
		if reason := ozonAIBudgetGuardReason(current, int64(*proposal.NewValue), weekly, params); reason != "" {
			return reason
		}
		return s.decisionCooldownReason(ctx, cabinetID, proposal, params, now)
	case domain.AIActionCampaignPause, domain.AIActionCampaignActivate:
		campaign, ok := data.campaignsByOzonID[proposal.Target.OzonCampaignID]
		if !ok {
			return fmt.Sprintf("campaign %d not found in this cabinet", proposal.Target.OzonCampaignID)
		}
		if reason := ozonAICampaignStateGuardReason(proposal.ActionType, pgTextValue(campaign.State)); reason != "" {
			return reason
		}
		return s.decisionCooldownReason(ctx, cabinetID, proposal, params, now)
	case domain.AIActionCPOBid:
		if proposal.Target.SKU <= 0 {
			return "cpo_bid requires target.sku"
		}
		if proposal.NewValue == nil {
			return "cpo_bid requires new_value"
		}
		if reason := ozonAICPOBidGuardReason(*proposal.NewValue, 0); reason != "" {
			return reason
		}
		return s.cpoCooldownReason(ctx, cabinetID, proposal, params, now)
	case domain.AIActionCPOEnable, domain.AIActionCPODisable:
		if proposal.Target.SKU <= 0 {
			return proposal.ActionType + " requires target.sku"
		}
		return s.decisionCooldownReason(ctx, cabinetID, proposal, params, now)
	default:
		return fmt.Sprintf("unknown action_type %q", proposal.ActionType)
	}
}

// bidCooldownReason merges the write-side (ozon_bid_changes, strategy+ai) and
// decision-side (ai_decisions) history into one cooldown/daily-cap verdict.
func (s *OzonAIManagerService) bidCooldownReason(ctx context.Context, campaignID pgtype.UUID, cabinetID uuid.UUID, proposal aiProposal, params domain.StrategyParams, now time.Time) string {
	dayStart := pgtype.Timestamptz{Time: ozonStrategyDayStart(now), Valid: true}
	writeGuard, err := s.queries.GetOzonAIBidGuardState(ctx, sqlcgen.GetOzonAIBidGuardStateParams{
		DayStart:   dayStart,
		CampaignID: campaignID,
		Sku:        pgtype.Int8{Int64: proposal.Target.SKU, Valid: true},
	})
	if err != nil {
		return "guard state unavailable: " + truncateError(err.Error())
	}
	return s.mergedCooldownReason(ctx, cabinetID, proposal, params, now, writeGuard.ChangesToday, writeGuard.LastChangeAt)
}

// cpoCooldownReason is the CPO flavor of bidCooldownReason.
func (s *OzonAIManagerService) cpoCooldownReason(ctx context.Context, cabinetID uuid.UUID, proposal aiProposal, params domain.StrategyParams, now time.Time) string {
	dayStart := pgtype.Timestamptz{Time: ozonStrategyDayStart(now), Valid: true}
	writeGuard, err := s.queries.GetOzonAICPOGuardState(ctx, sqlcgen.GetOzonAICPOGuardStateParams{
		DayStart:        dayStart,
		SellerCabinetID: uuidToPgtype(cabinetID),
		Sku:             pgtype.Int8{Int64: proposal.Target.SKU, Valid: true},
	})
	if err != nil {
		return "guard state unavailable: " + truncateError(err.Error())
	}
	return s.mergedCooldownReason(ctx, cabinetID, proposal, params, now, writeGuard.ChangesToday, writeGuard.LastChangeAt)
}

// decisionCooldownReason covers actions with no ozon_bid_changes trail
// (budgets, pause/activate, cpo toggles): ai_decisions history only.
func (s *OzonAIManagerService) decisionCooldownReason(ctx context.Context, cabinetID uuid.UUID, proposal aiProposal, params domain.StrategyParams, now time.Time) string {
	return s.mergedCooldownReason(ctx, cabinetID, proposal, params, now, 0, pgtype.Timestamptz{})
}

func (s *OzonAIManagerService) mergedCooldownReason(ctx context.Context, cabinetID uuid.UUID, proposal aiProposal, params domain.StrategyParams, now time.Time, writeChanges int64, writeLast pgtype.Timestamptz) string {
	targetJSON, _ := json.Marshal(proposal.Target)
	decisionGuard, err := s.queries.GetAIDecisionGuardState(ctx, sqlcgen.GetAIDecisionGuardStateParams{
		DayStart:        pgtype.Timestamptz{Time: ozonStrategyDayStart(now), Valid: true},
		SellerCabinetID: uuidToPgtype(cabinetID),
		ActionType:      proposal.ActionType,
		Target:          targetJSON,
	})
	if err != nil {
		return "ai decision guard state unavailable: " + truncateError(err.Error())
	}
	// The two histories overlap (an applied decision also writes a bid
	// change), so the stricter (max) counter guards, not the sum.
	changes := writeChanges
	if decisionGuard.ChangesToday > changes {
		changes = decisionGuard.ChangesToday
	}
	var last *time.Time
	if writeLast.Valid {
		t := writeLast.Time
		last = &t
	}
	if decisionGuard.LastChangeAt.Valid && (last == nil || decisionGuard.LastChangeAt.Time.After(*last)) {
		t := decisionGuard.LastChangeAt.Time
		last = &t
	}
	return ozonStrategyGuardReason(changes, last, params, now)
}

func campaignBudget(campaign sqlcgen.OzonCampaign) (current *int64, weekly bool) {
	// The budget field the campaign already uses wins; weekly when both or
	// only weekly is set (Ozon CPC campaigns are typically weekly-budgeted).
	if campaign.WeeklyBudgetRub.Valid && campaign.WeeklyBudgetRub.Int64 > 0 {
		v := campaign.WeeklyBudgetRub.Int64
		return &v, true
	}
	if campaign.DailyBudgetRub.Valid && campaign.DailyBudgetRub.Int64 > 0 {
		v := campaign.DailyBudgetRub.Int64
		return &v, false
	}
	return nil, true
}

// applyProposal performs the real write through the audited action paths.
// It returns (guardrailReason, err): a non-empty reason means the apply-time
// clamp (Ozon minimum bids) rejected the write.
func (s *OzonAIManagerService) applyProposal(ctx context.Context, workspaceID, cabinetID uuid.UUID, proposal aiProposal, data *aiCabinetData) (string, error) {
	reason := "AI: " + truncateError(proposal.Rationale)
	switch proposal.ActionType {
	case domain.AIActionBidChange:
		campaign := data.campaignsByOzonID[proposal.Target.OzonCampaignID]
		// Apply-time clamp: Ozon's real per-SKU minimum. A failed lookup
		// blocks the write — without real minimums it is not provably valid
		// (same rule as the deterministic strategy).
		minBid, err := s.minSKUBid(ctx, workspaceID, cabinetID, proposal.Target.SKU, "CPC")
		if err != nil {
			return "", fmt.Errorf("min sku bid unavailable, live write blocked: %w", err)
		}
		if minBid > 0 && *proposal.NewValue < minBid {
			return fmt.Sprintf("proposed bid %.2f₽ is below Ozon minimum %.2f₽", *proposal.NewValue, minBid), nil
		}
		_, err = s.actions.SetProductBidsWithSource(ctx, workspaceID, uuidFromPgtype(campaign.ID),
			[]OzonBidInput{{SKU: proposal.Target.SKU, BidRub: roundRub(*proposal.NewValue)}},
			domain.BidSourceAI, reason)
		return "", err
	case domain.AIActionBudgetChange:
		campaign := data.campaignsByOzonID[proposal.Target.OzonCampaignID]
		value := int64(*proposal.NewValue)
		_, weekly := campaignBudget(campaign)
		if weekly {
			return "", s.actions.UpdateBudget(ctx, workspaceID, uuidFromPgtype(campaign.ID), nil, &value)
		}
		return "", s.actions.UpdateBudget(ctx, workspaceID, uuidFromPgtype(campaign.ID), &value, nil)
	case domain.AIActionCampaignPause:
		campaign := data.campaignsByOzonID[proposal.Target.OzonCampaignID]
		return "", s.actions.DeactivateCampaign(ctx, workspaceID, uuidFromPgtype(campaign.ID))
	case domain.AIActionCampaignActivate:
		campaign := data.campaignsByOzonID[proposal.Target.OzonCampaignID]
		return "", s.actions.ActivateCampaign(ctx, workspaceID, uuidFromPgtype(campaign.ID))
	case domain.AIActionCPOBid:
		// CPO minimums are best-effort: CPO charges per order, so an
		// unavailable minimum does not block (Ozon rejects invalid bids).
		if minBid, err := s.cpoMinBid(ctx, workspaceID, cabinetID, proposal.Target.SKU); err == nil {
			if verdict := ozonAICPOBidGuardReason(*proposal.NewValue, minBid); verdict != "" {
				return verdict, nil
			}
		}
		return "", s.actions.SetCPOBidsWithSource(ctx, workspaceID, cabinetID,
			[]OzonCPOBidInput{{SKU: proposal.Target.SKU, BidRub: roundRub(*proposal.NewValue)}},
			domain.BidSourceAI, reason)
	case domain.AIActionCPOEnable:
		return "", s.actions.SetCPOEnabledWithSource(ctx, workspaceID, cabinetID, []int64{proposal.Target.SKU}, true, domain.BidSourceAI, reason)
	case domain.AIActionCPODisable:
		return "", s.actions.SetCPOEnabledWithSource(ctx, workspaceID, cabinetID, []int64{proposal.Target.SKU}, false, domain.BidSourceAI, reason)
	default:
		return "", fmt.Errorf("unknown action_type %q", proposal.ActionType)
	}
}

func (s *OzonAIManagerService) cabinetPerfCreds(ctx context.Context, workspaceID, cabinetID uuid.UUID) (ozon.Credentials, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return ozon.Credentials{}, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.WorkspaceID != workspaceID || cabinet.Marketplace != domain.MarketplaceOzon {
		return ozon.Credentials{}, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return ozon.Credentials{}, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	if !creds.HasPerformanceAPI() {
		return ozon.Credentials{}, apperror.New(apperror.ErrValidation, "performance api credentials are missing for this cabinet")
	}
	return ozonClientCreds(creds), nil
}

func (s *OzonAIManagerService) minSKUBid(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku int64, paymentType string) (float64, error) {
	creds, err := s.cabinetPerfCreds(ctx, workspaceID, cabinetID)
	if err != nil {
		return 0, err
	}
	rows, err := s.perfClient.GetMinSKUBids(ctx, creds, []int64{sku}, paymentType)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.SKU == sku {
			return row.BidRub, nil
		}
	}
	return 0, nil
}

func (s *OzonAIManagerService) cpoMinBid(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku int64) (float64, error) {
	creds, err := s.cabinetPerfCreds(ctx, workspaceID, cabinetID)
	if err != nil {
		return 0, err
	}
	rows, err := s.perfClient.GetCPOMinBids(ctx, creds, []int64{sku})
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.SKU == sku {
			return row.BidRub, nil
		}
	}
	return 0, nil
}

// --- prompt + tools ---

// aiSystemPrompt is the Russian system prompt: an experienced Ozon ads
// manager optimizing toward the strategy's target ДРР under hard rules the
// guardrails re-enforce in code afterwards.
func aiSystemPrompt(params domain.StrategyParams) string {
	return fmt.Sprintf(`Ты — опытный менеджер рекламы Ozon. Управляешь кампаниями кабинета продавца на основе данных за 14 дней.

Цель: привести ДРР (доля рекламных расходов = расход / выручка × 100%%) кабинета к целевому уровню %.1f%% без потери объёма заказов.

Жёсткие правила (нарушения отклонит автоматика):
- Не предлагай изменение ставки или бюджета больше чем на %.0f%% от текущего значения.
- Ставки CPC держи в диапазоне %d–%d ₽ и не ниже минимальной ставки Ozon.
- Учитывай маржу SKU: маржа ≈ price − net_price − price × commission_pct / 100. Не масштабируй SKU с отрицательной или неизвестной маржой.
- CPO («Продвижение в поиске») списывает деньги только за заказ — безрисково по ДРР: включай смело для SKU с положительной маржой.
- Кампании с ДРР сильно выше цели — снижай ставки или ставь на паузу; с ДРР ниже цели и хорошей маржой — масштабируй.
- Не трогай кампании без статистики за окно (мало данных — не действие, а наблюдение).
- Каждое предложение обосновывай цифрами из контекста (rationale) и ожидаемым эффектом (expected_effect).

Инструменты:
- request_data — ОДИН дополнительный запрос данных, только если без них решение невозможно (конкурентные ставки и т.п.).
- submit_proposals — финальный список предложений + краткое резюме по кабинету (summary, по-русски).

Действия: bid_change (target: ozon_campaign_id+sku, new_value = ставка ₽), budget_change (target: ozon_campaign_id, new_value = бюджет ₽ в том поле, которое кампания уже использует), campaign_pause / campaign_activate (target: ozon_campaign_id), cpo_bid (target: sku, new_value = фикс. ставка ₽), cpo_enable / cpo_disable (target: sku).

Если менять нечего — вызови submit_proposals с пустым списком proposals и объясни в summary, почему.`,
		params.TargetACoS, params.MaxChangePercent, params.MinBid, params.MaxBid)
}

func aiTools() []llm.Tool {
	return []llm.Tool{
		llm.NewFunctionTool("submit_proposals",
			"Отправить финальный список предложений по кабинету. Вызывается ровно один раз.",
			`{
  "type": "object",
  "properties": {
    "summary": {"type": "string", "description": "Краткое резюме анализа кабинета по-русски"},
    "proposals": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "action_type": {"type": "string", "enum": ["bid_change", "budget_change", "campaign_pause", "campaign_activate", "cpo_bid", "cpo_enable", "cpo_disable"]},
          "target": {
            "type": "object",
            "properties": {
              "ozon_campaign_id": {"type": "integer"},
              "sku": {"type": "integer"}
            }
          },
          "new_value": {"type": ["number", "null"], "description": "Новая ставка/бюджет в рублях; null для pause/activate/enable/disable"},
          "rationale": {"type": "string", "description": "Обоснование цифрами из контекста"},
          "expected_effect": {"type": "string", "description": "Ожидаемый эффект"}
        },
        "required": ["action_type", "target", "rationale"]
      }
    }
  },
  "required": ["summary", "proposals"]
}`),
		llm.NewFunctionTool("request_data",
			"Запросить дополнительные данные (доступно не более одного запроса за прогон).",
			`{
  "type": "object",
  "properties": {
    "what": {"type": "string", "enum": ["competitive_bids", "search_queries", "price_history"]},
    "campaign_ids": {"type": "array", "items": {"type": "integer"}, "description": "ozon_campaign_id (максимум 3)"},
    "skus": {"type": "array", "items": {"type": "integer"}}
  },
  "required": ["what"]
}`),
	}
}
