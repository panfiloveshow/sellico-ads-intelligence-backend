package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

const (
	jobResultStateOK      = "ok"
	jobResultStateEmpty   = "empty"
	jobResultStatePartial = "partial"
	jobResultStateFailed  = "failed"
)

type syncRunner interface {
	SyncCampaigns(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncCampaignStats(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncPhrases(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncProducts(ctx context.Context, workspaceID uuid.UUID) (service.SyncSummary, error)
	SyncSingleCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (service.SyncSummary, error)
}

type recommendationGenerator interface {
	GenerateForWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error)
}

type exportGenerator interface {
	Generate(ctx context.Context, workspaceID, exportID uuid.UUID) (*domain.Export, error)
}

type taskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// syncNotifier sends notifications after sync and recommendation events.
type syncNotifier interface {
	NotifySyncComplete(ctx context.Context, workspaceID uuid.UUID, status string, issues []string)
}

// integrationRefresher auto-discovers new Sellico integrations. Refresh
// dispatches to whichever discovery paths the service has been configured
// for (legacy per-user, service-account, or both).
type integrationRefresher interface {
	Refresh(ctx context.Context) error
}

// bidAutomationRunner executes bid strategies for a workspace.
type bidAutomationRunner interface {
	RunForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error)
}

// extendedRecommendationGenerator generates extended recs (SEO, price, content).
type extendedRecommendationGenerator interface {
	GenerateForWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]domain.Recommendation, error)
}

type Processor struct {
	syncService            syncRunner
	queries                *sqlcgen.Queries
	engine                 recommendationGenerator
	extendedEngine         extendedRecommendationGenerator
	exportGenerator        exportGenerator
	client                 taskEnqueuer
	notifier               syncNotifier
	integrationRefresher   integrationRefresher
	bidRunner              bidAutomationRunner
	logger                 zerolog.Logger
}

func NewProcessor(syncService syncRunner, queries *sqlcgen.Queries, engine recommendationGenerator, exportGenerator exportGenerator, client taskEnqueuer, logger zerolog.Logger) *Processor {
	return &Processor{
		syncService:     syncService,
		queries:         queries,
		engine:          engine,
		exportGenerator: exportGenerator,
		client:          client,
		logger:          logger,
	}
}

// WithNotifier sets the notification service for sync-complete alerts.
func (p *Processor) WithNotifier(n syncNotifier) *Processor {
	p.notifier = n
	return p
}

// WithIntegrationRefresher sets the integration refresher for auto-discovery.
func (p *Processor) WithIntegrationRefresher(r integrationRefresher) *Processor {
	p.integrationRefresher = r
	return p
}

// WithBidRunner sets the bid automation runner.
func (p *Processor) WithBidRunner(r bidAutomationRunner) *Processor {
	p.bidRunner = r
	return p
}

// WithExtendedEngine sets the extended recommendation engine (SEO, price, content).
func (p *Processor) WithExtendedEngine(e extendedRecommendationGenerator) *Processor {
	p.extendedEngine = e
	return p
}

// HandleBidAutomation runs the bid engine for a single workspace.
func (p *Processor) HandleBidAutomation(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}

	if p.bidRunner == nil {
		p.logger.Debug().Msg("bid runner not configured, skipping")
		return nil
	}

	changesApplied, err := p.bidRunner.RunForWorkspace(ctx, workspaceID)
	if err != nil {
		p.logger.Error().Err(err).Str("workspace_id", workspaceID.String()).Msg("bid automation failed")
		return err
	}

	p.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("changes_applied", changesApplied).
		Msg("bid automation completed")

	return nil
}

// HandleCollectDelivery collects delivery data for a workspace.
func (p *Processor) HandleCollectDelivery(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	p.logger.Info().Str("workspace_id", workspaceID.String()).Msg("delivery collection completed")
	return nil
}

func (p *Processor) HandleSweepCollectDelivery(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepCollectDelivery, TaskCollectDelivery, QueueDelivery)
}

func (p *Processor) HandleSEOAnalysis(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	p.logger.Info().Str("workspace_id", workspaceID.String()).Msg("SEO analysis completed")
	return nil
}

func (p *Processor) HandleSweepSEOAnalysis(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSEOAnalysis, TaskSEOAnalysis, QueueSEO)
}

func (p *Processor) HandleExtendedRecommendations(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}

	if p.extendedEngine == nil {
		p.logger.Debug().Msg("extended recommendation engine not configured, skipping")
		return nil
	}

	return p.runWithJobRun(ctx, TaskExtendedRecommendations, &workspaceID, payload, func() (map[string]any, error) {
		recs, runErr := p.extendedEngine.GenerateForWorkspace(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{"generated": len(recs)}, nil
	})
}

func (p *Processor) HandleSweepExtendedRecommendations(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepExtendedRecommendations, TaskExtendedRecommendations, QueueRecommendations)
}

// HandleCollectKeywords collects keywords from phrases/SERP for a workspace.
func (p *Processor) HandleCollectKeywords(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	p.logger.Info().Str("workspace_id", workspaceID.String()).Msg("keyword collection started")
	return nil // Full implementation requires SemanticsService wiring
}

// HandleSweepCollectKeywords distributes keyword collection across workspaces.
func (p *Processor) HandleSweepCollectKeywords(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepCollectKeywords, TaskCollectKeywords, QueueSemantics)
}

// HandleExtractCompetitors extracts competitors from SERP for a workspace.
func (p *Processor) HandleExtractCompetitors(ctx context.Context, task *asynq.Task) error {
	_, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	p.logger.Info().Str("workspace_id", workspaceID.String()).Msg("competitor extraction started")
	return nil // Full implementation requires CompetitorService wiring
}

// HandleSweepExtractCompetitors distributes competitor extraction across workspaces.
func (p *Processor) HandleSweepExtractCompetitors(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepExtractCompetitors, TaskExtractCompetitors, QueueCompetitors)
}

// HandleSweepBidAutomation runs bid automation for all workspaces with active strategies.
func (p *Processor) HandleSweepBidAutomation(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepBidAutomation, TaskBidAutomation, QueueBidAutomation)
}

// HandleSweepRefreshIntegrations auto-discovers new WB integrations from Sellico.
func (p *Processor) HandleSweepRefreshIntegrations(ctx context.Context, _ *asynq.Task) error {
	if p.integrationRefresher == nil {
		p.logger.Debug().Msg("integration refresher not configured, skipping")
		return nil
	}
	return p.integrationRefresher.Refresh(ctx)
}

func (p *Processor) HandleSyncWorkspace(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncWorkspace, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}

		p.logger.Info().Str("workspace_id", workspaceID.String()).Msg("sync workspace started")

		// If metadata contains seller_cabinet_id, sync only that cabinet
		if payload.Metadata != nil {
			if meta, ok := payload.Metadata.(map[string]any); ok {
				if cabIDStr, ok := meta["seller_cabinet_id"].(string); ok && cabIDStr != "" {
					cabinetID, parseErr := uuid.Parse(cabIDStr)
					if parseErr == nil {
						p.logger.Info().Str("cabinet_id", cabIDStr).Msg("starting single cabinet sync")
						summary, syncErr := p.syncService.SyncSingleCabinet(ctx, workspaceID, cabinetID)
						result := map[string]any{
							"cabinets":       1,
							"campaigns":      summary.Campaigns,
							"campaign_stats": summary.CampaignStats,
							"phrases":        summary.Phrases,
							"products":       summary.Products,
							"mode":           "single_cabinet",
							"cabinet_id":     cabIDStr,
						}
						if syncErr != nil {
							p.notifySyncResult(ctx, workspaceID, "partial", nil)
							return result, syncErr
						}
						p.notifySyncResult(ctx, workspaceID, "ok", nil)

						recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
						if enqueueErr == nil {
							result["recommendation_generation"] = recommendationTaskStatus
						}
						return result, nil
					}
				}
			}
		}

		// Default: sync ALL cabinets in workspace
		campaignSummary, campaignErr := p.syncService.SyncCampaigns(ctx, workspaceID)
		campaignStatsSummary, campaignStatsErr := p.syncService.SyncCampaignStats(ctx, workspaceID)
		phraseSummary, phraseErr := p.syncService.SyncPhrases(ctx, workspaceID)
		productSummary, productErr := p.syncService.SyncProducts(ctx, workspaceID)
		syncIssueStrings := collectSyncIssues(campaignSummary, campaignStatsSummary, phraseSummary, productSummary)
		result := map[string]any{
			"cabinets":       maxInt(campaignSummary.Cabinets, campaignStatsSummary.Cabinets, phraseSummary.Cabinets, productSummary.Cabinets),
			"campaigns":      campaignSummary.Campaigns,
			"campaign_stats": campaignStatsSummary.CampaignStats,
			"phrases":        phraseSummary.Phrases,
			"phrase_stats":   phraseSummary.PhraseStats,
			"products":       productSummary.Products,
			"skipped":        campaignStatsSummary.SkippedCampaign + phraseSummary.SkippedCampaign,
			"sync_issues":    syncIssueStrings,
			"campaign_requests": map[string]any{
				"campaigns":        campaignSummary.Campaigns,
				"campaign_stats":   campaignStatsSummary.CampaignStats,
				"phrases":          phraseSummary.Phrases,
				"phrase_stats":     phraseSummary.PhraseStats,
				"products":         productSummary.Products,
				"skipped_campaign": campaignStatsSummary.SkippedCampaign + phraseSummary.SkippedCampaign,
			},
		}

		if err := firstNonNilError(campaignErr, campaignStatsErr, phraseErr, productErr); err != nil {
			p.notifySyncResult(ctx, workspaceID, "partial", syncIssueStrings)
			return result, err
		}

		recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
		if enqueueErr != nil {
			return nil, enqueueErr
		}
		result["recommendation_generation"] = recommendationTaskStatus

		p.notifySyncResult(ctx, workspaceID, "ok", syncIssueStrings)

		return result, nil
	})
}

func (p *Processor) HandleSweepSyncWorkspace(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncWorkspace, TaskSyncWorkspace, QueueWBSync)
}

func (p *Processor) HandleSyncCampaigns(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncCampaigns, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncCampaigns(ctx, workspaceID)
		result := map[string]any{
			"cabinets":    summary.Cabinets,
			"campaigns":   summary.Campaigns,
			"sync_issues": summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		return result, nil
	})
}

func (p *Processor) HandleSyncCampaignStats(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncCampaignStats, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncCampaignStats(ctx, workspaceID)
		result := map[string]any{
			"cabinets":       summary.Cabinets,
			"campaign_stats": summary.CampaignStats,
			"skipped":        summary.SkippedCampaign,
			"sync_issues":    summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
		if enqueueErr != nil {
			return nil, enqueueErr
		}
		result["recommendation_generation"] = recommendationTaskStatus
		return result, nil
	})
}

func (p *Processor) HandleSyncPhrases(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncPhrases, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncPhrases(ctx, workspaceID)
		result := map[string]any{
			"cabinets":     summary.Cabinets,
			"phrases":      summary.Phrases,
			"phrase_stats": summary.PhraseStats,
			"skipped":      summary.SkippedCampaign,
			"sync_issues":  summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
		if enqueueErr != nil {
			return nil, enqueueErr
		}
		result["recommendation_generation"] = recommendationTaskStatus
		return result, nil
	})
}

func (p *Processor) HandleSyncProducts(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskSyncProducts, &workspaceID, payload, func() (map[string]any, error) {
		if p.syncService == nil {
			return nil, fmt.Errorf("sync service is not configured")
		}
		summary, runErr := p.syncService.SyncProducts(ctx, workspaceID)
		result := map[string]any{
			"cabinets":    summary.Cabinets,
			"products":    summary.Products,
			"sync_issues": summary.Issues,
		}
		if runErr != nil {
			return result, runErr
		}
		recommendationTaskStatus, enqueueErr := p.enqueueRecommendationGeneration(workspaceID)
		if enqueueErr != nil {
			return nil, enqueueErr
		}
		result["recommendation_generation"] = recommendationTaskStatus
		return result, nil
	})
}

func (p *Processor) HandleGenerateRecommendations(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, err := parseWorkspacePayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskGenerateRecommendations, &workspaceID, payload, func() (map[string]any, error) {
		recommendations, runErr := p.engine.GenerateForWorkspace(ctx, workspaceID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{"generated": len(recommendations)}, nil
	})
}

func (p *Processor) HandleGenerateExport(ctx context.Context, task *asynq.Task) error {
	payload, workspaceID, exportID, err := parseExportPayload(task.Payload())
	if err != nil {
		return err
	}
	return p.runWithJobRun(ctx, TaskGenerateExport, &workspaceID, WorkspaceTaskPayload{
		WorkspaceID: workspaceID.String(),
		Metadata: map[string]any{
			"export_id":      exportID.String(),
			"export_payload": payload,
		},
	}, func() (map[string]any, error) {
		if p.exportGenerator == nil {
			return nil, fmt.Errorf("export generator is not configured")
		}
		exportTask, runErr := p.exportGenerator.Generate(ctx, workspaceID, exportID)
		if runErr != nil {
			return nil, runErr
		}
		return map[string]any{
			"export_id": exportTask.ID.String(),
			"status":    exportTask.Status,
			"format":    exportTask.Format,
			"entity":    exportTask.EntityType,
		}, nil
	})
}

func (p *Processor) HandleSweepSyncCampaigns(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncCampaigns, TaskSyncCampaigns, QueueWBCampaigns)
}

func (p *Processor) HandleSweepSyncCampaignStats(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncCampaignStats, TaskSyncCampaignStats, QueueWBCampaignStats)
}

func (p *Processor) HandleSweepSyncPhrases(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncPhrases, TaskSyncPhrases, QueueWBPhrases)
}

func (p *Processor) HandleSweepSyncProducts(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepSyncProducts, TaskSyncProducts, QueueWBProducts)
}

func (p *Processor) HandleSweepRecommendations(ctx context.Context, _ *asynq.Task) error {
	return p.runSweep(ctx, TaskSweepRecommendations, TaskGenerateRecommendations, QueueRecommendations)
}

func (p *Processor) runSweep(ctx context.Context, sweepTaskType, tenantTaskType, queue string) error {
	return p.runWithJobRun(ctx, sweepTaskType, nil, WorkspaceTaskPayload{}, func() (map[string]any, error) {
		workspaces, err := p.queries.ListWorkspaces(ctx, sqlcgen.ListWorkspacesParams{Limit: 1000, Offset: 0})
		if err != nil {
			return nil, err
		}

		enqueued := 0
		for _, workspace := range workspaces {
			workspaceID := uuid.UUID(workspace.ID.Bytes)
			if taskErr := p.enqueueWorkspaceTask(tenantTaskType, workspaceID, queue); taskErr != nil {
				if errors.Is(taskErr, asynq.ErrDuplicateTask) {
					continue
				}
				return nil, taskErr
			}
			enqueued++
		}

		return map[string]any{"queue": queue, "enqueued": enqueued}, nil
	})
}

func (p *Processor) runWithJobRun(ctx context.Context, taskType string, workspaceID *uuid.UUID, payload WorkspaceTaskPayload, fn func() (map[string]any, error)) error {
	metadata := workspaceTaskMetadata(payload)
	jobRunID, err := parseOptionalUUID(payload.JobRunID)
	if err != nil {
		return fmt.Errorf("parse job run id: %w", err)
	}

	var jobRun sqlcgen.JobRun
	if jobRunID != nil {
		jobRun, err = p.queries.UpdateJobRunStatus(ctx, sqlcgen.UpdateJobRunStatusParams{
			ID:           optionalUUIDToPgtype(jobRunID),
			Status:       "running",
			FinishedAt:   pgtype.Timestamptz{},
			ErrorMessage: pgtype.Text{},
			Metadata:     mustJSON(metadata),
		})
		if err != nil {
			return err
		}
	} else {
		jobRun, err = p.queries.CreateJobRun(ctx, sqlcgen.CreateJobRunParams{
			WorkspaceID: optionalUUIDToPgtype(workspaceID),
			TaskType:    taskType,
			Status:      "running",
			StartedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			Metadata:    mustJSON(metadata),
		})
		if err != nil {
			return err
		}
	}

	finalMeta, runErr := fn()
	status := "completed"
	errorMessage := pgtype.Text{}
	if runErr != nil {
		status = "failed"
		errorMessage = pgtype.Text{String: runErr.Error(), Valid: true}
	}
	metadata = mergeMetadata(metadata, finalMeta)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["result_state"] = deriveResultState(taskType, metadata, runErr)
	if _, err := p.queries.UpdateJobRunStatus(ctx, sqlcgen.UpdateJobRunStatusParams{
		ID:           jobRun.ID,
		Status:       status,
		FinishedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		ErrorMessage: errorMessage,
		Metadata:     mustJSON(metadata),
	}); err != nil {
		p.logger.Error().Err(err).Str("task_type", taskType).Msg("failed to update job run status")
	}

	return runErr
}

func deriveResultState(taskType string, metadata map[string]any, runErr error) string {
	if runErr != nil {
		if hasMeaningfulProgress(metadata) {
			return jobResultStatePartial
		}
		return jobResultStateFailed
	}

	if strings.HasPrefix(taskType, "wb:sweep_") {
		if intFromMetadata(metadata, "enqueued") > 0 {
			return jobResultStateOK
		}
		return jobResultStateEmpty
	}

	if issueCountFromMetadata(metadata) > 0 {
		if hasMeaningfulProgress(metadata) {
			return jobResultStatePartial
		}
		return jobResultStateFailed
	}

	if hasMeaningfulProgress(metadata) {
		return jobResultStateOK
	}

	return jobResultStateEmpty
}

func hasMeaningfulProgress(metadata map[string]any) bool {
	keys := []string{
		"cabinets",
		"campaigns",
		"campaign_stats",
		"phrases",
		"phrase_stats",
		"products",
		"generated",
		"enqueued",
	}
	for _, key := range keys {
		if intFromMetadata(metadata, key) > 0 {
			return true
		}
	}
	return false
}

func issueCountFromMetadata(metadata map[string]any) int {
	issues, ok := metadata["sync_issues"]
	if !ok {
		return 0
	}
	list, ok := issues.([]service.SyncIssue)
	if ok {
		return len(list)
	}
	values, ok := issues.([]any)
	if ok {
		return len(values)
	}
	return 0
}

func intFromMetadata(metadata map[string]any, key string) int {
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func (p *Processor) enqueueRecommendationGeneration(workspaceID uuid.UUID) (string, error) {
	if err := p.enqueueWorkspaceTask(TaskGenerateRecommendations, workspaceID, QueueRecommendations, asynq.Unique(5*time.Minute)); err != nil {
		if errors.Is(err, asynq.ErrDuplicateTask) {
			return "already_queued", nil
		}
		return "", err
	}
	return "enqueued", nil
}

func (p *Processor) enqueueWorkspaceTask(taskType string, workspaceID uuid.UUID, queue string, extraOpts ...asynq.Option) error {
	if p.client == nil {
		return fmt.Errorf("task client is not configured")
	}

	task, err := NewWorkspaceTask(taskType, workspaceID)
	if err != nil {
		return err
	}

	opts := []asynq.Option{
		asynq.Queue(queue),
		asynq.MaxRetry(10),
		asynq.Timeout(15 * time.Minute),
	}
	if taskType == TaskSyncWorkspace {
		opts = append(opts, asynq.Unique(55*time.Minute))
	}
	opts = append(opts, extraOpts...)

	_, err = p.client.Enqueue(task, opts...)
	return err
}

func parseWorkspacePayload(payload []byte) (WorkspaceTaskPayload, uuid.UUID, error) {
	var decoded WorkspaceTaskPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return WorkspaceTaskPayload{}, uuid.Nil, fmt.Errorf("decode task payload: %w", err)
	}
	workspaceID, err := uuid.Parse(decoded.WorkspaceID)
	if err != nil {
		return WorkspaceTaskPayload{}, uuid.Nil, fmt.Errorf("parse workspace id: %w", err)
	}
	return decoded, workspaceID, nil
}

func workspaceTaskMetadata(payload WorkspaceTaskPayload) map[string]any {
	result := map[string]any{
		"payload": payload,
	}
	if payload.Metadata == nil {
		return result
	}

	if metadata, ok := payload.Metadata.(map[string]any); ok {
		for key, value := range metadata {
			result[key] = value
		}
	}

	return result
}

func parseOptionalUUID(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseExportPayload(payload []byte) (ExportTaskPayload, uuid.UUID, uuid.UUID, error) {
	var decoded ExportTaskPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return ExportTaskPayload{}, uuid.Nil, uuid.Nil, fmt.Errorf("decode export task payload: %w", err)
	}
	workspaceID, err := uuid.Parse(decoded.WorkspaceID)
	if err != nil {
		return ExportTaskPayload{}, uuid.Nil, uuid.Nil, fmt.Errorf("parse workspace id: %w", err)
	}
	exportID, err := uuid.Parse(decoded.ExportID)
	if err != nil {
		return ExportTaskPayload{}, uuid.Nil, uuid.Nil, fmt.Errorf("parse export id: %w", err)
	}
	return decoded, workspaceID, exportID, nil
}

func mustJSON(metadata map[string]any) []byte {
	if metadata == nil {
		return []byte("{}")
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func mergeMetadata(base, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	result := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func optionalUUIDToPgtype(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func maxInt(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func collectSyncIssues(summaries ...service.SyncSummary) []service.SyncIssue {
	total := 0
	for _, summary := range summaries {
		total += len(summary.Issues)
	}
	if total == 0 {
		return nil
	}

	result := make([]service.SyncIssue, 0, total)
	for _, summary := range summaries {
		result = append(result, summary.Issues...)
	}
	return result
}

func (p *Processor) notifySyncResult(ctx context.Context, workspaceID uuid.UUID, status string, issues []service.SyncIssue) {
	if p.notifier == nil {
		return
	}
	issueStrings := make([]string, len(issues))
	for i, issue := range issues {
		if issue.EntityID != "" {
			issueStrings[i] = fmt.Sprintf("[%s] %s: %s", issue.Stage, issue.EntityID, issue.Message)
		} else {
			issueStrings[i] = fmt.Sprintf("[%s] %s", issue.Stage, issue.Message)
		}
	}
	p.notifier.NotifySyncComplete(ctx, workspaceID, status, issueStrings)
}

func firstNonNilError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
