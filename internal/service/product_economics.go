package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type ProductEconomicsService struct {
	queries *sqlcgen.Queries
}

func NewProductEconomicsService(queries *sqlcgen.Queries) *ProductEconomicsService {
	return &ProductEconomicsService{queries: queries}
}

func (s *ProductEconomicsService) List(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]domain.ProductEconomics, error) {
	rows, err := s.queries.ListProductEconomicsByWorkspace(ctx, uuidToPgtype(workspaceID), limit, offset)
	if err != nil {
		return nil, apperror.New(apperror.ErrInternal, "failed to list product economics")
	}
	items := make([]domain.ProductEconomics, len(rows))
	for i, row := range rows {
		items[i] = productEconomicsFromSqlc(row)
	}
	return items, nil
}

func (s *ProductEconomicsService) Import(ctx context.Context, actorID, workspaceID uuid.UUID, rows []domain.ProductEconomicsInput) (*domain.ProductEconomicsImportResult, error) {
	// updated_by → NULL for system imports (uuid.Nil); it's an FK to users(id).
	updatedBy := uuidToPgtype(actorID)
	if actorID == uuid.Nil {
		updatedBy = pgtype.UUID{}
	}
	result := &domain.ProductEconomicsImportResult{Items: make([]domain.ProductEconomics, 0, len(rows))}
	for index, row := range rows {
		normalized, err := validateProductEconomicsInput(row)
		if err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: %s", index+1, err.Error()))
			continue
		}
		saved, err := s.queries.UpsertProductEconomics(ctx, sqlcgen.UpsertProductEconomicsParams{
			WorkspaceID:         uuidToPgtype(workspaceID),
			WbProductID:         normalized.WBProductID,
			CostPrice:           int64PtrToPgtype(normalized.CostPrice),
			LogisticsCost:       int64PtrToPgtype(normalized.LogisticsCost),
			OtherCosts:          int64PtrToPgtype(normalized.OtherCosts),
			TaxRatePercent:      float64PtrToPgtype(normalized.TaxRatePercent),
			CommissionPercent:   float64PtrToPgtype(normalized.CommissionPercent),
			TargetMarginPercent: float64PtrToPgtype(normalized.TargetMarginPercent),
			MaxAllowedDrr:       float64PtrToPgtype(normalized.MaxAllowedDRR),
			Source:              normalized.Source,
			EffectiveAt:         timePtrToPgDate(normalized.EffectiveAt),
			UpdatedBy:           updatedBy,
		})
		if err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("row %d: failed to save product economics", index+1))
			continue
		}
		result.Imported++
		result.Items = append(result.Items, productEconomicsFromSqlc(saved))
	}
	return result, nil
}

func validateProductEconomicsInput(input domain.ProductEconomicsInput) (domain.ProductEconomicsInput, error) {
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		input.Source = "manual"
	}
	if input.WBProductID <= 0 {
		return input, fmt.Errorf("wb_product_id must be positive")
	}
	if input.CostPrice == nil &&
		input.LogisticsCost == nil &&
		input.OtherCosts == nil &&
		input.TaxRatePercent == nil &&
		input.CommissionPercent == nil &&
		input.TargetMarginPercent == nil &&
		input.MaxAllowedDRR == nil {
		return input, fmt.Errorf("at least one economics field is required")
	}
	if err := validateNonNegativeInt64("cost_price", input.CostPrice); err != nil {
		return input, err
	}
	if err := validateNonNegativeInt64("logistics_cost", input.LogisticsCost); err != nil {
		return input, err
	}
	if err := validateNonNegativeInt64("other_costs", input.OtherCosts); err != nil {
		return input, err
	}
	for name, value := range map[string]*float64{
		"tax_rate_percent":      input.TaxRatePercent,
		"commission_percent":    input.CommissionPercent,
		"target_margin_percent": input.TargetMarginPercent,
		"max_allowed_drr":       input.MaxAllowedDRR,
	} {
		if err := validatePercent(name, value); err != nil {
			return input, err
		}
	}
	return input, nil
}

func validateNonNegativeInt64(name string, value *int64) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("%s must be non-negative", name)
	}
	return nil
}

func validatePercent(name string, value *float64) error {
	if value != nil && (*value < 0 || *value > 100) {
		return fmt.Errorf("%s must be between 0 and 100", name)
	}
	return nil
}

func productEconomicsFromSqlc(row sqlcgen.ProductEconomics) domain.ProductEconomics {
	item := domain.ProductEconomics{
		ID:                  uuidFromPgtype(row.ID),
		WorkspaceID:         uuidFromPgtype(row.WorkspaceID),
		WBProductID:         row.WbProductID,
		CostPrice:           int8ToPtr(row.CostPrice),
		LogisticsCost:       int8ToPtr(row.LogisticsCost),
		OtherCosts:          int8ToPtr(row.OtherCosts),
		TaxRatePercent:      float8ToPtr(row.TaxRatePercent),
		CommissionPercent:   float8ToPtr(row.CommissionPercent),
		TargetMarginPercent: float8ToPtr(row.TargetMarginPercent),
		MaxAllowedDRR:       float8ToPtr(row.MaxAllowedDrr),
		Source:              row.Source,
		CreatedAt:           row.CreatedAt.Time,
		UpdatedAt:           row.UpdatedAt.Time,
	}
	if row.EffectiveAt.Valid {
		effectiveAt := row.EffectiveAt.Time
		item.EffectiveAt = &effectiveAt
	}
	if row.UpdatedBy.Valid {
		updatedBy := uuidFromPgtype(row.UpdatedBy)
		item.UpdatedBy = &updatedBy
	}
	return item
}

func int64PtrToPgtype(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func float64PtrToPgtype(value *float64) pgtype.Float8 {
	if value == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *value, Valid: true}
}

func timePtrToPgDate(value *time.Time) pgtype.Date {
	if value == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *value, Valid: true}
}
