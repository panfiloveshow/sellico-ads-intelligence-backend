package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/app"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	emailclient "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/email"
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
	mailClient := emailclient.NewClient(emailclient.Config{
		Host:      cfg.SMTPHost,
		Port:      cfg.SMTPPort,
		Username:  cfg.SMTPUsername,
		Password:  cfg.SMTPPassword,
		FromEmail: cfg.SMTPFromEmail,
		FromName:  cfg.SMTPFromName,
		Timeout:   cfg.SMTPTimeout,
	})
	sellicoClient := sellico.NewClient(cfg.SellicoAPIBaseURL, cfg.SellicoAPITimeout)
	notificationService := service.NewNotificationService(deps.Queries, tgClient, deps.Logger, service.WithNotificationEmailSender(mailClient))
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
	productsClient := sellico.NewClient(cfg.ProductsAPIBaseURL, cfg.SellicoAPITimeout)
	var unitEconomicsReadinessProvider service.UnitEconomicsReadinessProvider
	if cfg.ProductsAPIBaseURL != "" && cfg.ProductsAPIToken != "" && cfg.ProductsUnitEconomicsReadinessPath != "" {
		unitEconomicsReadinessProvider = service.NewProductsUnitEconomicsReadinessProvider(
			deps.Queries, productsClient, cfg.ProductsAPIToken,
			cfg.ProductsUnitEconomicsReadinessPath, cfg.ProductsUnitEconomicsMaxAge,
		)
	} else if sellicoTokenManager.IsConfigured() && cfg.SellicoUnitEconomicsReadinessPath != "" {
		unitEconomicsReadinessProvider = service.NewSellicoUnitEconomicsReadinessProvider(sellicoClient, sellicoTokenManager, cfg.SellicoUnitEconomicsReadinessPath, deps.Logger)
	}
	unitEconomicsReadinessConfigured := unitEconomicsReadinessProvider != nil
	adsReadService := service.NewAdsReadService(deps.Queries, wbClient, []byte(cfg.EncryptionKey), deps.Logger,
		service.WithAdsReadLimits(cfg.AdsReadEntityLimit, cfg.AdsReadStatsLimit),
		service.WithAdsReadBackendVersion(cfg.AppVersion),
		service.WithAdsReadUnitEconomicsConfigured(unitEconomicsReadinessConfigured),
	)
	integrationRefreshService := service.NewIntegrationRefreshService(deps.Queries, sellicoClient, sellerCabinetService, []byte(cfg.EncryptionKey), deps.Logger)
	if sellicoTokenManager.IsConfigured() {
		integrationRefreshService = integrationRefreshService.WithServiceAccount(sellicoTokenManager)
		deps.Logger.Info().Msg("sellico service-account discovery enabled")
	} else {
		deps.Logger.Info().Msg("sellico service-account NOT configured; only legacy per-user discovery active")
	}
	recommendationService := service.NewRecommendationService(deps.Queries)
	engine := service.NewRecommendationEngine(deps.Queries, recommendationService, notificationService, deps.Logger).
		WithAdsReadInsights(adsReadService)
	extendedEngine := service.NewExtendedRecommendationEngine(deps.Queries, recommendationService, deps.Logger)
	semanticsService := service.NewSemanticsService(deps.Queries, deps.Logger)
	competitorService := service.NewCompetitorService(deps.Queries, deps.Logger)
	deliveryService := service.NewDeliveryService(deps.Queries, wbClient, deps.Logger)
	seoAnalyzerService := service.NewSEOAnalyzerService(deps.Queries, semanticsService, deps.Logger)
	strategyService := service.NewStrategyService(deps.Queries)
	bidEngine := service.NewBidEngine(deps.Logger)
	bidAutomationOpts := []service.BidAutomationOption{}
	if unitEconomicsReadinessConfigured {
		bidAutomationOpts = append(bidAutomationOpts, service.WithUnitEconomicsReadinessProvider(
			unitEconomicsReadinessProvider,
		))
		deps.Logger.Info().Str("path", cfg.ProductsUnitEconomicsReadinessPath).Msg("unit-economics readiness enabled for bid automation")
	} else {
		deps.Logger.Info().Msg("sellico unit-economics readiness NOT configured; bid automation will not increase bids")
	}
	bidAutomationService := service.NewBidAutomationService(deps.Queries, strategyService, bidEngine, wbClient, []byte(cfg.EncryptionKey), deps.Logger, bidAutomationOpts...)
	repricerService := service.NewRepricerService(deps.Queries, wbClient, []byte(cfg.EncryptionKey), deps.Logger,
		service.WithRepricerStrategyService(strategyService),
		service.WithRepricerEngine(service.NewPriceEngine(deps.Logger)),
		service.WithRepricerNotifications(notificationService),
	)
	exportGenerator := service.NewExportGenerator(deps.Queries, cfg.ExportStoragePath)
	// products-backend → product_economics cost bridge (feeds the margin-floor
	// strategy). Separate host + shared service token, not the CRM token flow.
	var economicsSyncService *service.SellicoEconomicsSyncService
	if cfg.ProductsAPIBaseURL != "" && cfg.ProductsAPIToken != "" && cfg.SellicoUnitEconomicsExportPath != "" {
		economicsSyncService = service.NewSellicoEconomicsSyncService(
			deps.Queries, productsClient, cfg.ProductsAPIToken,
			service.NewProductEconomicsService(deps.Queries),
			cfg.SellicoUnitEconomicsExportPath, deps.Logger,
		)
		deps.Logger.Info().Str("base", cfg.ProductsAPIBaseURL).Str("path", cfg.SellicoUnitEconomicsExportPath).Msg("products economics bridge enabled for repricer")
	}
	runtime, err := worker.NewRuntime(cfg, syncService, deps.Queries, engine, extendedEngine, exportGenerator, notificationService, integrationRefreshService, bidAutomationService, repricerService, economicsSyncService, semanticsService, competitorService, deliveryService, seoAnalyzerService, adsReadService, recommendationService, deps.Logger)
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
