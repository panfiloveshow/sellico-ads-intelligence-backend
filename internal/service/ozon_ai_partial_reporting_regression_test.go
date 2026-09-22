package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAIExecution_PartialResultSurvivesDatabaseFailure(t *testing.T) {
	cases := []struct {
		name             string
		trigger          string
		wantCalls        []int64
		wantSecondStatus string
	}{
		{
			name: "outcome persistence fails after Ozon accepts second write",
			trigger: `CREATE FUNCTION fail_second_ai_outcome() RETURNS trigger LANGUAGE plpgsql AS $$
			 BEGIN
			  IF NEW.target->>'ozon_campaign_id'='992' AND NEW.status='auto_applied' THEN
			   RAISE EXCEPTION 'test-only second outcome persistence failure';
			  END IF;
			  RETURN NEW;
			 END $$;
			 CREATE TRIGGER fail_second_ai_outcome BEFORE UPDATE ON ai_decisions
			 FOR EACH ROW EXECUTE FUNCTION fail_second_ai_outcome()`,
			wantCalls:        []int64{991, 992},
			wantSecondStatus: domain.AIDecisionStatusApproved,
		},
		{
			name: "second claim fails before contacting Ozon",
			trigger: `CREATE FUNCTION fail_second_ai_claim() RETURNS trigger LANGUAGE plpgsql AS $$
			 BEGIN
			  IF NEW.target->>'ozon_campaign_id'='992' THEN
			   RAISE EXCEPTION 'test-only second claim persistence failure';
			  END IF;
			  RETURN NEW;
			 END $$;
			 CREATE TRIGGER fail_second_ai_claim BEFORE INSERT ON ai_decisions
			 FOR EACH ROW EXECUTE FUNCTION fail_second_ai_claim()`,
			wantCalls: []int64{991},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newAIExecutionFixture(t)
			ctx := context.Background()
			budget := int64(10000)
			for _, ozonID := range []int64{991, 992} {
				c := seedOzonCampaign(t, fx.db, fx.cabinetID, ozonID, "CAMPAIGN_STATE_RUNNING", nil, &budget)
				_, err := fx.db.Pool.Exec(ctx, `UPDATE ozon_campaigns SET title=$2 WHERE id=$1`, c.ID, fmt.Sprintf("Campaign %d", ozonID))
				require.NoError(t, err)
				seedOzonCampaignStat(t, fx.db, c.ID, time.Now().UTC().AddDate(0, 0, -1), 1000, 100, 4, 300, 1000)
			}
			strategyID := seedAIStrategy(t, fx, 3)
			strategy, err := fx.db.Queries.GetStrategyByID(ctx, uuidToPgtype(strategyID))
			require.NoError(t, err)
			_, err = fx.db.Pool.Exec(ctx, tc.trigger)
			require.NoError(t, err)
			client := &fakeLLM{enabled: true, responses: []*llm.ChatResponse{submitProposalsResponse("Оба бюджета изменены, продажи сохранены", []map[string]any{
				{"action_type": "budget_change", "target": map[string]any{"ozon_campaign_id": 991}, "new_value": 8000},
				{"action_type": "budget_change", "target": map[string]any{"ozon_campaign_id": 992}, "new_value": 8000},
			})}}
			perf := &fakePerfClient{}
			mgr := newAIManagerWithPerf(fx.db, client, perf, perf)
			err = mgr.RunForCabinet(ctx, fx.workspaceID, fx.cabinetID, strategyFromSqlc(strategy), domain.AIRunTriggerManual)
			require.Error(t, err, "the persistence failure must remain visible to the caller")
			assert.Equal(t, tc.wantCalls, perf.budgetCalls)
			var firstStatus string
			require.NoError(t, fx.db.Pool.QueryRow(ctx, `SELECT status FROM ai_decisions WHERE target->>'ozon_campaign_id'='991'`).Scan(&firstStatus))
			assert.Equal(t, domain.AIDecisionStatusAutoApplied, firstStatus, "a later failure does not roll back a confirmed external action")
			if tc.wantSecondStatus != "" {
				var secondStatus string
				require.NoError(t, fx.db.Pool.QueryRow(ctx, `SELECT status FROM ai_decisions WHERE target->>'ozon_campaign_id'='992'`).Scan(&secondStatus))
				assert.Equal(t, tc.wantSecondStatus, secondStatus, "an uncertain outcome must remain claimed and cannot be counted as applied")
			} else {
				var secondRows int
				require.NoError(t, fx.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ai_decisions WHERE target->>'ozon_campaign_id'='992'`).Scan(&secondRows))
				assert.Zero(t, secondRows)
			}
			runs, _, err := mgr.ListRuns(ctx, fx.workspaceID, fx.cabinetID, 10, 0)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			assert.Equal(t, domain.AIRunStatusFailed, runs[0].Status)
			assert.NotEmpty(t, runs[0].Error)
			assert.Contains(t, runs[0].Summary, "Выполнено: 1.", "the failed run must retain its confirmed partial result")
			assert.NotContains(t, runs[0].Summary, "Выполнено: 2.")
			if tc.wantSecondStatus != "" {
				assert.Contains(t, runs[0].Summary, "Выполнение не подтверждено:")
			} else {
				assert.Contains(t, runs[0].Summary, "Не выполнено:")
				assert.NotContains(t, runs[0].Summary, "Команда могла быть выполнена", "the failed claim never reached Ozon")
			}
			assert.Contains(t, runs[0].Summary, "992", "the interrupted step must remain identifiable in the report")
			assert.NotContains(t, runs[0].Summary, "продажи сохранены", "an unexecuted model promise must not become the execution report")
		})
	}
}
