package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type WorkspaceSyncEnqueuer interface {
	EnqueueWorkspaceSync(workspaceID uuid.UUID, jobRunID *uuid.UUID, metadata map[string]any) (string, error)
}

type SyncTriggerResult struct {
	TaskType    string
	Status      string
	WorkspaceID uuid.UUID
	CabinetID   uuid.UUID
	JobRunID    uuid.UUID
}

// SyncJobService validates scope and enqueues background sync jobs.
type SyncJobService struct {
	queries  *sqlcgen.Queries
	enqueuer WorkspaceSyncEnqueuer
}

func NewSyncJobService(queries *sqlcgen.Queries, enqueuer WorkspaceSyncEnqueuer) *SyncJobService {
	return &SyncJobService{
		queries:  queries,
		enqueuer: enqueuer,
	}
}

func (s *SyncJobService) TriggerSellerCabinetSync(ctx context.Context, actorID, workspaceID, cabinetID uuid.UUID) (*SyncTriggerResult, error) {
	if s.enqueuer == nil {
		return nil, apperror.New(apperror.ErrInternal, "sync enqueuer is not configured")
	}

	cabinet, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get seller cabinet")
	}
	if uuidFromPgtype(cabinet.WorkspaceID) != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}

	jobRunMetadata := map[string]any{
		"seller_cabinet_id":   cabinetID.String(),
		"seller_cabinet_name": cabinet.Name,
		"task_type":           "wb:sync_workspace",
	}
	jobRun, err := s.queries.CreateJobRun(ctx, sqlcgen.CreateJobRunParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		TaskType:    "wb:sync_workspace",
		Status:      "pending",
		StartedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		Metadata:    mustJSON(jobRunMetadata),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to create sync job run")
	}

	status, err := s.enqueuer.EnqueueWorkspaceSync(workspaceID, uuidPtr(uuidFromPgtype(jobRun.ID)), jobRunMetadata)
	if err != nil {
		_, _ = s.queries.UpdateJobRunStatus(ctx, sqlcgen.UpdateJobRunStatusParams{
			ID:           jobRun.ID,
			Status:       "failed",
			FinishedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
			Metadata:     mustJSON(jobRunMetadata),
		})
		return nil, apperror.New(apperror.ErrInternal, "failed to enqueue workspace sync")
	}

	jobRunMetadata["queue_status"] = status
	_, _ = s.queries.UpdateJobRunStatus(ctx, sqlcgen.UpdateJobRunStatusParams{
		ID:           jobRun.ID,
		Status:       "pending",
		FinishedAt:   pgtype.Timestamptz{},
		ErrorMessage: pgtype.Text{},
		Metadata:     mustJSON(jobRunMetadata),
	})

	meta, _ := json.Marshal(map[string]string{
		"seller_cabinet_id":   cabinetID.String(),
		"seller_cabinet_name": cabinet.Name,
		"task_type":           "wb:sync_workspace",
		"queue_status":        status,
		"job_run_id":          uuidFromPgtype(jobRun.ID).String(),
	})
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "trigger_seller_cabinet_sync",
		EntityType:  "seller_cabinet",
		EntityID:    cabinet.ID,
		Metadata:    meta,
	})

	return &SyncTriggerResult{
		TaskType:    "wb:sync_workspace",
		Status:      status,
		WorkspaceID: workspaceID,
		CabinetID:   cabinetID,
		JobRunID:    uuidFromPgtype(jobRun.ID),
	}, nil
}

func uuidPtr(value uuid.UUID) *uuid.UUID {
	return &value
}

func mustJSON(data map[string]any) []byte {
	if data == nil {
		return []byte("{}")
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return []byte("{}")
	}
	return payload
}
