package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/app"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
	embeddedopenapi "github.com/panfiloveshow/sellico-ads-intelligence-backend/openapi"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	deps, err := app.NewDependencies(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap api dependencies: %v\n", err)
		os.Exit(1)
	}
	defer deps.Close()

	wbClient := wb.NewClient(cfg, deps.Logger)
	asynqRedisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse api redis uri: %v\n", err)
		os.Exit(1)
	}
	asynqClient := asynq.NewClient(asynqRedisOpt)
	defer asynqClient.Close()

	sellicoClient := sellico.NewClient(cfg.SellicoAPIBaseURL, cfg.SellicoAPITimeout)
	authService := service.NewAuthService(deps.Queries, cfg.JWTSecret, cfg.JWTAccessTokenTTL, cfg.JWTRefreshTokenTTL)
	workspaceService := service.NewWorkspaceService(deps.Queries)
	// sellicoBridgeService is constructed but unused while local-auth mode is active.
	// Keeping the assignment commented documents the wiring point for the SSO option.
	// _ = service.NewSellicoBridgeService(deps.Queries, sellicoClient, []byte(cfg.EncryptionKey))
	_ = sellicoClient
	sellerCabinetService := service.NewSellerCabinetService(deps.Queries, []byte(cfg.EncryptionKey), wbClient, sellicoClient)
	adsReadService := service.NewAdsReadService(deps.Queries, wbClient, []byte(cfg.EncryptionKey), deps.Logger,
		service.WithAdsReadLimits(cfg.AdsReadEntityLimit, cfg.AdsReadStatsLimit),
	)
	syncJobService := service.NewSyncJobService(deps.Queries, workspaceSyncEnqueuerFunc(func(workspaceID uuid.UUID, jobRunID *uuid.UUID, metadata map[string]any) (string, error) {
		task, taskErr := worker.NewWorkspaceTaskWithMetadata(worker.TaskSyncWorkspace, workspaceID, jobRunID, metadata)
		if taskErr != nil {
			return "", taskErr
		}
		_, taskErr = asynqClient.Enqueue(task, asynq.Queue(worker.QueueWBSync), asynq.MaxRetry(10), asynq.Timeout(30*time.Minute), asynq.Unique(55*time.Minute))
		if errors.Is(taskErr, asynq.ErrDuplicateTask) {
			return "already_queued", nil
		}
		if taskErr == nil {
			return "enqueued", nil
		}
		return "", taskErr
	}))
	campaignService := service.NewCampaignService(deps.Queries)
	phraseService := service.NewPhraseService(deps.Queries)
	recommendationEnqueuer := recommendationEnqueuerFunc(func(workspaceID uuid.UUID) (string, error) {
		task, taskErr := worker.NewWorkspaceTask(worker.TaskGenerateRecommendations, workspaceID)
		if taskErr != nil {
			return "", taskErr
		}
		_, taskErr = asynqClient.Enqueue(task, asynq.Queue(worker.QueueRecommendations), asynq.MaxRetry(10), asynq.Timeout(30*time.Minute), asynq.Unique(5*time.Minute))
		if taskErr == nil {
			return "enqueued", nil
		}
		if errors.Is(taskErr, asynq.ErrDuplicateTask) {
			return "already_queued", nil
		}
		return "", taskErr
	})
	bidService := service.NewBidService(deps.Queries, recommendationEnqueuer, deps.Logger)
	productService := service.NewProductService(deps.Queries)
	positionService := service.NewPositionService(deps.Queries, recommendationEnqueuer, deps.Logger)
	serpService := service.NewSERPService(deps.Queries, deps.DB, recommendationEnqueuer, deps.Logger)
	recommendationService := service.NewRecommendationService(deps.Queries)
	recommendationJobService := service.NewRecommendationJobService(deps.Queries, recommendationEnqueuer)
	exportService := service.NewExportService(deps.Queries, cfg.ExportStoragePath, exportEnqueuerFunc(func(workspaceID, exportID uuid.UUID) error {
		task, taskErr := worker.NewExportTask(workspaceID, exportID)
		if taskErr != nil {
			return taskErr
		}
		_, taskErr = asynqClient.Enqueue(task, asynq.Queue(worker.QueueExports), asynq.MaxRetry(10), asynq.Timeout(30*time.Minute))
		return taskErr
	}))
	countService := service.NewCountService(deps.Queries)
	strategyService := service.NewStrategyService(deps.Queries)
	campaignActionService := service.NewCampaignActionService(deps.Queries, wbClient, []byte(cfg.EncryptionKey), deps.Logger)
	campaignPhraseService := service.NewCampaignPhraseService(deps.Queries)
	semanticsService := service.NewSemanticsService(deps.Queries, deps.Logger)
	competitorService := service.NewCompetitorService(deps.Queries, deps.Logger)
	seoAnalyzerService := service.NewSEOAnalyzerService(deps.Queries, semanticsService, deps.Logger)
	deliveryService := service.NewDeliveryService(deps.Queries, wbClient, deps.Logger)
	productEventService := service.NewProductEventService(deps.Queries, deps.Logger)
	eventBroker := service.NewEventBroker()
	workspaceSettingsService := service.NewWorkspaceSettingsService(deps.Queries)
	extensionService := service.NewExtensionService(deps.Queries, cfg.AppVersion)
	auditLogService := service.NewAuditLogService(deps.Queries)
	jobRunService := service.NewJobRunService(deps.Queries, jobRunRetryEnqueuerFunc{
		enqueueWorkspaceTaskFn: func(taskType string, workspaceID uuid.UUID) (string, error) {
			task, taskErr := worker.NewWorkspaceTask(taskType, workspaceID)
			if taskErr != nil {
				return "", taskErr
			}

			queue := worker.QueueWBSync
			opts := []asynq.Option{asynq.MaxRetry(10), asynq.Timeout(30 * time.Minute)}
			switch taskType {
			case worker.TaskSyncWorkspace:
				queue = worker.QueueWBSync
				opts = append(opts, asynq.Unique(5*time.Minute))
			case worker.TaskSyncCampaigns:
				queue = worker.QueueWBCampaigns
			case worker.TaskSyncCampaignStats:
				queue = worker.QueueWBCampaignStats
			case worker.TaskSyncPhrases:
				queue = worker.QueueWBPhrases
			case worker.TaskSyncProducts:
				queue = worker.QueueWBProducts
			case worker.TaskGenerateRecommendations:
				queue = worker.QueueRecommendations
				opts = append(opts, asynq.Unique(5*time.Minute))
			default:
				return "", fmt.Errorf("unsupported retry task type: %s", taskType)
			}

			opts = append([]asynq.Option{asynq.Queue(queue)}, opts...)
			_, taskErr = asynqClient.Enqueue(task, opts...)
			if taskErr == nil {
				return "enqueued", nil
			}
			if errors.Is(taskErr, asynq.ErrDuplicateTask) {
				return "already_queued", nil
			}
			return "", taskErr
		},
		enqueueExportTaskFn: func(workspaceID, exportID uuid.UUID) (string, error) {
			task, taskErr := worker.NewExportTask(workspaceID, exportID)
			if taskErr != nil {
				return "", taskErr
			}
			_, taskErr = asynqClient.Enqueue(task, asynq.Queue(worker.QueueExports), asynq.MaxRetry(10), asynq.Timeout(30*time.Minute))
			if taskErr != nil {
				return "", taskErr
			}
			return "enqueued", nil
		},
	})

	router := transport.NewRouter(transport.RouterDeps{
		CORSAllowOrigins: cfg.CORSAllowOrigins,
		RateLimit: transport.RateLimitOpts{
			RequestsPerSecond: cfg.RateLimitRPS,
			Burst:             cfg.RateLimitBurst,
		},
		JWTSecret:         cfg.JWTSecret,
		MembershipChecker: workspaceService,
		// Local-auth mode: leaving WorkspaceResolver and Authenticator nil makes
		// router.go fall back to middleware.Auth (local JWT validation) and
		// middleware.TenantScope (local workspace_members lookup) instead of
		// the Sellico SSO bridge. The bridge still exists in the binary and is
		// used by the worker for token caching; only the HTTP auth path is local.
		// To re-enable Sellico SSO, restore the two assignments below.
		// WorkspaceResolver: sellicoBridgeService,
		// Authenticator:     sellicoBridgeService,
		DocsHandler:           handler.NewDocsHandler(embeddedopenapi.Spec),
		HealthHandler:         handler.NewHealthHandler(deps.Ready),
		AuthHandler:           handler.NewAuthHandler(authService),
		WorkspaceHandler:      handler.NewWorkspaceHandler(workspaceService),
		SellerCabinetHandler:  handler.NewSellerCabinetHandler(sellerCabinetSyncFacade{sellerCabinetService, syncJobService}),
		AdsReadHandler:        handler.NewAdsReadHandler(adsReadService),
		CampaignHandler:       handler.NewCampaignHandler(campaignService).WithCounter(countService),
		PhraseHandler:         handler.NewPhraseHandler(phraseService),
		BidHandler:            handler.NewBidHandler(bidService),
		ProductHandler:        handler.NewProductHandler(productService).WithCounter(countService),
		PositionHandler:       handler.NewPositionHandler(positionService),
		SERPHandler:           handler.NewSERPHandler(serpService),
		RecommendationHandler: handler.NewRecommendationHandler(recommendationTriggerFacade{recommendationService, recommendationJobService}).WithCounter(countService),
		ExportHandler:         handler.NewExportHandler(exportService).WithCounter(countService),
		ExtensionHandler:      handler.NewExtensionHandler(extensionService),
		AuditLogHandler:          handler.NewAuditLogHandler(auditLogService),
		JobRunHandler:            handler.NewJobRunHandler(jobRunService),
		EventsHandler:            handler.NewEventsHandler(eventBroker),
		WorkspaceSettingsHandler: handler.NewWorkspaceSettingsHandler(workspaceSettingsService),
		StrategyHandler:          handler.NewStrategyHandler(strategyService),
		CampaignActionHandler:    handler.NewCampaignActionHandler(campaignActionService, campaignPhraseService),
		SemanticsHandler:         handler.NewSemanticsHandler(semanticsService),
		CompetitorHandler:        handler.NewCompetitorHandler(competitorService),
		DeliveryHandler:          handler.NewDeliveryHandler(deliveryService),
		SEOHandler:               handler.NewSEOHandler(seoAnalyzerService),
		ProductEventHandler:      handler.NewProductEventHandler(productEventService),
	})


	addr := fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.ServerPort)
	server := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		deps.Logger.Info().Str("addr", addr).Msg("starting api server")
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			deps.Logger.Error().Err(err).Msg("api shutdown failed")
			os.Exit(1)
		}
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			deps.Logger.Error().Err(err).Msg("api server stopped unexpectedly")
			os.Exit(1)
		}
	}
}

type exportEnqueuerFunc func(workspaceID, exportID uuid.UUID) error

func (f exportEnqueuerFunc) EnqueueExport(workspaceID, exportID uuid.UUID) error {
	return f(workspaceID, exportID)
}

type workspaceSyncEnqueuerFunc func(workspaceID uuid.UUID, jobRunID *uuid.UUID, metadata map[string]any) (string, error)

func (f workspaceSyncEnqueuerFunc) EnqueueWorkspaceSync(workspaceID uuid.UUID, jobRunID *uuid.UUID, metadata map[string]any) (string, error) {
	return f(workspaceID, jobRunID, metadata)
}

type sellerCabinetSyncFacade struct {
	*service.SellerCabinetService
	*service.SyncJobService
}

func (f sellerCabinetSyncFacade) TriggerSellerCabinetSync(ctx context.Context, actorID uuid.UUID, token, workspaceRef string, workspaceID uuid.UUID, cabinetRef string) (*service.SyncTriggerResult, error) {
	cabinetID, err := f.SellerCabinetService.ResolveCabinetID(ctx, token, workspaceRef, workspaceID, cabinetRef)
	if err != nil {
		return nil, err
	}

	return f.SyncJobService.TriggerSellerCabinetSync(ctx, actorID, workspaceID, cabinetID)
}

type recommendationEnqueuerFunc func(workspaceID uuid.UUID) (string, error)

func (f recommendationEnqueuerFunc) EnqueueRecommendationGeneration(workspaceID uuid.UUID) (string, error) {
	return f(workspaceID)
}

type recommendationTriggerFacade struct {
	*service.RecommendationService
	*service.RecommendationJobService
}

type jobRunRetryEnqueuerFunc struct {
	enqueueWorkspaceTaskFn func(taskType string, workspaceID uuid.UUID) (string, error)
	enqueueExportTaskFn    func(workspaceID, exportID uuid.UUID) (string, error)
}

func (f jobRunRetryEnqueuerFunc) EnqueueWorkspaceTask(taskType string, workspaceID uuid.UUID) (string, error) {
	return f.enqueueWorkspaceTaskFn(taskType, workspaceID)
}

func (f jobRunRetryEnqueuerFunc) EnqueueExportTask(workspaceID, exportID uuid.UUID) (string, error) {
	return f.enqueueExportTaskFn(workspaceID, exportID)
}
