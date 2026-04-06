package domain

import (
	"time"

	"github.com/google/uuid"
)

// Product event types.
const (
	EventPriceChange       = "price_change"
	EventStockChange       = "stock_change"
	EventContentChange     = "content_change"
	EventPhotoChange       = "photo_change"
	EventRatingChange      = "rating_change"
	EventReviewCountChange = "review_count_change"
	EventCategoryChange    = "category_change"
	EventBrandChange       = "brand_change"
	EventTitleChange       = "title_change"
	EventCreated           = "created"
	EventSynced            = "synced"
)

// ProductEvent represents a tracked change to a product card.
type ProductEvent struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	ProductID   uuid.UUID  `json:"product_id"`
	EventType   string     `json:"event_type"`
	FieldName   string     `json:"field_name,omitempty"`
	OldValue    string     `json:"old_value,omitempty"`
	NewValue    string     `json:"new_value,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	DetectedAt  time.Time  `json:"detected_at"`
	Source      string     `json:"source"`
}

// ProductSnapshot represents a point-in-time capture of product state for diffing.
type ProductSnapshot struct {
	ID           uuid.UUID `json:"id"`
	ProductID    uuid.UUID `json:"product_id"`
	Title        string    `json:"title"`
	Brand        string    `json:"brand"`
	Category     string    `json:"category"`
	Price        int64     `json:"price"`
	Rating       float64   `json:"rating"`
	ReviewsCount int       `json:"reviews_count"`
	StockTotal   int       `json:"stock_total"`
	ImageURL     string    `json:"image_url"`
	ContentHash  string    `json:"content_hash"`
	CapturedAt   time.Time `json:"captured_at"`
}
