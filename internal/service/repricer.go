package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

const priceListPageSize = 1000

// RepricerService manages WB product prices: sync (read), strategy evaluation,
// upload, polling, manual bulk changes, rollback, and quarantine monitoring.
type RepricerService struct {
	queries       *sqlcgen.Queries
	strategies    *StrategyService
	engine        *PriceEngine
	wbClient      *wb.Client
	encryptionKey []byte
	economics     UnitEconomicsReadinessProvider
	notifications *NotificationService
	logger        zerolog.Logger
}

type RepricerOption func(*RepricerService)

func WithRepricerStrategyService(s *StrategyService) RepricerOption {
	return func(r *RepricerService) { r.strategies = s }
}

func WithRepricerEngine(e *PriceEngine) RepricerOption {
	return func(r *RepricerService) { r.engine = e }
}

func WithRepricerUnitEconomics(p UnitEconomicsReadinessProvider) RepricerOption {
	return func(r *RepricerService) { r.economics = p }
}

func WithRepricerNotifications(n *NotificationService) RepricerOption {
	return func(r *RepricerService) { r.notifications = n }
}

func NewRepricerService(
	queries *sqlcgen.Queries,
	wbClient *wb.Client,
	encryptionKey []byte,
	logger zerolog.Logger,
	opts ...RepricerOption,
) *RepricerService {
	svc := &RepricerService{
		queries:       queries,
		wbClient:      wbClient,
		encryptionKey: encryptionKey,
		logger:        logger.With().Str("component", "repricer").Logger(),
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// SyncPrices refreshes product_prices for every active cabinet of a workspace
// from WB. A cabinet whose token lacks the prices scope is marked 'missing' and
// skipped; a rate-limited cabinet records a cooldown and is skipped. Returns the
// number of price rows upserted.
func (s *RepricerService) SyncPrices(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	cabinets, err := s.queries.ListActiveSellerCabinetsByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return 0, err
	}

	total := 0
	var syncErrors []error
	for _, cabinet := range cabinets {
		cabinetID := uuidFromPgtype(cabinet.ID)
		token, decErr := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
		if decErr != nil {
			s.logger.Warn().Err(decErr).Str("cabinet_id", cabinetID.String()).Msg("failed to decrypt cabinet token")
			syncErrors = append(syncErrors, fmt.Errorf("cabinet %s token: %w", cabinetID, decErr))
			continue
		}

		// Always refresh the catalog (names/images/brand) from content cards —
		// the Content scope is separate from Prices&Discounts, so titles/photos
		// populate even for cabinets whose token can't read prices.
		s.enrichCabinetProducts(ctx, workspaceID, cabinetID, token)

		// Real FBW stock from WB Statistics (separate scope; falls back to the
		// card.wb.ru storefront quantity at read time when unavailable).
		s.syncCabinetStocks(ctx, workspaceID, cabinetID, token)

		// Prices (scope-gated / rate-limited).
		if s.pricesEndpointCoolingDown(ctx, cabinetID, wbEndpointPricesList) {
			s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("prices list cooling down, skipping prices for cabinet")
			syncErrors = append(syncErrors, fmt.Errorf("cabinet %s prices endpoint is cooling down", cabinetID))
			continue
		}
		count, syncErr := s.syncCabinetPrices(ctx, workspaceID, cabinetID, token)
		if syncErr != nil {
			if errors.Is(syncErr, wb.ErrPricesScopeMissing) {
				s.markCabinetScope(ctx, cabinetID, domain.PricesScopeMissing)
				s.logger.Warn().Str("cabinet_id", cabinetID.String()).Msg("cabinet token missing prices scope")
				syncErrors = append(syncErrors, fmt.Errorf("cabinet %s: %w", cabinetID, syncErr))
				continue
			}
			s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesList, syncErr)
			s.logger.Warn().Err(syncErr).Str("cabinet_id", cabinetID.String()).Msg("price sync failed for cabinet")
			syncErrors = append(syncErrors, fmt.Errorf("cabinet %s price sync: %w", cabinetID, syncErr))
			continue
		}
		s.markCabinetScope(ctx, cabinetID, domain.PricesScopeOK)
		total += count
	}
	return total, errors.Join(syncErrors...)
}

// enrichCabinetProducts refreshes products (title, brand, image) from WB content
// cards for a cabinet, so repriced products display names and photos.
func (s *RepricerService) enrichCabinetProducts(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) {
	cards, err := s.wbClient.ListProducts(ctx, token)
	if err != nil {
		s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("product enrichment (content cards) skipped")
		return
	}
	enriched := 0
	for _, c := range cards {
		var price pgtype.Int8
		if c.Price != nil {
			price = pgtype.Int8{Int64: *c.Price, Valid: true}
		}
		if _, err := s.queries.UpsertProduct(ctx, sqlcgen.UpsertProductParams{
			WorkspaceID:     uuidToPgtype(workspaceID),
			SellerCabinetID: uuidToPgtype(cabinetID),
			WbProductID:     c.NmID,
			Title:           c.Title,
			Brand:           textToPgtype(c.Brand),
			Category:        textToPgtype(c.Category),
			ImageUrl:        textToPgtype(c.ImageURL),
			Price:           price,
		}); err != nil {
			s.logger.Warn().Err(err).Int64("wb_product_id", c.NmID).Msg("failed to upsert enriched product")
			continue
		}
		enriched++
	}
	if enriched > 0 {
		s.logger.Info().Str("cabinet_id", cabinetID.String()).Int("enriched", enriched).Msg("product names/images enriched from content cards")
	}
}

// syncCabinetStocks pulls real FBW stock from WB Statistics and writes it to
// products.stock_total. Best-effort: a missing Statistics scope or a rate limit
// just leaves the storefront fallback in place.
func (s *RepricerService) syncCabinetStocks(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) {
	// Bound the stats call: WB statistics is 1 req/min, so a rate-limited cabinet
	// would otherwise burn ~2 min of retries and starve the repricer queue (polls,
	// other syncs). Cap it and let the next hourly sync retry.
	stockCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	stocks, err := s.wbClient.ListSupplierStocks(stockCtx, token)
	if err != nil {
		if !errors.Is(err, wb.ErrStatsScopeMissing) {
			s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("stock sync skipped")
		}
		return
	}
	updated := 0
	for nm, qty := range stocks {
		if err := s.queries.SetProductStock(ctx, uuidToPgtype(workspaceID), uuidToPgtype(cabinetID), nm, int32(qty)); err == nil {
			updated++
		}
	}
	if updated > 0 {
		s.logger.Info().Str("cabinet_id", cabinetID.String()).Int("stocks", updated).Msg("real FBW stock synced")
	}
}

func (s *RepricerService) syncCabinetPrices(ctx context.Context, workspaceID, cabinetID uuid.UUID, token string) (int, error) {
	offset := 0
	count := 0
	for {
		goods, err := s.wbClient.ListGoodsPrices(ctx, token, priceListPageSize, offset, nil)
		if err != nil {
			return count, err
		}
		for _, g := range goods {
			var discounted pgtype.Int8
			if g.DiscountedPrice > 0 {
				discounted = pgtype.Int8{Int64: g.DiscountedPrice, Valid: true}
			}
			if _, err := s.queries.UpsertProductPrice(ctx, sqlcgen.UpsertProductPriceParams{
				WorkspaceID:         uuidToPgtype(workspaceID),
				SellerCabinetID:     uuidToPgtype(cabinetID),
				WbProductID:         g.NmID,
				PriceRub:            g.Price,
				DiscountPercent:     int32(g.Discount),
				ClubDiscountPercent: int32(g.ClubDiscount),
				DiscountedPriceRub:  discounted,
				EditableSizePrice:   g.EditableSizePrice,
			}); err != nil {
				return count, err
			}
			count++
		}
		if len(goods) < priceListPageSize {
			break
		}
		offset += priceListPageSize
	}
	return count, nil
}

// ListCatalog returns the full product catalog (all products) left-joined with
// their current prices, optionally narrowed to one cabinet. Products without a
// synced price appear with nil price fields.
func (s *RepricerService) ListCatalog(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID, limit, offset int32) ([]domain.ProductCatalogItem, error) {
	cabinetFilter := pgtype.UUID{}
	if cabinetID != nil {
		cabinetFilter = uuidToPgtype(*cabinetID)
	}
	rows, err := s.queries.ListCatalogWithPrices(ctx, uuidToPgtype(workspaceID), cabinetFilter, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ProductCatalogItem, len(rows))
	for i, r := range rows {
		item := domain.ProductCatalogItem{
			WBProductID: r.WbProductID,
			Title:       r.Title,
			HasPrice:    r.PriceRub.Valid,
		}
		if r.Brand.Valid {
			item.Brand = &r.Brand.String
		}
		if r.ImageUrl.Valid {
			item.ImageURL = &r.ImageUrl.String
		}
		if r.StockTotal.Valid {
			v := int(r.StockTotal.Int32)
			item.StockTotal = &v
		}
		if r.PriceRub.Valid {
			v := r.PriceRub.Int64
			item.PriceRub = &v
		}
		if r.DiscountPercent.Valid {
			v := int(r.DiscountPercent.Int32)
			item.DiscountPercent = &v
		}
		if r.ClubDiscountPercent.Valid {
			v := int(r.ClubDiscountPercent.Int32)
			item.ClubDiscountPercent = &v
		}
		if r.DiscountedPriceRub.Valid {
			v := r.DiscountedPriceRub.Int64
			item.DiscountedPriceRub = &v
		}
		if r.EditableSizePrice.Valid {
			v := r.EditableSizePrice.Bool
			item.EditableSizePrice = &v
		}
		if r.SppPercent.Valid {
			v := r.SppPercent.Float64
			item.SppPercent = &v
		}
		if r.CustomerPriceRub.Valid {
			v := r.CustomerPriceRub.Int64
			item.ShowcasePriceRub = &v
		}
		if r.SyncedAt.Valid {
			t := r.SyncedAt.Time
			item.SyncedAt = &t
		}
		out[i] = item
	}
	s.enrichCatalogShowcase(ctx, out)
	return out, nil
}

type showcaseEntry struct {
	sc     wb.Showcase
	expiry time.Time
}

// showcaseCache memoizes card.wb.ru lookups. ponytail: process-global map for a
// single-instance API; a per-request miss fans out at most len/100 HTTP calls.
var showcaseCache = struct {
	mu   sync.Mutex
	data map[int64]showcaseEntry
}{data: map[int64]showcaseEntry{}}

const showcaseTTL = 15 * time.Minute

// enrichCatalogShowcase fills each item's image URL (always, computed from the
// nmID) and the buyer price + СПП from the tokenless card.wb.ru storefront, so
// the catalog shows a price even without the "Цены и скидки" scope.
func (s *RepricerService) enrichCatalogShowcase(ctx context.Context, items []domain.ProductCatalogItem) {
	if len(items) == 0 {
		return
	}
	now := time.Now()
	var missing []int64
	cached := make(map[int64]wb.Showcase, len(items))

	showcaseCache.mu.Lock()
	for _, it := range items {
		if it.ShowcasePriceRub != nil && it.SppPercent != nil {
			continue
		}
		if e, ok := showcaseCache.data[it.WBProductID]; ok && e.expiry.After(now) {
			cached[it.WBProductID] = e.sc
		} else {
			missing = append(missing, it.WBProductID)
		}
	}
	showcaseCache.mu.Unlock()

	if len(missing) > 0 {
		fetched, err := s.wbClient.ShowcaseByNmIDs(ctx, missing)
		if len(fetched) > 0 {
			showcaseCache.mu.Lock()
			exp := time.Now().Add(showcaseTTL)
			for nm, sc := range fetched {
				showcaseCache.data[nm] = showcaseEntry{sc: sc, expiry: exp}
				cached[nm] = sc
			}
			showcaseCache.mu.Unlock()
		}
		if err != nil {
			s.logger.Warn().Err(err).Msg("catalog showcase enrichment failed")
		}
	}

	for i := range items {
		nm := items[i].WBProductID
		if items[i].ImageURL == nil || *items[i].ImageURL == "" {
			url := wb.WBImageURL(nm)
			items[i].ImageURL = &url
		}
		sc, ok := cached[nm]
		if !ok {
			continue
		}
		if items[i].Title == "" && sc.Name != "" {
			items[i].Title = sc.Name
		}
		// Stock comes only from real WB Statistics (products.stock_total). The
		// card.wb.ru storefront quantity is a capped, geo-limited number that
		// misleads (shows ~44 when the cabinet has hundreds), so it's not used.
		if items[i].ShowcasePriceRub == nil && sc.BuyerRub > 0 {
			buyer := sc.BuyerRub
			items[i].ShowcasePriceRub = &buyer
		}
		if sc.BasicRub > 0 {
			basic := sc.BasicRub
			items[i].ShowcaseBasicRub = &basic
		}
		if items[i].SppPercent == nil {
			if spp := catalogSppPercent(items[i], sc.BuyerRub); spp > 0 {
				items[i].SppPercent = &spp
			}
		}
	}
}

// catalogSppPercent compares the public buyer price with the seller's current
// effective price. card.wb.ru basic includes the seller discount and therefore
// cannot be used as the СПП denominator.
func catalogSppPercent(item domain.ProductCatalogItem, buyerRub int64) float64 {
	if buyerRub <= 0 {
		return 0
	}
	sellerRub := int64(0)
	if item.DiscountedPriceRub != nil {
		sellerRub = *item.DiscountedPriceRub
	} else if item.PriceRub != nil {
		discount := 0
		if item.DiscountPercent != nil {
			discount = *item.DiscountPercent
		}
		sellerRub = effectiveOf(*item.PriceRub, discount)
	}
	if sellerRub <= 0 || buyerRub >= sellerRub {
		return 0
	}
	return math.Round((1-float64(buyerRub)/float64(sellerRub))*10000) / 100
}

// SetPause freezes (or unfreezes) a cabinet's repricer auto-apply until the
// given time. until=nil unfreezes immediately.
func (s *RepricerService) SetPause(ctx context.Context, workspaceID, cabinetID uuid.UUID, until *time.Time) error {
	ts := pgtype.Timestamptz{}
	if until != nil {
		ts = pgtype.Timestamptz{Time: *until, Valid: true}
	}
	return s.queries.SetCabinetRepricerPause(ctx, uuidToPgtype(cabinetID), ts, uuidToPgtype(workspaceID))
}

// Health returns a one-glance repricer status summary for a cabinet.
func (s *RepricerService) Health(ctx context.Context, workspaceID, cabinetID uuid.UUID) (*domain.RepricerHealth, error) {
	r, err := s.queries.RepricerHealth(ctx, uuidToPgtype(workspaceID), uuidToPgtype(cabinetID))
	if err != nil {
		return nil, err
	}
	h := &domain.RepricerHealth{
		Products:         int(r.Products),
		WithPrice:        int(r.WithPrice),
		ActiveStrategies: int(r.ActiveStrategies),
		AppliedToday:     int(r.ChangesApplied),
		Recommendations:  int(r.Recommendations),
		FailedToday:      int(r.Failed),
	}
	if r.LastSyncAt.Valid {
		t := r.LastSyncAt.Time
		h.LastSyncAt = &t
	}
	if r.PausedUntil.Valid && r.PausedUntil.Time.After(time.Now()) {
		t := r.PausedUntil.Time
		h.PausedUntil = &t
	}
	return h, nil
}

// SendDailyDigest notifies the workspace with a repricer summary and suggests
// promoting well-behaving dry-run strategies to auto. No-op when nothing happened.
func (s *RepricerService) SendDailyDigest(ctx context.Context, workspaceID uuid.UUID) error {
	if s.notifications == nil {
		return nil
	}
	applied, recommendations, failed, err := s.queries.RepricerDigestCounts(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return err
	}
	var promoteReady []string
	if failed == 0 && recommendations > 0 && s.strategies != nil {
		if active, err := s.strategies.ListActive(ctx, workspaceID); err == nil {
			weekAgo := time.Now().Add(-7 * 24 * time.Hour)
			for _, st := range active {
				if domain.IsPriceStrategy(st.Type) &&
					st.Params.MergedPriceParams().PriceApplyMode == domain.PriceApplyModeDryRun &&
					st.CreatedAt.Before(weekAgo) {
					promoteReady = append(promoteReady, st.Name)
				}
			}
		}
	}
	s.notifications.NotifyRepricerDigest(ctx, workspaceID, int(applied), int(recommendations), int(failed), promoteReady)
	return nil
}

// ListCabinetsScope reports each cabinet's prices-scope status for the workspace.
func (s *RepricerService) ListCabinetsScope(ctx context.Context, workspaceID uuid.UUID) ([]domain.CabinetPricesScope, error) {
	rows, err := s.queries.ListCabinetPricesScope(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	out := make([]domain.CabinetPricesScope, len(rows))
	for i, r := range rows {
		item := domain.CabinetPricesScope{
			SellerCabinetID:   uuidFromPgtype(r.ID),
			Name:              r.Name,
			PricesScopeStatus: r.PricesScopeStatus,
		}
		if r.PricesScopeCheckedAt.Valid {
			t := r.PricesScopeCheckedAt.Time
			item.CheckedAt = &t
		}
		out[i] = item
	}
	return out, nil
}

// ListChanges returns price changes for a workspace matching the filter.
func (s *RepricerService) ListChanges(ctx context.Context, workspaceID uuid.UUID, f domain.PriceChangeFilter) ([]domain.PriceChange, error) {
	arg := sqlcgen.ListPriceChangesParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       f.Limit,
		Offset:      f.Offset,
	}
	if f.WBProductID != nil {
		arg.WbProductID = pgtype.Int8{Int64: *f.WBProductID, Valid: true}
	}
	if f.Source != "" {
		arg.Source = pgtype.Text{String: f.Source, Valid: true}
	}
	if f.Status != "" {
		arg.WbStatus = pgtype.Text{String: f.Status, Valid: true}
	}
	rows, err := s.queries.ListPriceChanges(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PriceChange, len(rows))
	for i, row := range rows {
		out[i] = priceChangeFromSqlc(row)
	}
	return out, nil
}

// ListUploadTasks returns recent upload tasks for a workspace.
func (s *RepricerService) ListUploadTasks(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.PriceUploadTask, error) {
	rows, err := s.queries.ListPriceUploadTasksByWorkspace(ctx, uuidToPgtype(workspaceID), limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]domain.PriceUploadTask, len(rows))
	for i, row := range rows {
		out[i] = priceUploadTaskFromSqlc(row)
	}
	return out, nil
}

func priceUploadTaskFromSqlc(row sqlcgen.PriceUploadTask) domain.PriceUploadTask {
	t := domain.PriceUploadTask{
		ID:              uuidFromPgtype(row.ID),
		WorkspaceID:     uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
		WBTaskID:        row.WbTaskID,
		Status:          row.Status,
		ItemsCount:      int(row.ItemsCount),
		PollCount:       int(row.PollCount),
	}
	if row.LastPolledAt.Valid {
		v := row.LastPolledAt.Time
		t.LastPolledAt = &v
	}
	if row.CompletedAt.Valid {
		v := row.CompletedAt.Time
		t.CompletedAt = &v
	}
	if row.Error.Valid {
		t.Error = &row.Error.String
	}
	if row.CreatedAt.Valid {
		t.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		t.UpdatedAt = row.UpdatedAt.Time
	}
	return t
}

// SyncQuarantine refreshes the price-quarantine list per cabinet: new detections
// are persisted (and logged), and products no longer quarantined are resolved.
// Returns the number of newly detected quarantined products.
func (s *RepricerService) SyncQuarantine(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	cabinets, err := s.queries.ListActiveSellerCabinetsByWorkspace(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return 0, err
	}
	newlyDetected := 0
	for _, cabinet := range cabinets {
		cabinetID := uuidFromPgtype(cabinet.ID)
		if s.pricesEndpointCoolingDown(ctx, cabinetID, wbEndpointPricesQuarantine) {
			continue
		}
		token, decErr := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
		if decErr != nil {
			continue
		}
		goods := make([]wb.QuarantineGood, 0)
		quarantineSyncFailed := false
		for offset := 0; ; offset += priceListPageSize {
			page, listErr := s.wbClient.ListQuarantineGoods(ctx, token, priceListPageSize, offset)
			if listErr != nil {
				if errors.Is(listErr, wb.ErrPricesScopeMissing) {
					s.markCabinetScope(ctx, cabinetID, domain.PricesScopeMissing)
				}
				s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesQuarantine, listErr)
				quarantineSyncFailed = true
				break
			}
			goods = append(goods, page...)
			if len(page) < priceListPageSize {
				break
			}
		}
		if quarantineSyncFailed {
			continue
		}
		present := make([]int64, 0, len(goods))
		var newNms []int64
		for _, g := range goods {
			present = append(present, g.NmID)
			row, upErr := s.queries.UpsertQuarantineGood(ctx, sqlcgen.UpsertQuarantineGoodParams{
				WorkspaceID:     uuidToPgtype(workspaceID),
				SellerCabinetID: uuidToPgtype(cabinetID),
				WbProductID:     g.NmID,
				OldPriceRub:     pgtype.Int8{Int64: g.OldPrice, Valid: g.OldPrice > 0},
				NewPriceRub:     pgtype.Int8{Int64: g.NewPrice, Valid: g.NewPrice > 0},
			})
			if errors.Is(upErr, pgx.ErrNoRows) {
				continue // already active in quarantine
			}
			if upErr != nil {
				s.logger.Warn().Err(upErr).Int64("wb_product_id", g.NmID).Msg("failed to upsert quarantine good")
				continue
			}
			newlyDetected++
			newNms = append(newNms, g.NmID)
			s.logger.Warn().
				Str("workspace_id", workspaceID.String()).
				Int64("wb_product_id", g.NmID).
				Msg("product entered WB price quarantine (release is cabinet-UI-only)")
			if err := s.queries.MarkQuarantineNotified(ctx, row.ID); err != nil {
				s.logger.Warn().Err(err).Msg("failed to mark quarantine notified")
			}
		}
		if len(newNms) > 0 && s.notifications != nil {
			sample := newNms
			if len(sample) > 10 {
				sample = sample[:10]
			}
			s.notifications.NotifyPriceQuarantine(ctx, workspaceID, len(newNms), sample)
		}
		if err := s.queries.ResolveQuarantineGoodsExcept(ctx, uuidToPgtype(cabinetID), present); err != nil {
			s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("failed to resolve cleared quarantine goods")
		}
	}
	return newlyDetected, nil
}

// ListQuarantine returns active quarantine goods for a workspace.
func (s *RepricerService) ListQuarantine(ctx context.Context, workspaceID uuid.UUID) ([]domain.PriceQuarantineGood, error) {
	rows, err := s.queries.ListActiveQuarantineGoods(ctx, uuidToPgtype(workspaceID))
	if err != nil {
		return nil, err
	}
	out := make([]domain.PriceQuarantineGood, len(rows))
	for i, row := range rows {
		g := domain.PriceQuarantineGood{
			ID:              uuidFromPgtype(row.ID),
			WorkspaceID:     uuidFromPgtype(row.WorkspaceID),
			SellerCabinetID: uuidFromPgtype(row.SellerCabinetID),
			WBProductID:     row.WbProductID,
			Notified:        row.Notified,
		}
		if row.OldPriceRub.Valid {
			v := row.OldPriceRub.Int64
			g.OldPriceRub = &v
		}
		if row.NewPriceRub.Valid {
			v := row.NewPriceRub.Int64
			g.NewPriceRub = &v
		}
		if row.DetectedAt.Valid {
			g.DetectedAt = row.DetectedAt.Time
		}
		out[i] = g
	}
	return out, nil
}

func (s *RepricerService) markCabinetScope(ctx context.Context, cabinetID uuid.UUID, status string) {
	if err := s.queries.SetCabinetPricesScopeStatus(ctx, uuidToPgtype(cabinetID), status); err != nil {
		s.logger.Warn().Err(err).Str("cabinet_id", cabinetID.String()).Msg("failed to set cabinet prices scope status")
	}
}

// pricesEndpointCoolingDown reports whether a persisted rate-limit window is still active.
func (s *RepricerService) pricesEndpointCoolingDown(ctx context.Context, cabinetID uuid.UUID, endpoint string) bool {
	limit, err := s.queries.GetWBAPIRateLimit(ctx, uuidToPgtype(cabinetID), wbRateLimitStorageKey(endpoint))
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return false
	}
	if !limit.NextAllowedAt.Valid {
		return false
	}
	return limit.NextAllowedAt.Time.UTC().After(time.Now().UTC())
}

// recordPricesRateLimit persists a cooldown window when err is a WB rate-limit error.
func (s *RepricerService) recordPricesRateLimit(ctx context.Context, cabinetID uuid.UUID, endpoint string, err error) {
	if err == nil || !isRateLimitIssue(err.Error()) {
		return
	}
	delay := wbEndpointFallbackDelay(endpoint)
	next := time.Now().UTC().Add(delay)
	lastError := err.Error()
	if len(lastError) > 500 {
		lastError = lastError[:500]
	}
	if upErr := s.queries.UpsertWBAPIRateLimit(ctx, sqlcgen.UpsertWBAPIRateLimitParams{
		SellerCabinetID:   uuidToPgtype(cabinetID),
		EndpointKey:       wbRateLimitStorageKey(endpoint),
		NextAllowedAt:     pgtype.Timestamptz{Time: next, Valid: true},
		RetryAfterSeconds: int32(math.Ceil(delay.Seconds())),
		LastStatus:        429,
		LastError:         pgtype.Text{String: lastError, Valid: true},
	}); upErr != nil {
		s.logger.Warn().Err(upErr).Str("endpoint", endpoint).Msg("failed to persist prices rate limit")
	}
}

func productPriceFromSqlc(row sqlcgen.ProductPrice) domain.ProductPrice {
	p := domain.ProductPrice{
		ID:                  uuidFromPgtype(row.ID),
		WorkspaceID:         uuidFromPgtype(row.WorkspaceID),
		SellerCabinetID:     uuidFromPgtype(row.SellerCabinetID),
		WBProductID:         row.WbProductID,
		PriceRub:            row.PriceRub,
		DiscountPercent:     int(row.DiscountPercent),
		ClubDiscountPercent: int(row.ClubDiscountPercent),
		EditableSizePrice:   row.EditableSizePrice,
	}
	if row.DiscountedPriceRub.Valid {
		v := row.DiscountedPriceRub.Int64
		p.DiscountedPriceRub = &v
	}
	if row.SyncedAt.Valid {
		p.SyncedAt = row.SyncedAt.Time
	}
	if row.UpdatedAt.Valid {
		p.UpdatedAt = row.UpdatedAt.Time
	}
	return p
}
