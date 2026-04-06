package service

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// SEOAnalyzerService scores product listings and generates SEO recommendations.
type SEOAnalyzerService struct {
	queries    *sqlcgen.Queries
	semantics  *SemanticsService
	logger     zerolog.Logger
}

func NewSEOAnalyzerService(queries *sqlcgen.Queries, semantics *SemanticsService, logger zerolog.Logger) *SEOAnalyzerService {
	return &SEOAnalyzerService{
		queries:   queries,
		semantics: semantics,
		logger:    logger.With().Str("component", "seo_analyzer").Logger(),
	}
}

// AnalyzeProduct runs SEO analysis on a single product card.
func (s *SEOAnalyzerService) AnalyzeProduct(ctx context.Context, workspaceID uuid.UUID, product domain.Product) (*domain.SEOAnalysis, error) {
	// Get top keywords for this workspace
	keywords, _ := s.queries.ListKeywords(ctx, sqlcgen.ListKeywordsParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       100,
		Offset:      0,
	})

	topKeywords := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		topKeywords = append(topKeywords, kw.Normalized)
	}

	analysis := &domain.SEOAnalysis{
		WorkspaceID: workspaceID,
		ProductID:   product.ID,
	}

	// Analyze title
	analysis.TitleScore, analysis.TitleIssues = s.analyzeTitle(product.Title, topKeywords)

	// Keyword coverage
	analysis.KeywordCoverage = s.checkKeywordCoverage(product.Title, topKeywords)

	// Calculate overall score
	coveredCount := 0
	for _, covered := range analysis.KeywordCoverage {
		if covered {
			coveredCount++
		}
	}
	if len(topKeywords) > 0 {
		analysis.KeywordsScore = coveredCount * 100 / len(topKeywords)
	}

	analysis.OverallScore = (analysis.TitleScore + analysis.KeywordsScore) / 2

	// Generate recommendations
	analysis.Recommendations = s.generateRecommendations(product, analysis, topKeywords)

	// Save to DB
	titleIssuesJSON, _ := json.Marshal(analysis.TitleIssues)
	descIssuesJSON, _ := json.Marshal(analysis.DescriptionIssues)
	coverageJSON, _ := json.Marshal(analysis.KeywordCoverage)
	recsJSON, _ := json.Marshal(analysis.Recommendations)

	row, err := s.queries.CreateSEOAnalysis(ctx, sqlcgen.CreateSEOAnalysisParams{
		WorkspaceID:       uuidToPgtype(workspaceID),
		ProductID:         uuidToPgtype(product.ID),
		TitleScore:        int32(analysis.TitleScore),
		DescriptionScore:  int32(analysis.DescriptionScore),
		KeywordsScore:     int32(analysis.KeywordsScore),
		OverallScore:      int32(analysis.OverallScore),
		TitleIssues:       titleIssuesJSON,
		DescriptionIssues: descIssuesJSON,
		KeywordCoverage:   coverageJSON,
		Recommendations:   recsJSON,
	})
	if err == nil {
		analysis.AnalyzedAt = row.AnalyzedAt.Time
	}

	return analysis, nil
}

// AnalyzeWorkspace runs SEO analysis for all products in a workspace.
func (s *SEOAnalyzerService) AnalyzeWorkspace(ctx context.Context, workspaceID uuid.UUID) (int, error) {
	products, err := s.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       500,
		Offset:      0,
	})
	if err != nil {
		return 0, err
	}

	analyzed := 0
	for _, p := range products {
		product := productFromSqlc(p)
		if _, err := s.AnalyzeProduct(ctx, workspaceID, product); err != nil {
			continue
		}
		analyzed++
	}

	s.logger.Info().
		Str("workspace_id", workspaceID.String()).
		Int("analyzed", analyzed).
		Msg("SEO analysis completed")

	return analyzed, nil
}

// GetAnalysis returns the latest SEO analysis for a product.
func (s *SEOAnalyzerService) GetAnalysis(ctx context.Context, productID uuid.UUID) (*domain.SEOAnalysis, error) {
	row, err := s.queries.GetLatestSEOAnalysis(ctx, uuidToPgtype(productID))
	if err != nil {
		return nil, err
	}

	analysis := &domain.SEOAnalysis{
		ID:               uuidFromPgtype(row.ID),
		WorkspaceID:      uuidFromPgtype(row.WorkspaceID),
		ProductID:        uuidFromPgtype(row.ProductID),
		TitleScore:       int(row.TitleScore),
		DescriptionScore: int(row.DescriptionScore),
		KeywordsScore:    int(row.KeywordsScore),
		OverallScore:     int(row.OverallScore),
		AnalyzedAt:       row.AnalyzedAt.Time,
	}
	_ = json.Unmarshal(row.TitleIssues, &analysis.TitleIssues)
	_ = json.Unmarshal(row.DescriptionIssues, &analysis.DescriptionIssues)
	_ = json.Unmarshal(row.KeywordCoverage, &analysis.KeywordCoverage)
	_ = json.Unmarshal(row.Recommendations, &analysis.Recommendations)

	return analysis, nil
}

func (s *SEOAnalyzerService) analyzeTitle(title string, keywords []string) (int, []domain.SEOIssue) {
	score := 100
	var issues []domain.SEOIssue

	titleLen := utf8.RuneCountInString(title)

	// Too short
	if titleLen < 30 {
		score -= 30
		issues = append(issues, domain.SEOIssue{
			Type: "too_short", Severity: "high", Field: "title",
			Message: "Заголовок слишком короткий. Рекомендуется минимум 30 символов для лучшей индексации.",
		})
	}

	// Too long
	if titleLen > 120 {
		score -= 15
		issues = append(issues, domain.SEOIssue{
			Type: "too_long", Severity: "medium", Field: "title",
			Message: "Заголовок слишком длинный. WB обрезает после ~120 символов.",
		})
	}

	// Check keyword presence in title
	titleLower := strings.ToLower(title)
	keywordsFound := 0
	for _, kw := range keywords[:min(10, len(keywords))] {
		if strings.Contains(titleLower, strings.ToLower(kw)) {
			keywordsFound++
		}
	}

	if keywordsFound == 0 && len(keywords) > 0 {
		score -= 25
		issues = append(issues, domain.SEOIssue{
			Type: "missing_keyword", Severity: "high", Field: "title",
			Message: "Заголовок не содержит ни одного популярного ключевого слова.",
		})
	}

	// All caps check
	upperCount := 0
	for _, r := range title {
		if r >= 'A' && r <= 'Z' || r >= 'А' && r <= 'Я' {
			upperCount++
		}
	}
	if titleLen > 0 && float64(upperCount)/float64(titleLen) > 0.5 {
		score -= 10
		issues = append(issues, domain.SEOIssue{
			Type: "caps_abuse", Severity: "low", Field: "title",
			Message: "Заголовок содержит слишком много заглавных букв.",
		})
	}

	if score < 0 {
		score = 0
	}
	return score, issues
}

func (s *SEOAnalyzerService) checkKeywordCoverage(title string, keywords []string) map[string]bool {
	coverage := make(map[string]bool)
	titleLower := strings.ToLower(title)
	for _, kw := range keywords[:min(20, len(keywords))] {
		coverage[kw] = strings.Contains(titleLower, strings.ToLower(kw))
	}
	return coverage
}

func (s *SEOAnalyzerService) generateRecommendations(product domain.Product, analysis *domain.SEOAnalysis, keywords []string) []domain.SEORecommendation {
	var recs []domain.SEORecommendation

	// Missing keywords
	for kw, covered := range analysis.KeywordCoverage {
		if !covered {
			recs = append(recs, domain.SEORecommendation{
				Type:       "add_keyword",
				Priority:   1,
				Message:    "Добавьте ключевое слово «" + kw + "» в заголовок или описание.",
				Suggestion: kw,
			})
			if len(recs) >= 5 {
				break
			}
		}
	}

	// Title improvements
	for _, issue := range analysis.TitleIssues {
		if issue.Severity == "high" {
			recs = append(recs, domain.SEORecommendation{
				Type:     "improve_title",
				Priority: 2,
				Message:  issue.Message,
			})
		}
	}

	return recs
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
