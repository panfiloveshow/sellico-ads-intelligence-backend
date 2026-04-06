package domain

import (
	"time"

	"github.com/google/uuid"
)

// Keyword represents a collected search query with frequency metadata.
type Keyword struct {
	ID             uuid.UUID  `json:"id"`
	WorkspaceID    uuid.UUID  `json:"workspace_id"`
	Query          string     `json:"query"`
	Normalized     string     `json:"normalized"`
	Frequency      int        `json:"frequency"`
	FrequencyTrend string     `json:"frequency_trend"` // rising, falling, stable
	ClusterID      *uuid.UUID `json:"cluster_id,omitempty"`
	Source         string     `json:"source"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// KeywordFrequencyPoint represents a single frequency measurement.
type KeywordFrequencyPoint struct {
	ID        uuid.UUID `json:"id"`
	KeywordID uuid.UUID `json:"keyword_id"`
	Frequency int       `json:"frequency"`
	CheckedAt time.Time `json:"checked_at"`
}

// KeywordCluster groups related keywords by semantic similarity.
type KeywordCluster struct {
	ID             uuid.UUID `json:"id"`
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	Name           string    `json:"name"`
	MainKeyword    string    `json:"main_keyword"`
	KeywordCount   int       `json:"keyword_count"`
	TotalFrequency int       `json:"total_frequency"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Keywords       []Keyword `json:"keywords,omitempty"`
}

// KeywordRelation represents a relationship between two keywords.
type KeywordRelation struct {
	ID           uuid.UUID `json:"id"`
	KeywordID    uuid.UUID `json:"keyword_id"`
	RelatedID    uuid.UUID `json:"related_id"`
	RelationType string    `json:"relation_type"` // related, synonym, broader, narrower
	Strength     float64   `json:"strength"`
	CreatedAt    time.Time `json:"created_at"`
}

// SEOAnalysis represents an SEO audit result for a product.
type SEOAnalysis struct {
	ID                uuid.UUID        `json:"id"`
	WorkspaceID       uuid.UUID        `json:"workspace_id"`
	ProductID         uuid.UUID        `json:"product_id"`
	TitleScore        int              `json:"title_score"`
	DescriptionScore  int              `json:"description_score"`
	KeywordsScore     int              `json:"keywords_score"`
	OverallScore      int              `json:"overall_score"`
	TitleIssues       []SEOIssue       `json:"title_issues"`
	DescriptionIssues []SEOIssue       `json:"description_issues"`
	KeywordCoverage   map[string]bool  `json:"keyword_coverage"`
	Recommendations   []SEORecommendation `json:"recommendations"`
	AnalyzedAt        time.Time        `json:"analyzed_at"`
}

// SEOIssue represents a specific SEO problem found.
type SEOIssue struct {
	Type     string `json:"type"`     // missing_keyword, too_short, too_long, duplicate, low_relevance
	Severity string `json:"severity"` // high, medium, low
	Message  string `json:"message"`
	Field    string `json:"field"`    // title, description, characteristics
}

// SEORecommendation represents an actionable SEO suggestion.
type SEORecommendation struct {
	Type       string `json:"type"`       // add_keyword, improve_title, add_description, optimize_characteristics
	Priority   int    `json:"priority"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"` // specific text suggestion
}

// FrequencyTrend constants.
const (
	FrequencyTrendRising  = "rising"
	FrequencyTrendFalling = "falling"
	FrequencyTrendStable  = "stable"
)
