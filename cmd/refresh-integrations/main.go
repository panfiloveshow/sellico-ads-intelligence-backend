package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/app"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := config.Load()
	deps, err := app.NewDependencies(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap dependencies: %v\n", err)
		os.Exit(1)
	}
	defer deps.Close()

	wbClient := wb.NewClient(cfg, deps.Logger)
	sellicoClient := sellico.NewClient(cfg.SellicoAPIBaseURL, cfg.SellicoAPITimeout)
	sellerCabinetService := service.NewSellerCabinetService(deps.Queries, []byte(cfg.EncryptionKey), wbClient, sellicoClient)
	tokenManager := sellico.NewServiceTokenManager(sellicoClient, sellico.ServiceTokenConfig{
		StaticToken: cfg.SellicoServiceToken,
		Email:       cfg.SellicoServiceEmail,
		Password:    cfg.SellicoServicePassword,
	})
	refreshService := service.NewIntegrationRefreshService(deps.Queries, sellicoClient, sellerCabinetService, []byte(cfg.EncryptionKey), deps.Logger)
	if tokenManager.IsConfigured() {
		refreshService = refreshService.WithServiceAccount(tokenManager)
	}

	if err := refreshService.Refresh(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "refresh integrations: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("refresh integrations completed")
}
