package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func withFastCatalogRetryTiming(t *testing.T) {
	t.Helper()
	origBaseBackoff := baseBackoff
	baseBackoff = time.Millisecond
	t.Cleanup(func() {
		baseBackoff = origBaseBackoff
	})
}

// Feature: sellico-ads-intelligence-backend, Property 25: WB_Catalog_Parser — поиск и определение позиции
// Проверяет: Требования 23.2, 23.3

// newTestParser creates a Parser pointing at the given test server URL with no
// proxy and zero delay so property tests run fast.
func newTestParser(serverURL string) *Parser {
	return &Parser{
		searchURL: serverURL,
		pool:      NewProxyPool(nil),
		minDelay:  0,
		logger:    zerolog.Nop(),
	}
}

// genCatalogProductDTO generates a random CatalogProductDTO.
func genCatalogProductDTO() *rapid.Generator[CatalogProductDTO] {
	return rapid.Custom[CatalogProductDTO](func(t *rapid.T) CatalogProductDTO {
		return CatalogProductDTO{
			ID:        rapid.Int64Range(1, 1_000_000_000).Draw(t, "id"),
			Name:      rapid.StringMatching(`[A-Za-zА-Яа-я0-9 ]{3,50}`).Draw(t, "name"),
			Brand:     rapid.StringMatching(`[A-Za-z]{2,20}`).Draw(t, "brand"),
			PriceU:    rapid.IntRange(100, 10_000_000).Draw(t, "priceU"),
			Rating:    float64(rapid.IntRange(0, 50).Draw(t, "rating_x10")) / 10.0,
			Feedbacks: rapid.IntRange(0, 100_000).Draw(t, "feedbacks"),
		}
	})
}

// serveCatalogJSON returns an httptest.Server that responds with the given
// products encoded as a CatalogSearchResponse JSON.
func serveCatalogJSON(products []CatalogProductDTO) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := CatalogSearchResponse{
			Data: CatalogSearchData{Products: products},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// TestProperty_CatalogParser_PositionsAre1Based verifies that SearchProducts
// assigns 1-based positions matching the order of products in the response.
// For any list of N products, position[i] == i+1.
func TestProperty_CatalogParser_PositionsAre1Based(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 250).Draw(t, "product_count")
		dtos := make([]CatalogProductDTO, n)
		for i := range dtos {
			dtos[i] = genCatalogProductDTO().Draw(t, "product")
			dtos[i].ID = int64(i + 1) // ensure unique IDs
		}

		server := serveCatalogJSON(dtos)
		defer server.Close()

		parser := newTestParser(server.URL)
		products, err := parser.SearchProducts(context.Background(), "test", "")
		require.NoError(t, err)
		require.Len(t, products, n)

		for i, p := range products {
			assert.Equal(t, i+1, p.Position,
				"product at index %d must have position %d", i, i+1)
		}
	})
}

// TestProperty_CatalogParser_PageComputation verifies that the Page field is
// computed correctly: page = ceil(position / 100).
func TestProperty_CatalogParser_PageComputation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 350).Draw(t, "product_count")
		dtos := make([]CatalogProductDTO, n)
		for i := range dtos {
			dtos[i] = genCatalogProductDTO().Draw(t, "product")
			dtos[i].ID = int64(i + 1)
		}

		server := serveCatalogJSON(dtos)
		defer server.Close()

		parser := newTestParser(server.URL)
		products, err := parser.SearchProducts(context.Background(), "query", "")
		require.NoError(t, err)

		for i, p := range products {
			expectedPage := (i / resultsPerPage) + 1
			assert.Equal(t, expectedPage, p.Page,
				"product at index %d (position %d) must be on page %d", i, p.Position, expectedPage)
		}
	})
}

// TestProperty_CatalogParser_FieldsPreserved verifies that all DTO fields are
// correctly mapped to CatalogProduct fields for any random product list.
func TestProperty_CatalogParser_FieldsPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "product_count")
		dtos := make([]CatalogProductDTO, n)
		for i := range dtos {
			dtos[i] = genCatalogProductDTO().Draw(t, "product")
		}

		server := serveCatalogJSON(dtos)
		defer server.Close()

		parser := newTestParser(server.URL)
		products, err := parser.SearchProducts(context.Background(), "q", "")
		require.NoError(t, err)
		require.Len(t, products, n)

		for i, p := range products {
			assert.Equal(t, dtos[i].ID, p.ID, "ID mismatch at index %d", i)
			assert.Equal(t, dtos[i].Name, p.Name, "Name mismatch at index %d", i)
			assert.Equal(t, dtos[i].Brand, p.Brand, "Brand mismatch at index %d", i)
			assert.Equal(t, dtos[i].PriceU, p.Price, "Price mismatch at index %d", i)
			assert.Equal(t, dtos[i].Rating, p.Rating, "Rating mismatch at index %d", i)
			assert.Equal(t, dtos[i].Feedbacks, p.Reviews, "Reviews mismatch at index %d", i)
		}
	})
}

// TestProperty_CatalogParser_FindProductPosition_Found verifies that
// FindProductPosition returns the correct 1-based position when the target
// product exists anywhere in the results.
func TestProperty_CatalogParser_FindProductPosition_Found(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 200).Draw(t, "product_count")
		dtos := make([]CatalogProductDTO, n)
		for i := range dtos {
			dtos[i] = genCatalogProductDTO().Draw(t, "product")
			dtos[i].ID = int64(i + 1) // unique IDs: 1..n
		}

		// Pick a random product to search for.
		targetIdx := rapid.IntRange(0, n-1).Draw(t, "target_index")
		targetID := dtos[targetIdx].ID

		server := serveCatalogJSON(dtos)
		defer server.Close()

		parser := newTestParser(server.URL)
		pos, err := parser.FindProductPosition(context.Background(), "query", "", targetID)
		require.NoError(t, err)
		assert.Equal(t, targetIdx+1, pos,
			"product ID %d at index %d must have position %d", targetID, targetIdx, targetIdx+1)
	})
}

// TestProperty_CatalogParser_FindProductPosition_NotFound verifies that
// FindProductPosition returns -1 for any product ID that is not in the results.
func TestProperty_CatalogParser_FindProductPosition_NotFound(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 100).Draw(t, "product_count")
		dtos := make([]CatalogProductDTO, n)
		ids := make(map[int64]bool, n)
		for i := range dtos {
			dtos[i] = genCatalogProductDTO().Draw(t, "product")
			dtos[i].ID = int64(i + 1)
			ids[dtos[i].ID] = true
		}

		// Generate an ID guaranteed to be absent.
		missingID := int64(n + 1 + rapid.IntRange(0, 1000).Draw(t, "offset"))

		server := serveCatalogJSON(dtos)
		defer server.Close()

		parser := newTestParser(server.URL)
		pos, err := parser.FindProductPosition(context.Background(), "query", "", missingID)
		require.NoError(t, err)
		assert.Equal(t, -1, pos,
			"product ID %d not in results must return position -1", missingID)
	})
}

// TestProperty_CatalogParser_EmptyResults verifies that an empty catalog
// response produces an empty product slice and FindProductPosition returns -1.
func TestProperty_CatalogParser_EmptyResults(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		server := serveCatalogJSON(nil)
		defer server.Close()

		parser := newTestParser(server.URL)

		products, err := parser.SearchProducts(context.Background(), "empty", "")
		require.NoError(t, err)
		assert.Empty(t, products)

		anyID := rapid.Int64Range(1, 1_000_000).Draw(t, "any_id")
		pos, err := parser.FindProductPosition(context.Background(), "empty", "", anyID)
		require.NoError(t, err)
		assert.Equal(t, -1, pos)
	})
}

// TestProperty_CatalogParser_RetryOn5xx verifies that the parser retries on
// server errors and eventually succeeds when the server recovers.
func TestProperty_CatalogParser_RetryOn5xx(t *testing.T) {
	withFastCatalogRetryTiming(t)
	rapid.Check(t, func(t *rapid.T) {
		failCount := rapid.IntRange(1, maxRetries-1).Draw(t, "fail_count")
		statusCode := rapid.IntRange(500, 599).Draw(t, "status_code")

		dto := genCatalogProductDTO().Draw(t, "product")

		var attempts int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			if attempts <= failCount {
				w.WriteHeader(statusCode)
				return
			}
			resp := CatalogSearchResponse{
				Data: CatalogSearchData{Products: []CatalogProductDTO{dto}},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		parser := newTestParser(server.URL)
		products, err := parser.SearchProducts(context.Background(), "retry", "")
		require.NoError(t, err)
		require.Len(t, products, 1)
		assert.Equal(t, dto.ID, products[0].ID)
		assert.Equal(t, failCount+1, attempts,
			"expected %d failures + 1 success = %d total attempts", failCount, failCount+1)
	})
}

// TestProperty_CatalogParser_ContextCancelStopsRetries verifies that
// cancelling the context prevents further retry attempts.
func TestProperty_CatalogParser_ContextCancelStopsRetries(t *testing.T) {
	withFastCatalogRetryTiming(t)
	rapid.Check(t, func(t *rapid.T) {
		statusCode := rapid.IntRange(500, 599).Draw(t, "status_code")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(statusCode)
		}))
		defer server.Close()

		parser := newTestParser(server.URL)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := parser.SearchProducts(ctx, "cancel", "")
		assert.Error(t, err, "cancelled context must produce an error")
	})
}
