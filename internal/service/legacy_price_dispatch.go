package service

import (
	"context"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

var ErrInvalidLegacyPriceDispatch = errors.New("invalid legacy price dispatch attestation")

// ErrLegacyPriceDispatchRetired: by the owner's decision prices change only
// through personally approved commands. A retired sender records no admission,
// so it can never create a new account-wide hold.
var ErrLegacyPriceDispatchRetired = errors.New("legacy price dispatch retired")
var legacyPriceSHA = regexp.MustCompile(`^[a-f0-9]{64}$`)

type LegacyPriceDispatchService struct {
	queries *sqlcgen.Queries
	enabled bool
}

// enabled comes from LEGACY_PRICE_DISPATCH_ENABLED and is off by default.
func NewLegacyPriceDispatchService(q *sqlcgen.Queries, enabled bool) *LegacyPriceDispatchService {
	return &LegacyPriceDispatchService{queries: q, enabled: enabled}
}

func (s *LegacyPriceDispatchService) Authorize(ctx context.Context, r domain.LegacyPriceDispatchRequest) (domain.LegacyPriceDispatchGrant, error) {
	id, err := uuid.Parse(r.CommandID)
	validAccount := len(r.AccountKey) == len(r.Marketplace)+1+64 && len(r.Marketplace) > 0 && r.AccountKey[:len(r.Marketplace)+1] == r.Marketplace+":" && legacyPriceSHA.MatchString(r.AccountKey[len(r.Marketplace)+1:])
	if r.Version != "legacy_price_dispatch_v1" || r.Origin != "products" || r.Method != "POST" || err != nil || id.Version() != 4 || id.String() != r.CommandID || r.SentAt <= 0 || !validAccount || !legacyPriceSHA.MatchString(r.PayloadSHA256) || !legacyPriceEndpoint(r.Marketplace, r.Endpoint) {
		return domain.LegacyPriceDispatchGrant{}, ErrInvalidLegacyPriceDispatch
	}
	if s == nil || s.queries == nil {
		return domain.LegacyPriceDispatchGrant{}, errors.New("price dispatch authorization unavailable")
	}
	if !s.enabled {
		return domain.LegacyPriceDispatchGrant{}, ErrLegacyPriceDispatchRetired
	}
	return s.queries.AuthorizeLegacyPriceDispatch(ctx, r)
}

func legacyPriceEndpoint(market, endpoint string) bool {
	if market == "wb" {
		switch endpoint {
		case "/api/v2/upload/task", "/api/v2/upload/task/size", "/api/v2/upload/task/club-discount":
			return true
		}
	}
	if market == "ozon" {
		switch endpoint {
		case "/v1/product/import/prices", "/v3/product/import", "/v1/actions/products/activate", "/v1/actions/products/deactivate", "/v1/actions/hotsales/activate", "/v1/actions/hotsales/deactivate":
			return true
		}
	}
	return false
}
