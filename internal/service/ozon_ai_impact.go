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
//   - cpo_* actions (no campaign-level stats)     → 'not_evaluable'
//     TODO: evaluate cpo_* decisions from per-SKU sales (ozon_sales_daily)
//     once the attribution question is settled.
const (
	// aiImpactWindowDays is the length of each comparison window.
	aiImpactWindowDays = 7
	// aiImpactMinAfterDays is the minimum after-window days required to judge.
	aiImpactMinAfterDays = 3
	// aiImpactMaxAgeDays is when an unevaluated decision stops waiting for data.
	aiImpactMaxAgeDays = 14
	// aiImpactSummaryDays is the GET /ozon/ai/impact aggregation window.
	aiImpactSummaryDays = 30
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
		case domain.AIOutcomePendingEval:
			pending++
		default:
			skipped++
		}
	}
	s.logger.Info().
		Int("decisions", len(rows)).
		Int("evaluated", evaluated).
		Int("pending", pending).
		Int("not_evaluable", skipped).
		Msg("ozon ai impact sweep completed")
	return errors.Join(errs...)
}

// evaluateDecisionImpact measures one decision and persists the outcome.
func (s *OzonAIManagerService) evaluateDecisionImpact(ctx context.Context, row sqlcgen.AiDecision) (string, error) {
	if !row.AppliedAt.Valid {
		return "", fmt.Errorf("decision %s has no applied_at", uuidFromPgtype(row.ID))
	}
	now := time.Now().UTC()

	// cpo_* targets a SKU, not a campaign — ozon_campaign_stats has nothing
	// to attribute. TODO: per-SKU evaluation via ozon_sales_daily.
	switch row.ActionType {
	case domain.AIActionCPOBid, domain.AIActionCPOEnable, domain.AIActionCPODisable:
		return domain.AIOutcomeNotEvaluable, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomeNotEvaluable, nil, nil)
	}

	var target domain.AIDecisionTarget
	if len(row.Target) > 0 {
		if err := json.Unmarshal(row.Target, &target); err != nil {
			return "", fmt.Errorf("parse decision target: %w", err)
		}
	}
	if target.OzonCampaignID == 0 {
		// Campaign-typed action without a campaign target — nothing to measure.
		return domain.AIOutcomeNotEvaluable, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomeNotEvaluable, nil, nil)
	}
	campaign, err := s.queries.GetOzonCampaignByCabinetAndOzonID(ctx, sqlcgen.GetOzonCampaignByCabinetAndOzonIDParams{
		SellerCabinetID: row.SellerCabinetID,
		OzonCampaignID:  target.OzonCampaignID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Campaign disappeared from the mirror (deleted upstream).
		return domain.AIOutcomeNotEvaluable, s.writeDecisionOutcome(ctx, row.ID, domain.AIOutcomeNotEvaluable, nil, nil)
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
		return outcome.Status, s.writeDecisionOutcome(ctx, row.ID, outcome.Status, nil, nil)
	}
	return outcome.Status, s.writeDecisionOutcome(ctx, row.ID, outcome.Status, &before, &after)
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

func (s *OzonAIManagerService) writeDecisionOutcome(ctx context.Context, id pgtype.UUID, status string, before, after *aiImpactWindowTotals) error {
	params := sqlcgen.SetAIDecisionOutcomeParams{ID: id, OutcomeStatus: textToPgtype(status)}
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
