package handler

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

func parseDateRangeWithDefault(r *http.Request, defaultDays int) (time.Time, time.Time) {
	now := time.Now().UTC()
	dateTo := now
	dateFrom := now.AddDate(0, 0, -defaultDays)

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
