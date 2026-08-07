package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ozonListStatsWindowDays is the aggregate window shown on the campaigns list.
const ozonListStatsWindowDays = 7

// ResolveOzonCabinet verifies that the cabinet belongs to the workspace and
// is an Ozon cabinet. All read endpoints go through this tenancy gate.
func (s *OzonSyncService) ResolveOzonCabinet(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.SellerCabinet, error) {
	cabinet, err := s.loadOzonCabinet(ctx, cabinetID)
	if err != nil {
		return nil, err
	}
	if cabinet.WorkspaceID != workspaceID {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	return cabinet, nil
}

// ListCampaignsWithStats returns cabinet campaigns with a 7-day stats
// aggregate (views/clicks/spend/orders/revenue + computed DRR).
func (s *OzonSyncService) ListCampaignsWithStats(ctx context.Context, workspaceID, cabinetID uuid.UUID, limit, offset int32) ([]domain.OzonCampaignWithStats, int64, error) {
	if _, err := s.ResolveOzonCabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}

	total, err := s.queries.CountOzonCampaignsByCabinet(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count ozon campaigns")
	}

	rows, err := s.queries.ListOzonCampaignsByCabinet(ctx, sqlcgen.ListOzonCampaignsByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list ozon campaigns")
	}

	since := time.Now().UTC().AddDate(0, 0, -ozonListStatsWindowDays)
	aggregates, err := s.queries.AggregateOzonCampaignStatsByCabinet(ctx, sqlcgen.AggregateOzonCampaignStatsByCabinetParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            timePtrToPgDate(&since),
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to aggregate ozon campaign stats")
	}
	aggregateByID := make(map[uuid.UUID]sqlcgen.AggregateOzonCampaignStatsByCabinetRow, len(aggregates))
	for _, aggregate := range aggregates {
		aggregateByID[uuidFromPgtype(aggregate.CampaignID)] = aggregate
	}

	result := make([]domain.OzonCampaignWithStats, 0, len(rows))
	for _, row := range rows {
		item := domain.OzonCampaignWithStats{OzonCampaign: ozonCampaignFromSqlc(row)}
		if aggregate, ok := aggregateByID[item.ID]; ok {
			item.StatsViews = aggregate.Views
			item.StatsClicks = aggregate.Clicks
			item.StatsSpend = pgNumericToFloat(aggregate.SpendRub)
			item.StatsOrders = aggregate.Orders
			item.StatsRevenue = pgNumericToFloat(aggregate.RevenueRub)
			if item.StatsRevenue > 0 {
				item.DRR = item.StatsSpend / item.StatsRevenue * 100
			}
		}
		result = append(result, item)
	}
	return result, total, nil
}

// GetCampaign returns one campaign with its products/bids after verifying
// workspace ownership through the cabinet.
func (s *OzonSyncService) GetCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.OzonCampaign, []domain.OzonCampaignProduct, error) {
	campaign, err := s.getOwnedCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return nil, nil, err
	}

	productRows, err := s.queries.ListOzonCampaignProducts(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "failed to list ozon campaign products")
	}
	products := make([]domain.OzonCampaignProduct, 0, len(productRows))
	for _, row := range productRows {
		products = append(products, ozonCampaignProductFromSqlc(row))
	}
	return campaign, products, nil
}

// ListCampaignStats returns daily stat rows for [from, to].
func (s *OzonSyncService) ListCampaignStats(ctx context.Context, workspaceID, campaignID uuid.UUID, from, to time.Time) ([]domain.OzonCampaignStat, error) {
	if _, err := s.getOwnedCampaign(ctx, workspaceID, campaignID); err != nil {
		return nil, err
	}

	rows, err := s.queries.ListOzonCampaignStats(ctx, sqlcgen.ListOzonCampaignStatsParams{
		CampaignID: uuidToPgtype(campaignID),
		Date:       timePtrToPgDate(&from),
		Date_2:     timePtrToPgDate(&to),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list ozon campaign stats")
	}
	result := make([]domain.OzonCampaignStat, 0, len(rows))
	for _, row := range rows {
		result = append(result, ozonCampaignStatFromSqlc(row))
	}
	return result, nil
}

// ListPrices returns ozon_product_prices for a cabinet with optional search
// (name / offer_id substring, or exact SKU).
func (s *OzonSyncService) ListPrices(ctx context.Context, workspaceID, cabinetID uuid.UUID, search string, limit, offset int32) ([]domain.OzonProductPrice, int64, error) {
	if _, err := s.ResolveOzonCabinet(ctx, workspaceID, cabinetID); err != nil {
		return nil, 0, err
	}

	searchFilter := textToPgtype(search) // empty string → NULL filter
	total, err := s.queries.CountOzonProductPrices(ctx, sqlcgen.CountOzonProductPricesParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Search:          searchFilter,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to count ozon prices")
	}

	rows, err := s.queries.ListOzonProductPrices(ctx, sqlcgen.ListOzonProductPricesParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Search:          searchFilter,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		return nil, 0, apperror.New(apperror.ErrInternal, "failed to list ozon prices")
	}
	// Sellico unit-economics mirror: cost fallback for the informational floor
	// when Ozon itself does not report net_price for a product.
	economics := map[string]sqlcgen.OzonProductEconomic{}
	if econRows, econErr := s.queries.ListOzonProductEconomicsByCabinet(ctx, uuidToPgtype(cabinetID)); econErr == nil {
		for _, econ := range econRows {
			economics[econ.OfferID] = econ
		}
	}

	result := make([]domain.OzonProductPrice, 0, len(rows))
	for _, row := range rows {
		price := ozonProductPriceFromSqlc(row)
		applyOzonEconomicsFloorFallback(&price, row, economics)
		result = append(result, price)
	}
	return result, total, nil
}

// applyOzonEconomicsFloorFallback recomputes the informational floor from the
// Sellico cost (cost_price + other_costs; logistics для Ozon уже сидит в
// комиссиях — не добавляется) when the mirror row has no net_price of its own.
func applyOzonEconomicsFloorFallback(price *domain.OzonProductPrice, row sqlcgen.OzonProductPrice, economics map[string]sqlcgen.OzonProductEconomic) {
	if pgNumericToFloat(row.NetPriceRub) > 0 || !row.OfferID.Valid {
		return // Ozon's own net_price already produced the floor (or no key)
	}
	econ, ok := economics[row.OfferID.String]
	if !ok {
		return
	}
	cost := ozonEffectiveNetPrice(0, &econ)
	if cost <= 0 {
		return
	}
	if floor, reason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:      cost,
		CommissionFBOPct: pgNumericToFloat(row.CommissionFboPct),
		CommissionFBSPct: pgNumericToFloat(row.CommissionFbsPct),
		AcquiringRub:     pgNumericToFloat(row.AcquiringPct),
	}); reason == "" {
		price.FloorRub = &floor
	}
}

func (s *OzonSyncService) getOwnedCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.OzonCampaign, error) {
	row, err := s.queries.GetOzonCampaignByID(ctx, uuidToPgtype(campaignID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "ozon campaign not found")
	}
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to load ozon campaign")
	}
	if _, err := s.ResolveOzonCabinet(ctx, workspaceID, uuidFromPgtype(row.SellerCabinetID)); err != nil {
		return nil, apperror.New(apperror.ErrNotFound, "ozon campaign not found")
	}
	campaign := ozonCampaignFromSqlc(row)
	return &campaign, nil
}

// --- sqlc → domain mappers ---

func ozonCampaignFromSqlc(row sqlcgen.OzonCampaign) domain.OzonCampaign {
	campaign := domain.OzonCampaign{
		ID:                uuidFromPgtype(row.ID),
		SellerCabinetID:   uuidFromPgtype(row.SellerCabinetID),
		OzonCampaignID:    row.OzonCampaignID,
		Title:             pgTextValue(row.Title),
		AdvObjectType:     pgTextValue(row.AdvObjectType),
		State:             pgTextValue(row.State),
		Placement:         pgTextValue(row.Placement),
		AutopilotStrategy: pgTextValue(row.AutopilotStrategy),
		CreatedAt:         row.CreatedAt.Time,
		UpdatedAt:         row.UpdatedAt.Time,
	}
	if row.DailyBudgetRub.Valid {
		value := row.DailyBudgetRub.Int64
		campaign.DailyBudgetRub = &value
	}
	if row.WeeklyBudgetRub.Valid {
		value := row.WeeklyBudgetRub.Int64
		campaign.WeeklyBudgetRub = &value
	}
	if row.FromDate.Valid {
		value := row.FromDate.Time
		campaign.FromDate = &value
	}
	if row.ToDate.Valid {
		value := row.ToDate.Time
		campaign.ToDate = &value
	}
	return campaign
}

func ozonCampaignProductFromSqlc(row sqlcgen.OzonCampaignProduct) domain.OzonCampaignProduct {
	product := domain.OzonCampaignProduct{
		ID:         uuidFromPgtype(row.ID),
		CampaignID: uuidFromPgtype(row.CampaignID),
		SKU:        row.Sku,
		BidRub:     pgNumericToFloatPtr(row.BidRub),
		TargetCIR:  pgNumericToFloatPtr(row.TargetCir),
		IsActive:   row.IsActive,
		UpdatedAt:  row.UpdatedAt.Time,
	}
	if row.TopPosition.Valid {
		value := row.TopPosition.Int32
		product.TopPosition = &value
	}
	return product
}

func ozonCampaignStatFromSqlc(row sqlcgen.OzonCampaignStat) domain.OzonCampaignStat {
	return domain.OzonCampaignStat{
		ID:         uuidFromPgtype(row.ID),
		CampaignID: uuidFromPgtype(row.CampaignID),
		Date:       row.Date.Time,
		Views:      row.Views,
		Clicks:     row.Clicks,
		SpendRub:   pgNumericToFloat(row.SpendRub),
		Orders:     row.Orders,
		RevenueRub: pgNumericToFloat(row.RevenueRub),
	}
}

func ozonProductPriceFromSqlc(row sqlcgen.OzonProductPrice) domain.OzonProductPrice {
	price := domain.OzonProductPrice{
		ID:                      uuidFromPgtype(row.ID),
		SellerCabinetID:         uuidFromPgtype(row.SellerCabinetID),
		SKU:                     row.Sku,
		OfferID:                 pgTextValue(row.OfferID),
		Name:                    pgTextValue(row.Name),
		PriceRub:                pgNumericToFloatPtr(row.PriceRub),
		OldPriceRub:             pgNumericToFloatPtr(row.OldPriceRub),
		MinPriceRub:             pgNumericToFloatPtr(row.MinPriceRub),
		NetPriceRub:             pgNumericToFloatPtr(row.NetPriceRub),
		MarketingSellerPriceRub: pgNumericToFloatPtr(row.MarketingSellerPriceRub),
		ColorIndex:              pgTextValue(row.ColorIndex),
		CommissionFBOPct:        pgNumericToFloatPtr(row.CommissionFboPct),
		CommissionFBSPct:        pgNumericToFloatPtr(row.CommissionFbsPct),
		AcquiringPct:            pgNumericToFloatPtr(row.AcquiringPct),
	}
	if row.SyncedAt.Valid {
		value := row.SyncedAt.Time
		price.SyncedAt = &value
	}
	if v := pgNumericToFloat(row.OzonIndexMinPriceRub); v > 0 {
		price.OzonIndexMinPriceRub = &v
	}
	if v := pgNumericToFloat(row.ExternalIndexMinPriceRub); v > 0 {
		price.ExternalIndexMinPriceRub = &v
	}
	if v := pgNumericToFloat(row.SelfIndexMinPriceRub); v > 0 {
		price.SelfIndexMinPriceRub = &v
	}
	// Informational floor: computed from the row's own Ozon economics with a
	// zero target margin (an active strategy may use its own margin).
	if floor, reason := computeOzonFloor(ozonFloorInputs{
		NetPriceRub:      pgNumericToFloat(row.NetPriceRub),
		CommissionFBOPct: pgNumericToFloat(row.CommissionFboPct),
		CommissionFBSPct: pgNumericToFloat(row.CommissionFbsPct),
		AcquiringRub:     pgNumericToFloat(row.AcquiringPct),
	}); reason == "" {
		price.FloorRub = &floor
	}
	return price
}
