package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseDateRangeDefaultsToYesterdayMSKMinus30Days(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/ads/overview", nil)

	dateFrom, dateTo := parseDateRange(req)

	location, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	now := time.Now().In(location)
	expectedTo := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -1)
	expectedFrom := expectedTo.AddDate(0, 0, -30)

	require.Equal(t, expectedTo.Format(dateLayout), dateTo.Format(dateLayout))
	require.Equal(t, expectedFrom.Format(dateLayout), dateFrom.Format(dateLayout))
}

func TestParseDateRangeKeepsExplicitQueryDates(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/ads/overview?date_from=2026-04-18&date_to=2026-05-18", nil)

	dateFrom, dateTo := parseDateRange(req)

	require.Equal(t, "2026-04-18", dateFrom.Format(dateLayout))
	require.Equal(t, "2026-05-18", dateTo.Format(dateLayout))
}
