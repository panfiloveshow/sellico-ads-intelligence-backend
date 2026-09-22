package ozon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

// Лимиты Performance API на запись (раздел «Лимиты на запросы», действуют с
// 14.09.2026) считаются на аккаунт: изменение бюджета и смена стратегии — 100
// в час и 500 в сутки, изменение ставок — 500 в час и 6000 в сутки (до 10 000
// товаров в одном запросе). Добавление товаров и создание кампаний клиент не
// вызывает. При превышении Ozon отвечает ошибкой до сброса счётчика.
//
// Учёт — в памяти процесса по client_id, скользящими окнами: API и воркер
// считают каждый своё, общий учёт автоматики — OzonAPIBudget в БД. Ответ 429
// на запись закрывает категорию до сброса, чтобы автопилот не долбил Ozon.
const (
	QuotaBudgetWrite = "budget_write"
	QuotaBidWrite    = "bid_write"
)

type perfQuotaLimit struct {
	hourly, daily int
	title         string
}

var perfWriteQuotas = map[string]perfQuotaLimit{
	QuotaBudgetWrite: {hourly: 100, daily: 500, title: "изменение бюджета"},
	QuotaBidWrite:    {hourly: 500, daily: 6000, title: "изменение ставок"},
}

var perfMSK = time.FixedZone("MSK", 3*60*60)

// QuotaError — лимит Ozon на запись исчерпан: запрос не отправлен (или Ozon
// ответил 429). Это не провал действия — его нужно отложить до ResetAt.
type QuotaError struct {
	Category string
	ResetAt  time.Time
}

func (e *QuotaError) Error() string {
	limit := perfWriteQuotas[e.Category]
	reset := e.ResetAt.In(perfMSK)
	layout := "15:04"
	if reset.YearDay() != time.Now().In(perfMSK).YearDay() {
		layout = "02.01 15:04"
	}
	return fmt.Sprintf("исчерпан лимит Ozon Performance API на %s (%d в час, %d в сутки) до %s МСК",
		limit.title, limit.hourly, limit.daily, reset.Format(layout))
}

// Unwrap отдаёт HTTP-слою 429 с тем же текстом.
func (e *QuotaError) Unwrap() error {
	return apperror.New(apperror.ErrRateLimited, e.Error())
}

type perfQuotaKey struct{}

// withPerfQuota помечает запрос как запись с лимитом: 429 на нём не повторяется.
func withPerfQuota(ctx context.Context, category string) context.Context {
	return context.WithValue(ctx, perfQuotaKey{}, category)
}

func perfQuotaCategory(ctx context.Context) string {
	category, _ := ctx.Value(perfQuotaKey{}).(string)
	return category
}

// perfQuotaTracker помнит моменты записей за последние сутки и запреты после
// 429. Нулевое значение готово к работе.
type perfQuotaTracker struct {
	mu      sync.Mutex
	calls   map[string][]time.Time
	blocked map[string]time.Time
}

func perfQuotaTrackerKey(clientID, category string) string {
	return clientID + "|" + category
}

// reserve засчитывает запись, если лимит позволяет, иначе — QuotaError со
// временем, когда освободится место.
func (t *perfQuotaTracker) reserve(clientID, category string, now time.Time) error {
	limit, ok := perfWriteQuotas[category]
	if !ok {
		return nil
	}
	key := perfQuotaTrackerKey(clientID, category)
	t.mu.Lock()
	defer t.mu.Unlock()
	if until := t.blocked[key]; now.Before(until) {
		return &QuotaError{Category: category, ResetAt: until}
	}
	calls := t.calls[key]
	dayStart := now.Add(-24 * time.Hour)
	for len(calls) > 0 && !calls[0].After(dayStart) {
		calls = calls[1:]
	}
	var resetAt time.Time
	if len(calls) >= limit.daily {
		resetAt = calls[len(calls)-limit.daily].Add(24 * time.Hour)
	}
	hourStart := now.Add(-time.Hour)
	inHour := 0
	for i := len(calls) - 1; i >= 0 && calls[i].After(hourStart); i-- {
		inHour++
	}
	if inHour >= limit.hourly {
		if hourReset := calls[len(calls)-limit.hourly].Add(time.Hour); hourReset.After(resetAt) {
			resetAt = hourReset
		}
	}
	if !resetAt.IsZero() {
		t.setCalls(key, calls)
		return &QuotaError{Category: category, ResetAt: resetAt}
	}
	t.setCalls(key, append(calls, now))
	return nil
}

func (t *perfQuotaTracker) setCalls(key string, calls []time.Time) {
	if t.calls == nil {
		t.calls = make(map[string][]time.Time)
	}
	t.calls[key] = calls
}

// block закрывает категорию до until после 429 от Ozon.
func (t *perfQuotaTracker) block(clientID, category string, until time.Time) *QuotaError {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.blocked == nil {
		t.blocked = make(map[string]time.Time)
	}
	t.blocked[perfQuotaTrackerKey(clientID, category)] = until
	return &QuotaError{Category: category, ResetAt: until}
}
