package ozon

import (
	"encoding/json"
	"github.com/rs/zerolog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// --- flexFloat / flexInt64 edge cases ---

func TestFlexFloat_Variants(t *testing.T) {
	var s struct {
		A flexFloat `json:"a"`
		B flexFloat `json:"b"`
		C flexFloat `json:"c"`
		D flexFloat `json:"d"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"a":"12.5","b":7,"c":null,"d":""}`), &s))
	assert.InDelta(t, 12.5, float64(s.A), 1e-9)
	assert.InDelta(t, 7, float64(s.B), 1e-9)
	assert.Zero(t, float64(s.C))
	assert.Zero(t, float64(s.D))

	var bad struct {
		X flexFloat `json:"x"`
	}
	err := json.Unmarshal([]byte(`{"x":"not-a-number"}`), &bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse number")
}

func TestFlexInt64_Variants(t *testing.T) {
	var s struct {
		A flexInt64 `json:"a"`
		B flexInt64 `json:"b"`
		C flexInt64 `json:"c"`
		D flexInt64 `json:"d"`
		E flexInt64 `json:"e"`
	}
	// string, number, null, empty, decimal-as-int fallback.
	require.NoError(t, json.Unmarshal([]byte(`{"a":"42","b":7,"c":null,"d":"","e":"12.0"}`), &s))
	assert.Equal(t, int64(42), int64(s.A))
	assert.Equal(t, int64(7), int64(s.B))
	assert.Zero(t, int64(s.C))
	assert.Zero(t, int64(s.D))
	assert.Equal(t, int64(12), int64(s.E), "decimal string falls back to float→int")

	var bad struct {
		X flexInt64 `json:"x"`
	}
	err := json.Unmarshal([]byte(`{"x":"garbage"}`), &bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse integer")
}

// --- decodeJSON snippet truncation ---

func TestDecodeJSON_ErrorSnippetTruncated(t *testing.T) {
	long := "{" + strings.Repeat("x", 400)
	var out map[string]any
	err := decodeJSON([]byte(long), &out, "thing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode thing response")
	// The snippet embedded in the message is capped at 200 chars.
	assert.Less(t, len(err.Error()), len(long)+120)
}

// --- MicroRubToRub negative rounding + error ---

func TestMicroRubToRub_NegativeAndError(t *testing.T) {
	got, err := MicroRubToRub("-1500000")
	require.NoError(t, err)
	assert.Equal(t, int64(-2), got, "negative rounds half away from zero")

	_, err = MicroRubToRub("nope")
	require.Error(t, err)
}

// --- placementString variants ---

func TestPlacementString(t *testing.T) {
	assert.Equal(t, "PDP", placementString("PDP"))
	assert.Equal(t, "SEARCH,PDP", placementString([]any{"SEARCH", "PDP", ""}))
	assert.Equal(t, "", placementString(nil))
	assert.Equal(t, "", placementString(123))
}

// --- parseOzonDate variants ---

func TestParseOzonDate(t *testing.T) {
	d, err := parseOzonDate("2026-08-01")
	require.NoError(t, err)
	assert.Equal(t, mustDate(2026, 8, 1), *d)

	d, err = parseOzonDate("2026-08-01T10:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, 2026, d.Year())

	_, err = parseOzonDate("")
	require.Error(t, err)
	_, err = parseOzonDate("0001-01-01T00:00:00Z")
	require.Error(t, err)
	_, err = parseOzonDate("08/01/2026")
	require.Error(t, err)
}

// --- campaignFromWire full field mapping ---

func TestCampaignFromWire_FullAndBadBudgets(t *testing.T) {
	wc := wireCampaign{
		ID:                       flexInt64(1),
		Title:                    "T",
		State:                    "CAMPAIGN_STATE_RUNNING",
		AdvObjectType:            "SKU",
		FromDate:                 "2026-08-01",
		ToDate:                   "2026-08-31",
		DailyBudget:              "530000000",
		WeeklyBudget:             "3710000000",
		Placement:                []any{"SEARCH"},
		ProductAutopilotStrategy: "MAX_CLICKS",
	}
	c := campaignFromWire(wc, zerolog.Nop())
	require.NotNil(t, c.DailyBudgetRub)
	assert.Equal(t, int64(530), *c.DailyBudgetRub)
	require.NotNil(t, c.WeeklyBudgetRub)
	assert.Equal(t, int64(3710), *c.WeeklyBudgetRub)
	require.NotNil(t, c.FromDate)
	require.NotNil(t, c.ToDate)
	assert.Equal(t, "SEARCH", c.Placement)
	assert.Equal(t, "MAX_CLICKS", c.AutopilotStrategy)

	// Unparseable budgets are logged and left nil.
	bad := campaignFromWire(wireCampaign{ID: 2, DailyBudget: "abc", WeeklyBudget: "xyz"}, zerolog.Nop())
	assert.Nil(t, bad.DailyBudgetRub)
	assert.Nil(t, bad.WeeklyBudgetRub)
}

// --- rawSnippet ---

func TestRawSnippet(t *testing.T) {
	assert.Equal(t, "short", rawSnippet([]byte("short")))
	long := strings.Repeat("y", rawSnippetLimit+50)
	assert.Len(t, rawSnippet([]byte(long)), rawSnippetLimit)
}
