package worker

import (
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

type Runtime struct {
	server    *asynq.Server
	scheduler *asynq.Scheduler
	client    *asynq.Client
	logger    zerolog.Logger
	mux       *asynq.ServeMux
}

func NewRuntime(cfg *config.Config, syncService *service.SyncService, queries *sqlcgen.Queries, engine *service.RecommendationEngine, extendedEngine *service.ExtendedRecommendationEngine, exportGenerator *service.ExportGenerator, notifier *service.NotificationService, integrationRefresher *service.IntegrationRefreshService, bidRunner *service.BidAutomationService, logger zerolog.Logger) (*Runtime, error) {
	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis uri for worker: %w", err)
	}

	client := asynq.NewClient(redisOpt)
	processor := NewProcessor(syncService, queries, engine, exportGenerator, client, logger).WithNotifier(notifier).WithIntegrationRefresher(integrationRefresher).WithBidRunner(bidRunner).WithExtendedEngine(extendedEngine)
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskSyncWorkspace, processor.HandleSyncWorkspace)
	mux.HandleFunc(TaskSyncCampaigns, processor.HandleSyncCampaigns)
	mux.HandleFunc(TaskSyncCampaignStats, processor.HandleSyncCampaignStats)
	mux.HandleFunc(TaskSyncPhrases, processor.HandleSyncPhrases)
	mux.HandleFunc(TaskSyncProducts, processor.HandleSyncProducts)
	mux.HandleFunc(TaskGenerateRecommendations, processor.HandleGenerateRecommendations)
	mux.HandleFunc(TaskGenerateExport, processor.HandleGenerateExport)
	mux.HandleFunc(TaskSweepSyncWorkspace, processor.HandleSweepSyncWorkspace)
	mux.HandleFunc(TaskSweepSyncCampaigns, processor.HandleSweepSyncCampaigns)
	mux.HandleFunc(TaskSweepSyncCampaignStats, processor.HandleSweepSyncCampaignStats)
	mux.HandleFunc(TaskSweepSyncPhrases, processor.HandleSweepSyncPhrases)
	mux.HandleFunc(TaskSweepSyncProducts, processor.HandleSweepSyncProducts)
	mux.HandleFunc(TaskSweepRecommendations, processor.HandleSweepRecommendations)
	mux.HandleFunc(TaskSweepRefreshIntegrations, processor.HandleSweepRefreshIntegrations)
	mux.HandleFunc(TaskBidAutomation, processor.HandleBidAutomation)
	mux.HandleFunc(TaskSweepBidAutomation, processor.HandleSweepBidAutomation)
	mux.HandleFunc(TaskCollectKeywords, processor.HandleCollectKeywords)
	mux.HandleFunc(TaskSweepCollectKeywords, processor.HandleSweepCollectKeywords)
	mux.HandleFunc(TaskExtractCompetitors, processor.HandleExtractCompetitors)
	mux.HandleFunc(TaskSweepExtractCompetitors, processor.HandleSweepExtractCompetitors)
	mux.HandleFunc(TaskCollectDelivery, processor.HandleCollectDelivery)
	mux.HandleFunc(TaskSweepCollectDelivery, processor.HandleSweepCollectDelivery)
	mux.HandleFunc(TaskSEOAnalysis, processor.HandleSEOAnalysis)
	mux.HandleFunc(TaskSweepSEOAnalysis, processor.HandleSweepSEOAnalysis)
	mux.HandleFunc(TaskExtendedRecommendations, processor.HandleExtendedRecommendations)
	mux.HandleFunc(TaskSweepExtendedRecommendations, processor.HandleSweepExtendedRecommendations)

	server := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:     12,
		ShutdownTimeout: 30 * time.Second,
		Queues: map[string]int{
			QueueWBSync:          2,
			QueueWBCampaigns:     3,
			QueueWBCampaignStats: 3,
			QueueWBPhrases:       3,
			QueueWBProducts:      2,
			QueueRecommendations: 4,
			QueueExports:         2,
			QueueBidAutomation:   3,
			QueueSemantics:       2,
			QueueCompetitors:     2,
			QueueDelivery:        2,
			QueueSEO:             2,
		},
	})

	scheduler := asynq.NewScheduler(redisOpt, nil)

	// Full autopilot: register all sweep schedulers.
	// Workspace sync triggers the full pipeline: sync → recommendations → notifications.
	syncInterval := cfg.SyncInterval
	recInterval := cfg.RecommendationInterval
	bidInterval := cfg.BidAutomationInterval

	sweepEntries := []struct {
		cron     string
		taskType string
		queue    string
	}{
		// Integration refresh runs before sync to discover new WB cabinets from Sellico
		{syncInterval, TaskSweepRefreshIntegrations, QueueWBSync},
		// SyncWorkspace does ALL stages (campaigns→stats→phrases→products) — no individual sweeps needed
		{syncInterval, TaskSweepSyncWorkspace, QueueWBSync},
		// Individual sweeps REMOVED — they caused 5x redundant sync (audit CRITICAL #3)
		{recInterval, TaskSweepRecommendations, QueueRecommendations},
		{bidInterval, TaskSweepBidAutomation, QueueBidAutomation},
		{syncInterval, TaskSweepCollectKeywords, QueueSemantics},
		{syncInterval, TaskSweepExtractCompetitors, QueueCompetitors},
		{syncInterval, TaskSweepCollectDelivery, QueueDelivery},
		{recInterval, TaskSweepSEOAnalysis, QueueSEO},
		{recInterval, TaskSweepExtendedRecommendations, QueueRecommendations},
	}
	for _, entry := range sweepEntries {
		if _, err := scheduler.Register(entry.cron, NewSweepTask(entry.taskType), asynq.Queue(entry.queue)); err != nil {
			return nil, fmt.Errorf("register sweep %s: %w", entry.taskType, err)
		}
	}

	logger.Info().
		Str("sync_interval", syncInterval).
		Str("recommendation_interval", recInterval).
		Int("sweep_entries", len(sweepEntries)).
		Msg("autopilot scheduler configured")

	return &Runtime{
		server:    server,
		scheduler: scheduler,
		client:    client,
		logger:    logger,
		mux:       mux,
	}, nil
}

func (r *Runtime) Start() error {
	if err := r.scheduler.Start(); err != nil {
		return err
	}
	if err := r.server.Start(r.mux); err != nil {
		r.scheduler.Shutdown()
		return err
	}
	r.logger.Info().Msg("worker runtime started")
	return nil
}

func (r *Runtime) Shutdown() {
	r.scheduler.Shutdown()
	r.server.Shutdown()
	_ = r.client.Close()
	r.logger.Info().Msg("worker runtime stopped")
}
