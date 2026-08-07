package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// HTTP-facing surface of the AI manager: run/decision listings, the manual
// run gate, and the copilot approve/reject flow.

// resolveAICabinet is the tenancy gate for every AI endpoint.
func (s *OzonAIManagerService) resolveAICabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) error {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.WorkspaceID != workspaceID || cabinet.Marketplace != domain.MarketplaceOzon {
		return apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	return nil
}

// ListRuns returns the ai_runs page for a cabinet (newest first).
func (s *OzonAIManagerService) ListRuns(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.AIRun, int64, error) {
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}
	total, err := s.queries.CountAIRunsByCabinet(ctx, sqlcgen.CountAIRunsByCabinetParams{
		WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: uuidToPgtype(cabinetID),
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count ai runs")
	}
	rows, err := s.queries.ListAIRunsByCabinet(ctx, sqlcgen.ListAIRunsByCabinetParams{
		WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: uuidToPgtype(cabinetID),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list ai runs")
	}
	result := make([]domain.AIRun, 0, len(rows))
	for _, row := range rows {
		result = append(result, aiRunFromSqlc(row))
	}
	return result, total, nil
}

// ListDecisions returns the ai_decisions page with optional status/run filters.
func (s *OzonAIManagerService) ListDecisions(ctx context.Context, workspaceID, cabinetID uuid.UUID, status string, runID *uuid.UUID, limit, offset int32) ([]domain.AIDecision, int64, error) {
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}
	var statusFilter pgtype.Text
	if status != "" {
		statusFilter = textToPgtype(status)
	}
	total, err := s.queries.CountAIDecisions(ctx, sqlcgen.CountAIDecisionsParams{
		WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: uuidToPgtype(cabinetID),
		Status: statusFilter, RunID: uuidToPgtypePtr(runID),
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count ai decisions")
	}
	rows, err := s.queries.ListAIDecisions(ctx, sqlcgen.ListAIDecisionsParams{
		WorkspaceID: uuidToPgtype(workspaceID), SellerCabinetID: uuidToPgtype(cabinetID),
		Status: statusFilter, RunID: uuidToPgtypePtr(runID),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list ai decisions")
	}
	result := make([]domain.AIDecision, 0, len(rows))
	for _, row := range rows {
		result = append(result, aiDecisionFromSqlc(row))
	}
	return result, total, nil
}

// CheckManualRunAllowed gates POST /ozon/ai/run: tenancy, an active AI
// strategy, and no run already in flight (409).
func (s *OzonAIManagerService) CheckManualRunAllowed(ctx context.Context, workspaceID, cabinetID uuid.UUID) error {
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return err
	}
	if _, err := s.queries.GetActiveOzonAIStrategyForCabinet(ctx, uuidToPgtype(cabinetID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperror.New(apperror.ErrValidation, "cabinet has no active ozon_ai_autopilot strategy")
		}
		return fmt.Errorf("load ai strategy: %w", err)
	}
	if _, err := s.queries.GetRunningAIRunForCabinet(ctx, uuidToPgtype(cabinetID)); err == nil {
		return apperror.New(apperror.ErrConflict, "an ai run is already in progress for this cabinet")
	}
	return nil
}

// ApproveDecision applies one 'proposed' (copilot) decision. Guardrails run
// again against fresh data — the cabinet may have changed since the run.
func (s *OzonAIManagerService) ApproveDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error) {
	row, err := s.queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{
		ID: uuidToPgtype(decisionID), WorkspaceID: uuidToPgtype(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "ai decision not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load ai decision: %w", err)
	}
	if row.Status != domain.AIDecisionStatusProposed {
		return nil, apperror.New(apperror.ErrConflict, fmt.Sprintf("decision is %s, only proposed decisions can be approved", row.Status))
	}
	cabinetID := uuidFromPgtype(row.SellerCabinetID)
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}

	proposal, err := proposalFromDecision(row)
	if err != nil {
		return nil, apperror.New(apperror.ErrValidation, "decision payload is not parseable: "+err.Error())
	}

	strategyRow, err := s.queries.GetActiveOzonAIStrategyForCabinet(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrValidation, "cabinet has no active ozon_ai_autopilot strategy anymore")
	}
	if err != nil {
		return nil, fmt.Errorf("load ai strategy: %w", err)
	}
	params := strategyFromSqlc(strategyRow).Params.Merged()

	data, err := s.loadFreshCabinetData(ctx, cabinetID, proposal)
	if err != nil {
		return nil, fmt.Errorf("load fresh cabinet data: %w", err)
	}

	markRejected := func(verdict string) (*domain.AIDecision, error) {
		if markErr := s.queries.SetAIDecisionStatus(ctx, sqlcgen.SetAIDecisionStatusParams{
			ID: row.ID, Status: domain.AIDecisionStatusRejectedByGuardrail,
			Error: textToPgtype(verdict), AppliedBy: uuidToPgtype(userID),
		}); markErr != nil {
			s.logger.Warn().Err(markErr).Msg("failed to mark ai decision guardrail rejection")
		}
		return nil, apperror.New(apperror.ErrValidation, "guardrail rejected the decision: "+verdict)
	}

	if verdict := s.evaluateProposal(ctx, cabinetID, params, proposal, data); verdict != "" {
		return markRejected(verdict)
	}
	applyVerdict, applyErr := s.applyProposal(ctx, workspaceID, cabinetID, proposal, data)
	if applyVerdict != "" {
		return markRejected(applyVerdict)
	}
	if applyErr != nil {
		if markErr := s.queries.SetAIDecisionStatus(ctx, sqlcgen.SetAIDecisionStatusParams{
			ID: row.ID, Status: domain.AIDecisionStatusFailed,
			Error: textToPgtype(truncateError(applyErr.Error())), AppliedBy: uuidToPgtype(userID),
		}); markErr != nil {
			s.logger.Warn().Err(markErr).Msg("failed to mark ai decision failure")
		}
		return nil, fmt.Errorf("apply ai decision: %w", applyErr)
	}

	if err := s.queries.SetAIDecisionStatus(ctx, sqlcgen.SetAIDecisionStatusParams{
		ID: row.ID, Status: domain.AIDecisionStatusApplied, AppliedBy: uuidToPgtype(userID),
	}); err != nil {
		return nil, fmt.Errorf("finalize ai decision: %w", err)
	}
	updated, err := s.queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{
		ID: uuidToPgtype(decisionID), WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, fmt.Errorf("reload ai decision: %w", err)
	}
	result := aiDecisionFromSqlc(updated)
	return &result, nil
}

// RejectDecision marks a 'proposed' decision as rejected by the user.
func (s *OzonAIManagerService) RejectDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error) {
	row, err := s.queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{
		ID: uuidToPgtype(decisionID), WorkspaceID: uuidToPgtype(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "ai decision not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load ai decision: %w", err)
	}
	if row.Status != domain.AIDecisionStatusProposed {
		return nil, apperror.New(apperror.ErrConflict, fmt.Sprintf("decision is %s, only proposed decisions can be rejected", row.Status))
	}
	if err := s.queries.SetAIDecisionStatus(ctx, sqlcgen.SetAIDecisionStatusParams{
		ID: row.ID, Status: domain.AIDecisionStatusRejectedByUser, AppliedBy: uuidToPgtype(userID),
	}); err != nil {
		return nil, fmt.Errorf("reject ai decision: %w", err)
	}
	updated, err := s.queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{
		ID: uuidToPgtype(decisionID), WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err != nil {
		return nil, fmt.Errorf("reload ai decision: %w", err)
	}
	result := aiDecisionFromSqlc(updated)
	return &result, nil
}

// loadFreshCabinetData rebuilds the minimal lookup set the guardrails need
// when a decision is approved later than its run.
func (s *OzonAIManagerService) loadFreshCabinetData(ctx context.Context, cabinetID uuid.UUID, proposal aiProposal) (*aiCabinetData, error) {
	data := &aiCabinetData{
		campaignsByOzonID: map[int64]sqlcgen.OzonCampaign{},
		bidsByCampaignSKU: map[int64]map[int64]float64{},
		cpoBySKU:          map[int64]domain.OzonCPOProduct{},
	}
	campaigns, err := s.queries.ListOzonCampaignsByCabinet(ctx, sqlcgen.ListOzonCampaignsByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID), Limit: 500, Offset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	for _, campaign := range campaigns {
		data.campaignsByOzonID[campaign.OzonCampaignID] = campaign
	}
	if proposal.Target.OzonCampaignID > 0 {
		if campaign, ok := data.campaignsByOzonID[proposal.Target.OzonCampaignID]; ok {
			productRows, listErr := s.queries.ListOzonCampaignProducts(ctx, campaign.ID)
			if listErr != nil {
				return nil, fmt.Errorf("list campaign products: %w", listErr)
			}
			bids := map[int64]float64{}
			for _, row := range productRows {
				if row.IsActive {
					bids[row.Sku] = pgNumericToFloat(row.BidRub)
				}
			}
			data.bidsByCampaignSKU[campaign.OzonCampaignID] = bids
		}
	}
	return data, nil
}

func proposalFromDecision(row sqlcgen.AiDecision) (aiProposal, error) {
	proposal := aiProposal{ActionType: row.ActionType, Rationale: pgTextValue(row.Rationale)}
	if len(row.Target) > 0 {
		if err := json.Unmarshal(row.Target, &proposal.Target); err != nil {
			return proposal, fmt.Errorf("parse target: %w", err)
		}
	}
	var payload struct {
		NewValue *float64 `json:"new_value"`
	}
	if len(row.Proposal) > 0 {
		if err := json.Unmarshal(row.Proposal, &payload); err != nil {
			return proposal, fmt.Errorf("parse proposal: %w", err)
		}
	}
	proposal.NewValue = payload.NewValue
	return proposal, nil
}

func aiRunFromSqlc(row sqlcgen.AiRun) domain.AIRun {
	run := domain.AIRun{
		ID:              uuidFromPgtype(row.ID),
		WorkspaceID:     uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		Status:          row.Status,
		Trigger:         row.Trigger,
		Summary:         pgTextValue(row.Summary),
		Error:           pgTextValue(row.Error),
		StartedAt:       row.StartedAt.Time,
	}
	if row.StrategyID.Valid {
		id := uuidFromPgtype(row.StrategyID)
		run.StrategyID = &id
	}
	if row.PromptTokens.Valid {
		run.PromptTokens = int(row.PromptTokens.Int32)
	}
	if row.CompletionTokens.Valid {
		run.CompletionTokens = int(row.CompletionTokens.Int32)
	}
	if row.FinishedAt.Valid {
		finished := row.FinishedAt.Time
		run.FinishedAt = &finished
	}
	return run
}

func aiDecisionFromSqlc(row sqlcgen.AiDecision) domain.AIDecision {
	decision := domain.AIDecision{
		ID:               uuidFromPgtype(row.ID),
		RunID:            uuidFromPgtype(row.RunID),
		WorkspaceID:      uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID:  uuidFromPgtype(row.SellerCabinetID),
		ActionType:       row.ActionType,
		Target:           json.RawMessage(row.Target),
		Proposal:         json.RawMessage(row.Proposal),
		Rationale:        pgTextValue(row.Rationale),
		ExpectedEffect:   pgTextValue(row.ExpectedEffect),
		GuardrailVerdict: row.GuardrailVerdict,
		Status:           row.Status,
		Error:            pgTextValue(row.Error),
		CreatedAt:        row.CreatedAt.Time,
	}
	if row.AppliedAt.Valid {
		applied := row.AppliedAt.Time
		decision.AppliedAt = &applied
	}
	if row.AppliedBy.Valid {
		id := uuidFromPgtype(row.AppliedBy)
		decision.AppliedBy = &id
	}
	return decision
}
