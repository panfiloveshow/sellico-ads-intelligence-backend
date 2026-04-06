package pagination

import (
	"net/http"
	"strconv"
)

const (
	DefaultPage    = 1
	DefaultPerPage = 20
	MaxPerPage     = 5000
)

// Params holds parsed pagination parameters.
type Params struct {
	Page    int
	PerPage int
}

// Offset returns the SQL OFFSET value.
func (p Params) Offset() int {
	return (p.Page - 1) * p.PerPage
}

// Parse extracts page and per_page from query parameters.
// Defaults: page=1, per_page=20. Max per_page=100.
// Invalid values fall back to defaults.
func Parse(r *http.Request) Params {
	q := r.URL.Query()

	page := parseIntOrDefault(q.Get("page"), DefaultPage)
	if page < 1 {
		page = DefaultPage
	}

	perPage := parseIntOrDefault(q.Get("per_page"), DefaultPerPage)
	if perPage < 1 {
		perPage = DefaultPerPage
	}
	if perPage > MaxPerPage {
		perPage = MaxPerPage
	}

	return Params{
		Page:    page,
		PerPage: perPage,
	}
}

func parseIntOrDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
