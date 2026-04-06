package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ExtensionService handles browser extension endpoints.
type ExtensionService struct {
	queries    *sqlcgen.Queries
	appVersion string
}

func NewExtensionService(queries *sqlcgen.Queries, appVersion string) *ExtensionService {
	return &ExtensionService{
		queries:    queries,
		appVersion: appVersion,
	}
}

func (s *ExtensionService) UpsertSession(ctx context.Context, userID, workspaceID uuid.UUID, extensionVersion string) (*domain.ExtensionSession, error) {
	existing, err := s.queries.GetExtensionSession(ctx, sqlcgen.GetExtensionSessionParams{
		UserID:      uuidToPgtype(userID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		row, createErr := s.queries.CreateExtensionSession(ctx, sqlcgen.CreateExtensionSessionParams{
			UserID:           uuidToPgtype(userID),
			WorkspaceID:      uuidToPgtype(workspaceID),
			ExtensionVersion: extensionVersion,
			LastActiveAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		})
		if createErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to create extension session")
		}
		result := extensionSessionFromSqlc(row)
		return &result, nil
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get extension session")
	}

	row, err := s.queries.UpdateExtensionSession(ctx, sqlcgen.UpdateExtensionSessionParams{
		ID:               existing.ID,
		ExtensionVersion: extensionVersion,
		LastActiveAt:     pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update extension session")
	}

	result := extensionSessionFromSqlc(row)
	return &result, nil
}

func (s *ExtensionService) CreateContextEvent(ctx context.Context, userID, workspaceID uuid.UUID, url, pageType string) (*domain.ExtensionContextEvent, error) {
	session, err := s.getSession(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}

	metadata, _ := json.Marshal(map[string]string{
		"url":       url,
		"page_type": pageType,
	})
	row, err := s.queries.CreateExtensionContextEvent(ctx, sqlcgen.CreateExtensionContextEventParams{
		SessionID:   session.ID,
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(userID),
		Url:         url,
		PageType:    pageType,
		Metadata:    metadata,
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create extension context event")
	}
	if err := s.touchSession(ctx, session.ID); err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to update extension activity")
	}

	result := extensionContextEventFromSqlc(row)
	return &result, nil
}

func (s *ExtensionService) Version() string {
	return s.appVersion
}

func (s *ExtensionService) getSession(ctx context.Context, userID, workspaceID uuid.UUID) (*sqlcgen.ExtensionSession, error) {
	session, err := s.queries.GetExtensionSession(ctx, sqlcgen.GetExtensionSessionParams{
		UserID:      uuidToPgtype(userID),
		WorkspaceID: uuidToPgtype(workspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrValidation, "extension session not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get extension session")
	}
	return &session, nil
}

func (s *ExtensionService) touchSession(ctx context.Context, sessionID pgtype.UUID) error {
	return s.queries.UpdateExtensionSessionActivity(ctx, sessionID)
}
