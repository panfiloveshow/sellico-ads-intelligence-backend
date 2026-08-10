package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/llm"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

func TestAIDecisionStatusFor(t *testing.T) {
	tests := []struct {
		name    string
		verdict string
		level   int
		want    string
	}{
		// Shadow (level 1) never applies anything, so write-time guardrails
		// (cooldown/caps) must NOT mask the recommendation — it stays shadow.
		{name: "shadow records even with a guardrail verdict", verdict: "cooldown violated", level: 1, want: domain.AIDecisionStatusShadow},
		{name: "guardrail verdict rejects on copilot level", verdict: "change over max percent", level: 2, want: domain.AIDecisionStatusRejectedByGuardrail},
		{name: "guardrail verdict rejects on autopilot level", verdict: "cooldown violated", level: 3, want: domain.AIDecisionStatusRejectedByGuardrail},
		{name: "guardrail verdict rejects campaign not running", verdict: "campaign is not running", level: 3, want: domain.AIDecisionStatusRejectedByGuardrail},
		{name: "level 1 passes to shadow", verdict: "", level: 1, want: domain.AIDecisionStatusShadow},
		{name: "level 2 passes to proposed", verdict: "", level: 2, want: domain.AIDecisionStatusProposed},
		{name: "level 3 passes to auto_applied", verdict: "", level: 3, want: domain.AIDecisionStatusAutoApplied},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, aiDecisionStatusFor(tc.verdict, tc.level))
		})
	}
}

// Shadow and proposed (copilot) decisions must never reach the write path:
// processProposal only calls applyProposal when the mapped status is
// auto_applied, so the mapping itself is the write gate.
func TestAIDecisionStatusFor_OnlyAutopilotWrites(t *testing.T) {
	for level := 1; level <= 2; level++ {
		status := aiDecisionStatusFor("", level)
		assert.NotEqual(t, domain.AIDecisionStatusAutoApplied, status,
			"automation level %d must never map to auto_applied", level)
	}
	// A rejected proposal never writes either, at copilot/autopilot levels.
	for level := 2; level <= 3; level++ {
		status := aiDecisionStatusFor("rejected", level)
		assert.NotEqual(t, domain.AIDecisionStatusAutoApplied, status)
	}
	// Shadow with a verdict stays shadow (never writes, never rejected).
	assert.Equal(t, domain.AIDecisionStatusShadow, aiDecisionStatusFor("rejected", 1))
}

func TestParseSubmission(t *testing.T) {
	t.Run("valid submission", func(t *testing.T) {
		args := `{"summary":"два предложения","proposals":[
			{"action_type":"bid_change","target":{"ozon_campaign_id":42,"sku":100},"new_value":25.5,"rationale":"ДРР высокий"},
			{"action_type":"campaign_pause","target":{"ozon_campaign_id":43}}
		]}`
		sub, err := parseSubmission(args)
		require.NoError(t, err)
		assert.Equal(t, "два предложения", sub.Summary)
		require.Len(t, sub.Proposals, 2)
		assert.Equal(t, "bid_change", sub.Proposals[0].ActionType)
		assert.Equal(t, int64(42), sub.Proposals[0].Target.OzonCampaignID)
		require.NotNil(t, sub.Proposals[0].NewValue)
		assert.InDelta(t, 25.5, *sub.Proposals[0].NewValue, 1e-9)
		assert.Nil(t, sub.Proposals[1].NewValue, "missing new_value stays nil, not zero")
	})

	t.Run("malformed json errors", func(t *testing.T) {
		_, err := parseSubmission(`{"summary": "oops"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse submit_proposals arguments")
	})

	t.Run("empty proposals is valid", func(t *testing.T) {
		sub, err := parseSubmission(`{"summary":"без изменений","proposals":[]}`)
		require.NoError(t, err)
		assert.Empty(t, sub.Proposals)
	})
}

func TestAddUsage(t *testing.T) {
	total := llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}
	total = addUsage(total, llm.Usage{PromptTokens: 50, CompletionTokens: 5, TotalTokens: 55})
	assert.Equal(t, llm.Usage{PromptTokens: 150, CompletionTokens: 25, TotalTokens: 175}, total)

	// Zero add is a no-op.
	assert.Equal(t, total, addUsage(total, llm.Usage{}))
}

func TestCampaignBudget(t *testing.T) {
	newCampaign := func(daily, weekly int64, dailyValid, weeklyValid bool) sqlcgen.OzonCampaign {
		return sqlcgen.OzonCampaign{
			DailyBudgetRub:  pgtype.Int8{Int64: daily, Valid: dailyValid},
			WeeklyBudgetRub: pgtype.Int8{Int64: weekly, Valid: weeklyValid},
		}
	}

	t.Run("weekly wins when both set", func(t *testing.T) {
		current, weekly := campaignBudget(newCampaign(1000, 7000, true, true))
		require.NotNil(t, current)
		assert.Equal(t, int64(7000), *current)
		assert.True(t, weekly)
	})

	t.Run("daily fallback", func(t *testing.T) {
		current, weekly := campaignBudget(newCampaign(1000, 0, true, false))
		require.NotNil(t, current)
		assert.Equal(t, int64(1000), *current)
		assert.False(t, weekly)
	})

	t.Run("zero weekly does not count", func(t *testing.T) {
		current, weekly := campaignBudget(newCampaign(500, 0, true, true))
		require.NotNil(t, current)
		assert.Equal(t, int64(500), *current)
		assert.False(t, weekly)
	})

	t.Run("no budget at all defaults to weekly semantics", func(t *testing.T) {
		current, weekly := campaignBudget(newCampaign(0, 0, false, false))
		assert.Nil(t, current)
		assert.True(t, weekly)
	})
}
