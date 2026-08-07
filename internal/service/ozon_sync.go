package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/ozon"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ozonStatsWindowDays is how far back SyncStats pulls daily statistics.
const ozonStatsWindowDays = 14

// OzonSyncService pulls campaigns, statistics and prices from the Ozon APIs
// into the local ozon_* tables. Phase 1 is strictly read-only towards Ozon
// (the only write is the Performance token request).
type OzonSyncService struct {
	queries       *sqlcgen.Queries
	sellerClient  *ozon.SellerClient
	perfClient    *ozon.PerfClient
	encryptionKey []byte
	logger        zerolog.Logger
}

// NewOzonSyncService wires the Ozon sync service.
func NewOzonSyncService(queries *sqlcgen.Queries, sellerClient *ozon.SellerClient, perfClient *ozon.PerfClient, encryptionKey []byte, logger zerolog.Logger) *OzonSyncService {
	return &OzonSyncService{
		queries:       queries,
		sellerClient:  sellerClient,
		perfClient:    perfClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "ozon_sync").Logger(),
	}
}

// ListOzonCabinetIDs returns every active Ozon cabinet for worker fan-out.
// WB cabinets are excluded by the marketplace filter in the query itself.
func (s *OzonSyncService) ListOzonCabinetIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.queries.ListActiveSellerCabinetsByMarketplace(ctx, domain.MarketplaceOzon)
	if err != nil {
		return nil, fmt.Errorf("list ozon cabinets: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, uuidFromPgtype(row.ID))
	}
	return ids, nil
}

// SyncCabinet runs the full phase-1 pipeline (campaigns → stats → prices) for
// one Ozon cabinet. Sub-step failures don't abort the remaining steps; the
// combined error lands in ozon_sync_states.last_error.
func (s *OzonSyncService) SyncCabinet(ctx context.Context, cabinetID uuid.UUID) error {
	cabinet, err := s.loadOzonCabinet(ctx, cabinetID)
	if err != nil {
		return err
	}
	creds, err := s.credentials(*cabinet)
	if err != nil {
		if stateErr := s.queries.TouchOzonSyncState(ctx, sqlcgen.TouchOzonSyncStateParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			LastError:       textToPgtype(fmt.Sprintf("credentials: %v", err)),
		}); stateErr != nil {
			s.logger.Warn().Err(stateErr).Msg("failed to record ozon credentials error")
		}
		return err
	}

	// issues fail the task (asynq retries); notes are recorded but expected
	// (e.g. prices-only mode) — retrying would not change them.
	var issues, notes []string
	if creds.HasPerformanceAPI() {
		if err := s.SyncCampaigns(ctx, *cabinet); err != nil {
			issues = append(issues, fmt.Sprintf("campaigns: %v", err))
		}
		if err := s.SyncStats(ctx, *cabinet); err != nil {
			issues = append(issues, fmt.Sprintf("stats: %v", err))
		}
	} else {
		notes = append(notes, "performance credentials missing: campaigns/stats sync disabled (prices-only mode)")
	}
	// Products before prices: SyncPrices fills ozon_product_prices.name from
	// the ozon_products mapping this step maintains.
	if err := s.SyncProducts(ctx, *cabinet); err != nil {
		issues = append(issues, fmt.Sprintf("products: %v", err))
	}
	if err := s.SyncPrices(ctx, *cabinet); err != nil {
		issues = append(issues, fmt.Sprintf("prices: %v", err))
	}

	if len(issues) > 0 || len(notes) > 0 {
		combined := strings.Join(append(append([]string{}, issues...), notes...), "; ")
		if len(combined) > 900 {
			combined = combined[:900]
		}
		if stateErr := s.queries.TouchOzonSyncState(ctx, sqlcgen.TouchOzonSyncStateParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			LastError:       textToPgtype(combined),
		}); stateErr != nil {
			s.logger.Warn().Err(stateErr).Msg("failed to record ozon sync error")
		}
		if len(issues) > 0 {
			return fmt.Errorf("ozon sync cabinet %s: %s", cabinet.ID, combined)
		}
		return nil // notes only (e.g. prices-only mode) — no point retrying
	}

	// Success clears last_error (narg last_error is NULL here by design).
	if stateErr := s.queries.TouchOzonSyncState(ctx, sqlcgen.TouchOzonSyncStateParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID),
	}); stateErr != nil {
		s.logger.Warn().Err(stateErr).Msg("failed to clear ozon sync error")
	}
	return nil
}

// SyncCampaigns mirrors Performance campaigns + per-SKU bids into
// ozon_campaigns / ozon_campaign_products.
func (s *OzonSyncService) SyncCampaigns(ctx context.Context, cabinet domain.SellerCabinet) error {
	creds, err := s.credentials(cabinet)
	if err != nil {
		return err
	}
	if !creds.HasPerformanceAPI() {
		return fmt.Errorf("performance credentials missing: campaigns sync disabled (prices-only mode)")
	}

	campaigns, err := s.perfClient.ListCampaigns(ctx, ozonClientCreds(creds))
	if err != nil {
		return fmt.Errorf("list campaigns: %w", err)
	}

	upsertedProducts := 0
	for _, campaign := range campaigns {
		row, upsertErr := s.queries.UpsertOzonCampaign(ctx, sqlcgen.UpsertOzonCampaignParams{
			SellerCabinetID:   uuidToPgtype(cabinet.ID),
			OzonCampaignID:    campaign.ID,
			Title:             textToPgtype(campaign.Title),
			AdvObjectType:     textToPgtype(campaign.AdvObjectType),
			State:             textToPgtype(campaign.State),
			Placement:         textToPgtype(campaign.Placement),
			AutopilotStrategy: textToPgtype(campaign.AutopilotStrategy),
			DailyBudgetRub:    int64PtrToPgInt8(campaign.DailyBudgetRub),
			WeeklyBudgetRub:   int64PtrToPgInt8(campaign.WeeklyBudgetRub),
			FromDate:          timePtrToPgDate(campaign.FromDate),
			ToDate:            timePtrToPgDate(campaign.ToDate),
		})
		if upsertErr != nil {
			return fmt.Errorf("upsert campaign %d: %w", campaign.ID, upsertErr)
		}

		products, productsErr := s.perfClient.ListCampaignProducts(ctx, ozonClientCreds(creds), campaign.ID)
		if productsErr != nil {
			// Products are best-effort per campaign (some types have none).
			s.logger.Warn().Err(productsErr).Int64("ozon_campaign_id", campaign.ID).Msg("failed to list campaign products")
			continue
		}
		for _, product := range products {
			if err := s.queries.UpsertOzonCampaignProduct(ctx, sqlcgen.UpsertOzonCampaignProductParams{
				CampaignID: row.ID,
				Sku:        product.SKU,
				BidRub:     floatToPgNumeric(product.BidRub),
				IsActive:   true,
			}); err != nil {
				return fmt.Errorf("upsert campaign %d product %d: %w", campaign.ID, product.SKU, err)
			}
			upsertedProducts++
		}
	}

	if err := s.queries.TouchOzonSyncState(ctx, sqlcgen.TouchOzonSyncStateParams{
		SellerCabinetID:   uuidToPgtype(cabinet.ID),
		CampaignsSyncedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to touch ozon campaigns sync state")
	}

	s.logger.Info().
		Str("cabinet_id", cabinet.ID.String()).
		Int("campaigns", len(campaigns)).
		Int("products", upsertedProducts).
		Msg("ozon campaigns synced")
	return nil
}

// SyncStats pulls daily statistics for the last ozonStatsWindowDays days and
// upserts them into ozon_campaign_stats (keyed by our local campaign UUID).
func (s *OzonSyncService) SyncStats(ctx context.Context, cabinet domain.SellerCabinet) error {
	creds, err := s.credentials(cabinet)
	if err != nil {
		return err
	}
	if !creds.HasPerformanceAPI() {
		return fmt.Errorf("performance credentials missing: stats sync disabled (prices-only mode)")
	}

	refs, err := s.queries.ListOzonCampaignRefsByCabinet(ctx, uuidToPgtype(cabinet.ID))
	if err != nil {
		return fmt.Errorf("list local campaign refs: %w", err)
	}
	if len(refs) == 0 {
		return nil // nothing synced yet — campaigns sync runs first
	}
	byOzonID := make(map[int64]pgtype.UUID, len(refs))
	campaignIDs := make([]int64, 0, len(refs))
	for _, ref := range refs {
		byOzonID[ref.OzonCampaignID] = ref.ID
		campaignIDs = append(campaignIDs, ref.OzonCampaignID)
	}

	dateTo := time.Now().UTC()
	dateFrom := dateTo.AddDate(0, 0, -ozonStatsWindowDays)
	rows, err := s.perfClient.DailyStats(ctx, ozonClientCreds(creds), dateFrom, dateTo, campaignIDs)
	if err != nil {
		return fmt.Errorf("statistics/daily: %w", err)
	}

	upserted := 0
	for _, row := range rows {
		localID, ok := byOzonID[row.CampaignID]
		if !ok {
			continue // campaign not mirrored locally (created between syncs)
		}
		if err := s.queries.UpsertOzonCampaignStat(ctx, sqlcgen.UpsertOzonCampaignStatParams{
			CampaignID: localID,
			Date:       pgtype.Date{Time: row.Date, Valid: true},
			Views:      row.Views,
			Clicks:     row.Clicks,
			SpendRub:   floatToPgNumeric(row.SpendRub),
			Orders:     row.Orders,
			RevenueRub: floatToPgNumeric(row.RevenueRub),
		}); err != nil {
			return fmt.Errorf("upsert stat for campaign %d @ %s: %w", row.CampaignID, row.Date.Format("2006-01-02"), err)
		}
		upserted++
	}

	if err := s.queries.TouchOzonSyncState(ctx, sqlcgen.TouchOzonSyncStateParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID),
		StatsSyncedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to touch ozon stats sync state")
	}

	s.logger.Info().
		Str("cabinet_id", cabinet.ID.String()).
		Int("rows", upserted).
		Msg("ozon stats synced")
	return nil
}

// SyncProducts mirrors the product catalog (product_id ↔ sales SKU +
// name/offer_id/image from /v3/product/info/list) into ozon_products. This
// mapping bridges the two Ozon key spaces: prices are keyed by product_id,
// campaigns/CPO by the sales SKU.
func (s *OzonSyncService) SyncProducts(ctx context.Context, cabinet domain.SellerCabinet) error {
	creds, err := s.credentials(cabinet)
	if err != nil {
		return err
	}
	if !creds.HasSellerAPI() {
		return fmt.Errorf("seller api credentials missing")
	}

	clientCreds := ozonClientCreds(creds)
	products, err := s.sellerClient.ListProducts(ctx, clientCreds)
	if err != nil {
		return fmt.Errorf("product/list: %w", err)
	}
	productIDs := make([]int64, 0, len(products))
	for _, product := range products {
		if product.ProductID != 0 {
			productIDs = append(productIDs, product.ProductID)
		}
	}
	if len(productIDs) == 0 {
		return nil
	}

	infos, err := s.sellerClient.GetProductInfoList(ctx, clientCreds, productIDs)
	if err != nil {
		return fmt.Errorf("product/info/list: %w", err)
	}

	upserted := 0
	for _, info := range infos {
		var sku pgtype.Int8
		if info.SKU != 0 {
			sku = pgtype.Int8{Int64: info.SKU, Valid: true}
		}
		if err := s.queries.UpsertOzonProduct(ctx, sqlcgen.UpsertOzonProductParams{
			SellerCabinetID: uuidToPgtype(cabinet.ID),
			ProductID:       info.ProductID,
			Sku:             sku,
			OfferID:         textToPgtype(info.OfferID),
			Name:            textToPgtype(info.Name),
			PrimaryImage:    textToPgtype(info.PrimaryImage),
		}); err != nil {
			return fmt.Errorf("upsert product %d: %w", info.ProductID, err)
		}
		upserted++
	}

	s.logger.Info().
		Str("cabinet_id", cabinet.ID.String()).
		Int("products", len(productIDs)).
		Int("upserted", upserted).
		Msg("ozon products synced")
	return nil
}

// SyncPrices mirrors the product list + v5 price/commission info into
// ozon_product_prices.
func (s *OzonSyncService) SyncPrices(ctx context.Context, cabinet domain.SellerCabinet) error {
	creds, err := s.credentials(cabinet)
	if err != nil {
		return err
	}
	if !creds.HasSellerAPI() {
		return fmt.Errorf("seller api credentials missing")
	}

	clientCreds := ozonClientCreds(creds)
	// product/list validates the Seller key and confirms catalog visibility;
	// names arrive in phase 2 via product/info — v3/product/list has none.
	products, err := s.sellerClient.ListProducts(ctx, clientCreds)
	if err != nil {
		return fmt.Errorf("product/list: %w", err)
	}

	prices, err := s.sellerClient.ListProductPrices(ctx, clientCreds)
	if err != nil {
		return fmt.Errorf("product/info/prices: %w", err)
	}

	// Name fill from the ozon_products mapping (SyncProducts runs first in
	// SyncCabinet). Best-effort: a missing mapping leaves name NULL and the
	// upsert's COALESCE keeps any previously stored name.
	nameByProductID := map[int64]string{}
	if productRows, mapErr := s.queries.ListOzonProductsByCabinet(ctx, uuidToPgtype(cabinet.ID)); mapErr == nil {
		for _, row := range productRows {
			if row.Name.Valid && row.Name.String != "" {
				nameByProductID[row.ProductID] = row.Name.String
			}
		}
	} else {
		s.logger.Warn().Err(mapErr).Msg("failed to load ozon product name map")
	}

	upserted := 0
	for _, price := range prices {
		if price.ProductID == 0 {
			continue
		}
		if err := s.queries.UpsertOzonProductPrice(ctx, sqlcgen.UpsertOzonProductPriceParams{
			SellerCabinetID:          uuidToPgtype(cabinet.ID),
			Sku:                      price.ProductID,
			OfferID:                  textToPgtype(price.OfferID),
			Name:                     textToPgtype(nameByProductID[price.ProductID]),
			PriceRub:                 floatToPgNumeric(price.PriceRub),
			OldPriceRub:              floatToPgNumeric(price.OldPriceRub),
			MinPriceRub:              floatToPgNumeric(price.MinPriceRub),
			NetPriceRub:              floatToPgNumeric(price.NetPriceRub),
			MarketingSellerPriceRub:  floatToPgNumeric(price.MarketingSellerPriceRub),
			ColorIndex:               textToPgtype(price.ColorIndex),
			CommissionFboPct:         floatToPgNumeric(price.CommissionFBOPct),
			CommissionFbsPct:         floatToPgNumeric(price.CommissionFBSPct),
			AcquiringPct:             floatToPgNumeric(price.AcquiringPct),
			OzonIndexMinPriceRub:     floatToPgNumeric(price.OzonIndexMinPriceRub),
			ExternalIndexMinPriceRub: floatToPgNumeric(price.ExternalIndexMinPriceRub),
			SelfIndexMinPriceRub:     floatToPgNumeric(price.SelfIndexMinPriceRub),
		}); err != nil {
			return fmt.Errorf("upsert price for sku %d: %w", price.ProductID, err)
		}
		upserted++
	}

	if err := s.queries.TouchOzonSyncState(ctx, sqlcgen.TouchOzonSyncStateParams{
		SellerCabinetID: uuidToPgtype(cabinet.ID),
		PricesSyncedAt:  pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}); err != nil {
		s.logger.Warn().Err(err).Msg("failed to touch ozon prices sync state")
	}

	s.logger.Info().
		Str("cabinet_id", cabinet.ID.String()).
		Int("products", len(products)).
		Int("prices", upserted).
		Msg("ozon prices synced")
	return nil
}

// --- helpers ---

func (s *OzonSyncService) loadOzonCabinet(ctx context.Context, cabinetID uuid.UUID) (*domain.SellerCabinet, error) {
	row, err := s.queries.GetSellerCabinetByID(ctx, uuidToPgtype(cabinetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.New(apperror.ErrNotFound, "seller cabinet not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load seller cabinet: %w", err)
	}
	cabinet := sellerCabinetFromSqlc(row)
	if cabinet.Marketplace != domain.MarketplaceOzon {
		return nil, apperror.New(apperror.ErrValidation, "seller cabinet is not an Ozon cabinet")
	}
	return &cabinet, nil
}

func (s *OzonSyncService) credentials(cabinet domain.SellerCabinet) (domain.OzonCredentials, error) {
	if cabinet.Marketplace != domain.MarketplaceOzon {
		return domain.OzonCredentials{}, apperror.New(apperror.ErrValidation, "seller cabinet is not an Ozon cabinet")
	}
	creds, err := decryptOzonCredentialsBlob(cabinet.EncryptedCredentials, s.encryptionKey)
	if err != nil {
		return domain.OzonCredentials{}, fmt.Errorf("decrypt ozon credentials: %w", err)
	}
	return creds, nil
}

func ozonClientCreds(creds domain.OzonCredentials) ozon.Credentials {
	return ozon.Credentials{
		ClientID:         creds.ClientID,
		APIKey:           creds.APIKey,
		PerfClientID:     creds.PerfClientID,
		PerfClientSecret: creds.PerfClientSecret,
	}
}

// floatToPgNumeric converts a float into pgtype.Numeric via its decimal
// string form (avoids binary big.Int plumbing; precision is 2dp money).
func floatToPgNumeric(value float64) pgtype.Numeric {
	var numeric pgtype.Numeric
	if err := numeric.Scan(strconv.FormatFloat(value, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}
	}
	return numeric
}

// pgNumericToFloat extracts a float64 (0 when NULL/invalid).
func pgNumericToFloat(numeric pgtype.Numeric) float64 {
	if !numeric.Valid {
		return 0
	}
	value, err := numeric.Float64Value()
	if err != nil || !value.Valid {
		return 0
	}
	return value.Float64
}

func pgNumericToFloatPtr(numeric pgtype.Numeric) *float64 {
	if !numeric.Valid {
		return nil
	}
	value := pgNumericToFloat(numeric)
	return &value
}
