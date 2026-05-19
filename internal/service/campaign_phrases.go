package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// CampaignPhraseService manages plus/minus phrases for campaigns.
type CampaignPhraseService struct {
	queries *sqlcgen.Queries
}

func NewCampaignPhraseService(queries *sqlcgen.Queries) *CampaignPhraseService {
	return &CampaignPhraseService{queries: queries}
}

func (s *CampaignPhraseService) ListMinusPhrases(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]domain.CampaignPhrase, error) {
	if err := s.ensureCampaignInWorkspace(ctx, workspaceID, campaignID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListMinusPhrases(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list minus phrases")
	}
	result := make([]domain.CampaignPhrase, len(rows))
	for i, row := range rows {
		result[i] = domain.CampaignPhrase{
			ID:         uuidFromPgtype(row.ID),
			CampaignID: uuidFromPgtype(row.CampaignID),
			Phrase:     row.Phrase,
			CreatedAt:  row.CreatedAt.Time,
		}
	}
	return result, nil
}

func (s *CampaignPhraseService) AddMinusPhrase(ctx context.Context, workspaceID, campaignID uuid.UUID, phrase string) (*domain.CampaignPhrase, error) {
	if err := s.ensureCampaignInWorkspace(ctx, workspaceID, campaignID); err != nil {
		return nil, err
	}
	row, err := s.queries.CreateMinusPhrase(ctx, uuidToPgtype(campaignID), phrase)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to add minus phrase")
	}
	return &domain.CampaignPhrase{
		ID:         uuidFromPgtype(row.ID),
		CampaignID: uuidFromPgtype(row.CampaignID),
		Phrase:     row.Phrase,
		CreatedAt:  row.CreatedAt.Time,
	}, nil
}

func (s *CampaignPhraseService) DeleteMinusPhrase(ctx context.Context, workspaceID, phraseID uuid.UUID) error {
	return s.queries.DeleteMinusPhraseInWorkspace(ctx, sqlcgen.DeleteCampaignPhraseInWorkspaceParams{
		ID:          uuidToPgtype(phraseID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
}

func (s *CampaignPhraseService) ListPlusPhrases(ctx context.Context, workspaceID, campaignID uuid.UUID) ([]domain.CampaignPhrase, error) {
	if err := s.ensureCampaignInWorkspace(ctx, workspaceID, campaignID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListPlusPhrases(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list plus phrases")
	}
	result := make([]domain.CampaignPhrase, len(rows))
	for i, row := range rows {
		result[i] = domain.CampaignPhrase{
			ID:         uuidFromPgtype(row.ID),
			CampaignID: uuidFromPgtype(row.CampaignID),
			Phrase:     row.Phrase,
			CreatedAt:  row.CreatedAt.Time,
		}
	}
	return result, nil
}

func (s *CampaignPhraseService) AddPlusPhrase(ctx context.Context, workspaceID, campaignID uuid.UUID, phrase string) (*domain.CampaignPhrase, error) {
	if err := s.ensureCampaignInWorkspace(ctx, workspaceID, campaignID); err != nil {
		return nil, err
	}
	row, err := s.queries.CreatePlusPhrase(ctx, uuidToPgtype(campaignID), phrase)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to add plus phrase")
	}
	return &domain.CampaignPhrase{
		ID:         uuidFromPgtype(row.ID),
		CampaignID: uuidFromPgtype(row.CampaignID),
		Phrase:     row.Phrase,
		CreatedAt:  row.CreatedAt.Time,
	}, nil
}

func (s *CampaignPhraseService) DeletePlusPhrase(ctx context.Context, workspaceID, phraseID uuid.UUID) error {
	return s.queries.DeletePlusPhraseInWorkspace(ctx, sqlcgen.DeleteCampaignPhraseInWorkspaceParams{
		ID:          uuidToPgtype(phraseID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
}

func (s *CampaignPhraseService) ensureCampaignInWorkspace(ctx context.Context, workspaceID, campaignID uuid.UUID) error {
	_, err := s.queries.GetCampaignByIDAndWorkspace(ctx, sqlcgen.GetCampaignByIDAndWorkspaceParams{
		ID:          uuidToPgtype(campaignID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(apperror.ErrNotFound, "campaign not found")
	}
	return apperror.New(apperror.ErrInternal, "failed to verify campaign")
}
