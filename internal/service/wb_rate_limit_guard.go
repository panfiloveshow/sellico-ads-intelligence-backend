package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const (
	wbEndpointAdverts         = "adv_adverts"
	wbEndpointFullstats       = "adv_fullstats"
	wbEndpointNormQueryStats  = "adv_normquery_stats"
	wbEndpointBudget          = "adv_budget"
	wbEndpointAdFinance       = "adv_finance"
	wbEndpointAnalyticsFunnel = "analytics_sales_funnel"
	wbEndpointTariffs         = "wb_tariffs"
	wbEndpointCampaignActions = "adv_campaign_actions"
)

func wbEndpointFallbackDelay(endpoint string) time.Duration {
	switch endpoint {
	case wbEndpointFullstats:
		return 20 * time.Second
	case wbEndpointNormQueryStats:
		return 7 * time.Second
	default:
		return 60 * time.Second
	}
}

func (s *SyncService) guardWBEndpoint(ctx context.Context, summary *SyncSummary, cabinetID uuid.UUID, endpoint, stage string) bool {
	limit, err := s.queries.GetWBAPIRateLimit(ctx, uuidToPgtype(cabinetID), endpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		s.logger.Warn().Err(err).Str("endpoint", endpoint).Str("cabinet_id", cabinetID.String()).Msg("failed to read WB rate limit guard")
		return false
	}
	if !limit.NextAllowedAt.Valid {
		return false
	}
	now := time.Now().UTC()
	next := limit.NextAllowedAt.Time.UTC()
	if !next.After(now) {
		return false
	}
	retryAfter := int(math.Ceil(next.Sub(now).Seconds()))
	if retryAfter < 1 {
		retryAfter = int(limit.RetryAfterSeconds)
	}
	summary.addRateLimitIssue(stage, cabinetID.String(), endpoint, next, retryAfter, "rate limited: WB endpoint %s cooling down until %s", endpoint, next.Format(time.RFC3339))
	return true
}

func (s *SyncService) recordWBRateLimitFromError(ctx context.Context, cabinetID uuid.UUID, endpoint string, err error) {
	if err == nil || !isRateLimitIssue(err.Error()) {
		return
	}
	next, retryAfterSeconds := wbRateLimitWindow(endpoint)
	lastError := strings.TrimSpace(err.Error())
	if len(lastError) > 500 {
		lastError = lastError[:500]
	}
	if upsertErr := s.queries.UpsertWBAPIRateLimit(ctx, sqlcgen.UpsertWBAPIRateLimitParams{
		SellerCabinetID:   uuidToPgtype(cabinetID),
		EndpointKey:       endpoint,
		NextAllowedAt:     pgtype.Timestamptz{Time: next, Valid: true},
		RetryAfterSeconds: int32(retryAfterSeconds),
		LastStatus:        429,
		LastError:         pgtype.Text{String: lastError, Valid: lastError != ""},
	}); upsertErr != nil {
		s.logger.Warn().Err(upsertErr).Str("endpoint", endpoint).Str("cabinet_id", cabinetID.String()).Msg("failed to persist WB rate limit")
	}
}

func (s *SyncService) markSummaryRateLimitFromError(summary *SyncSummary, endpoint string, err error) {
	if summary == nil || err == nil || !isRateLimitIssue(err.Error()) {
		return
	}
	next, retryAfterSeconds := wbRateLimitWindow(endpoint)
	summary.RateLimitEndpoint = endpoint
	summary.RetryAfterSeconds = retryAfterSeconds
	summary.NextAllowedAt = &next
}

func wbRateLimitWindow(endpoint string) (time.Time, int) {
	delay := wbEndpointFallbackDelay(endpoint)
	next := time.Now().UTC().Add(delay)
	return next, int(delay.Seconds())
}

func blockedByWBRateLimitMessage(endpoint string, next time.Time) string {
	return fmt.Sprintf("WB endpoint %s is rate limited until %s", endpoint, next.UTC().Format(time.RFC3339))
}

func wbEndpointRateLimitBlockReason(endpoint string, limit sqlcgen.WBAPIRateLimit, now time.Time) string {
	if !limit.NextAllowedAt.Valid {
		return ""
	}
	next := limit.NextAllowedAt.Time.UTC()
	if !next.After(now.UTC()) {
		return ""
	}
	retryAfter := int(math.Ceil(next.Sub(now.UTC()).Seconds()))
	if retryAfter < 1 && limit.RetryAfterSeconds > 0 {
		retryAfter = int(limit.RetryAfterSeconds)
	}
	message := blockedByWBRateLimitMessage(endpoint, next)
	if retryAfter > 0 {
		message = fmt.Sprintf("%s (retry_after_seconds=%d)", message, retryAfter)
	}
	if limit.LastStatus > 0 {
		message = fmt.Sprintf("%s, last_status=%d", message, limit.LastStatus)
	}
	return message
}
