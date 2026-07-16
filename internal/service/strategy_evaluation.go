package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type StrategyRolloutUpdate struct {
	DesiredMode string `json:"desired_mode"`
	ManualHold  bool   `json:"manual_hold,omitempty"`
	HoldReason  string `json:"hold_reason,omitempty"`
}

func validateStrategyRolloutUpdate(input StrategyRolloutUpdate) (string, error) {
	if input.DesiredMode != "live" && input.DesiredMode != "shadow" {
		return "", apperror.New(apperror.ErrValidation, "desired_mode must be live or shadow")
	}
	if input.ManualHold {
		return domain.RolloutManualHold, nil
	}
	// A requested live rollout always earns live through one confirmed shadow
	// cycle; the worker promotes it after persisting that cycle.
	return domain.RolloutShadowValidating, nil
}

func rolloutFromRow(row sqlcgen.StrategyBindingRolloutRow) domain.StrategyBindingRollout {
	result := domain.StrategyBindingRollout{
		BindingID: uuidFromPgtype(row.BindingID), WorkspaceID: uuidFromPgtype(row.WorkspaceID), StrategyID: uuidFromPgtype(row.StrategyID),
		State: row.State, DesiredMode: row.DesiredMode, ManualHold: row.State == domain.RolloutManualHold,
		HoldReason: row.HoldReason, LastBlockCode: row.LastBlockCode, LastBlockDetail: row.LastBlockDetail,
		ValidationStartedAt: row.ValidationStartedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if row.LiveEnabledAt.Valid {
		value := row.LiveEnabledAt.Time
		result.LiveEnabledAt = &value
	}
	return result
}

func evaluationFactFromRow(row sqlcgen.StrategyEvaluationFactRow) domain.StrategyEvaluationFact {
	return domain.StrategyEvaluationFact{ID: uuidFromPgtype(row.ID), RunID: uuidFromPgtype(row.RunID), Code: row.Code,
		Status: row.Status, Outcome: row.Outcome, Source: row.Source, Value: row.Value, ObservedAt: row.ObservedAt.Time}
}

func evaluationRunFromRow(row sqlcgen.StrategyEvaluationRunRow) domain.StrategyEvaluationRun {
	result := domain.StrategyEvaluationRun{
		ID: uuidFromPgtype(row.ID), WorkspaceID: uuidFromPgtype(row.WorkspaceID), SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		StrategyID: uuidFromPgtype(row.StrategyID), StrategyBindingID: uuidFromPgtype(row.StrategyBindingID), AutomationLevel: int(row.AutomationLevel),
		RolloutState: row.RolloutState, ApplyRequested: row.ApplyRequested, Outcome: row.Outcome, ReasonCode: row.ReasonCode,
		ReasonDetail: row.ReasonDetail, ProposedActions: int(row.ProposedActions), AppliedActions: int(row.AppliedActions), StartedAt: row.StartedAt.Time,
	}
	if row.CampaignID.Valid {
		id := uuidFromPgtype(row.CampaignID)
		result.CampaignID = &id
	}
	if row.ProductID.Valid {
		id := uuidFromPgtype(row.ProductID)
		result.ProductID = &id
	}
	if row.FinishedAt.Valid {
		value := row.FinishedAt.Time
		result.FinishedAt = &value
	}
	return result
}

func (s *StrategyService) UpdateBindingRollout(ctx context.Context, workspaceID, strategyID, bindingID, actorID uuid.UUID, input StrategyRolloutUpdate) (*domain.StrategyBindingRollout, error) {
	state, err := validateStrategyRolloutUpdate(input)
	if err != nil {
		return nil, err
	}
	current, err := s.queries.EnsureStrategyBindingRollout(ctx, uuidToPgtype(workspaceID), uuidToPgtype(strategyID), uuidToPgtype(bindingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "strategy binding not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load strategy rollout")
	}
	// Re-saving an already-live desired mode is idempotent; disabling a manual
	// hold deliberately returns through shadow validation.
	if current.State == domain.RolloutLive && input.DesiredMode == "live" && !input.ManualHold {
		state = domain.RolloutLive
	}
	row, err := s.queries.UpdateStrategyBindingRollout(ctx, sqlcgen.UpdateStrategyBindingRolloutParams{
		WorkspaceID: uuidToPgtype(workspaceID), StrategyID: uuidToPgtype(strategyID), BindingID: uuidToPgtype(bindingID),
		UpdatedBy: uuidToPgtype(actorID), DesiredMode: input.DesiredMode, State: state, HoldReason: input.HoldReason,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "strategy binding not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update strategy rollout")
	}
	result := rolloutFromRow(row)
	return &result, nil
}

func (s *StrategyService) UpdateStrategyRollout(ctx context.Context, workspaceID, strategyID, actorID uuid.UUID, input StrategyRolloutUpdate) ([]domain.StrategyBindingRollout, error) {
	strategy, err := s.Get(ctx, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	result := make([]domain.StrategyBindingRollout, 0, len(strategy.Bindings))
	for _, binding := range strategy.Bindings {
		rollout, updateErr := s.UpdateBindingRollout(ctx, workspaceID, strategyID, binding.ID, actorID, input)
		if updateErr != nil {
			return nil, updateErr
		}
		result = append(result, *rollout)
	}
	return result, nil
}

func (s *StrategyService) ListEvaluationRuns(ctx context.Context, workspaceID, strategyID uuid.UUID, limit, offset int32) ([]domain.StrategyEvaluationRun, error) {
	if _, err := s.Get(ctx, workspaceID, strategyID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListStrategyEvaluationRuns(ctx, uuidToPgtype(workspaceID), uuidToPgtype(strategyID), limit, offset)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list strategy evaluation runs")
	}
	items := make([]domain.StrategyEvaluationRun, len(rows))
	for index, row := range rows {
		items[index] = evaluationRunFromRow(row)
	}
	return items, nil
}

func (s *StrategyService) GetEvaluationRun(ctx context.Context, workspaceID, strategyID, runID uuid.UUID) (*domain.StrategyEvaluationRun, error) {
	row, err := s.queries.GetStrategyEvaluationRun(ctx, uuidToPgtype(workspaceID), uuidToPgtype(strategyID), uuidToPgtype(runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "strategy evaluation run not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load strategy evaluation run")
	}
	result := evaluationRunFromRow(row)
	facts, err := s.queries.ListStrategyEvaluationFacts(ctx, row.ID)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load strategy evaluation facts")
	}
	result.Facts = make([]domain.StrategyEvaluationFact, len(facts))
	for index, fact := range facts {
		result.Facts[index] = evaluationFactFromRow(fact)
	}
	return &result, nil
}

func (s *StrategyService) Activity(ctx context.Context, workspaceID, strategyID uuid.UUID) (*domain.StrategyActivity, error) {
	strategy, err := s.Get(ctx, workspaceID, strategyID)
	if err != nil {
		return nil, err
	}
	activity := &domain.StrategyActivity{StrategyID: strategyID, Status: "idle", Campaigns: []domain.StrategyActivityCampaign{},
		Facts: []domain.StrategyEvaluationFact{}, Blockers: []domain.StrategyActivityBlocker{}, DataFreshness: domain.StrategyDataFreshness{State: "unknown"}}
	for _, binding := range strategy.Bindings {
		row, rolloutErr := s.queries.EnsureStrategyBindingRollout(ctx, uuidToPgtype(workspaceID), uuidToPgtype(strategyID), uuidToPgtype(binding.ID))
		if rolloutErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to load strategy rollout")
		}
		rollout := rolloutFromRow(row)
		activity.Campaigns = append(activity.Campaigns, domain.StrategyActivityCampaign{BindingID: binding.ID, CampaignID: binding.CampaignID, ProductID: binding.ProductID, Rollout: rollout})
		switch rollout.State {
		case domain.RolloutManualHold:
			activity.Status = domain.RolloutManualHold
		case domain.RolloutBlocked:
			if activity.Status != domain.RolloutManualHold {
				activity.Status = domain.RolloutBlocked
			}
			activity.Blockers = append(activity.Blockers, domain.StrategyActivityBlocker{Code: rollout.LastBlockCode, Detail: rollout.LastBlockDetail})
		case domain.RolloutLive:
			if activity.Status == "idle" || activity.Status == domain.RolloutShadowValidating {
				activity.Status = domain.RolloutLive
			}
		case domain.RolloutShadowValidating:
			if activity.Status == "idle" {
				activity.Status = domain.RolloutShadowValidating
			}
		}
	}
	runs, err := s.ListEvaluationRuns(ctx, workspaceID, strategyID, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(runs) > 0 {
		latest, getErr := s.GetEvaluationRun(ctx, workspaceID, strategyID, runs[0].ID)
		if getErr != nil {
			return nil, getErr
		}
		activity.LatestRun, activity.Facts = latest, latest.Facts
		for _, fact := range latest.Facts {
			if fact.Outcome == domain.FactBlocked {
				activity.Blockers = append(activity.Blockers, domain.StrategyActivityBlocker{Code: fact.Code, Detail: string(fact.Value)})
			}
		}
	}
	// next_check_at stays null: scheduler cadence is runtime configuration and
	// this service deliberately does not invent a timestamp.
	return activity, nil
}
