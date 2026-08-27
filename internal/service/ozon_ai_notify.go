package service

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// OzonAINotifier delivers autopilot events into the CRM bell (storeExternal):
// applied-actions digests and automation-level downgrades. Every call is
// best-effort — a failed notification is logged and never fails the job.
type OzonAINotifier struct {
	queries *sqlcgen.Queries
	client  *sellico.Client
	tokens  *sellico.ServiceTokenManager
	logger  zerolog.Logger
}

func NewOzonAINotifier(queries *sqlcgen.Queries, client *sellico.Client, tokens *sellico.ServiceTokenManager, logger zerolog.Logger) *OzonAINotifier {
	return &OzonAINotifier{
		queries: queries,
		client:  client,
		tokens:  tokens,
		logger:  logger.With().Str("component", "ozon_ai_notifier").Logger(),
	}
}

// NotifyWorkspaceOwners sends one notification to the CRM owners of a local
// workspace. Silently skips when the workspace has no CRM mirror or the
// service account is not configured.
func (n *OzonAINotifier) NotifyWorkspaceOwners(ctx context.Context, workspaceID uuid.UUID, notifType, title, message string) {
	if n == nil || n.tokens == nil || !n.tokens.IsConfigured() {
		return
	}
	workspace, err := n.queries.GetWorkspaceByID(ctx, uuidToPgtype(workspaceID))
	if err != nil || !workspace.ExternalWorkspaceID.Valid || workspace.ExternalWorkspaceID.String == "" {
		return
	}
	ownerIDs, err := n.queries.ListWorkspaceOwnerExternalUserIDs(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		n.logger.Warn().Err(err).Msg("ai notify: owner lookup failed")
		return
	}
	recipients := make([]int64, 0, len(ownerIDs))
	for _, raw := range ownerIDs {
		if !raw.Valid {
			continue
		}
		if id, parseErr := strconv.ParseInt(raw.String, 10, 64); parseErr == nil {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return
	}
	token, err := n.tokens.Get(ctx)
	if err != nil {
		n.logger.Warn().Err(err).Msg("ai notify: service token unavailable")
		return
	}
	if err := n.client.SendWorkspaceNotification(ctx, token, workspace.ExternalWorkspaceID.String, sellico.NotificationPayload{
		UserIDs: recipients,
		Type:    notifType,
		Title:   title,
		Message: message,
	}); err != nil {
		n.logger.Warn().Err(err).Str("type", notifType).Msg("ai notify: send failed")
	}
}

// WithNotifier attaches the CRM notifier to the AI manager (worker wiring).
func (s *OzonAIManagerService) WithNotifier(n *OzonAINotifier) *OzonAIManagerService {
	s.notifier = n
	return s
}
