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
	}
	return total, nil
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

// ListPrices returns the synced current prices for a workspace (paginated).
func (s *RepricerService) ListPrices(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.ProductPrice, error) {
	rows, err := s.queries.ListProductPricesByWorkspace(ctx, uuidToPgtype(workspaceID), limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ProductPrice, len(rows))
	for i, row := range rows {
		out[i] = productPriceFromSqlc(row)
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
