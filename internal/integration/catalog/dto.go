package catalog

// CatalogSearchResponse represents the top-level JSON response from search.wb.ru.
type CatalogSearchResponse struct {
	Data CatalogSearchData `json:"data"`
}

// CatalogSearchData holds the products array from the search response.
type CatalogSearchData struct {
	Products []CatalogProductDTO `json:"products"`
}

// CatalogProductDTO represents a single product in the WB catalog search response.
type CatalogProductDTO struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Brand     string  `json:"brand"`
	PriceU    int     `json:"priceU"` // price in kopecks (×100)
	Rating    float64 `json:"rating"`
	Feedbacks int     `json:"feedbacks"` // reviews count
}

// CatalogProduct is the parsed product with computed position information.
type CatalogProduct struct {
	ID       int64
	Name     string
	Brand    string
	Price    int // price in kopecks
	Rating   float64
	Reviews  int
	Position int // 1-based position in search results
	Page     int // page number (1-based)
}
