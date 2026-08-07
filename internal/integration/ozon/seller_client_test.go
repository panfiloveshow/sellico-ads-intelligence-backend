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
