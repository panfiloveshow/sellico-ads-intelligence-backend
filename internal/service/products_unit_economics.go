package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type productsReadinessClient interface {
	CheckUnitEconomicsReadiness(ctx context.Context, serviceToken, path string, req sellico.UnitEconomicsReadinessRequest) (*sellico.UnitEconomicsReadinessResponse, error)
	ListWBUnitEconomics(ctx context.Context, serviceToken, path, integrationID string) ([]sellico.WBUnitEconomics, error)
}

type productsCabinetReader interface {
	GetSellerCabinetByID(ctx context.Context, id pgtype.UUID) (sqlcgen.SellerCabinet, error)
}

const productsReadinessMaxWBProductIDs = 100
const profitCeilingMaxAge = 24 * time.Hour

// ProductsUnitEconomicsReadinessProvider checks the products backend with the
// same service token used by the economics export. It validates cabinet tenant,
// integration echo, exact product coverage and freshness before returning ready.
type ProductsUnitEconomicsReadinessProvider struct {
	queries       productsCabinetReader
	client        productsReadinessClient
	token         string
	readinessPath string
	exportPath    string
	maxAge        time.Duration
	now           func() time.Time
}

func NewProductsUnitEconomicsReadinessProvider(queries productsCabinetReader, client productsReadinessClient, token, readinessPath, exportPath string, maxAge time.Duration) *ProductsUnitEconomicsReadinessProvider {
	if maxAge <= 0 {
		maxAge = 72 * time.Hour
	}
	return &ProductsUnitEconomicsReadinessProvider{
		queries:       queries,
		client:        client,
		token:         strings.TrimSpace(token),
		readinessPath: strings.TrimSpace(readinessPath),
		exportPath:    strings.TrimSpace(exportPath),
		maxAge:        maxAge,
		now:           time.Now,
	}
}

func (p *ProductsUnitEconomicsReadinessProvider) CheckBidIncreaseReadiness(ctx context.Context, input UnitEconomicsReadinessInput) (*UnitEconomicsReadiness, error) {
	if p == nil || p.queries == nil || p.client == nil || p.token == "" || p.readinessPath == "" || p.exportPath == "" {
		return nil, fmt.Errorf("products unit economics readiness is not configured")
	}
	requested := normalizedPositiveIDs(input.WBProductIDs)
	if len(requested) == 0 || len(requested) != len(input.WBProductIDs) {
		return nil, fmt.Errorf("products unit economics readiness requires unique positive WB product IDs")
	}

	cabinet, err := p.queries.GetSellerCabinetByID(ctx, uuidToPgtype(input.SellerCabinetID))
	if err != nil {
		return nil, fmt.Errorf("load seller cabinet for unit economics readiness: %w", err)
	}
	if uuidFromPgtype(cabinet.WorkspaceID) != input.WorkspaceID {
		return nil, fmt.Errorf("seller cabinet does not belong to workspace")
	}
	integrationID := pgTextValue(cabinet.ExternalIntegrationID)
	if integrationID == "" {
		return nil, fmt.Errorf("seller cabinet has no products integration ID")
	}

	var source string
	var checkedAt time.Time
	checkedProductIDs := make([]int64, 0, len(requested))
	missingIDs := make(map[int64]struct{})
	unprofitableIDs := make(map[int64]struct{})
	staleIDs := make(map[int64]struct{})
	blockedIDs := make(map[int64]struct{})
	minMaxAllowedDRR := 101.0
	now := p.now().UTC()
	productIDs := uuidStrings(input.ProductIDs)
	for chunkStart := 0; chunkStart < len(requested); chunkStart += productsReadinessMaxWBProductIDs {
		chunkEnd := chunkStart + productsReadinessMaxWBProductIDs
		if chunkEnd > len(requested) {
			chunkEnd = len(requested)
		}
		chunk := requested[chunkStart:chunkEnd]
		response, err := p.client.CheckUnitEconomicsReadiness(ctx, p.token, p.readinessPath, sellico.UnitEconomicsReadinessRequest{
			IntegrationID:   integrationID,
			WorkspaceID:     input.WorkspaceID.String(),
			SellerCabinetID: input.SellerCabinetID.String(),
			ProductIDs:      productIDs,
			WBProductIDs:    chunk,
		})
		chunkNumber := chunkStart/productsReadinessMaxWBProductIDs + 1
		if err != nil {
			return nil, fmt.Errorf("products unit economics readiness chunk %d: %w", chunkNumber, err)
		}
		if response == nil {
			return nil, fmt.Errorf("products unit economics readiness chunk %d returned no response", chunkNumber)
		}
		if !response.Complete {
			return nil, fmt.Errorf("products unit economics readiness response is incomplete for chunk %d", chunkNumber)
		}
		if response.IntegrationID != integrationID {
			return nil, fmt.Errorf("products unit economics readiness integration mismatch for chunk %d", chunkNumber)
		}
		if !sameExactIDs(chunk, response.CheckedProductIDs) {
			return nil, fmt.Errorf("products unit economics readiness product coverage mismatch for chunk %d", chunkNumber)
		}
		chunkSource := strings.TrimSpace(response.Source)
		if chunkSource == "" {
			return nil, fmt.Errorf("products unit economics readiness source is missing for chunk %d", chunkNumber)
		}
		if source == "" {
			source = chunkSource
		} else if chunkSource != source {
			return nil, fmt.Errorf("products unit economics readiness source mismatch for chunk %d", chunkNumber)
		}
		chunkCheckedAt := response.CheckedAt.UTC()
		if chunkCheckedAt.IsZero() || chunkCheckedAt.Before(now.Add(-p.maxAge)) || chunkCheckedAt.After(now.Add(5*time.Minute)) {
			return nil, fmt.Errorf("products unit economics readiness timestamp is stale or invalid for chunk %d", chunkNumber)
		}
		if checkedAt.IsZero() || chunkCheckedAt.Before(checkedAt) {
			checkedAt = chunkCheckedAt
		}

		checkedProductIDs = append(checkedProductIDs, chunk...)
		mergeReadinessIDs(missingIDs, blockedIDs, response.MissingEconomicsProductIDs)
		mergeReadinessIDs(unprofitableIDs, blockedIDs, response.UnprofitableProductIDs)
		mergeReadinessIDs(staleIDs, blockedIDs, response.StaleProductIDs)
		for _, nmID := range chunk {
			if _, blocked := blockedIDs[nmID]; blocked {
				continue
			}
			ceiling, ok := response.MaxAllowedDRRByProduct[nmID]
			if !ok || ceiling <= 0 || ceiling > 100 {
				return nil, fmt.Errorf("products unit economics readiness DRR ceiling is missing for WB product %d", nmID)
			}
			if ceiling < minMaxAllowedDRR {
				minMaxAllowedDRR = ceiling
			}
		}
	}
	if len(blockedIDs) > 0 {
		minMaxAllowedDRR = 0
	}

	maxAllowedCPO := 0.0
	buyerPrice := 0.0
	if len(blockedIDs) == 0 {
		maxAllowedCPO, buyerPrice, err = p.loadExactProfitCeilings(ctx, integrationID, requested, now)
		if err != nil {
			return nil, err
		}
	}

	return &UnitEconomicsReadiness{
		IntegrationID:              integrationID,
		Source:                     source,
		CheckedAt:                  checkedAt,
		Complete:                   true,
		CheckedProductIDs:          checkedProductIDs,
		MissingEconomicsProductIDs: sortedReadinessIDs(missingIDs),
		UnprofitableProductIDs:     sortedReadinessIDs(unprofitableIDs),
		StaleProductIDs:            sortedReadinessIDs(staleIDs),
		MaxAllowedDRRPercent:       minMaxAllowedDRR,
		MaxAllowedCPO:              maxAllowedCPO,
		BuyerPrice:                 buyerPrice,
	}, nil
}

func (p *ProductsUnitEconomicsReadinessProvider) loadExactProfitCeilings(ctx context.Context, integrationID string, requested []int64, now time.Time) (float64, float64, error) {
	rows, err := p.client.ListWBUnitEconomics(ctx, p.token, p.exportPath, integrationID)
	if err != nil {
		return 0, 0, fmt.Errorf("products unit economics export: %w", err)
	}
	byProduct := make(map[int64][]sellico.WBUnitEconomics, len(rows))
	for _, row := range rows {
		byProduct[row.NmID] = append(byProduct[row.NmID], row)
	}
	minCPO := 0.0
	singleBuyerPrice := 0.0
	for _, nmID := range requested {
		variants := byProduct[nmID]
		if len(variants) == 0 {
			return 0, 0, fmt.Errorf("products unit economics CPO ceiling is unavailable for WB product %d", nmID)
		}
		for _, row := range variants {
			if !row.Ready || row.MarginBeforeAds == nil || *row.MarginBeforeAds <= 0 {
				return 0, 0, fmt.Errorf("products unit economics CPO ceiling is unavailable for WB product %d", nmID)
			}
			if row.CalculatedAt == nil {
				return 0, 0, fmt.Errorf("products unit economics calculation time is unavailable for WB product %d", nmID)
			}
			maxAge := p.maxAge
			if maxAge > profitCeilingMaxAge {
				maxAge = profitCeilingMaxAge
			}
			age := now.Sub(row.CalculatedAt.UTC())
			if age < 0 || age > maxAge {
				return 0, 0, fmt.Errorf("products unit economics export is stale for WB product %d", nmID)
			}
			if minCPO == 0 || *row.MarginBeforeAds < minCPO {
				minCPO = *row.MarginBeforeAds
			}
			if len(requested) == 1 {
				if row.CustomerPrice == nil || *row.CustomerPrice <= 0 {
					return 0, 0, fmt.Errorf("products unit economics buyer price is unavailable for WB product %d", nmID)
				}
				if singleBuyerPrice == 0 || *row.CustomerPrice < singleBuyerPrice {
					singleBuyerPrice = *row.CustomerPrice
				}
			}
		}
	}
	return minCPO, singleBuyerPrice, nil
}

func mergeReadinessIDs(target, blocked map[int64]struct{}, ids []int64) {
	for _, id := range ids {
		target[id] = struct{}{}
		blocked[id] = struct{}{}
	}
}

func sortedReadinessIDs(ids map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func normalizedPositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sameExactIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	rightNormalized := normalizedPositiveIDs(right)
	if len(rightNormalized) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != rightNormalized[i] {
			return false
		}
	}
	return true
}
