package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/config"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	queries := sqlcgen.New(pool)
	logger := zerolog.Nop()
	wbClient := wb.NewClient(cfg, logger)

	cabinetID := uuid.MustParse("2b2b5759-251e-49b1-a823-900f09d3b770")
	campaignID := uuid.MustParse("cbf2813e-f496-49df-a325-3e211db91106")
	wbCampaignID := 27303278

	cabinet, err := queries.GetSellerCabinetByID(ctx, pgtype.UUID{Bytes: cabinetID, Valid: true})
	if err != nil {
		panic(err)
	}
	token, err := crypto.Decrypt(cabinet.EncryptedToken, []byte(cfg.EncryptionKey))
	if err != nil {
		panic(err)
	}

	dateFrom := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	dateTo := time.Now().UTC().Format("2006-01-02")
	stats, err := wbClient.GetCampaignStats(ctx, token, []int{wbCampaignID}, dateFrom, dateTo)
	if err != nil {
		panic(err)
	}

	var totalImpressions int64
	var totalClicks int64
	var totalSpend int64
	var totalOrders int64
	var totalRevenue int64

	for _, statDTO := range stats {
		stat, mapErr := wb.MapCampaignStatDTO(statDTO, campaignID)
		if mapErr != nil {
			panic(mapErr)
		}
		if _, upsertErr := queries.UpsertCampaignStat(ctx, sqlcgen.UpsertCampaignStatParams{
			CampaignID:  pgtype.UUID{Bytes: campaignID, Valid: true},
			Date:        pgtype.Date{Time: stat.Date, Valid: true},
			Impressions: stat.Impressions,
			Clicks:      stat.Clicks,
			Spend:       stat.Spend,
			Orders:      int64PtrToPgInt8(stat.Orders),
			Revenue:     int64PtrToPgInt8(stat.Revenue),
		}); upsertErr != nil {
			panic(upsertErr)
		}
		totalImpressions += stat.Impressions
		totalClicks += stat.Clicks
		totalSpend += stat.Spend
		if stat.Orders != nil {
			totalOrders += *stat.Orders
		}
		if stat.Revenue != nil {
			totalRevenue += *stat.Revenue
		}
	}

	fmt.Printf("campaign=%d days=%d impressions=%d clicks=%d spend=%d orders=%d revenue=%d\n", wbCampaignID, len(stats), totalImpressions, totalClicks, totalSpend, totalOrders, totalRevenue)
}

func int64PtrToPgInt8(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}
