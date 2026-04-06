package worker

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	TaskSyncWorkspace           = "wb:sync_workspace"
	TaskSyncCampaigns           = "wb:sync_campaigns"
	TaskSyncCampaignStats       = "wb:sync_campaign_stats"
	TaskSyncPhrases             = "wb:sync_phrases"
	TaskSyncProducts            = "wb:sync_products"
	TaskGenerateRecommendations = "recommendation:generate"
	TaskGenerateExport          = "export:generate"

	TaskSweepSyncWorkspace     = "wb:sweep_sync_workspace"
	TaskSweepSyncCampaigns     = "wb:sweep_sync_campaigns"
	TaskSweepSyncCampaignStats = "wb:sweep_sync_campaign_stats"
	TaskSweepSyncPhrases       = "wb:sweep_sync_phrases"
	TaskSweepSyncProducts      = "wb:sweep_sync_products"
	TaskSweepRecommendations       = "recommendation:sweep"
	TaskSweepRefreshIntegrations   = "sellico:sweep_refresh_integrations"
	TaskBidAutomation              = "bid:automation"
	TaskSweepBidAutomation         = "bid:sweep_automation"
	TaskCollectKeywords              = "semantics:collect_keywords"
	TaskSweepCollectKeywords         = "semantics:sweep_collect_keywords"
	TaskExtractCompetitors           = "competitor:extract"
	TaskSweepExtractCompetitors      = "competitor:sweep_extract"
	TaskCollectDelivery              = "delivery:collect"
	TaskSweepCollectDelivery         = "delivery:sweep_collect"
	TaskSEOAnalysis                  = "seo:analyze"
	TaskSweepSEOAnalysis             = "seo:sweep_analyze"
	TaskExtendedRecommendations      = "recommendation:extended"
	TaskSweepExtendedRecommendations = "recommendation:sweep_extended"

	QueueBidAutomation = "bid-automation"
	QueueSemantics     = "semantics"
	QueueCompetitors   = "competitors"
	QueueDelivery      = "delivery"
	QueueSEO           = "seo"

	QueueWBSync          = "wb-sync"
	QueueWBCampaigns     = "wb-import-campaigns"
	QueueWBCampaignStats = "wb-import-campaign-stats"
	QueueWBPhrases       = "wb-import-phrases"
	QueueWBProducts      = "wb-import-products"
	QueueRecommendations = "recommendation-generation"
	QueueExports         = "export-generation"
)

type WorkspaceTaskPayload struct {
	WorkspaceID string `json:"workspace_id"`
	JobRunID    string `json:"job_run_id,omitempty"`
	Metadata    any    `json:"metadata,omitempty"`
}

type ExportTaskPayload struct {
	WorkspaceID string `json:"workspace_id"`
	ExportID    string `json:"export_id"`
}

func NewWorkspaceTask(taskType string, workspaceID uuid.UUID) (*asynq.Task, error) {
	return NewWorkspaceTaskWithMetadata(taskType, workspaceID, nil, nil)
}

func NewWorkspaceTaskWithMetadata(taskType string, workspaceID uuid.UUID, jobRunID *uuid.UUID, metadata map[string]any) (*asynq.Task, error) {
	payload := WorkspaceTaskPayload{
		WorkspaceID: workspaceID.String(),
		Metadata:    metadata,
	}
	if jobRunID != nil {
		payload.JobRunID = jobRunID.String()
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(taskType, data), nil
}

func NewWorkspaceSyncTask(workspaceID uuid.UUID) (*asynq.Task, error) {
	return NewWorkspaceTask(TaskSyncWorkspace, workspaceID)
}

func NewSweepTask(taskType string) *asynq.Task {
	return asynq.NewTask(taskType, nil)
}

func NewExportTask(workspaceID, exportID uuid.UUID) (*asynq.Task, error) {
	payload, err := json.Marshal(ExportTaskPayload{
		WorkspaceID: workspaceID.String(),
		ExportID:    exportID.String(),
	})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskGenerateExport, payload), nil
}
