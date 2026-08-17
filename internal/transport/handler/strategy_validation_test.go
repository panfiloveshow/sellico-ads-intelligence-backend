package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStrategyInput_RejectsInvalidValues(t *testing.T) {
	errs := validateStrategyInput(domain.Strategy{
		Type: "bad",
		Params: domain.StrategyParams{
			MinBid:              200,
			MaxBid:              100,
			MaxCPC:              -1,
			MaxCPO:              -1,
			AutomationLevel:     5,
			MaxChangePercent:    101,
			LookbackDays:        -1,
			MinClicks:           -1,
			MinStockForIncrease: -1,
			CooldownMinutes:     -1,
			MaxChangesPerDay:    -1,
			MaxDataAgeHours:     -1,
		},
	})

	assert.Equal(t, "is required", errs["name"])
	assert.Equal(t, "is required", errs["seller_cabinet_id"])
	assert.Contains(t, errs["type"], "must be one of")
	assert.Equal(t, "must be less than or equal to max_bid", errs["params.min_bid"])
	assert.Equal(t, "must be non-negative", errs["params.max_cpc"])
	assert.Equal(t, "must be non-negative", errs["params.max_cpo"])
	assert.Equal(t, "must be between 1 and 4", errs["params.automation_level"])
	assert.Equal(t, "must be between 0 and 100", errs["params.max_change_percent"])
	assert.Equal(t, "must be non-negative", errs["params.lookback_days"])
	assert.Equal(t, "must be non-negative", errs["params.min_clicks"])
	assert.Equal(t, "must be non-negative", errs["params.min_stock_for_increase"])
	assert.Equal(t, "must be non-negative", errs["params.cooldown_minutes"])
	assert.Equal(t, "must be non-negative", errs["params.max_changes_per_day"])
	assert.Equal(t, "must be non-negative", errs["params.max_data_age_hours"])
}

func TestValidateStrategyInput_AcceptsValidValues(t *testing.T) {
	errs := validateStrategyInput(domain.Strategy{
		SellerCabinetID: uuid.New(),
		Name:            "ACoS guard",
		Type:            domain.StrategyTypeACoS,
		Params: domain.StrategyParams{
			TargetACoS:          25,
			MinBid:              100,
			MaxBid:              500,
			MaxCPC:              50,
			MaxCPO:              1500,
			AutomationLevel:     2,
			MaxChangePercent:    20,
			LookbackDays:        7,
			MinClicks:           10,
			MinStockForIncrease: 3,
			CooldownMinutes:     120,
			MaxChangesPerDay:    3,
			MaxDataAgeHours:     36,
		},
	})

	assert.Empty(t, errs)
}

// Список типов в валидаторе и в CHECK-констрейнте базы обязан совпадать.
// Расхождение уже стоило дорого: ozon_ai_autopilot отсутствовал в валидаторе,
// поэтому смена уровня автоматизации у AI-стратегии возвращала 400, а уровень
// молча оставался на «Тени».
func TestKnownStrategyTypesMatchDatabaseConstraint(t *testing.T) {
	root := filepath.Join("..", "..", "..", "migrations")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	// Констрейнт пересоздаётся несколькими миграциями — берём последнюю версию.
	var constraint string
	re := regexp.MustCompile(`(?s)strategies_type_check CHECK \(type IN \((.*?)\)\)`)
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(root, name))
		require.NoError(t, err)
		if m := re.FindStringSubmatch(string(body)); m != nil {
			constraint = m[1]
		}
	}
	require.NotEmpty(t, constraint, "не найден strategies_type_check в миграциях")

	inDB := map[string]bool{}
	for _, quoted := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(constraint, -1) {
		inDB[quoted[1]] = true
	}

	for _, known := range domain.KnownStrategyTypes {
		assert.True(t, inDB[known],
			"тип %q принимается API, но запрещён CHECK-констрейнтом — вставка упадёт", known)
		delete(inDB, known)
	}
	for leftover := range inDB {
		assert.Fail(t, "тип разрешён базой, но не принимается API",
			"%q есть в strategies_type_check, но нет в domain.KnownStrategyTypes", leftover)
	}
}

// Уровень автоматизации у AI-стратегии обязан сохраняться: именно это не
// работало.
func TestAIAutopilotStrategyPassesValidation(t *testing.T) {
	errs := validateStrategyInput(domain.Strategy{
		Name:            "ИИ-автопилот",
		SellerCabinetID: uuid.New(),
		Type:            domain.StrategyTypeOzonAIAutopilot,
		Params:          domain.StrategyParams{TargetACoS: 15, AutomationLevel: 2, MinBid: 5, MaxBid: 500},
	})
	assert.Empty(t, errs, "AI-стратегия с уровнем «С подтверждением» обязана проходить валидацию")
}
