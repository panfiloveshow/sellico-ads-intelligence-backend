package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/llm"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// aiWeeklyWindowDays is the recap lookback (one calendar week of stats).
const aiWeeklyWindowDays = 7

// --- weekly natural-language report (ozon:ai_weekly_report) ---

// GenerateWeeklyReports is the ozon:ai_weekly_report cron entry point: it writes
// one plain-Russian recap per cabinet with an active AI strategy, guarded to one
// per cabinet per ISO week. Best-effort per cabinet — one failure never stops
// the rest. A disabled LLM makes the whole sweep a graceful no-op.
func (s *OzonAIManagerService) GenerateWeeklyReports(ctx context.Context) error {
	if !s.Enabled() {
		s.logger.Debug().Msg("llm disabled; skipping weekly report generation")
		return nil
	}
	ids, err := s.queries.ListActiveOzonAICabinets(ctx)
	if err != nil {
		return fmt.Errorf("list ai cabinets: %w", err)
	}
	written := 0
	var errs []error
	for _, id := range ids {
		generated, genErr := s.GenerateWeeklyReportForCabinetID(ctx, uuidFromPgtype(id))
		if genErr != nil {
			s.logger.Warn().Err(genErr).Str("cabinet_id", uuidFromPgtype(id).String()).
				Msg("weekly report generation failed for cabinet")
			errs = append(errs, genErr)
			continue
		}
		if generated {
			written++
		}
	}
	s.logger.Info().Int("cabinets", len(ids)).Int("reports_written", written).Msg("ozon ai weekly report sweep completed")
	return errors.Join(errs...)
}

// GenerateWeeklyReportForCabinetID resolves the cabinet and writes its weekly
// recap. Returns (true, nil) when a report was written, (false, nil) when it was
// skipped (already exists this ISO week, LLM disabled, or not an Ozon cabinet).
func (s *OzonAIManagerService) GenerateWeeklyReportForCabinetID(ctx context.Context, cabinetID uuid.UUID) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.Marketplace != domain.MarketplaceOzon {
		return false, nil
	}
	return s.generateWeeklyReport(ctx, cabinet.WorkspaceID, cabinetID, time.Now().UTC())
}

// isoWeekStart returns 00:00 UTC of the Monday of now's ISO week.
func isoWeekStart(now time.Time) time.Time {
	day := now.UTC().Truncate(24 * time.Hour)
	weekday := int(day.Weekday())
	if weekday == 0 {
		weekday = 7 // Sunday
	}
	return day.AddDate(0, 0, -(weekday - 1))
}

func (s *OzonAIManagerService) generateWeeklyReport(ctx context.Context, workspaceID, cabinetID uuid.UUID, now time.Time) (bool, error) {
	periodStart := isoWeekStart(now)
	periodEnd := periodStart.AddDate(0, 0, aiWeeklyWindowDays-1)

	// Guard: one report per cabinet per ISO week.
	if _, err := s.queries.GetOzonAIWeeklyReportForPeriod(ctx, sqlcgen.GetOzonAIWeeklyReportForPeriodParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		PeriodStart:     pgtype.Date{Time: periodStart, Valid: true},
	}); err == nil {
		return false, nil // already generated this week
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("check existing weekly report: %w", err)
	}

	trend := s.weeklyDRRTrend(ctx, cabinetID, now)
	decisions := s.weeklyDecisionSummary(ctx, cabinetID, now)
	// «ДРР от общего оборота» and the two-window comparison. There is no Ozon
	// advertising screen in the product, so this weekly recap is the only
	// channel that actually reaches a human — the second ДРР belongs here.
	total := loadCabinetTotalDRR(ctx, s.queries, s.logger, cabinetID,
		now.AddDate(0, 0, -aiWeeklyWindowDays), now, 0)
	incremental := loadIncrementalDRR(ctx, s.queries, s.logger, cabinetID, now, aiWeeklyWindowDays)

	text, err := s.weeklyReportText(ctx, trend, decisions, total, incremental)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(text) == "" {
		return false, fmt.Errorf("llm returned an empty weekly report")
	}

	_, err = s.queries.InsertOzonAIWeeklyReport(ctx, sqlcgen.InsertOzonAIWeeklyReportParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		PeriodStart:     pgtype.Date{Time: periodStart, Valid: true},
		PeriodEnd:       pgtype.Date{Time: periodEnd, Valid: true},
		DrrStart:        floatPtrToPgNumeric(trend.DRRStart),
		DrrEnd:          floatPtrToPgNumeric(trend.DRREnd),
		Text:            text,
	})
	if err != nil {
		return false, fmt.Errorf("insert weekly report: %w", err)
	}
	return true, nil
}

// aiWeeklyTrend holds the compact 7-day numbers the recap is built from.
type aiWeeklyTrend struct {
	SpendRub   float64
	RevenueRub float64
	DRR        *float64 // overall window ДРР
	DRRStart   *float64 // first day with data
	DRREnd     *float64 // last day with data
	DaysWith   int
}

// weeklyDRRTrend aggregates the cabinet's campaign stats over the trailing week.
// drr_start/drr_end are the first- and last-day ДРР (the trend indicator).
func (s *OzonAIManagerService) weeklyDRRTrend(ctx context.Context, cabinetID uuid.UUID, now time.Time) aiWeeklyTrend {
	since := now.AddDate(0, 0, -aiWeeklyWindowDays)
	rows, err := s.queries.ListOzonCampaignDailyStatsSince(ctx, sqlcgen.ListOzonCampaignDailyStatsSinceParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load weekly stats for report")
		return aiWeeklyTrend{}
	}
	type dayAgg struct{ spend, revenue float64 }
	byDate := map[string]*dayAgg{}
	var trend aiWeeklyTrend
	for _, row := range rows {
		key := row.Date.Time.Format("2006-01-02")
		d := byDate[key]
		if d == nil {
			d = &dayAgg{}
			byDate[key] = d
		}
		d.spend += pgNumericToFloat(row.SpendRub)
		d.revenue += pgNumericToFloat(row.RevenueRub)
		trend.SpendRub += pgNumericToFloat(row.SpendRub)
		trend.RevenueRub += pgNumericToFloat(row.RevenueRub)
	}
	trend.SpendRub = roundRub(trend.SpendRub)
	trend.RevenueRub = roundRub(trend.RevenueRub)
	trend.DRR = drrPct(trend.SpendRub, trend.RevenueRub)
	trend.DaysWith = len(byDate)
	if len(byDate) > 0 {
		dates := make([]string, 0, len(byDate))
		for k := range byDate {
			dates = append(dates, k)
		}
		sort.Strings(dates)
		first := byDate[dates[0]]
		last := byDate[dates[len(dates)-1]]
		trend.DRRStart = drrPct(first.spend, first.revenue)
		trend.DRREnd = drrPct(last.spend, last.revenue)
	}
	return trend
}

// aiWeeklyDecisions summarizes what the AI did during the week.
type aiWeeklyDecisions struct {
	Total    int
	Applied  int
	Proposed int
	Shadow   int
	ByAction map[string]int
}

func (s *OzonAIManagerService) weeklyDecisionSummary(ctx context.Context, cabinetID uuid.UUID, now time.Time) aiWeeklyDecisions {
	since := now.AddDate(0, 0, -aiWeeklyWindowDays)
	out := aiWeeklyDecisions{ByAction: map[string]int{}}
	rows, err := s.queries.ListAIDecisionsSince(ctx, sqlcgen.ListAIDecisionsSinceParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Since:           pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to load weekly decisions for report")
		return out
	}
	for _, row := range rows {
		out.Total++
		out.ByAction[row.ActionType]++
		switch row.Status {
		case domain.AIDecisionStatusApplied, domain.AIDecisionStatusAutoApplied:
			out.Applied++
		case domain.AIDecisionStatusProposed:
			out.Proposed++
		case domain.AIDecisionStatusShadow:
			out.Shadow++
		}
	}
	return out
}

// weeklyReportText asks the LLM for the manager-facing recap. The style matches
// the run summaries: plain Russian, no field names, 4–8 sentences, no actions.
func (s *OzonAIManagerService) weeklyReportText(
	ctx context.Context,
	trend aiWeeklyTrend,
	decisions aiWeeklyDecisions,
	total totalDRR,
	incremental incrementalDRR,
) (string, error) {
	facts := map[string]any{
		"расход_за_неделю_руб":        trend.SpendRub,
		"выручка_за_неделю_руб":       trend.RevenueRub,
		"дрр_за_неделю_пункты":        trend.DRR,
		"дрр_в_начале_недели":         trend.DRRStart,
		"дрр_в_конце_недели":          trend.DRREnd,
		"дней_со_статистикой":         trend.DaysWith,
		"всего_решений_ии":            decisions.Total,
		"применено":                   decisions.Applied,
		"предложено_на_подтверждение": decisions.Proposed,
		"в_режиме_наблюдения":         decisions.Shadow,
		"по_типам_действий":           decisions.ByAction,
	}
	// Only include the second ДРР when it was actually measured: a stale or
	// missing value must not reach the model as a number it can narrate.
	if total.Status == totalDRRStatusOK {
		facts["весь_оборот_магазина_руб"] = total.RevenueRub
		facts["дрр_от_общего_оборота_пункты"] = total.Value
	}
	if incremental.Verdict != incrementalDRRNotEnoughData {
		facts["изменение_расхода_руб"] = incremental.SpendDeltaRub
		facts["изменение_оборота_руб"] = incremental.TurnoverDeltaRub
		facts["вывод_по_доп_расходу"] = incremental.Verdict
	}
	payload, _ := json.Marshal(facts)

	messages := []llm.Message{
		{Role: "system", Content: aiWeeklyReportSystemPrompt()},
		{Role: "user", Content: "Данные за неделю (JSON):\n" + string(payload)},
	}
	resp, err := s.llm.ChatCompletion(ctx, llm.ChatRequest{Messages: messages})
	if err != nil {
		return "", fmt.Errorf("llm weekly report: %w", err)
	}
	return strings.TrimSpace(resp.Message.Content), nil
}

func aiWeeklyReportSystemPrompt() string {
	return `Ты — менеджер рекламы Ozon. Напиши краткий еженедельный отчёт для владельца магазина о том, что происходило с рекламой за прошедшую неделю.

Требования к тексту:
- 4–8 предложений простым деловым русским, без списков и заголовков.
- Это только резюме, БЕЗ предложений действий и без обещаний — просто расскажи, что было.
- Опиши динамику ДРР (доля рекламных расходов), расход и выручку человеческим языком, как изменилось за неделю.
- Если есть «дрр_от_общего_оборота_пункты» — объясни разницу простыми словами: обычная ДРР считается от выручки, которую Ozon приписал рекламе, а эта — от всего оборота магазина. Назови обе цифры.
- Если есть «вывод_по_доп_расходу»: "accretive" — рост рекламных затрат принёс дополнительный оборот; "cannibalizing" — затраты выросли, а оборот нет, то есть реклама выкупала заказы, которые магазин получил бы и так (это важно сказать прямо); "freed" — затраты снизили без потери оборота; "costly_cut" — сократили рекламу и вместе с ней потеряли оборот. Объясни своими словами, без этих английских слов.
- Упомяни, сколько решений принял ИИ и что было применено, если это уместно.
- НИКОГДА не используй имена полей и техножаргон: «расход», «выручка», «доля рекламных расходов (ДРР)», «заказы» — обычными словами. Числа пиши по-человечески.
- Если данных за неделю почти нет — честно скажи об этом одним-двумя предложениями.
- Пиши в прошедшем времени как обзор недели («за неделю расход составил…», «ДРР снизилась с … до …»), не давай указаний.`
}

// GetLatestWeeklyReport serves GET /ozon/ai/weekly-report: the newest recap for
// a cabinet, or nil when none exists yet.
func (s *OzonAIManagerService) GetLatestWeeklyReport(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonAIWeeklyReport, error) {
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}
	row, err := s.queries.GetLatestOzonAIWeeklyReport(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load weekly report: %w", err)
	}
	report := &domain.OzonAIWeeklyReport{
		PeriodStart: row.PeriodStart.Time,
		PeriodEnd:   row.PeriodEnd.Time,
		DRRStart:    pgNumericToFloatPtr(row.DrrStart),
		DRREnd:      pgNumericToFloatPtr(row.DrrEnd),
		Text:        row.Text,
		GeneratedAt: row.GeneratedAt.Time,
	}
	return report, nil
}

// --- shadow → next-level readiness (GET /ozon/ai/readiness) ---

// Readiness thresholds (documented): recommend promoting to the next automation
// level only when the shadow run has proven itself — it must have run in shadow
// for at least aiReadinessMinShadowDays, produced at least aiReadinessMinDecisions
// decisions, and kept at least aiReadinessMinGuardrailPct% of them within the
// guardrails. These are conservative gates, not a guarantee.
const (
	aiReadinessMinShadowDays   = 5
	aiReadinessMinDecisions    = 10
	aiReadinessMinGuardrailPct = 80.0
)

// GetReadiness serves GET /ozon/ai/readiness: a pure aggregate over the
// cabinet's ai_decisions for its active AI autopilot strategy (no writes).
func (s *OzonAIManagerService) GetReadiness(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.AIReadiness, error) {
	if err := s.resolveAICabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, err
	}
	strategyRow, err := s.queries.GetActiveOzonAIStrategyForCabinet(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // no active strategy → no readiness stat
	}
	if err != nil {
		return nil, fmt.Errorf("load ai strategy: %w", err)
	}
	strategy := strategyFromSqlc(strategyRow)
	params := strategy.Params.Merged()

	stats, err := s.queries.GetAIReadinessStats(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return nil, fmt.Errorf("load readiness stats: %w", err)
	}

	readiness := computeAIReadiness(params.AutomationLevel, strategy.CreatedAt, stats, time.Now().UTC())
	return &readiness, nil
}

// computeAIReadiness is the pure readiness math (unit-tested).
func computeAIReadiness(level int, strategyCreatedAt time.Time, stats sqlcgen.GetAIReadinessStatsRow, now time.Time) domain.AIReadiness {
	readiness := domain.AIReadiness{
		CurrentLevel:   level,
		DecisionsTotal: stats.DecisionsTotal,
	}

	// shadow_days: since the earlier of the strategy creation and the first
	// shadow decision (whichever marks the start of observation).
	start := strategyCreatedAt
	if stats.FirstShadowAt.Valid && stats.FirstShadowAt.Time.Before(start) {
		start = stats.FirstShadowAt.Time
	}
	if !start.IsZero() {
		days := int(now.Sub(start).Hours() / 24)
		if days < 0 {
			days = 0
		}
		readiness.ShadowDays = days
	}

	if stats.ShadowTotal > 0 {
		readiness.WithinGuardrailsPct = roundRub(float64(stats.ShadowPassed) / float64(stats.ShadowTotal) * 100)
	}

	if stats.EvaluatedPairs > 0 {
		delta := roundRub(pgNumericToFloat(stats.AvgDrrDelta))
		readiness.ProjectedDRRDelta = &delta
	}

	readiness.RecommendNextLevel, readiness.Reason = aiReadinessVerdict(level, readiness)
	return readiness
}

// aiReadinessVerdict applies the documented thresholds and produces a
// human-readable Russian reason.
func aiReadinessVerdict(level int, r domain.AIReadiness) (bool, string) {
	if level >= 3 {
		return false, "автопилот уже на максимальном уровне — повышать некуда"
	}
	var missing []string
	if r.ShadowDays < aiReadinessMinShadowDays {
		missing = append(missing, fmt.Sprintf("нужно ещё понаблюдать (прошло %d из %d дней)", r.ShadowDays, aiReadinessMinShadowDays))
	}
	if r.DecisionsTotal < aiReadinessMinDecisions {
		missing = append(missing, fmt.Sprintf("мало решений для оценки (%d из %d)", r.DecisionsTotal, aiReadinessMinDecisions))
	}
	if r.WithinGuardrailsPct < aiReadinessMinGuardrailPct {
		missing = append(missing, fmt.Sprintf("слишком много решений выходит за рамки правил (в рамках %.0f%%, нужно ≥ %.0f%%)", r.WithinGuardrailsPct, aiReadinessMinGuardrailPct))
	}
	if len(missing) == 0 {
		return true, "ИИ уверенно работает в текущем режиме — можно повысить уровень автоматизации"
	}
	return false, "пока рано повышать уровень: " + strings.Join(missing, "; ")
}
