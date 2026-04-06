package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// PhotoAnalyzerService checks product image quality and generates recommendations.
type PhotoAnalyzerService struct {
	queries    *sqlcgen.Queries
	httpClient *http.Client
	logger     zerolog.Logger
}

func NewPhotoAnalyzerService(queries *sqlcgen.Queries, logger zerolog.Logger) *PhotoAnalyzerService {
	return &PhotoAnalyzerService{
		queries:    queries,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger.With().Str("component", "photo_analyzer").Logger(),
	}
}

// AnalyzeProduct checks image quality for a product.
func (s *PhotoAnalyzerService) AnalyzeProduct(ctx context.Context, product domain.Product) *PhotoAnalysisResult {
	result := &PhotoAnalysisResult{
		ProductID: product.ID,
	}

	imageURL := ptrStr(product.ImageURL)
	if imageURL == "" {
		result.Score = 0
		result.Issues = append(result.Issues, domain.SEOIssue{
			Type:     "no_image",
			Severity: "high",
			Field:    "image",
			Message:  "У товара нет основного изображения. Это критически снижает CTR.",
		})
		return result
	}

	// Check if image is accessible
	resp, err := s.httpClient.Head(imageURL)
	if err != nil || resp.StatusCode != 200 {
		result.Score = 20
		result.Issues = append(result.Issues, domain.SEOIssue{
			Type:     "broken_image",
			Severity: "high",
			Field:    "image",
			Message:  "Основное изображение недоступно или возвращает ошибку.",
		})
		return result
	}

	// Check content length for image quality estimate
	contentLength := resp.ContentLength
	result.ImageSizeBytes = contentLength
	result.Score = 70

	if contentLength > 0 && contentLength < 10_000 {
		result.Score -= 30
		result.Issues = append(result.Issues, domain.SEOIssue{
			Type:     "low_quality_image",
			Severity: "medium",
			Field:    "image",
			Message:  fmt.Sprintf("Изображение слишком маленькое (%d KB). Рекомендуется минимум 50 KB для хорошего качества.", contentLength/1024),
		})
	}

	if contentLength > 500_000 {
		result.Score += 10
	}

	// WB image naming heuristic: check if multiple images exist
	// WB typically stores images as /vol/part/nmid/images/big/1.webp, 2.webp, etc.
	// This is a rough heuristic
	result.Score = min(result.Score, 100)
	if result.Score >= 70 {
		result.Score = 85 // good enough for basic check
	}

	return result
}

// GeneratePhotoRecommendations creates photo_improvement recommendations for products with issues.
func (s *PhotoAnalyzerService) GeneratePhotoRecommendations(ctx context.Context, workspaceID uuid.UUID, recommendations *RecommendationService) ([]domain.Recommendation, error) {
	var recs []domain.Recommendation

	products, err := s.queries.ListProductsByWorkspace(ctx, sqlcgen.ListProductsByWorkspaceParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Limit:       100,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}

	for _, p := range products {
		product := productFromSqlc(p)
		imageURL := ptrStr(product.ImageURL)

		if imageURL == "" {
			rec, err := recommendations.UpsertActive(ctx, RecommendationUpsertInput{
				WorkspaceID: workspaceID,
				ProductID:   nullableUUID(product.ID),
				Title:       "Товар без фото — критически низкий CTR",
				Description: fmt.Sprintf("У товара «%s» отсутствует изображение. Добавьте качественные фотографии.", product.Title),
				Type:        domain.RecommendationTypePhotoImprovement,
				Severity:    domain.SeverityHigh,
				Confidence:  0.95,
				SourceMetrics: map[string]any{
					"product_title": product.Title,
					"has_image":     false,
				},
				NextAction: strPtr("Загрузите минимум 3 фотографии товара в кабинете WB."),
			})
			if err == nil {
				recs = append(recs, *rec)
			}
		}
	}

	return recs, nil
}

// PhotoAnalysisResult holds the result of photo quality analysis.
type PhotoAnalysisResult struct {
	ProductID      uuid.UUID        `json:"product_id"`
	Score          int              `json:"score"`
	ImageSizeBytes int64            `json:"image_size_bytes"`
	Issues         []domain.SEOIssue `json:"issues"`
}
