package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeNotificationEmailSender struct {
	configured bool
	recipients []string
	subject    string
	body       string
}

func (f *fakeNotificationEmailSender) IsConfigured() bool {
	return f.configured
}

func (f *fakeNotificationEmailSender) SendPlainText(_ context.Context, recipients []string, subject, body string) error {
	f.recipients = append([]string(nil), recipients...)
	f.subject = subject
	f.body = body
	return nil
}

func TestNotificationService_FormatRecommendationsSummary(t *testing.T) {
	svc := &NotificationService{}

	recs := []domain.Recommendation{
		{Title: "High impressions zero clicks", Severity: "high"},
		{Title: "Campaign bid pressure too high", Severity: "medium"},
		{Title: "Phrase low engagement", Severity: "medium"},
	}

	text := svc.formatRecommendationsSummary(recs)
	assert.Contains(t, text, "New Recommendations: 3")
	assert.Contains(t, text, "[!] High severity: 1")
	assert.Contains(t, text, "[i] Medium severity: 2")
	assert.Contains(t, text, "High impressions zero clicks")
}

func TestNotificationService_FormatRecommendationsSummary_MoreThan5(t *testing.T) {
	svc := &NotificationService{}

	recs := make([]domain.Recommendation, 8)
	for i := range recs {
		recs[i] = domain.Recommendation{Title: "Rec", Severity: "medium"}
	}

	text := svc.formatRecommendationsSummary(recs)
	assert.Contains(t, text, "New Recommendations: 8")
	assert.Contains(t, text, "...and 3 more")
}

func TestNotificationService_FormatRecommendationsSummary_PrioritizesCritical(t *testing.T) {
	svc := &NotificationService{}

	recs := []domain.Recommendation{
		{Title: "Medium item", Severity: domain.SeverityMedium},
		{Title: "Critical stock alert", Severity: domain.SeverityCritical},
		{Title: "High budget alert", Severity: domain.SeverityHigh},
		{Title: "Low item", Severity: domain.SeverityLow},
	}

	text := svc.formatRecommendationsSummary(recs)
	assert.Contains(t, text, "[!!!] Critical severity: 1")
	assert.Contains(t, text, "[!] High severity: 1")
	assert.Contains(t, text, "[i] Medium severity: 1")
	assert.Contains(t, text, "[-] Low severity: 1")

	criticalPos := strings.Index(text, "Critical stock alert")
	highPos := strings.Index(text, "High budget alert")
	mediumPos := strings.Index(text, "Medium item")

	assert.NotEqual(t, -1, criticalPos)
	assert.NotEqual(t, -1, highPos)
	assert.NotEqual(t, -1, mediumPos)
	assert.Less(t, criticalPos, highPos)
	assert.Less(t, highPos, mediumPos)
}

func TestNotificationService_FormatWeeklyDigestGroupsRealRecommendationTypes(t *testing.T) {
	svc := &NotificationService{}
	nextAction := "Снизьте ставку и проверьте слабые кластеры"

	recs := []domain.Recommendation{
		{
			Title:      "Кампания тратит бюджет без заказов",
			Type:       domain.RecommendationTypeHighSpendLowOrders,
			Severity:   domain.SeverityCritical,
			NextAction: &nextAction,
			CreatedAt:  time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
		},
		{
			Title:     "Кластер подходит для SEO",
			Type:      domain.RecommendationTypeOptimizeSEO,
			Severity:  domain.SeverityMedium,
			CreatedAt: time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC),
		},
		{
			Title:     "Карточка теряет клики",
			Type:      domain.RecommendationTypeCardConversionIssue,
			Severity:  domain.SeverityHigh,
			CreatedAt: time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
		},
	}

	text := svc.formatWeeklyDigest(
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		recs,
	)

	assert.Contains(t, text, "Weekly WB Ads Digest")
	assert.Contains(t, text, "Period: 2026-05-20 - 2026-05-27")
	assert.Contains(t, text, "Total recommendations: 3")
	assert.Contains(t, text, "Risks: 1 critical, 1 high")
	assert.Contains(t, text, "Budget/loss control: 1")
	assert.Contains(t, text, "Growth opportunities: 1")
	assert.Contains(t, text, "Product/card tasks: 1")
	assert.Contains(t, text, "Снизьте ставку")
}

func TestNotificationService_FormatWeeklyDigestIncludesRealOverviewWhenProvided(t *testing.T) {
	svc := &NotificationService{}

	text := svc.formatWeeklyDigest(
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		[]domain.Recommendation{
			{
				Title:    "Кампания тратит бюджет без заказов",
				Type:     domain.RecommendationTypeHighSpendLowOrders,
				Severity: domain.SeverityHigh,
			},
		},
		&domain.AdsOverview{
			LastAutoSync: &domain.SellerCabinetAutoSyncSummary{
				Status:   "completed",
				WBErrors: 1,
			},
			PerformanceCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					Spend:   28000,
					Revenue: 210000,
					Orders:  96,
					DRR:     13.3,
					ROAS:    7.5,
				},
				Trend: "improving",
			},
			DataStatus: domain.AdsDataStatus{
				State:  "ready",
				Reason: "fresh WB weekly stats",
			},
			Totals: domain.AdsOverviewTotals{
				ActiveRecommendations:  4,
				OverdueRecommendations: 2,
				DecisionQueueBuckets: map[string]int{
					"losses":    3,
					"growth":    2,
					"api_risks": 1,
				},
				TaskOwnerBuckets: map[string]int{
					domain.RecommendationTaskOwnerMarketer:            2,
					domain.RecommendationTaskOwnerSEO:                 1,
					domain.RecommendationTaskOwnerTechnicalSpecialist: 1,
				},
			},
			Attention: []domain.AttentionItem{
				{
					Title:       "Бюджет кампании скоро закончится",
					Severity:    domain.SeverityCritical,
					ActionLabel: "Открыть кампанию",
				},
			},
		},
	)

	assert.Contains(t, text, "Money:")
	assert.Contains(t, text, "Spend: 28000")
	assert.Contains(t, text, "Revenue: 210000")
	assert.Contains(t, text, "Orders: 96")
	assert.Contains(t, text, "DRR: 13.3%")
	assert.Contains(t, text, "ROAS: 7.50")
	assert.Contains(t, text, "Trend: improving")
	assert.Contains(t, text, "Data status: ready - fresh WB weekly stats")
	assert.Contains(t, text, "Last sync: completed, WB errors: 1")
	assert.Contains(t, text, "Recommendation tasks: 4 active, 2 overdue")
	assert.Contains(t, text, "Decision queue buckets:")
	assert.Contains(t, text, "Budget/loss control: 3")
	assert.Contains(t, text, "Growth opportunities: 2")
	assert.Contains(t, text, "API/sync risks: 1")
	assert.Contains(t, text, "Task owners:")
	assert.Contains(t, text, "Marketer: 2")
	assert.Contains(t, text, "SEO: 1")
	assert.Contains(t, text, "Technical specialist: 1")
	assert.Contains(t, text, "[CRITICAL] Бюджет кампании скоро закончится -> Открыть кампанию")
	assert.Contains(t, text, "Total recommendations: 1")
}

func TestNotificationService_FormatWeeklyDigestShowsTruthfulUnavailableOverviewRevenue(t *testing.T) {
	svc := &NotificationService{}

	text := svc.formatWeeklyDigest(
		time.Time{},
		time.Time{},
		[]domain.Recommendation{{Title: "Нет статистики", Type: domain.RecommendationTypeLowCTR, Severity: domain.SeverityMedium}},
		&domain.AdsOverview{
			PerformanceCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					Spend:    1200,
					Revenue:  0,
					Orders:   0,
					DataMode: "exact",
				},
			},
			DataStatus: domain.AdsDataStatus{State: "empty_period", Reason: "no weekly orders"},
		},
	)

	assert.Contains(t, text, "Spend: 1200")
	assert.Contains(t, text, "Revenue: 0")
	assert.Contains(t, text, "DRR: unavailable, revenue is 0")
	assert.Contains(t, text, "ROAS: unavailable, spend/revenue evidence is incomplete")
	assert.Contains(t, text, "Data status: empty_period - no weekly orders")
}

func TestNotificationService_FormatDailyOwnerDigestUsesRealOverviewMetrics(t *testing.T) {
	svc := &NotificationService{}
	nextAction := "Снизить ставку"

	text := svc.formatDailyOwnerDigest(
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		domain.AdsOverview{
			LastAutoSync: &domain.SellerCabinetAutoSyncSummary{
				Status:   "completed",
				WBErrors: 2,
			},
			PerformanceCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					Spend:   4500,
					Revenue: 58000,
					Orders:  32,
					DRR:     7.7,
					ROAS:    12.89,
				},
				Trend: "improving",
			},
			DataStatus: domain.AdsDataStatus{
				State:  "ready",
				Reason: "fresh WB stats",
			},
			Attention: []domain.AttentionItem{
				{
					Title:       "Товар без остатков",
					Severity:    domain.SeverityCritical,
					ActionLabel: "Остановить масштабирование",
				},
				{
					Title:    "Найден кластер для SEO",
					Severity: domain.SeverityLow,
				},
			},
			Totals: domain.AdsOverviewTotals{
				ActiveRecommendations:  5,
				OverdueRecommendations: 1,
				DecisionQueueBuckets: map[string]int{
					"losses":     2,
					"growth":     1,
					"card_tasks": 1,
				},
				TaskOwnerBuckets: map[string]int{
					domain.RecommendationTaskOwnerMarketer: 1,
					domain.RecommendationTaskOwnerSEO:      1,
				},
				ProductDecisions: map[string]int{
					"scale":       3,
					"reduce_bid":  2,
					"fix_product": 1,
				},
			},
		},
		[]domain.Recommendation{
			{
				Title:      "Слабый кластер",
				Type:       domain.RecommendationTypeLowerBid,
				Severity:   domain.SeverityHigh,
				NextAction: &nextAction,
			},
			{
				Title:    "SEO запрос",
				Type:     domain.RecommendationTypeOptimizeSEO,
				Severity: domain.SeverityMedium,
			},
		},
	)

	assert.Contains(t, text, "Daily WB Ads Owner Report")
	assert.Contains(t, text, "Date: 2026-05-28")
	assert.Contains(t, text, "Spend: 4500")
	assert.Contains(t, text, "Revenue: 58000")
	assert.Contains(t, text, "Orders: 32")
	assert.Contains(t, text, "DRR: 7.7%")
	assert.Contains(t, text, "ROAS: 12.89")
	assert.Contains(t, text, "Data status: ready - fresh WB stats")
	assert.Contains(t, text, "Last sync: completed, WB errors: 2")
	assert.Contains(t, text, "Decision queue: 2 attention items, 2 active recommendations")
	assert.Contains(t, text, "Recommendation tasks: 5 active, 1 overdue")
	assert.Contains(t, text, "Decision queue buckets:")
	assert.Contains(t, text, "Budget/loss control: 2")
	assert.Contains(t, text, "Growth opportunities: 1")
	assert.Contains(t, text, "Product/card tasks: 1")
	assert.Contains(t, text, "Task owners:")
	assert.Contains(t, text, "Marketer: 1")
	assert.Contains(t, text, "SEO: 1")
	assert.Contains(t, text, "Budget/loss control: 1")
	assert.Contains(t, text, "Growth opportunities: 1")
	assert.Contains(t, text, "fix_product: 1")
	assert.Contains(t, text, "[CRITICAL] Товар без остатков -> Остановить масштабирование")
}

func TestNotificationService_FormatDailyOwnerDigestShowsTruthfulUnavailableRevenueState(t *testing.T) {
	svc := &NotificationService{}

	text := svc.formatDailyOwnerDigest(time.Time{}, domain.AdsOverview{
		PerformanceCompare: &domain.AdsPeriodCompare{
			Current: domain.AdsMetricsSummary{
				Spend:    1200,
				Revenue:  0,
				Orders:   0,
				DataMode: "exact",
			},
		},
		DataStatus: domain.AdsDataStatus{
			State:  "empty_period",
			Reason: "no orders in selected period",
		},
	}, nil)

	assert.Contains(t, text, "Spend: 1200")
	assert.Contains(t, text, "Revenue: 0")
	assert.Contains(t, text, "DRR: unavailable, revenue is 0")
	assert.Contains(t, text, "ROAS: unavailable, spend/revenue evidence is incomplete")
	assert.Contains(t, text, "Data status: empty_period - no orders in selected period")
}

func TestNotificationService_FormatAgencyClientReportUsesRealOverviewAndActionEvidence(t *testing.T) {
	svc := &NotificationService{}
	nextAction := "Подтвердить снижение ставки"

	text := svc.formatAgencyClientReport(
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		domain.AdsOverview{
			PerformanceCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					Spend:   94000,
					Revenue: 720000,
					Orders:  310,
					DRR:     13.1,
					ROAS:    7.66,
				},
				Delta: domain.AdsMetricsDelta{
					Spend:   -6000,
					Revenue: 85000,
					Orders:  42,
				},
				Trend: "improving",
			},
			DataStatus: domain.AdsDataStatus{State: "ready", Reason: "fresh WB 30-day stats"},
			Totals: domain.AdsOverviewTotals{
				Cabinets:              2,
				Products:              48,
				Campaigns:             19,
				ActiveCampaigns:       14,
				Queries:               320,
				ActiveRecommendations: 6,
				DecisionQueueBuckets: map[string]int{
					"losses": 2,
					"growth": 3,
				},
				ProductDecisions: map[string]int{
					"scale":      4,
					"reduce_bid": 2,
				},
			},
			TopCampaigns: []domain.CampaignPerformanceSummary{
				{
					ID:           uuid.New(),
					Name:         "Search towels",
					WBCampaignID: 1001,
					WorkspaceID:  uuid.New(),
					CampaignType: 9,
					RecentBidChanges: []domain.CampaignBidChangeSummary{
						{
							ID:        uuid.New(),
							OldBid:    340,
							NewBid:    290,
							Source:    "autopilot",
							WBStatus:  "applied",
							Reason:    "CPO above target",
							CreatedAt: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
						},
					},
				},
			},
			Attention: []domain.AttentionItem{
				{Title: "Товар без остатков", Severity: domain.SeverityCritical, ActionLabel: "Проверить остатки"},
			},
		},
		[]domain.Recommendation{
			{
				Title:      "Снизить ставку по дорогому кластеру",
				Type:       domain.RecommendationTypeLowerBid,
				Severity:   domain.SeverityHigh,
				Status:     domain.RecommendationStatusActive,
				NextAction: &nextAction,
				CreatedAt:  time.Date(2026, 5, 27, 11, 0, 0, 0, time.UTC),
			},
		},
	)

	assert.Contains(t, text, "Client WB Ads Audit")
	assert.Contains(t, text, "Report date: 2026-05-28")
	assert.Contains(t, text, "Period: 2026-04-29 - 2026-05-28")
	assert.Contains(t, text, "Cabinets: 2")
	assert.Contains(t, text, "Campaigns: 14 active / 19 total")
	assert.Contains(t, text, "Search queries: 320")
	assert.Contains(t, text, "Spend: 94000")
	assert.Contains(t, text, "Revenue: 720000")
	assert.Contains(t, text, "DRR: 13.1%")
	assert.Contains(t, text, "Spend delta: -6000")
	assert.Contains(t, text, "Revenue delta: 85000")
	assert.Contains(t, text, "Orders delta: 42")
	assert.Contains(t, text, "Recommendation tasks: 6 active")
	assert.Contains(t, text, "Budget/loss control: 2")
	assert.Contains(t, text, "Growth opportunities: 3")
	assert.Contains(t, text, "Action history evidence:")
	assert.Contains(t, text, "Search towels: 340 -> 290")
	assert.Contains(t, text, "source autopilot")
	assert.Contains(t, text, "WB status applied")
	assert.Contains(t, text, "reason: CPO above target")
	assert.Contains(t, text, "Next client actions:")
	assert.Contains(t, text, "Снизить ставку по дорогому кластеру -> Подтвердить снижение ставки")
	assert.Contains(t, text, "Product decisions:")
	assert.Contains(t, text, "reduce_bid: 2")
	assert.Contains(t, text, "[CRITICAL] Товар без остатков -> Проверить остатки")
}

func TestNotificationService_FormatAgencyClientReportShowsTruthfulEmptyActionEvidence(t *testing.T) {
	svc := &NotificationService{}

	text := svc.formatAgencyClientReport(
		time.Time{},
		time.Time{},
		time.Time{},
		domain.AdsOverview{
			PerformanceCompare: &domain.AdsPeriodCompare{
				Current: domain.AdsMetricsSummary{
					Spend:   1200,
					Revenue: 0,
					Orders:  0,
				},
			},
			DataStatus: domain.AdsDataStatus{State: "empty_period", Reason: "no real orders"},
		},
		nil,
	)

	assert.Contains(t, text, "Client WB Ads Audit")
	assert.Contains(t, text, "DRR: unavailable, revenue is 0")
	assert.Contains(t, text, "Action history: no bid changes in the current overview evidence")
	assert.Contains(t, text, "Next client actions: no active recommendations in the provided period")
	assert.NotContains(t, text, "example")
	assert.NotContains(t, text, "demo")
}

func TestNotificationService_SendEmailReportUsesConfiguredRealRecipients(t *testing.T) {
	sender := &fakeNotificationEmailSender{configured: true}
	svc := &NotificationService{email: sender, logger: zerolog.Nop()}
	settings := &domain.WorkspaceSettings{
		Notifications: &domain.NotificationSettings{
			Email: &domain.EmailSettings{
				Enabled:       true,
				Recipients:    []string{"client@example.com", "owner@example.com"},
				ClientReports: true,
			},
		},
	}

	sent := svc.sendEmailReport(context.Background(), uuid.New(), settings, settings.Notifications.Email.ClientReports, "Client WB Ads Audit", "real report", "agency client report")

	require.True(t, sent)
	require.Equal(t, []string{"client@example.com", "owner@example.com"}, sender.recipients)
	assert.Equal(t, "Client WB Ads Audit", sender.subject)
	assert.Equal(t, "real report", sender.body)
}

func TestNotificationService_SendEmailReportSkipsWhenSMTPNotConfigured(t *testing.T) {
	sender := &fakeNotificationEmailSender{configured: false}
	svc := &NotificationService{email: sender, logger: zerolog.Nop()}
	settings := &domain.WorkspaceSettings{
		Notifications: &domain.NotificationSettings{
			Email: &domain.EmailSettings{
				Enabled:       true,
				Recipients:    []string{"client@example.com"},
				ClientReports: true,
			},
		},
	}

	sent := svc.sendEmailReport(context.Background(), uuid.New(), settings, true, "Client WB Ads Audit", "real report", "agency client report")

	assert.False(t, sent)
	assert.Empty(t, sender.recipients)
	assert.Empty(t, sender.subject)
	assert.Empty(t, sender.body)
}

func TestRecommendationDigestBucketKeepsStockAsProductTask(t *testing.T) {
	assert.Equal(t, "card_tasks", recommendationDigestBucket(domain.RecommendationTypeStockAlert))
	assert.Equal(t, "losses", recommendationDigestBucket(domain.RecommendationTypeAddMinusPhrase))
	assert.Equal(t, "losses", recommendationDigestBucket(domain.RecommendationTypeCampaignTestSpend))
	assert.Equal(t, "growth", recommendationDigestBucket(domain.RecommendationTypeRaiseBid))
}

func TestAppendOverdueRecommendationTasksListsOnlyActiveOverdue(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	var sb strings.Builder

	appendOverdueRecommendationTasks(&sb, []domain.Recommendation{
		{
			Title:     "Слабый кластер без решения",
			Type:      domain.RecommendationTypeLowerBid,
			Severity:  domain.SeverityHigh,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: now.Add(-72 * time.Hour),
		},
		{
			Title:     "Свежая задача",
			Type:      domain.RecommendationTypeOptimizeSEO,
			Severity:  domain.SeverityMedium,
			Status:    domain.RecommendationStatusActive,
			CreatedAt: now.Add(-2 * time.Hour),
		},
		{
			Title:     "Уже закрытая задача",
			Type:      domain.RecommendationTypeStockAlert,
			Severity:  domain.SeverityCritical,
			Status:    domain.RecommendationStatusCompleted,
			CreatedAt: now.Add(-96 * time.Hour),
		},
	}, now, 5)

	text := sb.String()
	assert.Contains(t, text, "Overdue tasks:")
	assert.Contains(t, text, "Слабый кластер без решения")
	assert.Contains(t, text, "72h")
	assert.Contains(t, text, "Budget/loss control")
	assert.NotContains(t, text, "Свежая задача")
	assert.NotContains(t, text, "Уже закрытая задача")
}
