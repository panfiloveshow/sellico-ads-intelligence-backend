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

// ozonCPOOrdersWindowDays is the promoted-orders report request window: the
// last 14 days on every sync; the table accumulates history via upserts.
const ozonCPOOrdersWindowDays = 14

// SyncCPOOrders pulls the CPO («Оплата за заказ») promoted-orders report for
// one cabinet via the async all_sku_promo statistics flow and upserts
// ozon_cpo_orders. Skips quietly when the cabinet has no RUNNING promo
// campaign — the report would be empty and the account-level "one report
// generation at a time" budget is better spent elsewhere.
func (s *OzonSyncService) SyncCPOOrders(ctx context.Context, cabinet domain.SellerCabinet) error {
	creds, err := s.credentials(cabinet)
	if err != nil {
		return err
	}
	if !creds.HasPerformanceAPI() {
		return fmt.Errorf("performance credentials missing: cpo orders sync disabled")
	}

	promos, err := s.queries.ListOzonPromoCampaigns(ctx, uuidToPgtype(cabinet.ID))
	if err != nil {
		return fmt.Errorf("list promo campaigns: %w", err)
	}
	running := false
	for _, promo := range promos {
		if pgTextValue(promo.State) == "CAMPAIGN_STATE_RUNNING" {
			running = true
			break
		}
	}
	if !running {
		return nil // promo off — nothing to report
	}

	dateTo := time.Now().UTC()
	dateFrom := dateTo.AddDate(0, 0, -ozonCPOOrdersWindowDays)
	rows, err := s.perfClient.GetAllSKUPromoOrders(ctx, ozonClientCreds(creds), dateFrom, dateTo)
	if err != nil {
		return fmt.Errorf("all_sku_promo orders report: %w", err)
	}

	upserted := 0
	for _, row := range rows {
		if err := s.queries.UpsertOzonCpoOrder(ctx, sqlcgen.UpsertOzonCpoOrderParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			Date:            pgtype.Date{Time: row.Date, Valid: true},
			OrderID:         row.OrderID,
			OrderNumber:     textToPgtype(row.OrderNumber),
			// sku is part of the upsert key: always written (0 when the report
			// omitted it) so ON CONFLICT can match — a NULL key would dodge
			// the unique constraint and duplicate rows on every sync.
			Sku:          pgtype.Int8{Int64: row.SKU, Valid: true},
			AdvSku:       pgtype.Int8{Int64: row.AdvSKU, Valid: row.AdvSKU != 0},
			VendorCode:   textToPgtype(row.VendorCode),
			Name:         textToPgtype(row.Name),
			Quantity:     pgtype.Int4{Int32: clampInt32(int64(row.Quantity)), Valid: true},
			PriceRub:     floatToPgNumeric(row.PriceRub),
			SalePriceRub: floatToPgNumeric(row.SalePriceRub),
			BidPct:       floatToPgNumeric(row.BidPct),
			BidRub:       floatToPgNumeric(row.BidRub),
			SpendRub:     floatToPgNumeric(row.SpendRub),
		}); err != nil {
			return fmt.Errorf("upsert cpo order %s sku %d: %w", row.OrderID, row.SKU, err)
		}
		upserted++
	}

	s.logger.Info().
		Str("cabinet_id", cabinet.ID.String()).
		Int("rows", upserted).
		Msg("ozon cpo orders synced")
	return nil
}

// SyncCPOOrdersAllCabinets runs SyncCPOOrders for every active Ozon cabinet
// SEQUENTIALLY — the async statistics flow allows one report generation per
// account at a time (shared with the phrases report), and one failed cabinet
// never blocks the rest (best-effort with a joined error at the end).
func (s *OzonSyncService) SyncCPOOrdersAllCabinets(ctx context.Context) error {
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
		if syncErr := s.SyncCPOOrders(ctx, *cabinet); syncErr != nil {
			s.logger.Warn().Err(syncErr).Str("cabinet_id", cabinetID.String()).Msg("ozon cpo orders sync failed for cabinet")
			errs = append(errs, fmt.Errorf("cabinet %s: %w", cabinetID, syncErr))
		}
	}
	return errors.Join(errs...)
}

// ListCPOOrders serves GET /ozon/cpo/orders: the mirrored promoted orders of
// the last `days` days, newest first, paginated. Tenancy-gated like every
// other Ozon read.
func (s *OzonSyncService) ListCPOOrders(ctx context.Context, workspaceID, cabinetID uuid.UUID, days int, limit, offset int32) ([]domain.OzonCPOOrder, int64, error) {
	if _, err := s.ResolveOzonCabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	since := time.Now().UTC().AddDate(0, 0, -days)
	dateFrom := pgtype.Date{Time: since, Valid: true}

	total, err := s.queries.CountOzonCpoOrders(ctx, sqlcgen.CountOzonCpoOrdersParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            dateFrom,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count cpo orders")
	}
	rows, err := s.queries.ListOzonCpoOrders(ctx, sqlcgen.ListOzonCpoOrdersParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            dateFrom,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list cpo orders")
	}
	result := make([]domain.OzonCPOOrder, 0, len(rows))
	for _, row := range rows {
		result = append(result, ozonCPOOrderFromSqlc(row))
	}
	return result, total, nil
}

func ozonCPOOrderFromSqlc(row sqlcgen.OzonCpoOrder) domain.OzonCPOOrder {
	order := domain.OzonCPOOrder{
		ID:              uuidFromPgtype(row.ID),
		SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		Date:            row.Date.Time,
		OrderID:         row.OrderID,
		OrderNumber:     pgTextValue(row.OrderNumber),
		VendorCode:      pgTextValue(row.VendorCode),
		Name:            pgTextValue(row.Name),
		PriceRub:        pgNumericToFloatPtr(row.PriceRub),
		SalePriceRub:    pgNumericToFloatPtr(row.SalePriceRub),
		BidPct:          pgNumericToFloatPtr(row.BidPct),
		BidRub:          pgNumericToFloatPtr(row.BidRub),
		SpendRub:        pgNumericToFloatPtr(row.SpendRub),
	}
	if row.Sku.Valid {
		order.SKU = row.Sku.Int64
	}
	if row.AdvSku.Valid {
		order.AdvSKU = row.AdvSku.Int64
	}
	if row.Quantity.Valid {
		order.Quantity = row.Quantity.Int32
	}
	return order
}
