package ozon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	maxRetries        = 3
	defaultRetryAfter = 30 * time.Second
	baseBackoff       = 1 * time.Second
)

// APIError carries machine-readable Ozon response metadata (mirrors
// wb.APIError so callers can honor Retry-After instead of guessing).
type APIError struct {
	StatusCode int
	RetryAfter time.Duration
	URL        string
	Message    string
}

func (e *APIError) Error() string { return e.Message }

// limiterPool hands out one rate.Limiter per credential key. Cabinet counts
// are small (one per Ozon integration), so a plain map is sufficient — no LRU
// eviction needed at this cardinality.
type limiterPool struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	limit    rate.Limit
	burst    int
}

func newLimiterPool(limit rate.Limit, burst int) *limiterPool {
	return &limiterPool{limiters: make(map[string]*rate.Limiter), limit: limit, burst: burst}
}

func (p *limiterPool) get(key string) *rate.Limiter {
	p.mu.Lock()
	defer p.mu.Unlock()
	if lim, ok := p.limiters[key]; ok {
		return lim
	}
	lim := rate.NewLimiter(p.limit, p.burst)
	p.limiters[key] = lim
	return lim
}

func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return defaultRetryAfter
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(header); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
	}
	return defaultRetryAfter
}

func backoffDuration(attempt int) time.Duration {
	d := baseBackoff
	for i := 1; i < attempt; i++ {
		d *= 3
	}
	return d
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// flexFloat unmarshals a JSON number that may arrive as a string or a number.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	trimmed := strings.Trim(string(data), `"`)
	if trimmed == "" || trimmed == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return fmt.Errorf("ozon: parse number %q: %w", string(data), err)
	}
	*f = flexFloat(v)
	return nil
}

// flexInt64 unmarshals a JSON integer that may arrive as a string or a number.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(data []byte) error {
	trimmed := strings.Trim(string(data), `"`)
	if trimmed == "" || trimmed == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		// Some counters arrive as decimals ("12.0") — fall back to float parse.
		fv, ferr := strconv.ParseFloat(trimmed, 64)
		if ferr != nil {
			return fmt.Errorf("ozon: parse integer %q: %w", string(data), err)
		}
		*f = flexInt64(int64(fv))
		return nil
	}
	*f = flexInt64(v)
	return nil
}

// decodeJSON decodes body into out with a helpful error message.
func decodeJSON(body []byte, out any, what string) error {
	if err := json.Unmarshal(body, out); err != nil {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return fmt.Errorf("ozon: decode %s response: %w (body: %s)", what, err, snippet)
	}
	return nil
}

// marketplaceIDRU — обязательный параметр методов Performance API, который мы
// не слали. Все кабинеты российские; появится другая страна — параметр придёт
// из настроек кабинета, а не отсюда.
const marketplaceIDRU = "MARKETPLACE_ID_RU"

// bidRub — денежное поле Performance API, которое приходит то строкой, то
// числом, и в двух разных масштабах. Строка — микрорубли ("5000000" = 5 ₽),
// голое число — рубли ({"bid":5} = 5 ₽). Отличить их можно только по кавычкам,
// поэтому тип их и запоминает: сначала смотрит на форму, потом на значение.
//
// Числовую форму документация задаёт для /api/client/min/sku и
// search_promo/get_cpo_min_bids (bid: number <double>); что число именно в
// рублях, документация прямо не пишет — это вывод: пришедшее с прода
// {"sku":"1275704268","bid":5} осмысленно как 5 ₽ и бессмысленно как
// 0,000005 ₽. Ошибка в эту сторону завышает нижний порог ставки и блокирует
// запись, а не разрешает лишнего.
//
// Разбор никогда не возвращает ошибку: одна порченая строка в ответе не должна
// ронять весь пакет, поэтому непрочитанное помечается OK=false, и вызывающий
// такую строку пропускает.
type bidRub struct {
	Rub float64
	OK  bool
}

func (b *bidRub) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*b = bidRub{}
		return nil
	}
	quoted := strings.HasPrefix(raw, `"`)
	v, err := strconv.ParseFloat(strings.Trim(raw, `"`), 64)
	if err != nil {
		*b = bidRub{}
		return nil
	}
	if quoted {
		v /= microPerRuble
	}
	*b = bidRub{Rub: v, OK: true}
	return nil
}
