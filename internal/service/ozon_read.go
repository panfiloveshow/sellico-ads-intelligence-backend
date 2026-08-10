package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

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

// GetCPOOverview summarises the cabinet's CPO («Оплата за заказ») promo: the
// backing SEARCH_PROMO/ALL_SKU_PROMO campaign, whether it is running, the
// mirrored product count and the 7-day stats aggregate (same window/formula as
// ListCampaignsWithStats). Tenancy-gated like every other Ozon read; when the
// cabinet has no promo campaign it returns enabled=false with zero stats.
func (s *OzonSyncService) GetCPOOverview(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.OzonCPOOverview, error) {
	cabinet, err := s.ResolveOzonCabinet(ctx, workspaceID, cabinetID)
	if err != nil {
		return nil, err
	}

	overview := &domain.OzonCPOOverview{}

	campaigns, err := s.queries.ListOzonPromoCampaigns(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list ozon promo campaigns")
	}
	// ListOzonPromoCampaigns orders RUNNING-first, so the first row is the most
	// representative promo campaign for the header.
	if len(campaigns) > 0 {
		lead := campaigns[0]
		id := lead.OzonCampaignID
		overview.PromoCampaignID = &id
		overview.PromoCampaignTitle = pgTextValue(lead.Title)
		for _, c := range campaigns {
			if pgTextValue(c.State) == "CAMPAIGN_STATE_RUNNING" {
				overview.Enabled = true
				break
			}
		}
	}

	// When the promo is running, read the current whole-cabinet rate (5/7/9 %).
	// Best-effort: leave rate_pct null on any error. Never call this when the
	// promo is off — the activate endpoint used to read the rate would turn the
	// promo on.
	if overview.Enabled {
		if creds, credErr := s.credentials(*cabinet); credErr == nil && creds.HasPerformanceAPI() {
			if pct, rateErr := s.perfClient.GetAllSKUPromoRate(ctx, ozonClientCreds(creds)); rateErr != nil {
				s.logger.Warn().Err(rateErr).Str("cabinet_id", cabinetID.String()).Msg("failed to read cpo rate; leaving rate_pct null")
			} else {
				rate := float64(pct)
				overview.RatePct = &rate
			}
		}
	}

	count, err := s.queries.CountOzonCpoProducts(ctx, uuidToPgtype(cabinetID))
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to count cpo products")
	}
	overview.ProductsCount = count

	since := time.Now().UTC().AddDate(0, 0, -ozonListStatsWindowDays)
	agg, err := s.queries.AggregateOzonPromoStats(ctx, sqlcgen.AggregateOzonPromoStatsParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            timePtrToPgDate(&since),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to aggregate ozon promo stats")
	}
	overview.Stats7d = domain.OzonCPOStats7d{
		Views:      agg.Views,
		Clicks:     agg.Clicks,
		SpendRub:   pgNumericToFloat(agg.SpendRub),
		Orders:     agg.Orders,
		RevenueRub: pgNumericToFloat(agg.RevenueRub),
	}
	if overview.Stats7d.RevenueRub > 0 {
		overview.Stats7d.DRR = overview.Stats7d.SpendRub / overview.Stats7d.RevenueRub * 100
	}

	// Factual promoted orders over the same 7-day window, sourced from the
	// async all_sku_promo orders report mirror (ozon_cpo_orders). This is a
	// DIFFERENT surface than Stats7d: campaign_stats carries the campaign
	// statistics counters (views/clicks/spend), the orders report carries the
	// actual orders the promotion charged for. Both are served side by side.
	ordersAgg, err := s.queries.AggregateOzonCpoOrders(ctx, sqlcgen.AggregateOzonCpoOrdersParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Date:            timePtrToPgDate(&since),
	})
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to aggregate cpo orders")
	}
	overview.OrdersCount7d = ordersAgg.OrdersCount
	overview.SoldUnits7d = ordersAgg.SoldUnits
	overview.PromoRevenue7d = pgNumericToFloat(ordersAgg.RevenueRub)
	overview.PromoSpend7d = pgNumericToFloat(ordersAgg.SpendRub)
	overview.AvgBidPct7d = pgNumericToFloatPtr(ordersAgg.AvgBidPct)
	return overview, nil
}

// GetCampaign returns one campaign with its products/bids after verifying
// workspace ownership through the cabinet. Products are enriched with
// name/offer_id from the ozon_products mapping (one batched lookup).
func (s *OzonSyncService) GetCampaign(ctx context.Context, workspaceID, campaignID uuid.UUID) (*domain.OzonCampaign, []domain.OzonCampaignProduct, error) {
	campaign, err := s.getOwnedCampaign(ctx, workspaceID, campaignID)
	if err != nil {
		return nil, nil, err
	}

	productRows, err := s.queries.ListOzonCampaignProducts(ctx, uuidToPgtype(campaignID))
	if err != nil {
		return nil, nil, apperror.New(apperror.ErrInternal, "failed to list ozon campaign products")
	}
	skus := make([]int64, 0, len(productRows))
	for _, row := range productRows {
		skus = append(skus, row.Sku)
	}
	names := ozonProductInfoBySKU(ctx, s.queries, s.logger, campaign.SellerCabinetID, skus)

	products := make([]domain.OzonCampaignProduct, 0, len(productRows))
	for _, row := range productRows {
		product := ozonCampaignProductFromSqlc(row)
		if info, ok := names[row.Sku]; ok {
			product.Name = pgTextValue(info.Name)
			product.OfferID = pgTextValue(info.OfferID)
		}
		products = append(products, product)
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

// ozonProductInfoBySKU returns a sales-SKU → ozon_products row map for
// batched name/offer_id enrichment (one query per page, no N+1). Enrichment
// is best-effort: a lookup failure yields an empty map, never an error.
func ozonProductInfoBySKU(ctx context.Context, queries *sqlcgen.Queries, logger zerolog.Logger, cabinetID uuid.UUID, skus []int64) map[int64]sqlcgen.ListOzonProductsBySkusRow {
	result := map[int64]sqlcgen.ListOzonProductsBySkusRow{}
	unique := make([]int64, 0, len(skus))
	seen := make(map[int64]struct{}, len(skus))
	for _, sku := range skus {
		if sku == 0 {
			continue
		}
		if _, ok := seen[sku]; ok {
			continue
		}
		seen[sku] = struct{}{}
		unique = append(unique, sku)
	}
	if len(unique) == 0 {
		return result
	}
	rows, err := queries.ListOzonProductsBySkus(ctx, sqlcgen.ListOzonProductsBySkusParams{
		SellerCabinetID: uuidToPgtype(cabinetID),
		Skus:            unique,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load ozon product enrichment map")
		return result
	}
	for _, row := range rows {
		if row.Sku.Valid {
			result[row.Sku.Int64] = row
		}
	}
	return result
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
