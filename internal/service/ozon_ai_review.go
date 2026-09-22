package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

	skus := make([]int64, 0, len(productRows))
	for _, row := range productRows {
		skus = append(skus, row.Sku)
	}
	// Мост идентификаторов: рекламный SKU кампании → артикул → продажные
	// цены/стоки/продажи (см. ozonSKUBridge).
	bridge := s.buildOzonSKUBridge(ctx, cabinetID, skus)
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

	// Продажи за 14 дней (все заказы SKU — ключ прямой, рекламный).
	type salesAgg struct {
		units   int64
		revenue float64
	}
	sales14 := map[int64]salesAgg{}
	if salesRows, salesErr := s.queries.OzonSalesVelocityByCabinet(ctx, sqlcgen.OzonSalesVelocityByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            pgtype.Date{Time: time.Now().UTC().AddDate(0, 0, -14), Valid: true},
	}); salesErr == nil {
		for _, row := range salesRows {
			sales14[row.Sku] = salesAgg{units: row.Units, revenue: pgNumericToFloat(row.RevenueRub)}
		}
	}

	// Продажи, атрибутированные ИМЕННО этой кампании (отчёт Performance API,
	// ozon_campaign_sku_stats) — ключ рекламный SKU, как и в кампании.
	campaignSales := map[int64]salesAgg{}
	if attrRows, attrErr := s.queries.AggregateOzonCampaignSkuStats(ctx, sqlcgen.AggregateOzonCampaignSkuStatsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		OzonCampaignID:  campaignRow.OzonCampaignID,
		DateFrom:        pgtype.Date{Time: time.Now().UTC().AddDate(0, 0, -14), Valid: true},
	}); attrErr == nil {
		for _, row := range attrRows {
			campaignSales[row.Sku] = salesAgg{units: row.Orders, revenue: pgNumericToFloat(row.RevenueRub)}
		}
	}

	insights := make([]domain.OzonProductInsight, 0, len(productRows))
	for _, row := range productRows {
		insight := domain.OzonProductInsight{SKU: row.Sku}
		if agg, ok := campaignSales[row.Sku]; ok {
			u, r := agg.units, roundRub(agg.revenue)
			insight.CampaignOrders14d = &u
			insight.CampaignRevenue14dRub = &r
		}
		if price, ok := bridge.priceBySKU[row.Sku]; ok {
			priceRub := pgNumericToFloatPtr(price.PriceRub)
			cost := pgNumericToFloatPtr(price.NetPriceRub)
			if priceRub != nil && cost != nil {
				commission := pgNumericToFloat(price.CommissionFboPct)
				if fbs := pgNumericToFloat(price.CommissionFbsPct); fbs > commission {
					commission = fbs
				}
				insight.MarginPct = ozonSKUMarginPct(*priceRub, *cost, commission, pgNumericToFloat(price.AcquiringPct))
			}
		}
		if stock, ok := bridge.stockBySKU[row.Sku]; ok {
			v := stock
			insight.Stock = &v
			// Продажи идут под рекламным SKU — прямой ключ первым.
			perDay := unitsPerDay[row.Sku]
			if perDay == 0 {
				if salesSKU, has := bridge.salesSKUBySKU[row.Sku]; has {
					perDay = unitsPerDay[salesSKU]
				}
			}
			if perDay > 0 {
				cover := roundRub(float64(stock) / perDay)
				insight.DaysOfCover = &cover
			}
		}
		funnel, hasFunnel := funnelBySKU[strconv.FormatInt(row.Sku, 10)]
		if !hasFunnel {
			if offer := bridge.offerBySKU[row.Sku]; offer != "" {
				funnel, hasFunnel = funnelBySKU[offer]
			}
		}
		if !hasFunnel {
			if salesSKU, has := bridge.salesSKUBySKU[row.Sku]; has {
				funnel, hasFunnel = funnelBySKU[strconv.FormatInt(salesSKU, 10)]
			}
		}
		if hasFunnel {
			views := funnel.CardViews
			insight.CardViews14d = &views
			// Без Ozon Premium корзина/заказы приходят нулями при живых
			// просмотрах — это «не измерено», а не конверсия 0%.
			if views > 0 && (funnel.CartAdds > 0 || funnel.Orders > 0) {
				toCart := roundRub(float64(funnel.CartAdds) / float64(views) * 100)
				insight.ConvToCartPct = &toCart
				toOrder := roundRub(float64(funnel.Orders) / float64(views) * 100)
				insight.ConvToOrderPct = &toOrder
			}
		}
		// Продажи: прямой ключ, затем продажный SKU из моста.
		if agg, ok := sales14[row.Sku]; ok {
			u, r := agg.units, roundRub(agg.revenue)
			insight.Orders14d = &u
			insight.Revenue14dRub = &r
		} else if salesSKU, has := bridge.salesSKUBySKU[row.Sku]; has {
			if agg, ok := sales14[salesSKU]; ok {
				u, r := agg.units, roundRub(agg.revenue)
				insight.Orders14d = &u
				insight.Revenue14dRub = &r
			}
		}
		ratingName := bridge.nameBySKU[row.Sku]
		if ratingName == "" && hasFunnel {
			ratingName = funnel.Name
		}
		if ratingName != "" {
			if rating, ok := ratingByName[ratingName]; ok {
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

// ApproveDecision applies a copilot proposal using the current strategy scope
// and exactly the same fresh guardrail context as automatic execution.
func (s *OzonAIManagerService) ApproveDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error) {
	row, err := s.loadReviewDecision(ctx, workspaceID, decisionID)
	if err != nil {
		return nil, err
	}
	cabinetID := uuidFromPgtype(row.SellerCabinetID)
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}
	var result *domain.AIDecision
	err = s.queries.WithOzonAIExecutionLock(ctx, row.SellerCabinetID, func(q *sqlcgen.Queries) error {
		// A competing approve/reject may have finished while we waited.
		row, err := s.loadReviewDecision(ctx, workspaceID, decisionID)
		if err != nil {
			return err
		}
		if row.Status != domain.AIDecisionStatusProposed {
			return apperror.New(apperror.ErrConflict, fmt.Sprintf("decision is %s, only proposed decisions can be approved", row.Status))
		}
		proposal, err := proposalFromDecision(row)
		if err != nil {
			return apperror.New(apperror.ErrValidation, "decision payload is not parseable: "+err.Error())
		}
		proposal.ExcludeDecisionID = decisionID
		strategyRow, err := q.GetActiveOzonAIStrategyForCabinet(ctx, row.SellerCabinetID)
		if errors.Is(err, pgx.ErrNoRows) {
			return apperror.New(apperror.ErrValidation, "cabinet has no active ozon_ai_autopilot strategy anymore")
		}
		if err != nil {
			return fmt.Errorf("load ai strategy: %w", err)
		}
		originStrategyID, err := q.GetAIDecisionOriginStrategy(ctx, row.ID, row.WorkspaceID)
		if err != nil {
			return fmt.Errorf("load decision strategy: %w", err)
		}
		if !originStrategyID.Valid || originStrategyID != strategyRow.ID {
			return apperror.New(apperror.ErrConflict, "decision belongs to a previous strategy; request a new analysis")
		}
		strategy := strategyFromSqlc(strategyRow)
		params := strategy.Params.Merged()
		if params.AutomationLevel < 2 {
			return apperror.New(apperror.ErrConflict, "strategy is in observation mode; decision was not applied")
		}
		_, data, err := s.buildAIContext(ctx, workspaceID, cabinetID, strategy, params)
		if err != nil {
			return fmt.Errorf("load fresh cabinet data: %w", err)
		}
		if verdict := s.evaluateProposal(ctx, cabinetID, params, proposal, data); verdict != "" {
			return s.rejectReviewedDecision(ctx, row, userID, domain.AIDecisionStatusProposed, verdict)
		}
		// Commit the claim BEFORE any external write. If the process dies or
		// an external result is uncertain, the row cannot be approved again.
		payload := map[string]json.RawMessage{}
		if err := json.Unmarshal(row.Proposal, &payload); err != nil {
			return fmt.Errorf("parse decision proposal: %w", err)
		}
		payload["new_value"], err = json.Marshal(proposal.NewValue)
		if err != nil {
			return fmt.Errorf("encode approved value: %w", err)
		}
		proposalJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode approved proposal: %w", err)
		}
		if err := s.transitionReviewedDecision(ctx, row, userID, domain.AIDecisionStatusProposed, domain.AIDecisionStatusApproved, "", proposalJSON); err != nil {
			return err
		}
		applyVerdict, applyErr := s.applyProposal(ctx, workspaceID, cabinetID, params, proposal, data)
		if applyVerdict != "" {
			return s.rejectReviewedDecision(ctx, row, userID, domain.AIDecisionStatusApproved, applyVerdict)
		}
		if applyErr != nil {
			if err := s.transitionReviewedDecision(ctx, row, userID, domain.AIDecisionStatusApproved, domain.AIDecisionStatusFailed, truncateError(applyErr.Error()), nil); err != nil {
				return fmt.Errorf("apply ai decision: %w; record failure: %v", applyErr, err)
			}
			return fmt.Errorf("apply ai decision: %w", applyErr)
		}
		if err := s.transitionReviewedDecision(ctx, row, userID, domain.AIDecisionStatusApproved, domain.AIDecisionStatusApplied, "", nil); err != nil {
			return fmt.Errorf("finalize ai decision after external application: %w", err)
		}
		updated, err := s.loadReviewDecision(ctx, workspaceID, decisionID)
		if err != nil {
			return err
		}
		decision := aiDecisionFromSqlc(updated)
		result = &decision
		return nil
	})
	return result, err
}

func (s *OzonAIManagerService) loadReviewDecision(ctx context.Context, workspaceID, decisionID uuid.UUID) (sqlcgen.AiDecision, error) {
	row, err := s.queries.GetAIDecisionByID(ctx, sqlcgen.GetAIDecisionByIDParams{
		ID: uuidToPgtype(decisionID), WorkspaceID: uuidToPgtype(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, apperror.New(apperror.ErrNotFound, "ai decision not found")
	}
	if err != nil {
		return row, fmt.Errorf("load ai decision: %w", err)
	}
	return row, nil
}

func (s *OzonAIManagerService) transitionReviewedDecision(ctx context.Context, row sqlcgen.AiDecision, userID uuid.UUID, from, to, reason string, proposal []byte) error {
	changed, err := s.queries.TransitionAIDecision(ctx, sqlcgen.TransitionAIDecisionParams{
		ID: row.ID, WorkspaceID: row.WorkspaceID, ExpectedStatus: from, Status: to,
		Error: textToPgtype(reason), AppliedBy: uuidToPgtype(userID), Proposal: proposal,
	})
	if err != nil {
		return fmt.Errorf("transition ai decision: %w", err)
	}
	if !changed {
		return apperror.New(apperror.ErrConflict, "decision changed while being reviewed; no further action was taken")
	}
	return nil
}

func (s *OzonAIManagerService) rejectReviewedDecision(ctx context.Context, row sqlcgen.AiDecision, userID uuid.UUID, from, verdict string) error {
	// Cooldowns and quotas are temporary: keep the human proposal available
	// instead of permanently rejecting it for trying before the next window.
	transient := strings.HasPrefix(verdict, "cooldown active:") || strings.HasPrefix(verdict, "daily change limit reached") || strings.Contains(verdict, "дневной лимит")
	status := domain.AIDecisionStatusRejectedByGuardrail
	if transient {
		status = domain.AIDecisionStatusProposed
	}
	if err := s.transitionReviewedDecision(ctx, row, userID, from, status, verdict, nil); err != nil {
		return err
	}
	return apperror.New(apperror.ErrValidation, "guardrail rejected the decision: "+verdict)
}

// RejectDecision can only transition a still-proposed decision. It shares the
// cabinet lock with approval so it cannot overwrite an in-flight/applied action.
func (s *OzonAIManagerService) RejectDecision(ctx context.Context, workspaceID, decisionID, userID uuid.UUID) (*domain.AIDecision, error) {
	row, err := s.loadReviewDecision(ctx, workspaceID, decisionID)
	if err != nil {
		return nil, err
	}
	var result *domain.AIDecision
	err = s.queries.WithOzonAIExecutionLock(ctx, row.SellerCabinetID, func(q *sqlcgen.Queries) error {
		if err := s.transitionReviewedDecision(ctx, row, userID, domain.AIDecisionStatusProposed, domain.AIDecisionStatusRejectedByUser, "", nil); err != nil {
			return err
		}
		updated, err := s.loadReviewDecision(ctx, workspaceID, decisionID)
		if err != nil {
			return err
		}
		decision := aiDecisionFromSqlc(updated)
		result = &decision
		return nil
	})
	return result, err
}

// ApproveDecisionsBatch applies prerequisites before lifecycle actions using
// the same fresh approval path. A failed prerequisite stops the remainder of
// the selected batch; results retain the input order for the review UI.
func (s *OzonAIManagerService) ApproveDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult {
	type item struct {
		index int
		stage int
		err   error
	}
	items := make([]item, 0, len(ids))
	results := make([]domain.AIDecisionBatchResult, len(ids))
	for i, id := range ids {
		row, err := s.loadReviewDecision(ctx, workspaceID, id)
		items = append(items, item{index: i, stage: aiExecutionStage(aiProposal{ActionType: row.ActionType}), err: err})
		results[i].ID = id
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].stage < items[j].stage })
	var failedID uuid.UUID
	failed := false
	for _, item := range items {
		res := &results[item.index]
		if failed {
			res.Error = fmt.Sprintf("not applied because earlier batch decision %s failed; review the remaining proposals", failedID)
			continue
		}
		err := item.err
		if err == nil {
			_, err = s.ApproveDecision(ctx, workspaceID, res.ID, userID)
		}
		res.OK = err == nil
		if err != nil {
			res.Error = err.Error()
			failedID = res.ID
			failed = true
		}
	}
	return results
}

// RejectDecisionsBatch is the reject flavor of ApproveDecisionsBatch.
func (s *OzonAIManagerService) RejectDecisionsBatch(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) []domain.AIDecisionBatchResult {
	results := make([]domain.AIDecisionBatchResult, 0, len(ids))
	for _, id := range ids {
		_, err := s.RejectDecision(ctx, workspaceID, id, userID)
		res := domain.AIDecisionBatchResult{ID: id, OK: err == nil}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
	}
	return results
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
