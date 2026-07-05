package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/metrics"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

type Runtime struct {
	server         *asynq.Server
	repricerServer *asynq.Server
	scheduler      *asynq.Scheduler
	client        *asynq.Client
	inspector     *asynq.Inspector
	queries       *sqlcgen.Queries
	queues        []string
	metricsCancel context.CancelFunc
	logger        zerolog.Logger
	mux           *asynq.ServeMux
}

func NewRuntime(cfg *config.Config, syncService *service.SyncService, queries *sqlcgen.Queries, engine *service.RecommendationEngine, extendedEngine *service.ExtendedRecommendationEngine, exportGenerator *service.ExportGenerator, notifier *service.NotificationService, integrationRefresher *service.IntegrationRefreshService, bidRunner *service.BidAutomationService, repricer *service.RepricerService, semantics *service.SemanticsService, competitors *service.CompetitorService, delivery *service.DeliveryService, seo *service.SEOAnalyzerService, adsRead *service.AdsReadService, recommendations *service.RecommendationService, logger zerolog.Logger) (*Runtime, error) {
	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis uri for worker: %w", err)
	}

	client := asynq.NewClient(redisOpt)
	processor := NewProcessor(syncService, queries, engine, exportGenerator, client, logger).
		WithNotifier(notifier).
		WithIntegrationRefresher(integrationRefresher).
		WithBidRunner(bidRunner).
		WithRepricer(repricer).
		WithSemanticsCollector(semantics).
		WithCompetitorExtractor(competitors).
		WithDeliveryCollector(delivery).
		WithSEOAnalyzer(seo).
		WithExtendedEngine(extendedEngine).
		WithReportDependencies(adsRead, recommendations, notifier)
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
	mux.HandleFunc(TaskRepricer, processor.HandleRepricer)
	mux.HandleFunc(TaskSweepRepricer, processor.HandleSweepRepricer)
	mux.HandleFunc(TaskPollPriceTasks, processor.HandlePollPriceTasks)
	mux.HandleFunc(TaskSweepPollPriceTasks, processor.HandleSweepPollPriceTasks)
	mux.HandleFunc(TaskExecutePriceSchedule, processor.HandleExecutePriceSchedules)
	mux.HandleFunc(TaskSyncPrices, processor.HandleSyncPrices)
	mux.HandleFunc(TaskSweepSyncPrices, processor.HandleSweepSyncPrices)
	mux.HandleFunc(TaskSweepRepricerDigest, processor.HandleSweepRepricerDigest)
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
	mux.HandleFunc(TaskSendClientAuditReport, processor.HandleSendClientAuditReport)
	mux.HandleFunc(TaskSweepClientAuditReports, processor.HandleSweepClientAuditReports)

	// WB queues are all weight 1 and the server runs with Concurrency 1 so WB
	// jobs execute sequentially — WB API rate limits reject parallel calls.
	queueWeights := map[string]int{
		QueueWBSync:          1,
		QueueWBCampaigns:     1,
		QueueWBCampaignStats: 1,
		QueueWBPhrases:       1,
		QueueWBProducts:      1,
		QueueRecommendations: 4,
		QueueExports:         2,
		QueueBidAutomation:   3,
		QueueSemantics:       2,
		QueueCompetitors:     2,
		QueueDelivery:        2,
		QueueSEO:             2,
	}

	server := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:     1,
		ShutdownTimeout: 30 * time.Second,
		Queues:          queueWeights,
		StrictPriority:  true,
	})

	// Dedicated server for the repricer queue with its own slot, so the
	// user-interactive sync/bulk/run never waits behind a long-running WB-sync
	// task (which holds the main server's single slot for minutes). Concurrency 1
	// here too — the repricer's own WB rate-limit guard serializes its calls.
	repricerServer := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:     1,
		ShutdownTimeout: 30 * time.Second,
		Queues:          map[string]int{QueueRepricer: 1},
	})

	scheduler := asynq.NewScheduler(redisOpt, nil)
	inspector := asynq.NewInspector(redisOpt)

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
		{cfg.RepricerInterval, TaskSweepSyncPrices, QueueRepricer},
		// Strategy engine on the top of the hour so demand-based (peak-hours)
		// pricing lands the right price at the hour boundary, not a boot-offset.
		{"@hourly", TaskSweepRepricer, QueueRepricer},
		// Daily digest at 06:00 UTC (09:00 MSK).
		{"0 6 * * *", TaskSweepRepricerDigest, QueueRepricer},
		{cfg.RepricerPollInterval, TaskSweepPollPriceTasks, QueueRepricer},
		{cfg.RepricerScheduleInterval, TaskExecutePriceSchedule, QueueRepricer},
		{syncInterval, TaskSweepCollectKeywords, QueueSemantics},
		{syncInterval, TaskSweepExtractCompetitors, QueueCompetitors},
		{syncInterval, TaskSweepCollectDelivery, QueueDelivery},
		{recInterval, TaskSweepSEOAnalysis, QueueSEO},
		{recInterval, TaskSweepExtendedRecommendations, QueueRecommendations},
		{syncInterval, TaskSweepClientAuditReports, QueueRecommendations},
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
		server:         server,
		repricerServer: repricerServer,
		scheduler:      scheduler,
		client:         client,
		inspector:      inspector,
		queries:        queries,
		queues:         append(queueNames(queueWeights), QueueRepricer),
		logger:         logger,
		mux:       mux,
	}, nil
}

func (r *Runtime) Start() error {
	metricsCtx, cancel := context.WithCancel(context.Background())
	r.metricsCancel = cancel
	go r.recordQueueMetrics(metricsCtx, 30*time.Second)

	if r.queries != nil {
		expired, err := r.queries.ExpireStaleJobRuns(metricsCtx, time.Now().UTC().Add(-workspaceSyncTaskTimeout))
		if err != nil {
			r.logger.Warn().Err(err).Msg("failed to expire stale job runs")
		} else if expired > 0 {
			r.logger.Warn().Int64("expired", expired).Msg("expired stale job runs on worker startup")
		}
	}

	if err := r.scheduler.Start(); err != nil {
		cancel()
		return err
	}
	if err := r.server.Start(r.mux); err != nil {
		cancel()
		r.scheduler.Shutdown()
		return err
	}
	if err := r.repricerServer.Start(r.mux); err != nil {
		cancel()
		r.scheduler.Shutdown()
		r.server.Shutdown()
		return err
	}
	r.logger.Info().Msg("worker runtime started")

	// Kick off one catalog/price sync a few seconds after boot so the repricer
	// view is populated without waiting for the first scheduled sweep — after a
	// restart that sweep is a full REPRICER_INTERVAL away. Unique guards against
	// piling up if the scheduler fires around the same time.
	if _, err := r.client.Enqueue(NewSweepTask(TaskSweepSyncPrices),
		asynq.Queue(QueueRepricer), asynq.ProcessIn(15*time.Second), asynq.Unique(30*time.Minute)); err != nil {
		r.logger.Warn().Err(err).Msg("failed to enqueue startup price sync")
	}
	return nil
}

func (r *Runtime) Shutdown() {
	if r.metricsCancel != nil {
		r.metricsCancel()
	}
	r.scheduler.Shutdown()
	r.server.Shutdown()
	r.repricerServer.Shutdown()
	if r.inspector != nil {
		_ = r.inspector.Close()
	}
	_ = r.client.Close()
	r.logger.Info().Msg("worker runtime stopped")
}

func (r *Runtime) recordQueueMetrics(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.collectQueueMetrics()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.collectQueueMetrics()
		}
	}
}

func (r *Runtime) collectQueueMetrics() {
	if r.inspector == nil {
		return
	}
	for _, queue := range r.queues {
		info, err := r.inspector.GetQueueInfo(queue)
		if err != nil {
			r.logger.Warn().Err(err).Str("queue", queue).Msg("failed to collect asynq queue metrics")
			continue
		}
		metrics.WorkerQueueLength.WithLabelValues(queue, "pending").Set(float64(info.Pending))
		metrics.WorkerQueueLength.WithLabelValues(queue, "active").Set(float64(info.Active))
		metrics.WorkerQueueLength.WithLabelValues(queue, "scheduled").Set(float64(info.Scheduled))
		metrics.WorkerQueueLength.WithLabelValues(queue, "retry").Set(float64(info.Retry))
		metrics.WorkerQueueLength.WithLabelValues(queue, "archived").Set(float64(info.Archived))
		metrics.WorkerQueueLength.WithLabelValues(queue, "completed").Set(float64(info.Completed))
		metrics.WorkerQueueLength.WithLabelValues(queue, "aggregating").Set(float64(info.Aggregating))
	}
}

func queueNames(weights map[string]int) []string {
	queues := make([]string, 0, len(weights))
	for queue := range weights {
		queues = append(queues, queue)
	}
	return queues
}
