package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ozonPhrasesWindowDays is the phrases report request window: the last 7 days
// on every sync; the table accumulates history via upserts.
const ozonPhrasesWindowDays = 7

// SyncPhrases pulls the search-query (phrases) report for one cabinet's
// running SKU / SEARCH_PROMO campaigns and upserts ozon_search_queries.
// The Performance API generates one report per account at a time, so the
// client runs chunks (≤10 campaigns) strictly sequentially.
func (s *OzonSyncService) SyncPhrases(ctx context.Context, cabinet domain.SellerCabinet) error {
	creds, err := s.credentials(cabinet)
	if err != nil {
		return err
	}
	if !creds.HasPerformanceAPI() {
		return fmt.Errorf("performance credentials missing: phrases sync disabled")
	}

	refs, err := s.queries.ListOzonPhraseCampaignRefs(ctx, uuidToPgtype(cabinet.ID))
	if err != nil {
		return fmt.Errorf("list phrase campaigns: %w", err)
	}
	if len(refs) == 0 {
		return nil // no running SKU/SEARCH_PROMO campaigns — nothing to ask for
	}
	campaignIDs := make([]int64, 0, len(refs))
	for _, ref := range refs {
		campaignIDs = append(campaignIDs, ref.OzonCampaignID)
	}

	dateTo := time.Now().UTC()
	dateFrom := dateTo.AddDate(0, 0, -ozonPhrasesWindowDays)
	rows, err := s.perfClient.GetPhrasesReport(ctx, ozonClientCreds(creds), campaignIDs, dateFrom, dateTo)
	if err != nil {
		return fmt.Errorf("statistics/phrases: %w", err)
	}

	upserted := 0
	for _, row := range rows {
		if row.Query == "" {
			continue
		}
		var position pgtype.Numeric
		if row.AvgPosition != nil {
			position = floatToPgNumeric(*row.AvgPosition)
		}
		if err := s.queries.UpsertOzonSearchQuery(ctx, sqlcgen.UpsertOzonSearchQueryParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			Sku:             row.SKU,
			Query:           row.Query,
			Date:            pgtype.Date{Time: row.Date, Valid: true},
			Views:           row.Views,
			Clicks:          row.Clicks,
			Orders:          row.Orders,
			SpendRub:        floatToPgNumeric(row.SpendRub),
			AvgPosition:     position,
		}); err != nil {
			return fmt.Errorf("upsert search query %q @ %s: %w", row.Query, row.Date.Format("2006-01-02"), err)
		}
		upserted++
	}

	s.logger.Info().
		Str("cabinet_id", cabinet.ID.String()).
		Int("campaigns", len(campaignIDs)).
		Int("rows", upserted).
		Msg("ozon search queries synced")
	return nil
}

// SyncPhrasesAllCabinets runs SyncPhrases for every active Ozon cabinet
// SEQUENTIALLY (one report generation per account at a time; a failed cabinet
// never blocks the rest — best-effort with a joined error at the end).
func (s *OzonSyncService) SyncPhrasesAllCabinets(ctx context.Context) error {
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
		if syncErr := s.SyncPhrases(ctx, *cabinet); syncErr != nil {
			s.logger.Warn().Err(syncErr).Str("cabinet_id", cabinetID.String()).Msg("ozon phrases sync failed for cabinet")
			errs = append(errs, fmt.Errorf("cabinet %s: %w", cabinetID, syncErr))
		}
	}
	return errors.Join(errs...)
}

// ListSearchQueries serves GET /ozon/search-queries: per-query aggregates
// over the last `days` days, optionally filtered by SKU and substring, with
// product names joined from ozon_products by the query's top SKU.
func (s *OzonSyncService) ListSearchQueries(ctx context.Context, workspaceID, cabinetID uuid.UUID, sku *int64, search string, days int, limit, offset int32) ([]domain.OzonSearchQueryStat, int64, error) {
	if _, err := s.ResolveOzonCabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}
	if days <= 0 {
		days = 30
	}
	if days > 90 {
		days = 90
	}
	since := time.Now().UTC().AddDate(0, 0, -days)
	dateFrom := pgtype.Date{Time: since, Valid: true}

	var skuFilter pgtype.Int8
	if sku != nil {
		skuFilter = pgtype.Int8{Int64: *sku, Valid: true}
	}
	searchFilter := textToPgtype(search) // empty → NULL filter

	total, err := s.queries.CountOzonSearchQueries(ctx, sqlcgen.CountOzonSearchQueriesParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		DateFrom:        dateFrom,
		Sku:             skuFilter,
		Search:          searchFilter,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count search queries")
	}
	rows, err := s.queries.AggregateOzonSearchQueries(ctx, sqlcgen.AggregateOzonSearchQueriesParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		DateFrom:        dateFrom,
		Sku:             skuFilter,
		Search:          searchFilter,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to aggregate search queries")
	}

	skus := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.TopSku != 0 {
			skus = append(skus, row.TopSku)
		}
	}
	names := ozonProductInfoBySKU(ctx, s.queries, s.logger, cabinetID, skus)

	result := make([]domain.OzonSearchQueryStat, 0, len(rows))
	for _, row := range rows {
		stat := domain.OzonSearchQueryStat{
			Query:       row.Query,
			SKU:         row.TopSku,
			Views:       row.Views,
			Clicks:      row.Clicks,
			Orders:      row.Orders,
			SpendRub:    pgNumericToFloat(row.SpendRub),
			AvgPosition: pgNumericToFloatPtr(row.AvgPosition),
		}
		if stat.Views > 0 {
			stat.CTR = roundRub(float64(stat.Clicks) / float64(stat.Views) * 100)
		}
		if info, ok := names[row.TopSku]; ok {
			stat.ProductName = pgTextValue(info.Name)
		}
		result = append(result, stat)
	}
	return result, total, nil
}
