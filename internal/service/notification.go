package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/telegram"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// NotificationService sends alerts through configured channels (Telegram, etc.).
type NotificationService struct {
	queries  *sqlcgen.Queries
	telegram *telegram.Client
	email    notificationEmailSender
	logger   zerolog.Logger
}

type notificationEmailSender interface {
	SendPlainText(ctx context.Context, recipients []string, subject, body string) error
	IsConfigured() bool
}

type NotificationDeliveryResult struct {
	TelegramSent bool
	EmailSent    bool
}

type NotificationOption func(*NotificationService)

func WithNotificationEmailSender(sender notificationEmailSender) NotificationOption {
	return func(s *NotificationService) {
		s.email = sender
	}
}

// NewNotificationService creates a new NotificationService.
func NewNotificationService(queries *sqlcgen.Queries, tg *telegram.Client, logger zerolog.Logger, opts ...NotificationOption) *NotificationService {
	service := &NotificationService{
		queries:  queries,
		telegram: tg,
		logger:   logger.With().Str("component", "notifications").Logger(),
	}
	for _, opt := range opts {
		opt(service)
	}
	return service
}

// NotifyNewRecommendations sends a summary of new recommendations to configured channels.
func (s *NotificationService) NotifyNewRecommendations(ctx context.Context, workspaceID uuid.UUID, recommendations []domain.Recommendation) {
	if len(recommendations) == 0 {
		return
	}

	settings := s.loadSettings(ctx, workspaceID)
	if settings == nil || settings.Notifications == nil || settings.Notifications.Telegram == nil {
		return
	}

	tgSettings := settings.Notifications.Telegram
	if !tgSettings.Enabled || tgSettings.BotToken == "" || tgSettings.ChatID == "" {
		return
	}
	if s.telegram == nil {
		s.logger.Warn().Str("workspace_id", workspaceID.String()).Msg("telegram notification enabled but client is not configured")
		return
	}

	text := s.formatRecommendationsSummary(recommendations)

	if err := s.telegram.SendMessage(ctx, tgSettings.BotToken, tgSettings.ChatID, text, ""); err != nil {
		s.logger.Error().
			Err(err).
			Str("workspace_id", workspaceID.String()).
			Int("count", len(recommendations)).
			Msg("failed to send telegram notification")
		return
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("count", len(recommendations)).
		Msg("telegram notification sent")
}

// NotifySyncComplete sends a notification when a workspace sync completes.
func (s *NotificationService) NotifySyncComplete(ctx context.Context, workspaceID uuid.UUID, status string, issues []string) {
	settings := s.loadSettings(ctx, workspaceID)
	if settings == nil || settings.Notifications == nil || settings.Notifications.Telegram == nil {
		return
	}

	tgSettings := settings.Notifications.Telegram
	if !tgSettings.Enabled || tgSettings.BotToken == "" || tgSettings.ChatID == "" {
		return
	}
	if s.telegram == nil {
		s.logger.Warn().Str("workspace_id", workspaceID.String()).Msg("telegram sync notification enabled but client is not configured")
		return
	}

	emoji := "OK"
	if status == "failed" {
		emoji = "FAIL"
	} else if status == "partial" {
		emoji = "WARN"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] Sync completed for workspace\n\nStatus: %s", emoji, status)

	if len(issues) > 0 {
		sb.WriteString("\n\nIssues:\n")
		for _, issue := range issues {
			fmt.Fprintf(&sb, "- %s\n", issue)
		}
	}

	if err := s.telegram.SendMessage(ctx, tgSettings.BotToken, tgSettings.ChatID, sb.String(), ""); err != nil {
		s.logger.Error().
			Err(err).
			Str("workspace_id", workspaceID.String()).
			Msg("failed to send sync notification")
	}
}

// NotifyPriceQuarantine alerts when products enter WB price quarantine (release
// is cabinet-UI-only). sampleNms are a few affected nmIDs for the message.
func (s *NotificationService) NotifyPriceQuarantine(ctx context.Context, workspaceID uuid.UUID, count int, sampleNms []int64) {
	if count <= 0 {
		return
	}
	settings := s.loadSettings(ctx, workspaceID)
	if settings == nil {
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[WARN] Товары в карантине цен WB: %d\n\nПродаются по старой цене. Снять карантин можно только вручную в кабинете WB.", count)
	if len(sampleNms) > 0 {
		sb.WriteString("\n\nАртикулы: ")
		for i, nm := range sampleNms {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%d", nm)
		}
	}
	s.sendTelegramText(ctx, workspaceID, settings, sb.String(), "price quarantine")
	s.sendEmailReport(ctx, workspaceID, settings, settings.Notifications != nil && settings.Notifications.Email != nil, "WB: товары в карантине цен", sb.String(), "price quarantine")
}

// NotifyPriceUploadResult alerts when a price upload batch fails or partially fails.
func (s *NotificationService) NotifyPriceUploadResult(ctx context.Context, workspaceID uuid.UUID, itemsCount int, outcome string) {
	if outcome != "failed" && outcome != "partial" {
		return
	}
	settings := s.loadSettings(ctx, workspaceID)
	if settings == nil {
		return
	}
	label := "частично применена"
	if outcome == "failed" {
		label = "не применена"
	}
	text := fmt.Sprintf("[WARN] Загрузка цен в WB %s (позиций: %d). Проверьте раздел «Управление ценами → Задачи загрузки».", label, itemsCount)
	s.sendTelegramText(ctx, workspaceID, settings, text, "price upload result")
	s.sendEmailReport(ctx, workspaceID, settings, settings.Notifications != nil && settings.Notifications.Email != nil, "WB: результат загрузки цен", text, "price upload result")
}

// NotifyWeeklyDigest sends a period summary for owner/client reporting.
func (s *NotificationService) NotifyWeeklyDigest(ctx context.Context, workspaceID uuid.UUID, dateFrom, dateTo time.Time, recommendations []domain.Recommendation, overviews ...*domain.AdsOverview) {
	if len(recommendations) == 0 {
		return
	}

	settings := s.loadSettings(ctx, workspaceID)
	if settings == nil || settings.Notifications == nil {
		return
	}

	text := s.formatWeeklyDigest(dateFrom, dateTo, recommendations, firstOverview(overviews...))
	s.sendTelegramText(ctx, workspaceID, settings, text, "weekly digest")
	s.sendEmailReport(ctx, workspaceID, settings, settings.Notifications.Email != nil && settings.Notifications.Email.WeeklyDigest, "Weekly WB Ads Digest", text, "weekly digest")
}

// NotifyDailyOwnerDigest sends a daily owner-facing summary from the real ads overview.
func (s *NotificationService) NotifyDailyOwnerDigest(ctx context.Context, workspaceID uuid.UUID, reportDate time.Time, overview *domain.AdsOverview, recommendations []domain.Recommendation) {
	if overview == nil {
		return
	}

	settings := s.loadSettings(ctx, workspaceID)
	if settings == nil || settings.Notifications == nil {
		return
	}

	text := s.formatDailyOwnerDigest(reportDate, *overview, recommendations)
	s.sendTelegramText(ctx, workspaceID, settings, text, "daily owner digest")
	s.sendEmailReport(ctx, workspaceID, settings, settings.Notifications.Email != nil && settings.Notifications.Email.DailyOwnerDigest, "Daily WB Ads Owner Report", text, "daily owner digest")
}

// NotifyAgencyClientReport sends a client-ready ads audit from real overview data.
func (s *NotificationService) NotifyAgencyClientReport(ctx context.Context, workspaceID uuid.UUID, reportDate, dateFrom, dateTo time.Time, overview *domain.AdsOverview, recommendations []domain.Recommendation) NotificationDeliveryResult {
	if overview == nil {
		return NotificationDeliveryResult{}
	}

	settings := s.loadSettings(ctx, workspaceID)
	if settings == nil || settings.Notifications == nil {
		return NotificationDeliveryResult{}
	}

	text := s.formatAgencyClientReport(reportDate, dateFrom, dateTo, *overview, recommendations)
	return NotificationDeliveryResult{
		TelegramSent: s.sendTelegramText(ctx, workspaceID, settings, text, "agency client report"),
		EmailSent:    s.sendEmailReport(ctx, workspaceID, settings, settings.Notifications.Email != nil && settings.Notifications.Email.ClientReports, "Client WB Ads Audit", text, "agency client report"),
	}
}

// BuildAgencyClientReport formats a client-ready report without sending it.
func (s *NotificationService) BuildAgencyClientReport(reportDate, dateFrom, dateTo time.Time, overview domain.AdsOverview, recommendations []domain.Recommendation) string {
	return s.formatAgencyClientReport(reportDate, dateFrom, dateTo, overview, recommendations)
}

func (s *NotificationService) sendTelegramText(ctx context.Context, workspaceID uuid.UUID, settings *domain.WorkspaceSettings, text, label string) bool {
	if settings == nil || settings.Notifications == nil || settings.Notifications.Telegram == nil {
		return false
	}
	tgSettings := settings.Notifications.Telegram
	if !tgSettings.Enabled || tgSettings.BotToken == "" || tgSettings.ChatID == "" {
		return false
	}
	if s.telegram == nil {
		s.logger.Warn().Str("workspace_id", workspaceID.String()).Str("report", label).Msg("telegram notification enabled but client is not configured")
		return false
	}
	if err := s.telegram.SendMessage(ctx, tgSettings.BotToken, tgSettings.ChatID, text, ""); err != nil {
		s.logger.Error().Err(err).Str("workspace_id", workspaceID.String()).Str("report", label).Msg("failed to send telegram notification")
		return false
	}
	s.logger.Info().Str("workspace_id", workspaceID.String()).Str("report", label).Msg("telegram notification sent")
	return true
}

func (s *NotificationService) sendEmailReport(ctx context.Context, workspaceID uuid.UUID, settings *domain.WorkspaceSettings, enabled bool, subject, text, label string) bool {
	if !enabled || settings == nil || settings.Notifications == nil || settings.Notifications.Email == nil {
		return false
	}
	emailSettings := settings.Notifications.Email
	if !emailSettings.Enabled || len(emailSettings.Recipients) == 0 {
		return false
	}
	if s.email == nil || !s.email.IsConfigured() {
		s.logger.Warn().Str("workspace_id", workspaceID.String()).Str("report", label).Msg("email report enabled but smtp sender is not configured")
		return false
	}
	if err := s.email.SendPlainText(ctx, emailSettings.Recipients, subject, text); err != nil {
		s.logger.Error().Err(err).Str("workspace_id", workspaceID.String()).Str("report", label).Msg("failed to send email report")
		return false
	}
	s.logger.Info().Str("workspace_id", workspaceID.String()).Str("report", label).Int("recipients", len(emailSettings.Recipients)).Msg("email report sent")
	return true
}

func (s *NotificationService) loadSettings(ctx context.Context, workspaceID uuid.UUID) *domain.WorkspaceSettings {
	raw, err := s.queries.GetWorkspaceSettings(ctx, uuidToPgtype(workspaceID))
	if err != nil || len(raw) == 0 || string(raw) == "{}" {
		return nil
	}

	var settings domain.WorkspaceSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		s.logger.Debug().Err(err).Msg("failed to parse workspace settings")
		return nil
	}
	return &settings
}

func (s *NotificationService) formatRecommendationsSummary(recs []domain.Recommendation) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "New Recommendations: %d\n\n", len(recs))

	criticalCount := 0
	highCount := 0
	mediumCount := 0
	lowCount := 0
	for _, rec := range recs {
		switch rec.Severity {
		case domain.SeverityCritical:
			criticalCount++
		case domain.SeverityHigh:
			highCount++
		case domain.SeverityMedium, "":
			mediumCount++
		default:
			lowCount++
		}
	}

	if criticalCount > 0 {
		fmt.Fprintf(&sb, "[!!!] Critical severity: %d\n", criticalCount)
	}
	if highCount > 0 {
		fmt.Fprintf(&sb, "[!] High severity: %d\n", highCount)
	}
	if mediumCount > 0 {
		fmt.Fprintf(&sb, "[i] Medium severity: %d\n", mediumCount)
	}
	if lowCount > 0 {
		fmt.Fprintf(&sb, "[-] Low severity: %d\n", lowCount)
	}

	sb.WriteString("\nTop items:\n")
	topRecs := append([]domain.Recommendation(nil), recs...)
	sort.SliceStable(topRecs, func(i, j int) bool {
		return notificationSeverityRank(topRecs[i].Severity) > notificationSeverityRank(topRecs[j].Severity)
	})

	limit := 5
	if len(topRecs) < limit {
		limit = len(topRecs)
	}
	for i := 0; i < limit; i++ {
		rec := topRecs[i]
		fmt.Fprintf(&sb, "- [%s] %s\n", strings.ToUpper(rec.Severity), rec.Title)
	}

	if len(recs) > 5 {
		fmt.Fprintf(&sb, "\n...and %d more", len(recs)-5)
	}

	return sb.String()
}

func (s *NotificationService) formatDailyOwnerDigest(reportDate time.Time, overview domain.AdsOverview, recs []domain.Recommendation) string {
	var sb strings.Builder
	sb.WriteString("Daily WB Ads Owner Report\n")
	if !reportDate.IsZero() {
		fmt.Fprintf(&sb, "Date: %s\n", reportDate.Format("2006-01-02"))
	}

	current := domain.AdsMetricsSummary{DataMode: "unavailable"}
	trend := ""
	if overview.PerformanceCompare != nil {
		current = overview.PerformanceCompare.Current
		trend = strings.TrimSpace(overview.PerformanceCompare.Trend)
	}
	sb.WriteString("\nMoney:\n")
	fmt.Fprintf(&sb, "- Spend: %d\n", current.Spend)
	fmt.Fprintf(&sb, "- Revenue: %d\n", current.Revenue)
	fmt.Fprintf(&sb, "- Orders: %d\n", current.Orders)
	if current.Revenue > 0 {
		fmt.Fprintf(&sb, "- DRR: %.1f%%\n", current.DRR)
		fmt.Fprintf(&sb, "- ROAS: %.2f\n", current.ROAS)
	} else {
		fmt.Fprintf(&sb, "- DRR: unavailable, revenue is 0\n")
		fmt.Fprintf(&sb, "- ROAS: unavailable, spend/revenue evidence is incomplete\n")
	}
	if trend != "" {
		fmt.Fprintf(&sb, "- Trend: %s\n", trend)
	}

	fmt.Fprintf(&sb, "\nData status: %s", overview.DataStatus.State)
	if strings.TrimSpace(overview.DataStatus.Reason) != "" {
		fmt.Fprintf(&sb, " - %s", strings.TrimSpace(overview.DataStatus.Reason))
	}
	sb.WriteString("\n")
	if overview.LastAutoSync != nil {
		fmt.Fprintf(&sb, "Last sync: %s", overview.LastAutoSync.Status)
		if overview.LastAutoSync.WBErrors > 0 {
			fmt.Fprintf(&sb, ", WB errors: %d", overview.LastAutoSync.WBErrors)
		}
		sb.WriteString("\n")
	}

	fmt.Fprintf(&sb, "\nDecision queue: %d attention items, %d active recommendations\n", len(overview.Attention), len(recs))
	appendRecommendationTaskCounts(&sb, overview.Totals)
	appendDecisionQueueBuckets(&sb, overview.Totals.DecisionQueueBuckets)
	appendTaskOwnerBuckets(&sb, overview.Totals.TaskOwnerBuckets)
	appendRecommendationBuckets(&sb, recs)
	appendOverdueRecommendationTasks(&sb, recs, time.Now(), 5)
	appendProductDecisionCounts(&sb, overview.Totals.ProductDecisions)
	appendTopAttention(&sb, overview.Attention, 5)

	return strings.TrimRight(sb.String(), "\n")
}

func (s *NotificationService) formatAgencyClientReport(reportDate, dateFrom, dateTo time.Time, overview domain.AdsOverview, recs []domain.Recommendation) string {
	var sb strings.Builder
	sb.WriteString("Client WB Ads Audit\n")
	if !reportDate.IsZero() {
		fmt.Fprintf(&sb, "Report date: %s\n", reportDate.Format("2006-01-02"))
	}
	if !dateFrom.IsZero() && !dateTo.IsZero() {
		fmt.Fprintf(&sb, "Period: %s - %s\n", dateFrom.Format("2006-01-02"), dateTo.Format("2006-01-02"))
	}

	fmt.Fprintf(&sb, "\nAccount coverage:\n")
	fmt.Fprintf(&sb, "- Cabinets: %d\n", overview.Totals.Cabinets)
	fmt.Fprintf(&sb, "- Products: %d\n", overview.Totals.Products)
	fmt.Fprintf(&sb, "- Campaigns: %d active / %d total\n", overview.Totals.ActiveCampaigns, overview.Totals.Campaigns)
	fmt.Fprintf(&sb, "- Search queries: %d\n", overview.Totals.Queries)

	appendWeeklyOverview(&sb, overview)
	appendPeriodDelta(&sb, overview.PerformanceCompare)

	sb.WriteString("\nClient action plan:\n")
	appendRecommendationBuckets(&sb, recs)
	appendOverdueRecommendationTasks(&sb, recs, time.Now(), 5)
	appendProductDecisionCounts(&sb, overview.Totals.ProductDecisions)

	appendRecentBidChangeEvidence(&sb, overview.TopCampaigns, 5)
	appendTopClientActions(&sb, recs, 7)

	return strings.TrimRight(sb.String(), "\n")
}

func (s *NotificationService) formatWeeklyDigest(dateFrom, dateTo time.Time, recs []domain.Recommendation, overviews ...*domain.AdsOverview) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Weekly WB Ads Digest\n")
	if !dateFrom.IsZero() && !dateTo.IsZero() {
		fmt.Fprintf(&sb, "Period: %s - %s\n", dateFrom.Format("2006-01-02"), dateTo.Format("2006-01-02"))
	}
	if overview := firstOverview(overviews...); overview != nil {
		appendWeeklyOverview(&sb, *overview)
	}
	fmt.Fprintf(&sb, "\nTotal recommendations: %d\n", len(recs))

	critical, high := 0, 0
	losses, growth, cardTasks, apiRisks := 0, 0, 0, 0
	for _, rec := range recs {
		switch rec.Severity {
		case domain.SeverityCritical:
			critical++
		case domain.SeverityHigh:
			high++
		}
		switch recommendationDigestBucket(rec.Type) {
		case "losses":
			losses++
		case "growth":
			growth++
		case "card_tasks":
			cardTasks++
		case "api_risks":
			apiRisks++
		}
	}

	if critical > 0 || high > 0 {
		fmt.Fprintf(&sb, "\nRisks: %d critical, %d high\n", critical, high)
	}
	sb.WriteString("\nDecision queue:\n")
	fmt.Fprintf(&sb, "- Budget/loss control: %d\n", losses)
	fmt.Fprintf(&sb, "- Growth opportunities: %d\n", growth)
	fmt.Fprintf(&sb, "- Product/card tasks: %d\n", cardTasks)
	if apiRisks > 0 {
		fmt.Fprintf(&sb, "- API/sync risks: %d\n", apiRisks)
	}
	appendOverdueRecommendationTasks(&sb, recs, time.Now(), 5)

	top := append([]domain.Recommendation(nil), recs...)
	sort.SliceStable(top, func(i, j int) bool {
		leftRank := notificationSeverityRank(top[i].Severity)
		rightRank := notificationSeverityRank(top[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		return top[i].CreatedAt.After(top[j].CreatedAt)
	})

	limit := 7
	if len(top) < limit {
		limit = len(top)
	}
	sb.WriteString("\nTop actions:\n")
	for i := 0; i < limit; i++ {
		rec := top[i]
		fmt.Fprintf(&sb, "- [%s] %s", strings.ToUpper(rec.Severity), rec.Title)
		if rec.NextAction != nil && strings.TrimSpace(*rec.NextAction) != "" {
			fmt.Fprintf(&sb, " -> %s", strings.TrimSpace(*rec.NextAction))
		}
		sb.WriteString("\n")
	}
	if len(top) > limit {
		fmt.Fprintf(&sb, "\n...and %d more", len(top)-limit)
	}
	return sb.String()
}

func appendPeriodDelta(sb *strings.Builder, compare *domain.AdsPeriodCompare) {
	if compare == nil {
		return
	}
	sb.WriteString("\nPeriod delta from real comparison:\n")
	fmt.Fprintf(sb, "- Spend delta: %d\n", compare.Delta.Spend)
	fmt.Fprintf(sb, "- Revenue delta: %d\n", compare.Delta.Revenue)
	fmt.Fprintf(sb, "- Orders delta: %d\n", compare.Delta.Orders)
	if strings.TrimSpace(compare.Trend) != "" {
		fmt.Fprintf(sb, "- Trend: %s\n", strings.TrimSpace(compare.Trend))
	}
}

func appendRecentBidChangeEvidence(sb *strings.Builder, campaigns []domain.CampaignPerformanceSummary, limit int) {
	if limit <= 0 {
		return
	}
	type bidChangeEvidence struct {
		campaignName string
		change       domain.CampaignBidChangeSummary
	}
	changes := make([]bidChangeEvidence, 0)
	for _, campaign := range campaigns {
		for _, change := range campaign.RecentBidChanges {
			changes = append(changes, bidChangeEvidence{campaignName: campaign.Name, change: change})
		}
	}
	if len(changes) == 0 {
		sb.WriteString("Action history: no bid changes in the current overview evidence\n")
		return
	}
	sort.SliceStable(changes, func(i, j int) bool {
		return changes[i].change.CreatedAt.After(changes[j].change.CreatedAt)
	})
	if len(changes) < limit {
		limit = len(changes)
	}
	sb.WriteString("Action history evidence:\n")
	for i := 0; i < limit; i++ {
		item := changes[i]
		change := item.change
		fmt.Fprintf(sb, "- %s: %d -> %d", item.campaignName, change.OldBid, change.NewBid)
		if strings.TrimSpace(change.Source) != "" {
			fmt.Fprintf(sb, ", source %s", strings.TrimSpace(change.Source))
		}
		if strings.TrimSpace(change.WBStatus) != "" {
			fmt.Fprintf(sb, ", WB status %s", strings.TrimSpace(change.WBStatus))
		}
		if strings.TrimSpace(change.Reason) != "" {
			fmt.Fprintf(sb, ", reason: %s", strings.TrimSpace(change.Reason))
		}
		sb.WriteString("\n")
	}
}

func appendTopClientActions(sb *strings.Builder, recs []domain.Recommendation, limit int) {
	if limit <= 0 {
		return
	}
	active := make([]domain.Recommendation, 0, len(recs))
	for _, rec := range recs {
		if rec.Status == "" || rec.Status == domain.RecommendationStatusActive {
			active = append(active, rec)
		}
	}
	if len(active) == 0 {
		sb.WriteString("Next client actions: no active recommendations in the provided period\n")
		return
	}
	sort.SliceStable(active, func(i, j int) bool {
		leftRank := notificationSeverityRank(active[i].Severity)
		rightRank := notificationSeverityRank(active[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		return active[i].CreatedAt.After(active[j].CreatedAt)
	})
	if len(active) < limit {
		limit = len(active)
	}
	sb.WriteString("Next client actions:\n")
	for i := 0; i < limit; i++ {
		rec := active[i]
		fmt.Fprintf(sb, "- [%s] %s", strings.ToUpper(rec.Severity), rec.Title)
		if rec.NextAction != nil && strings.TrimSpace(*rec.NextAction) != "" {
			fmt.Fprintf(sb, " -> %s", strings.TrimSpace(*rec.NextAction))
		}
		if label := recommendationDigestBucketLabel(recommendationDigestBucket(rec.Type)); label != "" {
			fmt.Fprintf(sb, " (%s)", label)
		}
		sb.WriteString("\n")
	}
}

func firstOverview(overviews ...*domain.AdsOverview) *domain.AdsOverview {
	for _, overview := range overviews {
		if overview != nil {
			return overview
		}
	}
	return nil
}

func appendWeeklyOverview(sb *strings.Builder, overview domain.AdsOverview) {
	current := domain.AdsMetricsSummary{DataMode: "unavailable"}
	trend := ""
	if overview.PerformanceCompare != nil {
		current = overview.PerformanceCompare.Current
		trend = strings.TrimSpace(overview.PerformanceCompare.Trend)
	}

	sb.WriteString("\nMoney:\n")
	fmt.Fprintf(sb, "- Spend: %d\n", current.Spend)
	fmt.Fprintf(sb, "- Revenue: %d\n", current.Revenue)
	fmt.Fprintf(sb, "- Orders: %d\n", current.Orders)
	if current.Revenue > 0 {
		fmt.Fprintf(sb, "- DRR: %.1f%%\n", current.DRR)
		fmt.Fprintf(sb, "- ROAS: %.2f\n", current.ROAS)
	} else {
		fmt.Fprintf(sb, "- DRR: unavailable, revenue is 0\n")
		fmt.Fprintf(sb, "- ROAS: unavailable, spend/revenue evidence is incomplete\n")
	}
	if trend != "" {
		fmt.Fprintf(sb, "- Trend: %s\n", trend)
	}

	if strings.TrimSpace(overview.DataStatus.State) != "" {
		fmt.Fprintf(sb, "\nData status: %s", overview.DataStatus.State)
		if strings.TrimSpace(overview.DataStatus.Reason) != "" {
			fmt.Fprintf(sb, " - %s", strings.TrimSpace(overview.DataStatus.Reason))
		}
		sb.WriteString("\n")
	}
	if overview.LastAutoSync != nil {
		fmt.Fprintf(sb, "Last sync: %s", overview.LastAutoSync.Status)
		if overview.LastAutoSync.WBErrors > 0 {
			fmt.Fprintf(sb, ", WB errors: %d", overview.LastAutoSync.WBErrors)
		}
		sb.WriteString("\n")
	}
	appendRecommendationTaskCounts(sb, overview.Totals)
	appendDecisionQueueBuckets(sb, overview.Totals.DecisionQueueBuckets)
	appendTaskOwnerBuckets(sb, overview.Totals.TaskOwnerBuckets)
	if len(overview.Attention) > 0 {
		sb.WriteString("\n")
		appendTopAttention(sb, overview.Attention, 5)
	}
}

func appendRecommendationTaskCounts(sb *strings.Builder, totals domain.AdsOverviewTotals) {
	if totals.ActiveRecommendations == 0 && totals.OverdueRecommendations == 0 {
		return
	}
	fmt.Fprintf(sb, "Recommendation tasks: %d active", totals.ActiveRecommendations)
	if totals.OverdueRecommendations > 0 {
		fmt.Fprintf(sb, ", %d overdue", totals.OverdueRecommendations)
	}
	sb.WriteString("\n")
}

func appendDecisionQueueBuckets(sb *strings.Builder, buckets map[string]int) {
	if len(buckets) == 0 {
		return
	}
	ordered := []struct {
		key   string
		label string
	}{
		{"losses", "Budget/loss control"},
		{"growth", "Growth opportunities"},
		{"card_tasks", "Product/card tasks"},
		{"api_risks", "API/sync risks"},
	}
	sb.WriteString("Decision queue buckets:\n")
	for _, item := range ordered {
		if buckets[item.key] > 0 {
			fmt.Fprintf(sb, "- %s: %d\n", item.label, buckets[item.key])
		}
	}
}

func appendTaskOwnerBuckets(sb *strings.Builder, buckets map[string]int) {
	if len(buckets) == 0 {
		return
	}
	ordered := []struct {
		key   string
		label string
	}{
		{domain.RecommendationTaskOwnerMarketer, "Marketer"},
		{domain.RecommendationTaskOwnerMarketplaceManager, "Marketplace manager"},
		{domain.RecommendationTaskOwnerContent, "Content"},
		{domain.RecommendationTaskOwnerSEO, "SEO"},
		{domain.RecommendationTaskOwnerTechnicalSpecialist, "Technical specialist"},
	}
	sb.WriteString("Task owners:\n")
	for _, item := range ordered {
		if buckets[item.key] > 0 {
			fmt.Fprintf(sb, "- %s: %d\n", item.label, buckets[item.key])
		}
	}
}

func appendRecommendationBuckets(sb *strings.Builder, recs []domain.Recommendation) {
	if len(recs) == 0 {
		return
	}
	losses, growth, cardTasks, apiRisks := 0, 0, 0, 0
	for _, rec := range recs {
		switch recommendationDigestBucket(rec.Type) {
		case "losses":
			losses++
		case "growth":
			growth++
		case "card_tasks":
			cardTasks++
		case "api_risks":
			apiRisks++
		}
	}
	sb.WriteString("Recommendation buckets:\n")
	fmt.Fprintf(sb, "- Budget/loss control: %d\n", losses)
	fmt.Fprintf(sb, "- Growth opportunities: %d\n", growth)
	fmt.Fprintf(sb, "- Product/card tasks: %d\n", cardTasks)
	if apiRisks > 0 {
		fmt.Fprintf(sb, "- API/sync risks: %d\n", apiRisks)
	}
}

func appendOverdueRecommendationTasks(sb *strings.Builder, recs []domain.Recommendation, now time.Time, limit int) {
	if limit <= 0 {
		return
	}
	overdue := make([]domain.Recommendation, 0)
	for _, rec := range recs {
		if rec.Status != domain.RecommendationStatusActive || rec.CreatedAt.IsZero() || now.Before(rec.CreatedAt) {
			continue
		}
		if now.Sub(rec.CreatedAt) >= domain.RecommendationOverdueAfter {
			overdue = append(overdue, rec)
		}
	}
	if len(overdue) == 0 {
		return
	}
	sort.SliceStable(overdue, func(i, j int) bool {
		leftAge := now.Sub(overdue[i].CreatedAt)
		rightAge := now.Sub(overdue[j].CreatedAt)
		if leftAge != rightAge {
			return leftAge > rightAge
		}
		return notificationSeverityRank(overdue[i].Severity) > notificationSeverityRank(overdue[j].Severity)
	})
	if len(overdue) < limit {
		limit = len(overdue)
	}
	sb.WriteString("Overdue tasks:\n")
	for i := 0; i < limit; i++ {
		rec := overdue[i]
		ageHours := int(now.Sub(rec.CreatedAt).Hours())
		fmt.Fprintf(sb, "- [%s] %s (%dh", strings.ToUpper(rec.Severity), rec.Title, ageHours)
		if label := recommendationDigestBucketLabel(recommendationDigestBucket(rec.Type)); label != "" {
			fmt.Fprintf(sb, ", %s", label)
		}
		sb.WriteString(")\n")
	}
}

func recommendationDigestBucketLabel(bucket string) string {
	switch bucket {
	case domain.RecommendationTaskCategoryLosses:
		return "Budget/loss control"
	case domain.RecommendationTaskCategoryGrowth:
		return "Growth opportunities"
	case domain.RecommendationTaskCategoryCardTasks:
		return "Product/card tasks"
	case domain.RecommendationTaskCategoryAPIRisks:
		return "API/sync risks"
	default:
		return ""
	}
}

func appendProductDecisionCounts(sb *strings.Builder, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sb.WriteString("Product decisions:\n")
	for _, key := range keys {
		fmt.Fprintf(sb, "- %s: %d\n", key, counts[key])
	}
}

func appendTopAttention(sb *strings.Builder, items []domain.AttentionItem, limit int) {
	if len(items) == 0 || limit <= 0 {
		return
	}
	top := append([]domain.AttentionItem(nil), items...)
	sort.SliceStable(top, func(i, j int) bool {
		leftRank := notificationSeverityRank(top[i].Severity)
		rightRank := notificationSeverityRank(top[j].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		return top[i].Title < top[j].Title
	})
	if len(top) < limit {
		limit = len(top)
	}
	sb.WriteString("Top attention:\n")
	for i := 0; i < limit; i++ {
		item := top[i]
		fmt.Fprintf(sb, "- [%s] %s", strings.ToUpper(item.Severity), item.Title)
		if strings.TrimSpace(item.ActionLabel) != "" {
			fmt.Fprintf(sb, " -> %s", strings.TrimSpace(item.ActionLabel))
		}
		sb.WriteString("\n")
	}
}

func recommendationDigestBucket(recType string) string {
	return domain.RecommendationTaskCategory(recType)
}

func notificationSeverityRank(severity string) int {
	switch severity {
	case domain.SeverityCritical:
		return 4
	case domain.SeverityHigh:
		return 3
	case domain.SeverityMedium, "":
		return 2
	default:
		return 1
	}
}
