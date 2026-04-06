package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type RecommendationJobEnqueuer interface {
	EnqueueRecommendationGeneration(workspaceID uuid.UUID) (string, error)
}

type WorkspaceTaskTriggerResult struct {
	TaskType    string
	Status      string
	WorkspaceID uuid.UUID
}

const recommendationGenerateTaskType = "recommendation:generate"

// RecommendationJobService validates scope and enqueues recommendation generation jobs.
type RecommendationJobService struct {
	queries  *sqlcgen.Queries
	enqueuer RecommendationJobEnqueuer
}

func NewRecommendationJobService(queries *sqlcgen.Queries, enqueuer RecommendationJobEnqueuer) *RecommendationJobService {
	return &RecommendationJobService{
		queries:  queries,
		enqueuer: enqueuer,
	}
}

func (s *RecommendationJobService) TriggerGenerate(ctx context.Context, actorID, workspaceID uuid.UUID) (*WorkspaceTaskTriggerResult, error) {
	if s.enqueuer == nil {
		return nil, apperror.New(apperror.ErrInternal, "recommendation enqueuer is not configured")
	}

	_, err := s.queries.GetWorkspaceByID(ctx, uuidToPgtype(workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "workspace not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to get workspace")
	}

	status, err := s.enqueuer.EnqueueRecommendationGeneration(workspaceID)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to enqueue recommendation generation")
	}

	meta, _ := json.Marshal(map[string]string{
		"task_type":    recommendationGenerateTaskType,
		"queue_status": status,
	})
	writeAuditLog(ctx, s.queries, sqlcgen.CreateAuditLogParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		UserID:      uuidToPgtype(actorID),
		Action:      "trigger_recommendation_generation",
		EntityType:  "workspace",
		EntityID:    uuidToPgtype(workspaceID),
		Metadata:    meta,
	})

	return &WorkspaceTaskTriggerResult{
		TaskType:    recommendationGenerateTaskType,
		Status:      status,
		WorkspaceID: workspaceID,
	}, nil
}
