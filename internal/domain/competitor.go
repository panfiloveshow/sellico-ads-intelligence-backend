package domain

import (
	"time"

	"github.com/google/uuid"
)

// Competitor represents a tracked competitor product.
type Competitor struct {
	ID                   uuid.UUID `json:"id"`
	WorkspaceID          uuid.UUID `json:"workspace_id"`
	ProductID            uuid.UUID `json:"product_id"`
	CompetitorNMID       int64     `json:"competitor_nm_id"`
	CompetitorTitle      string    `json:"competitor_title"`
	CompetitorBrand      string    `json:"competitor_brand,omitempty"`
	CompetitorPrice      int64     `json:"competitor_price,omitempty"`
	CompetitorRating     float64   `json:"competitor_rating,omitempty"`
	CompetitorReviewsCount int    `json:"competitor_reviews_count,omitempty"`
	CompetitorImageURL   string    `json:"competitor_image_url,omitempty"`
	Query                string    `json:"query"`
	Region               string    `json:"region,omitempty"`
	FirstSeenAt          time.Time `json:"first_seen_at"`
	LastSeenAt           time.Time `json:"last_seen_at"`
	LastPosition         int       `json:"last_position,omitempty"`
	OurPosition          int       `json:"our_position,omitempty"`
	Source               string    `json:"source"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// CompetitorSnapshot represents a point-in-time capture of competitor metrics.
type CompetitorSnapshot struct {
	ID            uuid.UUID `json:"id"`
	CompetitorID  uuid.UUID `json:"competitor_id"`
	Price         int64     `json:"price"`
	Rating        float64   `json:"rating"`
	ReviewsCount  int       `json:"reviews_count"`
	Position      int       `json:"position"`
	OurPosition   int       `json:"our_position"`
	CapturedAt    time.Time `json:"captured_at"`
}

// SERPBreakdown shows organic vs promoted item counts.
type SERPBreakdown struct {
	Total    int `json:"total"`
	Organic  int `json:"organic"`
	Promoted int `json:"promoted"`
}

// CompetitorComparison compares our product with a competitor.
type CompetitorComparison struct {
	Competitor     Competitor `json:"competitor"`
	PriceDelta     int64      `json:"price_delta"`      // our price - their price
	PriceDeltaPct  float64    `json:"price_delta_pct"`
	RatingDelta    float64    `json:"rating_delta"`      // our rating - their rating
	ReviewsDelta   int        `json:"reviews_delta"`
	PositionDelta  int        `json:"position_delta"`    // our position - their position (negative = we're higher)
	Advantage      string     `json:"advantage"`         // price, rating, position, reviews
	Threat         string     `json:"threat"`            // what they do better
}
