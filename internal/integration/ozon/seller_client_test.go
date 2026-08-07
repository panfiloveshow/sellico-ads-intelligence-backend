package ozon

import (
	"encoding/json"
	"testing"
)

// TestProductInfoWireItem_SKUExtraction covers the defensive sales-SKU
// parsing of /v3/product/info/list: the SKU has moved between a top-level
// "sku" field and "sources":[{"sku":...}] across API versions, and numbers
// may arrive as strings. First non-zero wins, top-level first.
func TestProductInfoWireItem_SKUExtraction(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantSKU int64
	}{
		{
			name:    "top-level sku as number",
			payload: `{"id": 710993868, "name": "Товар", "sku": 1275702683}`,
			wantSKU: 1275702683,
		},
		{
			name:    "top-level sku as string",
			payload: `{"id": 710993868, "sku": "1275702683"}`,
			wantSKU: 1275702683,
		},
		{
			name:    "sku only inside sources",
			payload: `{"id": 710993868, "sources": [{"sku": 1275702683, "source": "sds"}]}`,
			wantSKU: 1275702683,
		},
		{
			name:    "sources with leading zero entry",
			payload: `{"id": 710993868, "sources": [{"sku": 0}, {"sku": "1275702683"}]}`,
			wantSKU: 1275702683,
		},
		{
			name:    "top-level wins over sources",
			payload: `{"id": 710993868, "sku": 111, "sources": [{"sku": 222}]}`,
			wantSKU: 111,
		},
		{
			name:    "no sku anywhere",
			payload: `{"id": 710993868, "name": "Товар"}`,
			wantSKU: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var item productInfoWireItem
			if err := json.Unmarshal([]byte(tc.payload), &item); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			info := item.toProductInfo()
			if info.SKU != tc.wantSKU {
				t.Errorf("SKU = %d, want %d", info.SKU, tc.wantSKU)
			}
			if info.ProductID != 710993868 {
				t.Errorf("ProductID = %d, want 710993868", info.ProductID)
			}
		})
	}
}

// TestProductInfoWireItem_FullItem checks name/offer_id/primary_image parsing
// including the primary_image string-vs-array drift and the images fallback.
func TestProductInfoWireItem_FullItem(t *testing.T) {
	payload := `{
		"id": "710993868",
		"name": "Кроссовки беговые",
		"offer_id": "ART-42",
		"sku": 1275702683,
		"primary_image": "https://cdn.ozon.ru/img/main.jpg",
		"images": ["https://cdn.ozon.ru/img/1.jpg"]
	}`
	var item productInfoWireItem
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	info := item.toProductInfo()
	if info.ProductID != 710993868 {
		t.Errorf("ProductID = %d, want 710993868", info.ProductID)
	}
	if info.Name != "Кроссовки беговые" {
		t.Errorf("Name = %q", info.Name)
	}
	if info.OfferID != "ART-42" {
		t.Errorf("OfferID = %q", info.OfferID)
	}
	if info.PrimaryImage != "https://cdn.ozon.ru/img/main.jpg" {
		t.Errorf("PrimaryImage = %q", info.PrimaryImage)
	}
}

func TestProductInfoWireItem_PrimaryImageVariants(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "primary_image as array",
			payload: `{"id": 1, "primary_image": ["https://cdn.ozon.ru/img/a.jpg", "https://cdn.ozon.ru/img/b.jpg"]}`,
			want:    "https://cdn.ozon.ru/img/a.jpg",
		},
		{
			name:    "fallback to images",
			payload: `{"id": 1, "images": ["https://cdn.ozon.ru/img/c.jpg"]}`,
			want:    "https://cdn.ozon.ru/img/c.jpg",
		},
		{
			name:    "empty primary_image array falls back to images",
			payload: `{"id": 1, "primary_image": [], "images": ["https://cdn.ozon.ru/img/d.jpg"]}`,
			want:    "https://cdn.ozon.ru/img/d.jpg",
		},
		{
			name:    "no image data",
			payload: `{"id": 1}`,
			want:    "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var item productInfoWireItem
			if err := json.Unmarshal([]byte(tc.payload), &item); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := item.primaryImageURL(); got != tc.want {
				t.Errorf("primaryImageURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPostingsWireResponse_BothWrappers covers the two posting-list response
// shapes: FBO's {"result":[...]} and FBS's {"result":{"postings":[...]}} —
// both endpoints are parsed with both wrappers because the docs have drifted.
func TestPostingsWireResponse_BothWrappers(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    int
	}{
		{
			name:    "fbo: result as array",
			payload: `{"result": [{"created_at": "2026-08-03T09:00:00Z", "products": [{"sku": 111, "quantity": 1}]}]}`,
			want:    1,
		},
		{
			name:    "fbs: result.postings",
			payload: `{"result": {"postings": [{"in_process_at": "2026-08-03T09:00:00Z", "products": [{"sku": 111, "quantity": 2}]}], "has_next": false}}`,
			want:    1,
		},
		{
			name:    "empty result",
			payload: `{"result": []}`,
			want:    0,
		},
		{
			name:    "missing result",
			payload: `{}`,
			want:    0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var resp postingsWireResponse
			if err := json.Unmarshal([]byte(tc.payload), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := len(resp.items()); got != tc.want {
				t.Errorf("items() = %d postings, want %d", got, tc.want)
			}
		})
	}
}

// TestFlattenPostings covers the flat-record extraction: in_process_at wins
// over created_at, string numbers are accepted, product lines fan out, and
// zero quantity defensively counts as one unit.
func TestFlattenPostings(t *testing.T) {
	payload := `{"result": [
		{
			"in_process_at": "2026-08-03T12:30:00Z",
			"created_at": "2026-08-03T09:00:00Z",
			"products": [
				{"sku": "111", "quantity": "2"},
				{"sku": 222, "quantity": 0},
				{"sku": 0, "quantity": 3}
			]
		},
		{
			"created_at": "2026-08-04T10:00:00.123Z",
			"products": [{"sku": 333, "quantity": 1}]
		},
		{
			"products": [{"sku": 444, "quantity": 1}]
		}
	]}`
	var resp postingsWireResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sales := flattenPostings(resp.items())
	if len(sales) != 3 {
		t.Fatalf("sales = %d records (%+v), want 3", len(sales), sales)
	}
	first := sales[0]
	if first.SKU != 111 || first.Quantity != 2 {
		t.Errorf("first = %+v, want sku=111 quantity=2", first)
	}
	wantTime := "2026-08-03T12:30:00Z" // in_process_at preferred over created_at
	if got := first.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"); got != wantTime {
		t.Errorf("first.CreatedAt = %s, want %s", got, wantTime)
	}
	second := sales[1]
	if second.SKU != 222 || second.Quantity != 1 {
		t.Errorf("second = %+v, want sku=222 quantity=1 (zero quantity counts as one)", second)
	}
	third := sales[2]
	if third.SKU != 333 {
		t.Errorf("third = %+v, want sku=333 (fractional-second timestamp accepted)", third)
	}
}
