package service

import (
	"context"

	"github.com/google/uuid"

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

func (s *CampaignPhraseService) ListMinusPhrases(ctx context.Context, campaignID uuid.UUID) ([]domain.CampaignPhrase, error) {
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

func (s *CampaignPhraseService) AddMinusPhrase(ctx context.Context, campaignID uuid.UUID, phrase string) (*domain.CampaignPhrase, error) {
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

func (s *CampaignPhraseService) DeleteMinusPhrase(ctx context.Context, phraseID uuid.UUID) error {
	return s.queries.DeleteMinusPhrase(ctx, uuidToPgtype(phraseID))
}

func (s *CampaignPhraseService) ListPlusPhrases(ctx context.Context, campaignID uuid.UUID) ([]domain.CampaignPhrase, error) {
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

func (s *CampaignPhraseService) AddPlusPhrase(ctx context.Context, campaignID uuid.UUID, phrase string) (*domain.CampaignPhrase, error) {
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

func (s *CampaignPhraseService) DeletePlusPhrase(ctx context.Context, phraseID uuid.UUID) error {
	return s.queries.DeletePlusPhrase(ctx, uuidToPgtype(phraseID))
}
