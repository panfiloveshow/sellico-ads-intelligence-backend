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
	TaskSendClientAuditReport   = "report:client_audit"

	TaskSweepSyncWorkspace           = "wb:sweep_sync_workspace"
	TaskSweepSyncCampaigns           = "wb:sweep_sync_campaigns"
	TaskSweepSyncCampaignStats       = "wb:sweep_sync_campaign_stats"
	TaskSweepSyncPhrases             = "wb:sweep_sync_phrases"
	TaskSweepSyncProducts            = "wb:sweep_sync_products"
	TaskSweepRecommendations         = "recommendation:sweep"
	TaskSweepClientAuditReports      = "report:sweep_client_audit"
	TaskSweepRefreshIntegrations     = "sellico:sweep_refresh_integrations"
	TaskBidAutomation                = "bid:automation"
	TaskSweepBidAutomation           = "bid:sweep_automation"
	TaskReconcileBidActions          = "bid:reconcile_actions"
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

	TaskRepricer              = "price:automation"
	TaskSweepRepricer         = "price:sweep_automation"
	TaskPollPriceTasks        = "price:poll_tasks"
	TaskSweepPollPriceTasks   = "price:sweep_poll_tasks"
	TaskExecutePriceSchedule  = "price:execute_schedules"
	TaskSyncPrices            = "price:sync"
	TaskSweepSyncPrices       = "price:sweep_sync"
	TaskSweepRepricerDigest   = "price:sweep_digest"
	TaskSweepSellicoEconomics = "price:sync_economics"

	QueueBidAutomation = "bid-automation"
	QueueSemantics     = "semantics"
	QueueCompetitors   = "competitors"
	QueueDelivery      = "delivery"
	QueueSEO           = "seo"

	// Ozon module (phase 1: read-only sync).
	TaskOzonSweepSync   = "ozon:sweep_sync"
	TaskOzonSyncCabinet = "ozon:sync_cabinet"
	QueueOzonSync       = "ozon-sync"

	// Ozon module (phase 2: deterministic strategies).
	TaskOzonStrategySweep = "ozon:strategy_sweep"
	TaskOzonStrategyRun   = "ozon:strategy_run"

	// Ozon module (phase 3: AI autopilot). Runs on the ozon-sync queue —
	// its low concurrency keeps multi-minute LLM cabinet runs sequential.
	TaskOzonAISweep = "ozon:ai_sweep"
	TaskOzonAIRun   = "ozon:ai_run"

	// Ozon module (phase 4: repricer). Per-workspace runs on the ozon-sync
	// queue; the service itself paces writes per cabinet.
	TaskOzonRepricerSweep = "ozon:repricer_sweep"
	TaskOzonRepricerRun   = "ozon:repricer_run"

	QueueWBSync          = "wb-sync"
	QueueWBCampaigns     = "wb-import-campaigns"
	QueueWBCampaignStats = "wb-import-campaign-stats"
	QueueWBPhrases       = "wb-import-phrases"
	QueueWBProducts      = "wb-import-products"
	QueueRecommendations = "recommendation-generation"
	QueueExports         = "export-generation"
	QueueRepricer        = "repricer"
	QueueRepricerPoll    = "repricer-poll"
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

// OzonCabinetTaskPayload scopes an ozon task to a single seller cabinet —
// Ozon syncs are per-cabinet, not per-workspace, because each cabinet holds
// its own credential pairs and rate budget.
type OzonCabinetTaskPayload struct {
	CabinetID string `json:"cabinet_id"`
}

func NewOzonCabinetTask(cabinetID uuid.UUID) (*asynq.Task, error) {
	data, err := json.Marshal(OzonCabinetTaskPayload{CabinetID: cabinetID.String()})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskOzonSyncCabinet, data), nil
}

// OzonAITaskPayload scopes one AI autopilot run to a cabinet; trigger is
// 'cron' (sweep) or 'manual' (POST /ozon/ai/run).
type OzonAITaskPayload struct {
	CabinetID string `json:"cabinet_id"`
	Trigger   string `json:"trigger"`
}

func NewOzonAIRunTask(cabinetID uuid.UUID, trigger string) (*asynq.Task, error) {
	data, err := json.Marshal(OzonAITaskPayload{CabinetID: cabinetID.String(), Trigger: trigger})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TaskOzonAIRun, data), nil
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
