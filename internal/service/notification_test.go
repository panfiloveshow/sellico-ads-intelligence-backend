package service

import (
	"testing"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestNotificationService_FormatRecommendationsSummary(t *testing.T) {
	svc := &NotificationService{}

	recs := []domain.Recommendation{
		{Title: "High impressions zero clicks", Severity: "high"},
		{Title: "Campaign bid pressure too high", Severity: "medium"},
		{Title: "Phrase low engagement", Severity: "medium"},
	}

	text := svc.formatRecommendationsSummary(recs)
	assert.Contains(t, text, "New Recommendations: 3")
	assert.Contains(t, text, "[!] High severity: 1")
	assert.Contains(t, text, "[i] Medium severity: 2")
	assert.Contains(t, text, "High impressions zero clicks")
}

func TestNotificationService_FormatRecommendationsSummary_MoreThan5(t *testing.T) {
	svc := &NotificationService{}

	recs := make([]domain.Recommendation, 8)
	for i := range recs {
		recs[i] = domain.Recommendation{Title: "Rec", Severity: "medium"}
	}

	text := svc.formatRecommendationsSummary(recs)
	assert.Contains(t, text, "New Recommendations: 8")
	assert.Contains(t, text, "...and 3 more")
}
