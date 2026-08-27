package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ozonCampaignSkuWindowDays: the campaign report request window — the last
// 14 days on every sync (matches the «Продажи 14д» UI window); the table
// accumulates history via upserts.
const ozonCampaignSkuWindowDays = 14

// SyncCampaignSkuStats pulls the per-SKU campaign statistics report for one
// cabinet's running SKU / SEARCH_PROMO campaigns and upserts
// ozon_campaign_sku_stats. Shares the one-report-generation-at-a-time
// account budget with the phrases/CPO reports — strictly sequential.
func (s *OzonSyncService) SyncCampaignSkuStats(ctx context.Context, cabinet domain.SellerCabinet) error {
	creds, err := s.credentials(cabinet)
	if err != nil {
		return err
	}
	if !creds.HasPerformanceAPI() {
		return fmt.Errorf("performance credentials missing: campaign sku stats sync disabled")
	}

	refs, err := s.queries.ListOzonSkuCampaignRefs(ctx, uuidToPgtype(cabinet.ID))
	if err != nil {
		return fmt.Errorf("list sku campaigns: %w", err)
	}
	if len(refs) == 0 {
		return nil
	}
	campaignIDs := make([]int64, 0, len(refs))
	for _, ref := range refs {
		campaignIDs = append(campaignIDs, ref.OzonCampaignID)
	}

	dateTo := time.Now().UTC()
	dateFrom := dateTo.AddDate(0, 0, -ozonCampaignSkuWindowDays)
	rows, err := s.perfClient.GetCampaignObjectsReport(ctx, ozonClientCreds(creds), campaignIDs, dateFrom, dateTo)
	if err != nil {
		return fmt.Errorf("statistics (campaign objects): %w", err)
	}

	upserted := 0
	for _, row := range rows {
		if row.CampaignID == 0 || row.SKU == 0 {
			continue
		}
		if err := s.queries.UpsertOzonCampaignSkuStat(ctx, sqlcgen.UpsertOzonCampaignSkuStatParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			OzonCampaignID:  row.CampaignID,
			Sku:             row.SKU,
			Date:            pgtype.Date{Time: row.Date, Valid: true},
			Views:           row.Views,
			Clicks:          row.Clicks,
			SpendRub:        floatToPgNumeric(row.SpendRub),
			Orders:          row.Orders,
			RevenueRub:      floatToPgNumeric(row.RevenueRub),
		}); err != nil {
			return fmt.Errorf("upsert campaign %d sku %d @ %s: %w", row.CampaignID, row.SKU, row.Date.Format("2006-01-02"), err)
		}
		upserted++
	}

	s.logger.Info().
		Str("cabinet_id", cabinet.ID.String()).
		Int("campaigns", len(campaignIDs)).
		Int("rows", upserted).
		Msg("ozon campaign sku stats synced")
	return nil
}

// SyncCampaignSkuStatsAllCabinets runs SyncCampaignSkuStats for every active
// Ozon cabinet sequentially; a failed cabinet never blocks the rest.
func (s *OzonSyncService) SyncCampaignSkuStatsAllCabinets(ctx context.Context) error {
	cabinetIDs, err := s.ListOzonCabinetIDs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, cabinetID := range cabinetIDs {
		cabinet, loadErr := s.loadOzonCabinet(ctx, cabinetID)
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("cabinet %s: %w", cabinetID, loadErr))
			continue
		}
		if syncErr := s.SyncCampaignSkuStats(ctx, *cabinet); syncErr != nil {
			s.logger.Warn().Err(syncErr).Str("cabinet_id", cabinetID.String()).Msg("ozon campaign sku stats sync failed for cabinet")
			errs = append(errs, fmt.Errorf("cabinet %s: %w", cabinetID, syncErr))
		}
	}
	return errors.Join(errs...)
}
