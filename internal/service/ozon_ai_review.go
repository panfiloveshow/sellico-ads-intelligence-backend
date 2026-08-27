package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

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

// ProductInsights exposes the AI-context signals (склад, воронка, рейтинг,
// маржа) per product of one campaign — the manager sees what the model sees.
// Every enrichment source is best-effort: an unavailable bridge just leaves
// its fields nil.
func (s *OzonAIManagerService) ProductInsights(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]domain.OzonProductInsight, error) {
	campaignRow, err := s.queries.GetOzonCampaignByID(ctx, uuidToPgtype(campaignID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "ozon campaign not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load ozon campaign: %w", err)
	}
	cabinetID := uuidFromPgtype(campaignRow.SellerCabinetID)
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}
	productRows, err := s.queries.ListOzonCampaignProducts(ctx, campaignRow.ID)
	if err != nil {
		return nil, fmt.Errorf("list campaign products: %w", err)
	}

	stocks := loadCabinetStocks(ctx, s.queries, s.logger, cabinetID)
	unitsPerDay := map[int64]float64{}
	if velocityRows, velErr := s.queries.OzonSalesVelocityByCabinet(ctx, sqlcgen.OzonSalesVelocityByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            pgtype.Date{Time: time.Now().UTC().AddDate(0, 0, -28), Valid: true},
	}); velErr == nil {
		for _, row := range velocityRows {
			if row.Units > 0 {
				unitsPerDay[row.Sku] = float64(row.Units) / 28
			}
		}
	}
	funnelBySKU, _ := s.loadCardFunnel(ctx, cabinetID)
	_, ratingByName := s.loadReviewRatings(ctx, cabinetID)

	// Маржа и offer_id — из зеркала цен (тот же расчёт, что в контексте ИИ).
	skus := make([]int64, 0, len(productRows))
	for _, row := range productRows {
		skus = append(skus, row.Sku)
	}
	marginBySKU := map[int64]*float64{}
	offerBySKU := map[int64]string{}
	if len(skus) > 0 {
		if priceRows, priceErr := s.queries.ListOzonProductPricesBySkus(ctx, sqlcgen.ListOzonProductPricesBySkusParams{
			SellerCabinetID: uuidToPgtype(cabinetID), Skus: skus,
		}); priceErr == nil {
			for _, row := range priceRows {
				offerBySKU[row.Sku] = pgTextValue(row.OfferID)
				price := pgNumericToFloatPtr(row.PriceRub)
				cost := pgNumericToFloatPtr(row.NetPriceRub)
				if price != nil && cost != nil {
					commission := pgNumericToFloat(row.CommissionFboPct)
					if fbs := pgNumericToFloat(row.CommissionFbsPct); fbs > commission {
						commission = fbs
					}
					marginBySKU[row.Sku] = ozonSKUMarginPct(*price, *cost, commission, pgNumericToFloat(row.AcquiringPct))
				}
			}
		}
	}

	insights := make([]domain.OzonProductInsight, 0, len(productRows))
	for _, row := range productRows {
		insight := domain.OzonProductInsight{SKU: row.Sku, MarginPct: marginBySKU[row.Sku]}
		if stock, ok := stocks[row.Sku]; ok {
			v := stock
			insight.Stock = &v
			if perDay := unitsPerDay[row.Sku]; perDay > 0 {
				cover := roundRub(float64(stock) / perDay)
				insight.DaysOfCover = &cover
			}
		}
		funnel, hasFunnel := funnelBySKU[strconv.FormatInt(row.Sku, 10)]
		if !hasFunnel {
			if offer := offerBySKU[row.Sku]; offer != "" {
				funnel, hasFunnel = funnelBySKU[offer]
			}
		}
		if hasFunnel {
			views := funnel.CardViews
			insight.CardViews14d = &views
			if views > 0 {
				toCart := roundRub(float64(funnel.CartAdds) / float64(views) * 100)
				insight.ConvToCartPct = &toCart
				toOrder := roundRub(float64(funnel.Orders) / float64(views) * 100)
				insight.ConvToOrderPct = &toOrder
			}
			if rating, ok := ratingByName[funnel.Name]; ok {
				r, cnt := rating.Rating, rating.ReviewsCount
				insight.Rating = &r
				insight.ReviewsCount = &cnt
			}
		}
		insights = append(insights, insight)
	}
	return insights, nil
}

// ExpireStaleProposals retires copilot proposals older than 72 hours (SQL-side
// TTL). Best-effort: called from the sweep, a failure only logs.
func (s *OzonAIManagerService) ExpireStaleProposals(ctx context.Context) {
	n, err := s.queries.ExpireStaleProposedAIDecisions(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to expire stale ai proposals")
		return
	}
	if n > 0 {
		s.logger.Info().Int64("expired", n).Msg("stale ai proposals expired")
	}
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
	applyVerdict, applyErr := s.applyProposal(ctx, workspaceID, cabinetID, params, proposal, data)
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

// ApproveDecisionsBatch approves each decision id in turn (same tenancy +
// guardrail path as ApproveDecision), returning a per-id result. A failure on
// one id never aborts the rest — the frontend groups the cards and shows which
// ones went through.
func (s *OzonAIManagerService) ApproveDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult {
	return s.decisionsBatch(ctx, workspaceID, ids, userID, true)
}

// RejectDecisionsBatch is the reject flavor of ApproveDecisionsBatch.
func (s *OzonAIManagerService) RejectDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult {
	return s.decisionsBatch(ctx, workspaceID, ids, userID, false)
}

func (s *OzonAIManagerService) decisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID, approve bool) []domain.AIDecisionBatchResult {
	results := make([]domain.AIDecisionBatchResult, 0, len(ids))
	for _, id := range ids {
		var err error
		if approve {
			_, err = s.ApproveDecision(ctx, workspaceID, id, userID)
		} else {
			_, err = s.RejectDecision(ctx, workspaceID, id, userID)
		}
		res := domain.AIDecisionBatchResult{ID: id, OK: err == nil}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results
}

// loadFreshCabinetData rebuilds the minimal lookup set the guardrails need
// when a decision is approved later than its run.
func (s *OzonAIManagerService) loadFreshCabinetData(ctx context.Context, cabinetID uuid.UUID, proposal aiProposal) (*aiCabinetData, error) {
	data := &aiCabinetData{
		campaignsByOzonID: map[int64]sqlcgen.OzonCampaign{},
		bidsByCampaignSKU: map[int64]map[int64]float64{},
		cpoBySKU:          map[int64]domain.OzonCPOProduct{},
		spend14ByOzonID:   map[int64]float64{},
		stockBySKU:        loadCabinetStocks(ctx, s.queries, s.logger, cabinetID),
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
			// Spend over the same window the run used — the anchor for budget
			// proposals on campaigns without a configured budget.
			since := time.Now().UTC().AddDate(0, 0, -aiPackStatsWindowDays)
			if totals, totalsErr := s.queries.GetOzonCampaignStatsWindowTotals(ctx, sqlcgen.GetOzonCampaignStatsWindowTotalsParams{
				CampaignID: campaign.ID,
				DateFrom:   pgtype.Date{Time: since, Valid: true},
				DateTo:     pgtype.Date{Time: time.Now().UTC(), Valid: true},
			}); totalsErr == nil {
				data.spend14ByOzonID[campaign.OzonCampaignID] = pgNumericToFloat(totals.SpendRub)
			}
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
	// Impact evaluation fields (ozon:ai_impact_sweep).
	decision.OutcomeStatus = pgTextValue(row.OutcomeStatus)
	decision.DRRBefore = pgNumericToFloatPtr(row.DrrBefore)
	decision.DRRAfter = pgNumericToFloatPtr(row.DrrAfter)
	decision.SpendBeforeRub = pgNumericToFloatPtr(row.SpendBeforeRub)
	decision.SpendAfterRub = pgNumericToFloatPtr(row.SpendAfterRub)
	decision.RevenueBeforeRub = pgNumericToFloatPtr(row.RevenueBeforeRub)
	decision.RevenueAfterRub = pgNumericToFloatPtr(row.RevenueAfterRub)
	if row.EvaluatedAt.Valid {
		evaluated := row.EvaluatedAt.Time
		decision.EvaluatedAt = &evaluated
	}
	return decision
}
