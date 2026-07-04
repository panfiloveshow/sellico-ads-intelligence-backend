package service

import (
	"context"
	"errors"
	"math"
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
	for _, cabinet := range cabinets {
		cabinetID := uuidFromPgtype(cabinet.ID)
		if s.pricesEndpointCoolingDown(ctx, cabinetID, wbEndpointPricesList) {
			s.logger.Info().Str("cabinet_id", cabinetID.String()).Msg("prices list cooling down, skipping cabinet")
			continue
		}
		token, decErr := crypto.Decrypt(cabinet.EncryptedToken, s.encryptionKey)
		if decErr != nil {
			s.logger.Warn().Err(decErr).Str("cabinet_id", cabinetID.String()).Msg("failed to decrypt cabinet token")
			continue
		}

		count, syncErr := s.syncCabinetPrices(ctx, workspaceID, cabinetID, token)
		if syncErr != nil {
			if errors.Is(syncErr, wb.ErrPricesScopeMissing) {
				s.markCabinetScope(ctx, cabinetID, domain.PricesScopeMissing)
				s.logger.Warn().Str("cabinet_id", cabinetID.String()).Msg("cabinet token missing prices scope")
				continue
			}
			s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesList, syncErr)
			s.logger.Warn().Err(syncErr).Str("cabinet_id", cabinetID.String()).Msg("price sync failed for cabinet")
			continue
		}
		s.markCabinetScope(ctx, cabinetID, domain.PricesScopeOK)
		total += count
		// Enrich product names/images from WB content cards so the priced
		// products show titles and photos. Best-effort — failures (e.g. no
		// content scope) don't fail the price sync.
		s.enrichCabinetProducts(ctx, workspaceID, cabinetID, token)
	}
	return total, nil
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

// ListPrices returns the synced current prices for a workspace (paginated),
// optionally narrowed to a single seller cabinet.
func (s *RepricerService) ListPrices(ctx context.Context, workspaceID uuid.UUID, cabinetID *uuid.UUID, limit, offset int32) ([]domain.ProductPrice, error) {
	cabinetFilter := pgtype.UUID{}
	if cabinetID != nil {
		cabinetFilter = uuidToPgtype(*cabinetID)
	}
	rows, err := s.queries.ListProductPricesFiltered(ctx, uuidToPgtype(workspaceID), cabinetFilter, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ProductPrice, len(rows))
	for i, row := range rows {
		out[i] = productPriceFromSqlc(row)
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
		goods, listErr := s.wbClient.ListQuarantineGoods(ctx, token, priceListPageSize, 0)
		if listErr != nil {
			if errors.Is(listErr, wb.ErrPricesScopeMissing) {
				s.markCabinetScope(ctx, cabinetID, domain.PricesScopeMissing)
				continue
			}
			s.recordPricesRateLimit(ctx, cabinetID, wbEndpointPricesQuarantine, listErr)
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
	limit, err := s.queries.GetWBAPIRateLimit(ctx, uuidToPgtype(cabinetID), endpoint)
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
		EndpointKey:       endpoint,
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
