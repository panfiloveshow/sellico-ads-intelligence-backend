package sqlcgen

import "github.com/jackc/pgx/v5/pgtype"

// OzonAiWeeklyReport mirrors the ozon_ai_weekly_reports table (migration
// 000057). Kept here under the selective-retention convention: the generated
// queries in ozon_ai.sql.go scan into this type, but the base models.go stays
// curated and free of full-schema regeneration.
type OzonAiWeeklyReport struct {
	ID              pgtype.UUID        `json:"id"`
	SellerCabinetID pgtype.UUID        `json:"seller_cabinet_id"`
	PeriodStart     pgtype.Date        `json:"period_start"`
	PeriodEnd       pgtype.Date        `json:"period_end"`
	DrrStart        pgtype.Numeric     `json:"drr_start"`
	DrrEnd          pgtype.Numeric     `json:"drr_end"`
	Text            string             `json:"text"`
	GeneratedAt     pgtype.Timestamptz `json:"generated_at"`
}
