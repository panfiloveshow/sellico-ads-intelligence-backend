package pagination

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 20: API формат — Response_Envelope и пагинация
// Проверяет: Требования 17.2, 17.3

// TestProperty_Parse_AlwaysReturnsBoundedParams verifies Requirement 17.3:
// For any valid page/per_page values, Parse() always returns page >= 1,
// per_page in [1, MaxPerPage].
func TestProperty_Parse_AlwaysReturnsBoundedParams(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		page := rapid.IntRange(1, 10000).Draw(t, "page")
		perPage := rapid.IntRange(1, 10000).Draw(t, "perPage")

		u, _ := url.Parse(fmt.Sprintf("http://localhost/items?page=%d&per_page=%d", page, perPage))
		r := &http.Request{URL: u}

		p := Parse(r)

		if p.Page < 1 {
			t.Fatalf("Page must be >= 1, got %d (input page=%d)", p.Page, page)
		}
		if p.PerPage < 1 || p.PerPage > MaxPerPage {
			t.Fatalf("PerPage must be in [1, %d], got %d (input per_page=%d)", MaxPerPage, p.PerPage, perPage)
		}
	})
}

// TestProperty_Parse_ArbitraryStringsNeverPanic verifies Requirement 17.3:
// For any arbitrary string inputs (including invalid/negative/zero),
// Parse() never panics and always returns valid bounded params.
func TestProperty_Parse_ArbitraryStringsNeverPanic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pageStr := rapid.String().Draw(t, "pageStr")
		perPageStr := rapid.String().Draw(t, "perPageStr")

		u, _ := url.Parse("http://localhost/items")
		q := u.Query()
		q.Set("page", pageStr)
		q.Set("per_page", perPageStr)
		u.RawQuery = q.Encode()
		r := &http.Request{URL: u}

		// Must not panic.
		p := Parse(r)

		if p.Page < 1 {
			t.Fatalf("Page must be >= 1, got %d (input %q)", p.Page, pageStr)
		}
		if p.PerPage < 1 || p.PerPage > MaxPerPage {
			t.Fatalf("PerPage must be in [1, %d], got %d (input %q)", MaxPerPage, p.PerPage, perPageStr)
		}
	})
}

// TestProperty_Offset_Correctness verifies Requirement 17.3:
// For any valid Params, Offset() == (Page-1) * PerPage.
func TestProperty_Offset_Correctness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		page := rapid.IntRange(1, 10000).Draw(t, "page")
		perPage := rapid.IntRange(1, MaxPerPage).Draw(t, "perPage")

		p := Params{Page: page, PerPage: perPage}
		expected := (page - 1) * perPage

		if p.Offset() != expected {
			t.Fatalf("Offset() = %d, expected (Page-1)*PerPage = %d (page=%d, perPage=%d)",
				p.Offset(), expected, page, perPage)
		}
	})
}

// TestProperty_Parse_NegativeAndZeroClampToDefaults verifies Requirement 17.3:
// For any negative or zero page/per_page values, Parse() clamps to defaults.
func TestProperty_Parse_NegativeAndZeroClampToDefaults(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		page := rapid.IntRange(-10000, 0).Draw(t, "page")
		perPage := rapid.IntRange(-10000, 0).Draw(t, "perPage")

		u, _ := url.Parse(fmt.Sprintf("http://localhost/items?page=%d&per_page=%d", page, perPage))
		r := &http.Request{URL: u}

		p := Parse(r)

		if p.Page != DefaultPage {
			t.Fatalf("negative/zero page %d should clamp to %d, got %d", page, DefaultPage, p.Page)
		}
		if p.PerPage != DefaultPerPage {
			t.Fatalf("negative/zero per_page %d should clamp to %d, got %d", perPage, DefaultPerPage, p.PerPage)
		}
	})
}
