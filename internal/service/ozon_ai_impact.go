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

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// «Эффект ИИ» — the impact sweep measures every applied AI decision by
// comparing its target campaign's stats over two windows around the apply
// moment:
//
//	before: [applied_date − 7d, applied_date)   (7 calendar days)
//	after:  (applied_date, applied_date + 7d]   (7 calendar days)
//
// The attribution is deliberately rough: no holdout group, no isolation from
// other changes on the campaign, day-level granularity. The numbers answer
// «стало лучше или хуже вокруг этого решения», not «что именно сделал ИИ».
//
// Evaluation states:
//   - after-window has ≥ 3 days of data           → 'evaluated'
//   - fewer days but decision younger than 14d    → 'pending_eval' (retry later)
//   - fewer days and decision older than 14 days  → 'not_evaluable'
//   - cpo_* actions: per-SKU turnover from ozon_sales_daily, revenue-only
//     (CPO spend is not tracked per SKU, so ДРР stays NULL and the decision
//     never enters the money aggregate)
const (
	// aiImpactWindowDays is the length of each comparison window.
	aiImpactWindowDays = 7
	// aiImpactMinAfterDays is the minimum after-window days required to judge.
	aiImpactMinAfterDays = 3
	// aiImpactMaxAgeDays is when an unevaluated decision stops waiting for data.
	aiImpactMaxAgeDays = 14
	// aiImpactSummaryDays is the GET /ozon/ai/impact aggregation window.
	aiImpactSummaryDays = 30
	// aiImpactMinEvaluatedForDisplay: below this many evaluated decisions the
	// aggregate is noise, and the summary is flagged low_data so the UI shows
	// «данных пока мало» instead of a verdict.
	aiImpactMinEvaluatedForDisplay = 5
	// aiImpactDowngradeStreak: this many consecutive evaluated decisions that
	// worsened their campaign's ДРР switch a level-3 cabinet back to copilot.
	aiImpactDowngradeStreak = 3
)

// EvaluateImpactSweep is the ozon:ai_impact_sweep entry point: it scans every
// applied decision without a final evaluation and writes before/after
// numbers. Best-effort per decision — one broken row never stops the sweep.
func (s *OzonAIManagerService) EvaluateImpactSweep(ctx context.Context) error {
	rows, err := s.queries.ListAIDecisionsForImpactEval(ctx)
	if err != nil {
		return fmt.Errorf("list decisions for impact eval: %w", err)
	}
	evaluated, pending, skipped := 0, 0, 0
	touchedCabinets := map[uuid.UUID]uuid.UUID{} // cabinet → workspace
	var errs []error
	for _, row := range rows {
		status, evalErr := s.evaluateDecisionImpact(ctx, row)
		if evalErr != nil {
			s.logger.Warn().Err(evalErr).
				Str("decision_id", uuidFromPgtype(row.ID).String()).
				Msg("ai impact evaluation failed for decision")
			errs = append(errs, evalErr)
			continue
		}
		switch status {
		case domain.AIOutcomeEvaluated:
			evaluated++
			touchedCabinets[uuidFromPgtype(row.SellerCabinetID)] = uuidFromPgtype(row.WorkspaceID)
		case domain.AIOutcomePendingEval:
			pending++
		default:
			skipped++
		}
	}
	// Safety brake: a level-3 cabinet whose last decisions keep making the ДРР
	// worse loses the right to act on its own until a human looks at it.
	for cabinetID, workspaceID := range touchedCabinets {
		s.maybeDowngradeAutopilot(ctx, workspaceID, cabinetID)
	}
	s.logger.Info().
		Int("decisions", len(rows)).
		Int("evaluated", evaluated).
		Int("pending", pending).
		Int("not_evaluable", skipped).
		Msg("ozon ai impact sweep completed")
	return errors.Join(errs...)
}

// maybeDowngradeAutopilot switches a cabinet's strategy from level 3 back to
// level 2 (copilot) when the last aiImpactDowngradeStreak evaluated decisions
// all worsened their campaign's ДРР. Best-effort: any read/write failure is
// logged and skipped — the brake must never break the sweep.
func (s *OzonAIManagerService) maybeDowngradeAutopilot(ctx context.Context, workspaceID, cabinetID uuid.UUID) {
	strategyRow, err := s.queries.GetActiveOzonAIStrategyForCabinet(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return
	}
	strategy := strategyFromSqlc(strategyRow)
	if strategy.Params.Merged().AutomationLevel < 3 {
		return
	}
	recent, err := s.queries.ListRecentAppliedAIDecisions(ctx, sqlcgen.ListRecentAppliedAIDecisionsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Lim:             int32(aiImpactDowngradeStreak * 3),
	})
	if err != nil {
		s.logger.Warn().Err(err).Msg("autopilot downgrade check: history read failed")
		return
	}
	streak := 0
	for _, row := range recent { // newest first
		if pgTextValue(row.OutcomeStatus) != domain.AIOutcomeEvaluated {
			continue
		}
		before := pgNumericToFloatPtr(row.DrrBefore)
		after := pgNumericToFloatPtr(row.DrrAfter)
		if before == nil || after == nil {
			continue
		}
		if *after <= *before {
			return // the newest evaluated decisions include a non-worsening one
		}
		streak++
		if streak >= aiImpactDowngradeStreak {
			break
		}
	}
	if streak < aiImpactDowngradeStreak {
		return
	}
	// Patch only automation_level in the stored params — a full re-marshal of
	// the merged struct would bake defaults into the row.
	var raw map[string]any
	if len(strategyRow.Params) > 0 {
		if err := json.Unmarshal(strategyRow.Params, &raw); err != nil {
			s.logger.Warn().Err(err).Msg("autopilot downgrade: params unmarshal failed")
			return
		}
	}
	if raw == nil {
		raw = map[string]any{}
	}
	raw["automation_level"] = 2
	patched, err := json.Marshal(raw)
	if err != nil {
		return
	}
	if _, err := s.queries.UpdateStrategy(ctx, sqlcgen.UpdateStrategyParams{
		ID: strategyRow.ID, Name: strategyRow.Name, Type: strategyRow.Type,
		Params: patched, IsActive: strategyRow.IsActive,
	}); err != nil {
		s.logger.Warn().Err(err).Msg("autopilot downgrade: strategy update failed")
		return
	}
	s.logger.Warn().
		Str("cabinet_id", cabinetID.String()).
		Int("streak", streak).
		Msg("autopilot downgraded to copilot after consecutive worsened decisions")
	s.notifier.NotifyWorkspaceOwners(ctx, workspaceID, "ads-ai-downgraded",
		"ИИ-автопилот переведён в режим Копилот",
		fmt.Sprintf("Последние %d оценённых решений ИИ ухудшили ДРР кампаний. Автопилот остановлен: новые предложения будут ждать вашего подтверждения.", aiImpactDowngradeStreak))
}

// evaluateDecisionImpact measures one decision and persists the outcome.
func (s *OzonAIManagerService) evaluateDecisionImpact(ctx context.Context, row sqlcgen.AiDecision) (string, error) {
	if !row.AppliedAt.Valid {
		return "", fmt.Errorf("decision %s has no applied_at", uuidFromPgtype(row.ID))
	}
	now := time.Now().UTC()

	// cpo_* targets a SKU, not a campaign — ozon_campaign_stats has nothing to
	// attribute. Its measurable surface is the SKU's own turnover
	// (ozon_sales_daily): revenue before/after only. CPO spend is not tracked
	// per SKU, so spend/ДРР stay NULL and the decision never enters the
	// «Эффект ИИ» aggregate — visible per decision, not counted as money.
	switch row.ActionType {
	case domain.AIActionCPOBid, domain.AIActionCPOEnable, domain.AIActionCPODisable:
		return s.evaluateCPODecisionImpact(ctx, row, now)
	}

	var target domain.AIDecisionTarget
	if len(row.Target) > 0 {
		if err := json.Unmarshal(row.Target, &target); err != nil {
			return "", fmt.Errorf("parse decision target: %w", err)
		}
	}
	if target.OzonCampaignID == 0 {
		// Campaign-typed action without a campaign target — nothing to measure.
		return domain.AIOutcomeNotEvaluable, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomeNotEvaluable, nil, nil, aiImpactTotalDRR{})
	}
	campaign, err := s.queries.GetOzonCampaignByCabinetAndOzonID(ctx, sqlcgen.GetOzonCampaignByCabinetAndOzonIDParams{
		SellerCabinetID: row.SellerCabinetID,
		OzonCampaignID:  target.OzonCampaignID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Campaign disappeared from the mirror (deleted upstream).
		return domain.AIOutcomeNotEvaluable, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomeNotEvaluable, nil, nil, aiImpactTotalDRR{})
	}
	if err != nil {
		return "", fmt.Errorf("load campaign %d: %w", target.OzonCampaignID, err)
	}

	windows := aiImpactWindows(row.AppliedAt.Time)
	before, err := s.campaignWindowTotals(ctx, campaign.ID, windows.BeforeFrom, windows.BeforeTo)
	if err != nil {
		return "", err
	}
	after, err := s.campaignWindowTotals(ctx, campaign.ID, windows.AfterFrom, windows.AfterTo)
	if err != nil {
		return "", err
	}

	outcome := aiImpactOutcome(before, after, row.AppliedAt.Time, now)
	if outcome.Status != domain.AIOutcomeEvaluated {
		return outcome.Status, s.writeDecisionOutcome(ctx, row.ID, outcome.Status, nil, nil, aiImpactTotalDRR{})
	}

	// The cabinet-wide ДРР over the same two windows. Without it the feedback
	// loop only ever sees the campaign ДРР, so a decision that improved the
	// campaign while pushing the cabinet's overall ДРР up is remembered as a
	// success — and the model repeats it. Best-effort: a failed read leaves
	// the columns NULL rather than blocking the evaluation.
	totals := s.cabinetTotalDRRWindows(ctx, row.SellerCabinetID, windows)

	return outcome.Status, s.writeDecisionOutcome(ctx, row.ID, outcome.Status, &before, &after, totals)
}

// evaluateCPODecisionImpact measures a cpo_* decision by the target SKU's own
// turnover over the same 7d/7d windows. Unlike campaign decisions, «enough
// data» is calendar time here: a SKU that sold nothing after the change has no
// sales rows at all, and that zero IS the result — so we evaluate as soon as
// the after-window has fully elapsed.
func (s *OzonAIManagerService) evaluateCPODecisionImpact(ctx context.Context, row sqlcgen.AiDecision, now time.Time) (string, error) {
	var target domain.AIDecisionTarget
	if len(row.Target) > 0 {
		if err := json.Unmarshal(row.Target, &target); err != nil {
			return "", fmt.Errorf("parse decision target: %w", err)
		}
	}
	if target.SKU == 0 {
		return domain.AIOutcomeNotEvaluable, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomeNotEvaluable, nil, nil, aiImpactTotalDRR{})
	}
	windows := aiImpactWindows(row.AppliedAt.Time)
	if now.Before(windows.AfterTo.Add(24 * time.Hour)) {
		if now.Sub(row.AppliedAt.Time) > time.Duration(aiImpactMaxAgeDays)*24*time.Hour {
			return domain.AIOutcomeNotEvaluable, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomeNotEvaluable, nil, nil, aiImpactTotalDRR{})
		}
		return domain.AIOutcomePendingEval, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomePendingEval, nil, nil, aiImpactTotalDRR{})
	}

	skuWindow := func(from, to time.Time) (float64, error) {
		totals, err := s.queries.GetOzonSKUSalesWindowTotals(ctx, sqlcgen.GetOzonSKUSalesWindowTotalsParams{
			SellerCabinetID: row.SellerCabinetID,
			Sku:             target.SKU,
			DateFrom:        pgtype.Date{Time: from, Valid: true},
			DateTo:          pgtype.Date{Time: to, Valid: true},
		})
		if err != nil {
			return 0, fmt.Errorf("sku sales window totals: %w", err)
		}
		return pgNumericToFloat(totals.RevenueRub), nil
	}
	beforeRev, err := skuWindow(windows.BeforeFrom, windows.BeforeTo)
	if err != nil {
		return "", err
	}
	afterRev, err := skuWindow(windows.AfterFrom, windows.AfterTo)
	if err != nil {
		return "", err
	}
	totals := s.cabinetTotalDRRWindows(ctx, row.SellerCabinetID, windows)

	params := sqlcgen.SetAIDecisionOutcomeParams{
		ID:               row.ID,
		OutcomeStatus:    textToPgtype(domain.AIOutcomeEvaluated),
		RevenueBeforeRub: floatToPgNumeric(roundRub(beforeRev)),
		RevenueAfterRub:  floatToPgNumeric(roundRub(afterRev)),
	}
	if totals.Before != nil {
		params.TotalDrrBefore = floatToPgNumeric(*totals.Before)
	}
	if totals.After != nil {
		params.TotalDrrAfter = floatToPgNumeric(*totals.After)
	}
	if err := s.queries.SetAIDecisionOutcome(ctx, params); err != nil {
		return "", fmt.Errorf("set cpo decision outcome: %w", err)
	}
	return domain.AIOutcomeEvaluated, nil
}

// aiImpactTotalDRR carries the cabinet-wide ДРР of both comparison windows.
// Nil members mean "not measurable" (no turnover, or the read failed).
type aiImpactTotalDRR struct {
	Before *float64
	After  *float64
}

// cabinetTotalDRRWindows measures the cabinet's total-turnover ДРР over the
// before/after windows of one decision. Errors degrade to nil, never fail the
// sweep — this number is observational (stage 0).
func (s *OzonAIManagerService) cabinetTotalDRRWindows(ctx context.Context, cabinetID pgtype.UUID, w aiImpactWindowBounds) aiImpactTotalDRR {
	return aiImpactTotalDRR{
		Before: s.cabinetTotalDRRWindow(ctx, cabinetID, w.BeforeFrom, w.BeforeTo),
		After:  s.cabinetTotalDRRWindow(ctx, cabinetID, w.AfterFrom, w.AfterTo),
	}
}

func (s *OzonAIManagerService) cabinetTotalDRRWindow(ctx context.Context, cabinetID pgtype.UUID, from, to time.Time) *float64 {
	dateFrom := pgtype.Date{Time: from, Valid: true}
	dateTo := pgtype.Date{Time: to, Valid: true}

	sales, err := s.queries.GetOzonCabinetSalesWindowTotals(ctx, sqlcgen.GetOzonCabinetSalesWindowTotalsParams{
		SellerCabinetID: cabinetID, DateFrom: dateFrom, DateTo: dateTo,
	})
	if err != nil {
		s.logger.Warn().Err(err).Msg("ai impact: cabinet turnover window read failed")
		return nil
	}
	spend, err := s.queries.GetOzonCabinetAdSpendWindowTotals(ctx, sqlcgen.GetOzonCabinetAdSpendWindowTotalsParams{
		SellerCabinetID: cabinetID, DateFrom: dateFrom, DateTo: dateTo,
	})
	if err != nil {
		s.logger.Warn().Err(err).Msg("ai impact: cabinet ad spend window read failed")
		return nil
	}
	// drrPct already returns nil on zero turnover — undefined, not zero.
	return drrPct(pgNumericToFloat(spend.SpendRub), pgNumericToFloat(sales.RevenueRub))
}

func (s *OzonAIManagerService) campaignWindowTotals(ctx context.Context, campaignID pgtype.UUID, from, to time.Time) (aiImpactWindowTotals, error) {
	row, err := s.queries.GetOzonCampaignStatsWindowTotals(ctx, sqlcgen.GetOzonCampaignStatsWindowTotalsParams{
		CampaignID: campaignID,
		DateFrom:   pgtype.Date{Time: from, Valid: true},
		DateTo:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return aiImpactWindowTotals{}, fmt.Errorf("campaign window totals: %w", err)
	}
	return aiImpactWindowTotals{
		SpendRub:   pgNumericToFloat(row.SpendRub),
		RevenueRub: pgNumericToFloat(row.RevenueRub),
		Days:       row.Days,
	}, nil
}

func (s *OzonAIManagerService) writeDecisionOutcome(ctx context.Context, id pgtype.UUID, status string, before, after *aiImpactWindowTotals, total aiImpactTotalDRR) error {
	params := sqlcgen.SetAIDecisionOutcomeParams{ID: id, OutcomeStatus: textToPgtype(status)}
	if total.Before != nil {
		params.TotalDrrBefore = floatToPgNumeric(*total.Before)
	}
	if total.After != nil {
		params.TotalDrrAfter = floatToPgNumeric(*total.After)
	}
	if status == domain.AIOutcomeEvaluated && before != nil && after != nil {
		params.SpendBeforeRub = floatToPgNumeric(roundRub(before.SpendRub))
		params.SpendAfterRub = floatToPgNumeric(roundRub(after.SpendRub))
		params.RevenueBeforeRub = floatToPgNumeric(roundRub(before.RevenueRub))
		params.RevenueAfterRub = floatToPgNumeric(roundRub(after.RevenueRub))
		if drr := drrPct(before.SpendRub, before.RevenueRub); drr != nil {
			params.DrrBefore = floatToPgNumeric(*drr)
		}
		if drr := drrPct(after.SpendRub, after.RevenueRub); drr != nil {
			params.DrrAfter = floatToPgNumeric(*drr)
		}
	}
	if err := s.queries.SetAIDecisionOutcome(ctx, params); err != nil {
		return fmt.Errorf("set decision outcome: %w", err)
	}
	return nil
}

// --- pure impact math (unit-tested) ---

// aiImpactWindowTotals is one comparison window's aggregate.
type aiImpactWindowTotals struct {
	SpendRub   float64
	RevenueRub float64
	Days       int64 // days that actually have stat rows
}

// aiImpactWindowBounds are the inclusive date bounds of both windows.
type aiImpactWindowBounds struct {
	BeforeFrom, BeforeTo time.Time
	AfterFrom, AfterTo   time.Time
}

// aiImpactWindows derives the calendar-day windows around the apply moment.
// The apply day itself is excluded from both windows: it is a mixed day.
func aiImpactWindows(appliedAt time.Time) aiImpactWindowBounds {
	day := appliedAt.UTC().Truncate(24 * time.Hour)
	return aiImpactWindowBounds{
		BeforeFrom: day.AddDate(0, 0, -aiImpactWindowDays),
		BeforeTo:   day.AddDate(0, 0, -1),
		AfterFrom:  day.AddDate(0, 0, 1),
		AfterTo:    day.AddDate(0, 0, aiImpactWindowDays),
	}
}

// aiImpactDecisionOutcome is what one decision's evaluation produced.
type aiImpactDecisionOutcome struct {
	Status string
}

// aiImpactOutcome decides the outcome status from the window data: enough
// after-days → evaluated; not enough but still young → pending; аged out →
// not_evaluable.
func aiImpactOutcome(before, after aiImpactWindowTotals, appliedAt, now time.Time) aiImpactDecisionOutcome {
	_ = before // the before-window may legitimately be empty (fresh campaign)
	if after.Days >= aiImpactMinAfterDays {
		return aiImpactDecisionOutcome{Status: domain.AIOutcomeEvaluated}
	}
	if now.Sub(appliedAt) > time.Duration(aiImpactMaxAgeDays)*24*time.Hour {
		return aiImpactDecisionOutcome{Status: domain.AIOutcomeNotEvaluable}
	}
	return aiImpactDecisionOutcome{Status: domain.AIOutcomePendingEval}
}

// drrPct computes ДРР = spend / revenue × 100. Zero revenue → nil (undefined,
// not 0: an ad that spends with no revenue has infinite ДРР, not a perfect one).
func drrPct(spend, revenue float64) *float64 {
	if revenue <= 0 {
		return nil
	}
	v := roundRub(spend / revenue * 100)
	return &v
}

// aiImpactRow is the aggregation input for one applied decision.
type aiImpactRow struct {
	Evaluated  bool
	DRRBefore  *float64
	DRRAfter   *float64
	SpendB     float64
	SpendA     float64
	RevenueB   float64
	RevenueA   float64
	HasNumbers bool // spend/revenue columns are populated
}

// aiImpactAggregate implements the GET /ozon/ai/impact formulas. They are
// simple and honest (документированная грубая атрибуция, окно 7д/7д):
//
//	spend_delta_rub   = Σ (spend_after − spend_before)                 по evaluated
//	revenue_delta_rub = Σ (revenue_after − revenue_before)             по evaluated
//	saved_rub         = Σ max(0, spend_before − spend_after)           по evaluated,
//	                    где revenue_after ≥ revenue_before × 0.9
//	                    (экономия расхода считается только если выручка
//	                     не просела больше чем на 10%)
//	extra_revenue_rub = Σ max(0, revenue_after − revenue_before)       по evaluated
//	avg_drr_before/after — среднее по evaluated-решениям, у которых ДРР
//	                    определён (выручка окна > 0)
func aiImpactAggregate(rows []aiImpactRow) domain.AIImpactSummary {
	summary := domain.AIImpactSummary{WindowDays: aiImpactSummaryDays}
	var drrBeforeSum, drrAfterSum float64
	var drrBeforeN, drrAfterN int
	for _, row := range rows {
		summary.DecisionsApplied++
		if !row.Evaluated || !row.HasNumbers {
			continue
		}
		summary.DecisionsEvaluated++
		summary.SpendDeltaRub += row.SpendA - row.SpendB
		summary.RevenueDeltaRub += row.RevenueA - row.RevenueB
		if row.RevenueA >= row.RevenueB*0.9 && row.SpendB > row.SpendA {
			summary.SavedRub += row.SpendB - row.SpendA
		}
		if row.RevenueA > row.RevenueB {
			summary.ExtraRevenueRub += row.RevenueA - row.RevenueB
		}
		if row.DRRBefore != nil {
			drrBeforeSum += *row.DRRBefore
			drrBeforeN++
		}
		if row.DRRAfter != nil {
			drrAfterSum += *row.DRRAfter
			drrAfterN++
		}
	}
	if drrBeforeN > 0 {
		avg := roundRub(drrBeforeSum / float64(drrBeforeN))
		summary.AvgDRRBefore = &avg
	}
	if drrAfterN > 0 {
		avg := roundRub(drrAfterSum / float64(drrAfterN))
		summary.AvgDRRAfter = &avg
	}
	summary.SpendDeltaRub = roundRub(summary.SpendDeltaRub)
	summary.RevenueDeltaRub = roundRub(summary.RevenueDeltaRub)
	summary.SavedRub = roundRub(summary.SavedRub)
	summary.ExtraRevenueRub = roundRub(summary.ExtraRevenueRub)
	summary.LowData = summary.DecisionsEvaluated < aiImpactMinEvaluatedForDisplay
	return summary
}

// GetImpact serves GET /ozon/ai/impact: the 30-day aggregate of applied
// decisions and their measured effect.
func (s *OzonAIManagerService) GetImpact(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIImpactSummary, error) {
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}
	since := time.Now().UTC().AddDate(0, 0, -aiImpactSummaryDays)
	rows, err := s.queries.ListAIDecisionImpactRows(ctx, sqlcgen.ListAIDecisionImpactRowsParams{
		WorkspaceID:     uuidToPgtype(workspaceID),
		SellerCabinetID: uuidToPgtype(cabinetID),
		Since:           pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list impact rows: %w", err)
	}
	impact := make([]aiImpactRow, 0, len(rows))
	for _, row := range rows {
		impact = append(impact, aiImpactRow{
			Evaluated:  pgTextValue(row.OutcomeStatus) == domain.AIOutcomeEvaluated,
			DRRBefore:  pgNumericToFloatPtr(row.DrrBefore),
			DRRAfter:   pgNumericToFloatPtr(row.DrrAfter),
			SpendB:     pgNumericToFloat(row.SpendBeforeRub),
			SpendA:     pgNumericToFloat(row.SpendAfterRub),
			RevenueB:   pgNumericToFloat(row.RevenueBeforeRub),
			RevenueA:   pgNumericToFloat(row.RevenueAfterRub),
			HasNumbers: row.SpendBeforeRub.Valid && row.SpendAfterRub.Valid,
		})
	}
	summary := aiImpactAggregate(impact)
	return &summary, nil
}
