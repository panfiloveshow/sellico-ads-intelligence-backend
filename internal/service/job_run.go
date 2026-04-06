package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type JobRunListFilter struct {
	TaskType string
	Status   string
}

type JobRunRetryResult struct {
	OriginalJobRunID uuid.UUID
	TaskType         string
	Status           string
	WorkspaceID      uuid.UUID
	ExportID         *uuid.UUID
}

type JobRunRetryEnqueuer interface {
	EnqueueWorkspaceTask(taskType string, workspaceID uuid.UUID) (string, error)
	EnqueueExportTask(workspaceID, exportID uuid.UUID) (string, error)
}

// JobRunService handles read operations for background job runs.
type JobRunService struct {
	queries  *sqlcgen.Queries
	enqueuer JobRunRetryEnqueuer
}

func NewJobRunService(queries *sqlcgen.Queries, enqueuer JobRunRetryEnqueuer) *JobRunService {
	return &JobRunService{queries: queries, enqueuer: enqueuer}
}

func (s *JobRunService) List(ctx context.Context, workspaceID uuid.UUID, filter JobRunListFilter, limit, offset int32) ([]domain.JobRun, error) {
	rows, err := s.queries.ListJobRunsByWorkspace(ctx, sqlcgen.ListJobRunsByWorkspaceParams{
		WorkspaceID:    uuidToPgtype(workspaceID),
		Limit:          limit,
		Offset:         offset,
		TaskTypeFilter: textToPgtype(filter.TaskType),
		StatusFilter:   textToPgtype(filter.Status),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list job runs")
	}

	extensionEvidence, evidenceErr := loadWorkspaceExtensionEvidence(ctx, s.queries, workspaceID, adsReadStatsLimit)
	if evidenceErr != nil {
		extensionEvidence = &workspaceExtensionEvidence{}
	}

	result := make([]domain.JobRun, len(rows))
	for i, row := range rows {
		result[i] = jobRunFromSqlc(row)
		result[i].Evidence = extensionEvidence.workspaceEvidence(domain.SourceAPI)
	}
	return result, nil
}

func (s *JobRunService) Get(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*domain.JobRun, error) {
	row, err := s.queries.GetJobRunByID(ctx, uuidToPgtype(jobRunID))
	if err == pgx.ErrNoRows {
		return nil, apperror.New(apperror.ErrNotFound, "job run not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get job run")
	}
	if !row.WorkspaceID.Valid || uuidFromPgtype(row.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "job run not found")
	}

	result := jobRunFromSqlc(row)
	if extensionEvidence, evidenceErr := loadWorkspaceExtensionEvidence(ctx, s.queries, workspaceID, adsReadStatsLimit); evidenceErr == nil {
		result.Evidence = extensionEvidence.workspaceEvidence(domain.SourceAPI)
	} else {
		result.Evidence = backendOnlyEvidence(domain.SourceAPI, 0.75)
	}
	return &result, nil
}

func (s *JobRunService) Retry(ctx context.Context, workspaceID, jobRunID uuid.UUID) (*JobRunRetryResult, error) {
	if s.enqueuer == nil {
		return nil, apperror.New(apperror.ErrInternal, "job run retry is not configured")
	}

	jobRun, err := s.Get(ctx, workspaceID, jobRunID)
	if err != nil {
		return nil, err
	}
	if jobRun.WorkspaceID == nil {
		return nil, apperror.New(apperror.ErrValidation, "job run is not workspace-scoped")
	}

	switch jobRun.TaskType {
	case "wb:sync_workspace", "wb:sync_campaigns", "wb:sync_campaign_stats", "wb:sync_phrases", "wb:sync_products", "recommendation:generate":
		status, enqueueErr := s.enqueuer.EnqueueWorkspaceTask(jobRun.TaskType, *jobRun.WorkspaceID)
		if enqueueErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to retry job run")
		}
		return &JobRunRetryResult{
			OriginalJobRunID: jobRun.ID,
			TaskType:         jobRun.TaskType,
			Status:           status,
			WorkspaceID:      *jobRun.WorkspaceID,
		}, nil
	case "export:generate":
		exportID, parseErr := exportIDFromJobRunMetadata(jobRun.Metadata)
		if parseErr != nil {
			return nil, apperror.New(apperror.ErrValidation, "job run metadata does not contain export_id")
		}
		status, enqueueErr := s.enqueuer.EnqueueExportTask(*jobRun.WorkspaceID, exportID)
		if enqueueErr != nil {
			return nil, apperror.New(apperror.ErrInternal, "failed to retry job run")
		}
		return &JobRunRetryResult{
			OriginalJobRunID: jobRun.ID,
			TaskType:         jobRun.TaskType,
			Status:           status,
			WorkspaceID:      *jobRun.WorkspaceID,
			ExportID:         &exportID,
		}, nil
	default:
		return nil, apperror.New(apperror.ErrValidation, "job run task type cannot be retried")
	}
}

func exportIDFromJobRunMetadata(metadata json.RawMessage) (uuid.UUID, error) {
	if len(metadata) == 0 {
		return uuid.Nil, errors.New("missing metadata")
	}
	var payload struct {
		ExportID string `json:"export_id"`
	}
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return uuid.Nil, err
	}
	if payload.ExportID == "" {
		return uuid.Nil, errors.New("missing export_id")
	}
	return uuid.Parse(payload.ExportID)
}
