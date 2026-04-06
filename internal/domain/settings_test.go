package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecommendationThresholds_Merged_AllDefaults(t *testing.T) {
	var th *RecommendationThresholds
	merged := th.Merged()

	assert.Equal(t, DefaultCampaignHighImpressions, merged.CampaignHighImpressions)
	assert.Equal(t, DefaultCampaignZeroOrdersClick, merged.CampaignZeroOrdersClick)
	assert.Equal(t, DefaultCampaignHighCPC, merged.CampaignHighCPC)
	assert.Equal(t, DefaultCampaignPoorCPO, merged.CampaignPoorCPO)
	assert.Equal(t, DefaultCampaignStrongROAS, merged.CampaignStrongROAS)
	assert.Equal(t, DefaultPhraseHighImpressions, merged.PhraseHighImpressions)
	assert.Equal(t, DefaultPhraseBidRaiseClicks, merged.PhraseBidRaiseClicks)
	assert.Equal(t, DefaultPositionDropThreshold, merged.PositionDropThreshold)
	assert.Equal(t, DefaultSERPCompetitorTop, merged.SERPCompetitorTop)
}

func TestRecommendationThresholds_Merged_PartialOverride(t *testing.T) {
	th := &RecommendationThresholds{
		CampaignHighImpressions: 2000,
		CampaignStrongROAS:      6.0,
	}
	merged := th.Merged()

	// Overridden
	assert.Equal(t, 2000, merged.CampaignHighImpressions)
	assert.Equal(t, 6.0, merged.CampaignStrongROAS)

	// Defaults
	assert.Equal(t, DefaultCampaignZeroOrdersClick, merged.CampaignZeroOrdersClick)
	assert.Equal(t, DefaultCampaignHighCPC, merged.CampaignHighCPC)
	assert.Equal(t, DefaultPhraseHighImpressions, merged.PhraseHighImpressions)
}

func TestRecommendationThresholds_Merged_FullOverride(t *testing.T) {
	th := &RecommendationThresholds{
		CampaignHighImpressions: 500,
		CampaignZeroOrdersClick: 20,
		CampaignHighCPC:         30.0,
		CampaignPoorCPO:         1000.0,
		CampaignRaiseBidClicks:  30,
		CampaignRaiseBidOrders:  3,
		CampaignStrongROAS:      3.0,
		PhraseHighImpressions:   300,
		PhraseBidRaiseClicks:    5,
		PositionDropThreshold:   5,
		SERPCompetitorTop:       3,
	}
	merged := th.Merged()

	assert.Equal(t, 500, merged.CampaignHighImpressions)
	assert.Equal(t, 20, merged.CampaignZeroOrdersClick)
	assert.Equal(t, 30.0, merged.CampaignHighCPC)
	assert.Equal(t, 1000.0, merged.CampaignPoorCPO)
	assert.Equal(t, 30, merged.CampaignRaiseBidClicks)
	assert.Equal(t, 3, merged.CampaignRaiseBidOrders)
	assert.Equal(t, 3.0, merged.CampaignStrongROAS)
	assert.Equal(t, 300, merged.PhraseHighImpressions)
	assert.Equal(t, 5, merged.PhraseBidRaiseClicks)
	assert.Equal(t, 5, merged.PositionDropThreshold)
	assert.Equal(t, 3, merged.SERPCompetitorTop)
}

func TestDefaultThresholds(t *testing.T) {
	th := DefaultThresholds()
	assert.Equal(t, DefaultCampaignHighImpressions, th.CampaignHighImpressions)
	assert.Equal(t, DefaultSERPCompetitorTop, th.SERPCompetitorTop)
}
