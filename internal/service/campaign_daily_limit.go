package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type CampaignDailyLimitView struct {
	CampaignID       uuid.UUID `json:"campaign_id"`
	DailyLimit       int64     `json:"daily_limit"`
	Enabled          bool      `json:"enabled"`
	PauseWhenReached bool      `json:"pause_when_reached"`
	ResumeNextDay    bool      `json:"resume_next_day"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func campaignDailyLimitView(row sqlcgen.CampaignDailyLimit) CampaignDailyLimitView {
	return CampaignDailyLimitView{CampaignID: uuidFromPgtype(row.CampaignID), DailyLimit: row.DailyLimit, Enabled: row.Enabled,
		PauseWhenReached: row.PauseWhenReached, ResumeNextDay: row.ResumeNextDay, UpdatedAt: row.UpdatedAt.Time}
}

func (s *CampaignActionService) GetDailyLimit(ctx context.Context, workspaceID, campaignID uuid.UUID) (*CampaignDailyLimitView, error) {
	campaign, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{ID: uuidToPgtype(campaignID), WorkspaceID: uuidToPgtype(workspaceID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load campaign")
	}
	row, err := s.queries.GetCampaignDailyLimit(ctx, campaign.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		result := CampaignDailyLimitView{CampaignID: campaignID, PauseWhenReached: true, ResumeNextDay: true}
		return &result, nil
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load campaign daily limit")
	}
	result := campaignDailyLimitView(row)
	return &result, nil
}

func (s *CampaignActionService) UpdateDailyLimit(ctx context.Context, workspaceID, campaignID uuid.UUID, dailyLimit int64, enabled bool) (*CampaignDailyLimitView, error) {
	if dailyLimit < 0 || (enabled && dailyLimit <= 0) {
		return nil, apperror.New(apperror.ErrValidation, "daily_limit must be greater than 0 when enabled")
	}
	row, err := s.queries.UpsertCampaignDailyLimit(ctx, sqlcgen.UpsertCampaignDailyLimitParams{
		WorkspaceID: uuidToPgtype(workspaceID), CampaignID: uuidToPgtype(campaignID), DailyLimit: dailyLimit, Enabled: enabled,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "campaign not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update campaign daily limit")
	}
	result := campaignDailyLimitView(row)
	return &result, nil
}
