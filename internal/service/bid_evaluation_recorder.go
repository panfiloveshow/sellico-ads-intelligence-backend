package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type bindingEvaluationRecorder struct {
	service               *BidAutomationService
	runID                 uuid.UUID
	bindingID             uuid.UUID
	rollout               domain.StrategyBindingRollout
	closed                bool
	fullyEvaluatedTargets int
	proposedTargets       int
}

func (r *bindingEvaluationRecorder) evaluatedTarget() {
	if r != nil {
		r.fullyEvaluatedTargets++
	}
}

func (r *bindingEvaluationRecorder) proposedTarget() {
	if r != nil {
		r.proposedTargets++
	}
}

func clusterEvaluationCompletion(r *bindingEvaluationRecorder, applied int) string {
	if applied > 0 {
		return "applied"
	}
	if r == nil || r.fullyEvaluatedTargets == 0 {
		return "blocked"
	}
	if r.proposedTargets > 0 {
		return "shadow_decision"
	}
	return "ready_no_change"
}

func (s *BidAutomationService) beginBindingEvaluation(ctx context.Context, strategy domain.Strategy, binding domain.StrategyBinding, workspaceLiveAllowed bool) (*bindingEvaluationRecorder, bool) {
	row, err := s.queries.EnsureStrategyBindingRollout(ctx, uuidToPgtype(strategy.WorkspaceID), uuidToPgtype(strategy.ID), uuidToPgtype(binding.ID))
	if err != nil {
		s.logger.Warn().Err(err).Str("binding_id", binding.ID.String()).Msg("failed to initialize strategy evaluation rollout")
		return nil, false
	}
	rollout := rolloutFromRow(row)
	if rollout.State == domain.RolloutBlocked && rollout.DesiredMode == "live" {
		_ = s.queries.SetStrategyBindingRolloutSystemState(ctx, sqlcgen.SetStrategyBindingRolloutSystemStateParams{BindingID: row.BindingID, State: domain.RolloutShadowValidating})
		row, _ = s.queries.GetStrategyBindingRollout(ctx, uuidToPgtype(strategy.WorkspaceID), uuidToPgtype(strategy.ID), uuidToPgtype(binding.ID))
		rollout = rolloutFromRow(row)
	}
	apply := workspaceLiveAllowed && rollout.State == domain.RolloutLive
	run, err := s.queries.CreateStrategyEvaluationRun(ctx, sqlcgen.CreateStrategyEvaluationRunParams{
		WorkspaceID: uuidToPgtype(strategy.WorkspaceID), SellerCabinetID: uuidToPgtype(strategy.SellerCabinetID),
		StrategyID: uuidToPgtype(strategy.ID), StrategyBindingID: uuidToPgtype(binding.ID),
		CampaignID: uuidToPgtypePtr(binding.CampaignID), ProductID: uuidToPgtypePtr(binding.ProductID),
		AutomationLevel: boundedInt32(strategy.Params.Merged().AutomationLevel), RolloutState: rollout.State, ApplyRequested: apply,
	})
	if err != nil {
		s.logger.Warn().Err(err).Str("binding_id", binding.ID.String()).Msg("failed to create strategy evaluation run")
		return nil, apply
	}
	recorder := &bindingEvaluationRecorder{service: s, runID: uuidFromPgtype(run.ID), bindingID: binding.ID, rollout: rollout}
	if rollout.State == domain.RolloutManualHold {
		recorder.fact(ctx, "binding_manual_hold", "blocked", domain.FactBlocked, "strategy_binding_rollouts", map[string]any{"reason": rollout.HoldReason})
	}
	return recorder, apply
}

func (r *bindingEvaluationRecorder) fact(ctx context.Context, code, status, outcome, source string, value any) {
	if r == nil || r.closed {
		return
	}
	payload, err := json.Marshal(value)
	if err != nil {
		payload = []byte(`{}`)
	}
	if _, err := r.service.queries.CreateStrategyEvaluationFact(ctx, sqlcgen.CreateStrategyEvaluationFactParams{
		RunID: uuidToPgtype(r.runID), Code: code, Status: status, Outcome: outcome, Source: source, Value: payload,
	}); err != nil {
		r.service.logger.Warn().Err(err).Str("run_id", r.runID.String()).Str("reason_code", code).Msg("failed to persist strategy evaluation fact")
	}
}

func (r *bindingEvaluationRecorder) finish(ctx context.Context, outcome, code, detail string, proposed, applied int, factOutcome string) {
	if r == nil || r.closed {
		return
	}
	status := "observed"
	if outcome == domain.EvaluationBlocked {
		status = "blocked"
	}
	if outcome == domain.EvaluationFailed {
		status = "error"
	}
	r.fact(ctx, code, status, factOutcome, "bid_automation", map[string]any{"detail": detail, "proposed_actions": proposed, "applied_actions": applied})
	if err := r.service.queries.CompleteStrategyEvaluationRun(ctx, sqlcgen.CompleteStrategyEvaluationRunParams{
		ID: uuidToPgtype(r.runID), Outcome: outcome, ReasonCode: code, ReasonDetail: detail,
		ProposedActions: boundedInt32(proposed), AppliedActions: boundedInt32(applied),
	}); err != nil {
		r.service.logger.Warn().Err(err).Str("run_id", r.runID.String()).Msg("failed to complete strategy evaluation run")
	}
	r.closed = true
}

func (r *bindingEvaluationRecorder) block(ctx context.Context, code, detail string, markRollout bool) {
	if r == nil {
		return
	}
	if markRollout && r.rollout.DesiredMode == "live" && r.rollout.State != domain.RolloutManualHold {
		_ = r.service.queries.SetStrategyBindingRolloutSystemState(ctx, sqlcgen.SetStrategyBindingRolloutSystemStateParams{
			BindingID: uuidToPgtype(r.bindingID), State: domain.RolloutBlocked, BlockCode: code, BlockDetail: detail,
		})
	}
	r.finish(ctx, domain.EvaluationBlocked, code, detail, 0, 0, domain.FactBlocked)
}

func (r *bindingEvaluationRecorder) readyNoChange(ctx context.Context, code, detail string) {
	if r == nil {
		return
	}
	r.finish(ctx, domain.EvaluationNoDecision, code, detail, 0, 0, domain.FactReadyNoChange)
	r.promoteAfterValidation(ctx)
}

func (r *bindingEvaluationRecorder) shadowDecision(ctx context.Context, code, detail string) {
	if r == nil {
		return
	}
	r.finish(ctx, domain.EvaluationShadowDecision, code, detail, 1, 0, domain.FactWouldApply)
	r.promoteAfterValidation(ctx)
}

func (r *bindingEvaluationRecorder) promoteAfterValidation(ctx context.Context) {
	if r == nil || r.rollout.DesiredMode != "live" || r.rollout.State != domain.RolloutShadowValidating {
		return
	}
	if err := r.service.queries.SetStrategyBindingRolloutSystemState(ctx, sqlcgen.SetStrategyBindingRolloutSystemStateParams{
		BindingID: uuidToPgtype(r.bindingID), State: domain.RolloutLive,
	}); err != nil {
		r.service.logger.Warn().Err(err).Str("binding_id", r.bindingID.String()).Msg("failed to promote validated strategy binding to live")
	}
}

func (s *BidAutomationService) recordStrategyBlocked(ctx context.Context, strategy domain.Strategy, code, detail string) {
	for _, binding := range strategy.Bindings {
		recorder, _ := s.beginBindingEvaluation(ctx, strategy, binding, false)
		recorder.block(ctx, code, detail, true)
	}
}
