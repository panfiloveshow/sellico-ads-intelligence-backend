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
	assert.Equal(t, DefaultCampaignMaxSpendNoOrder, merged.CampaignMaxSpendNoOrder)
	assert.Equal(t, DefaultCampaignMaxTestSpend, merged.CampaignMaxTestSpend)
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
	assert.Equal(t, DefaultCampaignMaxSpendNoOrder, merged.CampaignMaxSpendNoOrder)
	assert.Equal(t, DefaultCampaignMaxTestSpend, merged.CampaignMaxTestSpend)
	assert.Equal(t, DefaultCampaignHighCPC, merged.CampaignHighCPC)
	assert.Equal(t, DefaultPhraseHighImpressions, merged.PhraseHighImpressions)
}

func TestRecommendationThresholds_Merged_FullOverride(t *testing.T) {
	th := &RecommendationThresholds{
		CampaignHighImpressions: 500,
		CampaignZeroOrdersClick: 20,
		CampaignMaxSpendNoOrder: 2500,
		CampaignMaxTestSpend:    4000,
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
	assert.Equal(t, int64(2500), merged.CampaignMaxSpendNoOrder)
	assert.Equal(t, int64(4000), merged.CampaignMaxTestSpend)
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
	assert.Equal(t, DefaultCampaignMaxSpendNoOrder, th.CampaignMaxSpendNoOrder)
	assert.Equal(t, DefaultCampaignMaxTestSpend, th.CampaignMaxTestSpend)
	assert.Equal(t, DefaultSERPCompetitorTop, th.SERPCompetitorTop)
}

func TestWorkspaceSettingsValidateEmailRecipients(t *testing.T) {
	settings := WorkspaceSettings{
		Notifications: &NotificationSettings{
			Email: &EmailSettings{
				Enabled:       true,
				Recipients:    []string{"owner@example.com", "client@example.com"},
				ClientReports: true,
			},
		},
	}

	assert.Empty(t, settings.Validate())
}

func TestWorkspaceSettingsValidateEmailRequiresRecipientsWhenEnabled(t *testing.T) {
	settings := WorkspaceSettings{
		Notifications: &NotificationSettings{
			Email: &EmailSettings{Enabled: true},
		},
	}

	errs := settings.Validate()
	assert.Equal(t, "must contain at least one recipient when email notifications are enabled", errs["notifications.email.recipients"])
}

func TestWorkspaceSettingsValidateEmailRejectsInvalidRecipient(t *testing.T) {
	settings := WorkspaceSettings{
		Notifications: &NotificationSettings{
			Email: &EmailSettings{
				Recipients: []string{"bad-address"},
			},
		},
	}

	errs := settings.Validate()
	assert.Equal(t, "must be a valid email address", errs["notifications.email.recipients.0"])
}

func TestWorkspaceSettingsValidateAutomationRequiresExplicitDailyActionCap(t *testing.T) {
	settings := WorkspaceSettings{Automation: &AutomationSettings{Enabled: true}}
	errs := settings.Validate()
	assert.Equal(t, "is required and must be greater than 0 when live automation is enabled", errs["automation.max_bid_changes_per_day"])

	settings.Automation.MaxBidChangesPerDay = 25
	assert.Empty(t, settings.Validate())

	// An emergency hold must remain writable even on legacy settings which do
	// not have the newly required cap yet.
	settings.Automation.MaxBidChangesPerDay = 0
	settings.Automation.ManualHold = true
	assert.Empty(t, settings.Validate())
}

func TestWorkspaceSettingsValidateAutomationRejectsNegativeDailyActionCap(t *testing.T) {
	settings := WorkspaceSettings{Automation: &AutomationSettings{MaxBidChangesPerDay: -1}}
	errs := settings.Validate()
	assert.Equal(t, "must be greater than 0 when set", errs["automation.max_bid_changes_per_day"])
}
