package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func seedDRREvidenceSales(t *testing.T, pool *pgxpool.Pool, cabinetID uuid.UUID, date time.Time, sku int64, revenue float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO ozon_sales_daily(seller_cabinet_id,sku,date,ordered_units,revenue_rub) VALUES($1,$2,$3,10,$4)`, cabinetID, sku, date, revenue)
	require.NoError(t, err)
}

func TestTotalDRREvidence_MissingOrMisalignedDataCannotAuthorizeAnIncrease(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }
	for _, scenario := range []string{"missing ads", "ad day missing", "sales day missing", "one campaign missing", "unknown campaign state", "inactive campaign incomplete", "many SKU rows on one day", "stale ads"} {
		t.Run(scenario, func(t *testing.T) {
			pool := testdb.New(t)
			cabinetID := testdb.OzonCabinet(t, pool, testdb.Workspace(t, pool), testdb.OzonCredentials())
			campaignID := testdb.OzonCampaign(t, pool, cabinetID, 101, "Evidence", "CAMPAIGN_STATE_RUNNING")
			for d := 4; d <= 6; d++ {
				if !(scenario == "sales day missing" && d == 5) && !(scenario == "many SKU rows on one day" && d != 6) {
					seedDRREvidenceSales(t, pool, cabinetID, day(d), 1, 1000)
				}
				if scenario != "missing ads" && !(scenario == "ad day missing" && d == 5) && !(scenario == "stale ads" && d > 4) {
					testdb.OzonCampaignStat(t, pool, campaignID, day(d), 100, 10, 10, 1, 100)
				}
			}
			if scenario == "one campaign missing" {
				testdb.OzonCampaign(t, pool, cabinetID, 102, "Unreported", "CAMPAIGN_STATE_RUNNING")
			}
			if scenario == "unknown campaign state" {
				testdb.OzonCampaign(t, pool, cabinetID, 102, "Unknown", "")
			}
			if scenario == "inactive campaign incomplete" {
				inactive := testdb.OzonCampaign(t, pool, cabinetID, 102, "Inactive", "CAMPAIGN_STATE_INACTIVE")
				testdb.OzonCampaignStat(t, pool, inactive, day(4), 100, 10, 10, 1, 100)
				testdb.OzonCampaignStat(t, pool, inactive, day(6), 100, 10, 10, 1, 100)
			}
			if scenario == "many SKU rows on one day" {
				seedDRREvidenceSales(t, pool, cabinetID, day(6), 2, 1000)
				seedDRREvidenceSales(t, pool, cabinetID, day(6), 3, 1000)
			}
			got := loadCabinetTotalDRR(context.Background(), sqlcgen.New(pool), zerolog.Nop(), cabinetID, day(4), now, 36)
			require.NotEqual(t, totalDRRStatusOK, got.Status, "unknown spend must not look like healthy zero DRR")
			require.NotEmpty(t, totalDRRIncreaseBlockReason(ptrFloat(10), got))
			if scenario == "stale ads" {
				require.Equal(t, totalDRRStatusStale, got.Status)
			}
		})
	}
}

func TestTotalDRREvidence_ActualZeroSpendIsValidAndCurrentDayIsExcluded(t *testing.T) {
	pool := testdb.New(t)
	cabinetID := testdb.OzonCabinet(t, pool, testdb.Workspace(t, pool), testdb.OzonCredentials())
	campaignID := testdb.OzonCampaign(t, pool, cabinetID, 101, "Evidence", "CAMPAIGN_STATE_RUNNING")
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for d := 4; d <= 7; d++ {
		day := time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC)
		seedDRREvidenceSales(t, pool, cabinetID, day, 1, 1000)
		spend := 0.0
		if d == 7 {
			spend = 50000
		}
		testdb.OzonCampaignStat(t, pool, campaignID, day, 100, 10, spend, 1, 100)
	}
	got := loadCabinetTotalDRR(context.Background(), sqlcgen.New(pool), zerolog.Nop(), cabinetID, now.AddDate(0, 0, -3), now, 36)
	require.Equal(t, totalDRRStatusOK, got.Status)
	require.Zero(t, got.Value)
	require.Zero(t, got.SpendRub)
	require.Equal(t, 3000.0, got.RevenueRub)
	require.Empty(t, totalDRRIncreaseBlockReason(ptrFloat(10), got))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failed := loadCabinetTotalDRR(ctx, sqlcgen.New(pool), zerolog.Nop(), cabinetID, now.AddDate(0, 0, -3), now, 36)
	require.Equal(t, totalDRRStatusNoData, failed.Status)
	require.NotEmpty(t, totalDRRIncreaseBlockReason(ptrFloat(10), failed))
}

func TestTotalDRREvidence_KnownCampaignDatesLimitCoverageRequirements(t *testing.T) {
	pool := testdb.New(t)
	cabinetID := testdb.OzonCabinet(t, pool, testdb.Workspace(t, pool), testdb.OzonCredentials())
	campaignID := testdb.OzonCampaign(t, pool, cabinetID, 101, "Evidence", "CAMPAIGN_STATE_RUNNING")
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for d := 4; d <= 6; d++ {
		day := time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC)
		seedDRREvidenceSales(t, pool, cabinetID, day, 1, 1000)
		testdb.OzonCampaignStat(t, pool, campaignID, day, 100, 10, 10, 1, 100)
	}
	started := testdb.OzonCampaign(t, pool, cabinetID, 102, "Started later", "CAMPAIGN_STATE_RUNNING")
	expired := testdb.OzonCampaign(t, pool, cabinetID, 103, "Expired", "CAMPAIGN_STATE_INACTIVE")
	future := testdb.OzonCampaign(t, pool, cabinetID, 104, "Future", "CAMPAIGN_STATE_INACTIVE")
	_, err := pool.Exec(context.Background(), `UPDATE ozon_campaigns SET from_date=CASE WHEN id=$1 THEN '2026-08-06'::date ELSE '2026-08-08'::date END WHERE id IN ($1,$2)`, started, future)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `UPDATE ozon_campaigns SET to_date='2026-08-03' WHERE id=$1`, expired)
	require.NoError(t, err)
	testdb.OzonCampaignStat(t, pool, started, time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC), 100, 10, 100, 1, 100)
	got := loadCabinetTotalDRR(context.Background(), sqlcgen.New(pool), zerolog.Nop(), cabinetID, now.AddDate(0, 0, -3), now, 36)
	require.Equal(t, totalDRRStatusOK, got.Status)
	require.Equal(t, 130.0, got.SpendRub)
	require.Equal(t, 4.33, got.Value)
}

func TestTotalDRREvidence_HistoricalInactiveCampaignsDoNotDemandInventedZeroRows(t *testing.T) {
	pool := testdb.New(t)
	cabinetID := testdb.OzonCabinet(t, pool, testdb.Workspace(t, pool), testdb.OzonCredentials())
	campaignID := testdb.OzonCampaign(t, pool, cabinetID, 101, "Evidence", "CAMPAIGN_STATE_RUNNING")
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for d := 4; d <= 6; d++ {
		day := time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC)
		seedDRREvidenceSales(t, pool, cabinetID, day, 1, 1000)
		testdb.OzonCampaignStat(t, pool, campaignID, day, 100, 10, 10, 1, 100)
	}
	for i, state := range []string{"CAMPAIGN_STATE_INACTIVE", "CAMPAIGN_STATE_STOPPED", "CAMPAIGN_STATE_ARCHIVED", "CAMPAIGN_STATE_FINISHED"} {
		inactive := testdb.OzonCampaign(t, pool, cabinetID, int64(102+i), "Historical", state)
		testdb.OzonCampaignStat(t, pool, inactive, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), 100, 10, 50000, 1, 100)
	}
	got := loadCabinetTotalDRR(context.Background(), sqlcgen.New(pool), zerolog.Nop(), cabinetID, now.AddDate(0, 0, -3), now, 36)
	require.Equal(t, totalDRRStatusOK, got.Status)
	require.Equal(t, 30.0, got.SpendRub)
	require.Equal(t, 1.0, got.Value)
}

func TestIncrementalDRREvidence_EqualCompleteWindowsExcludeToday(t *testing.T) {
	pool := testdb.New(t)
	cabinetID := testdb.OzonCabinet(t, pool, testdb.Workspace(t, pool), testdb.OzonCredentials())
	campaignID := testdb.OzonCampaign(t, pool, cabinetID, 101, "Evidence", "CAMPAIGN_STATE_RUNNING")
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	for d := 1; d <= 7; d++ {
		day := time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC)
		spend, revenue := 100.0, 1000.0
		if d >= 4 {
			spend, revenue = 200, 2000
		}
		if d == 7 {
			spend, revenue = 50000, 1
		}
		seedDRREvidenceSales(t, pool, cabinetID, day, 1, revenue)
		testdb.OzonCampaignStat(t, pool, campaignID, day, 100, 10, spend, 1, 100)
	}
	queries := sqlcgen.New(pool)
	got := loadIncrementalDRR(context.Background(), queries, zerolog.Nop(), cabinetID, now, 3)
	require.Equal(t, incrementalDRRAccretive, got.Verdict)
	require.Equal(t, 10.0, got.Value, "300 extra rubles / 3000 extra turnover across equal three-day windows")
	require.Equal(t, 300.0, got.SpendDeltaRub)
	require.Equal(t, 3000.0, got.TurnoverDeltaRub)
	_, err := pool.Exec(context.Background(), `DELETE FROM ozon_campaign_stats WHERE campaign_id=$1 AND date='2026-08-05'`, campaignID)
	require.NoError(t, err)
	incomplete := loadIncrementalDRR(context.Background(), queries, zerolog.Nop(), cabinetID, now, 3)
	require.Equal(t, incrementalDRRNotEnoughData, incomplete.Verdict)
	require.Zero(t, incomplete.Value)
}

func TestCampaignAttributedTurnover_OnlyCompletedDaysAffectRevenueAndWeights(t *testing.T) {
	pool := testdb.New(t)
	cabinetID := testdb.OzonCabinet(t, pool, testdb.Workspace(t, pool), testdb.OzonCredentials())
	first := testdb.OzonCampaign(t, pool, cabinetID, 101, "First", "CAMPAIGN_STATE_RUNNING")
	second := testdb.OzonCampaign(t, pool, cabinetID, 102, "Second", "CAMPAIGN_STATE_RUNNING")
	for _, campaignID := range []uuid.UUID{first, second} {
		testdb.OzonCampaignProduct(t, pool, campaignID, 1, 10)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)
	seedDRREvidenceSales(t, pool, cabinetID, yesterday, 1, 1000)
	testdb.OzonCampaignStat(t, pool, first, yesterday, 100, 10, 100, 1, 100)
	testdb.OzonCampaignStat(t, pool, second, yesterday, 100, 10, 300, 1, 100)
	// Both unclosed and erroneous future rows must be outside every component
	// of attribution, including the campaign-spend weights.
	for offset := 0; offset <= 1; offset++ {
		date := today.AddDate(0, 0, offset)
		seedDRREvidenceSales(t, pool, cabinetID, date, 1, 100000)
		testdb.OzonCampaignStat(t, pool, first, date, 100, 10, 9000, 1, 100)
	}
	queries := sqlcgen.New(pool)
	rows := loadCampaignAttributedTurnover(context.Background(), queries, zerolog.Nop(), cabinetID, yesterday)
	require.Len(t, rows, 2)
	require.Equal(t, 250.0, pgNumericToFloat(rows[first].RevenueRub))
	require.Equal(t, 750.0, pgNumericToFloat(rows[second].RevenueRub))
	require.True(t, rows[first].RevenueShared)
	require.Equal(t, yesterday, rows[first].LastDate.Time)
	require.Empty(t, loadCampaignAttributedTurnover(context.Background(), sqlcgen.New(pool), zerolog.Nop(), cabinetID, today))
	agg, err := queries.AggregateOzonCampaignStatsWindow(context.Background(), uuidToPgtype(first),
		pgtype.Date{Time: yesterday, Valid: true}, pgtype.Date{Time: yesterday, Valid: true})
	require.NoError(t, err)
	require.Equal(t, 100.0, pgNumericToFloat(agg.SpendRub))
	require.EqualValues(t, 10, agg.Clicks)
	require.EqualValues(t, 1, agg.Orders)
}
