package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	logger   zerolog.Logger
}

// NewNotificationService creates a new NotificationService.
func NewNotificationService(queries *sqlcgen.Queries, tg *telegram.Client, logger zerolog.Logger) *NotificationService {
	return &NotificationService{
		queries:  queries,
		telegram: tg,
		logger:   logger.With().Str("component", "notifications").Logger(),
	}
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

	highCount := 0
	mediumCount := 0
	for _, rec := range recs {
		if rec.Severity == string(domain.SeverityHigh) {
			highCount++
		} else {
			mediumCount++
		}
	}

	if highCount > 0 {
		fmt.Fprintf(&sb, "[!] High severity: %d\n", highCount)
	}
	if mediumCount > 0 {
		fmt.Fprintf(&sb, "[i] Medium severity: %d\n", mediumCount)
	}

	sb.WriteString("\nTop items:\n")
	limit := 5
	if len(recs) < limit {
		limit = len(recs)
	}
	for i := 0; i < limit; i++ {
		rec := recs[i]
		fmt.Fprintf(&sb, "- [%s] %s\n", strings.ToUpper(rec.Severity), rec.Title)
	}

	if len(recs) > 5 {
		fmt.Fprintf(&sb, "\n...and %d more", len(recs)-5)
	}

	return sb.String()
}
