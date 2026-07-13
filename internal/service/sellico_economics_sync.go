package service

import (
	"context"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// economicsImporter is the slice of ProductEconomicsService the sync needs.
type economicsImporter interface {
	Import(ctx context.Context, actorID, workspaceID uuid.UUID, rows []domain.ProductEconomicsInput) (*domain.ProductEconomicsImportResult, error)
}

type sellicoEconomicsClient interface {
	ListWBUnitEconomics(ctx context.Context, serviceToken, path, integrationID string) ([]sellico.WBUnitEconomics, error)
}

// SellicoEconomicsSyncService pulls per-product cost/commission/tax from the
// products-backend unit economics and mirrors it into product_economics so the
// repricer margin-floor strategy has real data. Backend-to-backend: authenticates
// with a shared service token. Runs on a schedule; no user input needed.
type SellicoEconomicsSyncService struct {
	queries   *sqlcgen.Queries
	client    sellicoEconomicsClient
	token     string
	economics economicsImporter
	path      string
	logger    zerolog.Logger
}

func NewSellicoEconomicsSyncService(queries *sqlcgen.Queries, client sellicoEconomicsClient, token string, economics economicsImporter, path string, logger zerolog.Logger) *SellicoEconomicsSyncService {
	return &SellicoEconomicsSyncService{
		queries:   queries,
		client:    client,
		token:     token,
		economics: economics,
		path:      path,
		logger:    logger.With().Str("component", "sellico_economics_sync").Logger(),
	}
}

// Configured reports whether the bridge can run (client + token + path present).
func (s *SellicoEconomicsSyncService) Configured() bool {
	return s != nil && s.client != nil && s.token != "" && s.path != ""
}

// SyncWorkspace mirrors Sellico cost data into product_economics for every
// Sellico-linked cabinet of the workspace. Returns how many products were imported.
func (s *SellicoEconomicsSyncService) SyncWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	if !s.Configured() {
		return 0, nil // bridge disabled (no service account / path) — nothing to do
	}

	cabinets, err := s.queries.ListSellerCabinetsByWorkspace(ctx, sqlcgen.ListSellerCabinetsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       1000,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	imported := 0
	for _, cab := range cabinets {
		integrationID := pgTextValue(cab.ExternalIntegrationID)
		if integrationID == "" {
			continue // manual cabinet, no Sellico link
		}

		rows, err := s.client.ListWBUnitEconomics(ctx, s.token, s.path, integrationID)
		if err != nil {
			s.logger.Warn().Err(err).Str("integration_id", integrationID).Msg("fetch sellico economics failed")
			continue
		}

		inputs := buildEconomicsInputs(rows)
		if len(inputs) == 0 {
			continue
		}
		result, err := s.economics.Import(ctx, uuid.Nil, workspaceID, inputs)
		if err != nil {
			s.logger.Warn().Err(err).Str("integration_id", integrationID).Msg("import sellico economics failed")
			continue
		}
		imported += result.Imported
		for _, row := range rows {
			spp, customer := importedPricePresentation(row)
			if !spp.Valid && !customer.Valid {
				continue
			}
			if err := s.queries.UpdateProductPriceSPP(ctx, uuidToPgtype(workspaceID), cab.ID, row.NmID, spp, customer); err != nil {
				s.logger.Warn().Err(err).Int64("nm_id", row.NmID).Msg("import sellico SPP failed")
			}
		}
	}
	return imported, nil
}

func importedPricePresentation(row sellico.WBUnitEconomics) (pgtype.Float8, pgtype.Int8) {
	spp := pgtype.Float8{}
	if row.SppPercent != nil && *row.SppPercent >= 0 && *row.SppPercent <= 100 {
		spp = pgtype.Float8{Float64: *row.SppPercent, Valid: true}
	}
	customer := pgtype.Int8{}
	if row.CustomerPrice != nil && *row.CustomerPrice > 0 {
		customer = pgtype.Int8{Int64: int64(math.Round(*row.CustomerPrice)), Valid: true}
	}
	return spp, customer
}

// buildEconomicsInputs maps Sellico rows to product_economics inputs. Costs are
// rounded to integer rubles (repricer convention; ponytail: kopeck precision is
// noise against WB's ruble-granular prices).
func buildEconomicsInputs(rows []sellico.WBUnitEconomics) []domain.ProductEconomicsInput {
	inputs := make([]domain.ProductEconomicsInput, 0, len(rows))
	for _, r := range rows {
		if r.NmID <= 0 || r.CostPrice <= 0 {
			continue
		}
		cost := int64(math.Round(r.CostPrice))
		inputs = append(inputs, domain.ProductEconomicsInput{
			WBProductID:       r.NmID,
			CostPrice:         &cost,
			CommissionPercent: r.CommissionPercent,
			TaxRatePercent:    r.TaxPercent,
			Source:            "sellico",
		})
	}
	return inputs
}

func pgTextValue(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}
