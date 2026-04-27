package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/app"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/telegram"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	deps, err := app.NewDependencies(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap worker dependencies: %v\n", err)
		os.Exit(1)
	}
	defer deps.Close()

	wbClient := wb.NewClient(cfg, deps.Logger)
	tgClient := telegram.NewClient()
	sellicoClient := sellico.NewClient(cfg.SellicoAPIBaseURL, cfg.SellicoAPITimeout)
	notificationService := service.NewNotificationService(deps.Queries, tgClient, deps.Logger)
	syncService := service.NewSyncService(deps.Queries, wbClient, []byte(cfg.EncryptionKey), deps.Logger)
	sellerCabinetService := service.NewSellerCabinetService(deps.Queries, []byte(cfg.EncryptionKey), wbClient, sellicoClient)
	// Service-account discovery (financial-dashboard pattern). When neither
	// SELLICO_API_TOKEN nor SELLICO_EMAIL/PASSWORD are set, the manager is
	// still created but reports IsConfigured()==false; the service skips
	// the service-account path silently and only the legacy per-user path
	// runs (which itself is a no-op when no workspace has cached a token).
	sellicoTokenManager := sellico.NewServiceTokenManager(sellicoClient, sellico.ServiceTokenConfig{
		StaticToken: cfg.SellicoServiceToken,
		Email:       cfg.SellicoServiceEmail,
		Password:    cfg.SellicoServicePassword,
	})
	integrationRefreshService := service.NewIntegrationRefreshService(deps.Queries, sellicoClient, sellerCabinetService, []byte(cfg.EncryptionKey), deps.Logger)
	if sellicoTokenManager.IsConfigured() {
		integrationRefreshService = integrationRefreshService.WithServiceAccount(sellicoTokenManager)
		deps.Logger.Info().Msg("sellico service-account discovery enabled")
	} else {
		deps.Logger.Info().Msg("sellico service-account NOT configured; only legacy per-user discovery active")
	}
	recommendationService := service.NewRecommendationService(deps.Queries)
	engine := service.NewRecommendationEngine(deps.Queries, recommendationService, notificationService, deps.Logger)
	extendedEngine := service.NewExtendedRecommendationEngine(deps.Queries, recommendationService, deps.Logger)
	strategyService := service.NewStrategyService(deps.Queries)
	bidEngine := service.NewBidEngine(deps.Logger)
	bidAutomationService := service.NewBidAutomationService(deps.Queries, strategyService, bidEngine, wbClient, []byte(cfg.EncryptionKey), deps.Logger)
	exportGenerator := service.NewExportGenerator(deps.Queries, cfg.ExportStoragePath)
	runtime, err := worker.NewRuntime(cfg, syncService, deps.Queries, engine, extendedEngine, exportGenerator, notificationService, integrationRefreshService, bidAutomationService, deps.Logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap worker runtime: %v\n", err)
		os.Exit(1)
	}

	healthSrv := worker.NewHealthServer(8081, deps.Logger)
	healthSrv.Start()

	if err := runtime.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start worker runtime: %v\n", err)
		os.Exit(1)
	}

	<-ctx.Done()
	runtime.Shutdown()
	healthSrv.Shutdown()
}
