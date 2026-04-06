package handler

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

func parseDateRangeWithDefault(r *http.Request, defaultDays int) (time.Time, time.Time) {
	now := time.Now().UTC()
	// Truncate to midnight to ensure consistent date boundaries (audit fix: date precision bug)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dateTo := today
	dateFrom := today.AddDate(0, 0, -defaultDays)

	if v := r.URL.Query().Get("date_from"); v != "" {
		if t, err := time.Parse(dateLayout, v); err == nil {
			dateFrom = t
		}
	}
	if v := r.URL.Query().Get("date_to"); v != "" {
		if t, err := time.Parse(dateLayout, v); err == nil {
			dateTo = t
		}
	}

	return dateFrom, dateTo
}

func parseOptionalUUIDQuery(r *http.Request, key string) (*uuid.UUID, error) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
