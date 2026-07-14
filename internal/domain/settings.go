package domain

import (
	"net/mail"
	"strconv"
	"strings"
)

// WorkspaceSettings holds per-workspace configuration stored as JSONB.
type WorkspaceSettings struct {
	RecommendationThresholds *RecommendationThresholds `json:"recommendation_thresholds,omitempty"`
	Notifications            *NotificationSettings     `json:"notifications,omitempty"`
	Automation               *AutomationSettings       `json:"automation,omitempty"`
}

// AutomationSettings is the workspace-level fail-closed switch for external
// advertising actions. A missing block or Enabled=false means analytics only.
type AutomationSettings struct {
	Enabled    bool   `json:"enabled"`
	ManualHold bool   `json:"manual_hold,omitempty"`
	HoldReason string `json:"hold_reason,omitempty"`
}

// RecommendationThresholds configures the recommendation engine per workspace.
// Zero values mean "use default".
type RecommendationThresholds struct {
	CampaignHighImpressions int     `json:"campaign_high_impressions,omitempty"`   // default: 1000
	CampaignZeroOrdersClick int     `json:"campaign_zero_orders_click,omitempty"`  // default: 40
	CampaignMaxSpendNoOrder int64   `json:"campaign_max_spend_no_order,omitempty"` // default: 1500
	CampaignMaxTestSpend    int64   `json:"campaign_max_test_spend,omitempty"`     // default: 3000
	CampaignHighCPC         float64 `json:"campaign_high_cpc,omitempty"`           // default: 50
	CampaignPoorCPO         float64 `json:"campaign_poor_cpo,omitempty"`           // default: 1500
	CampaignRaiseBidClicks  int     `json:"campaign_raise_bid_clicks,omitempty"`   // default: 40
	CampaignRaiseBidOrders  int     `json:"campaign_raise_bid_orders,omitempty"`   // default: 5
	CampaignStrongROAS      float64 `json:"campaign_strong_roas,omitempty"`        // default: 4.0
	PhraseHighImpressions   int     `json:"phrase_high_impressions,omitempty"`     // default: 500
	PhraseBidRaiseClicks    int     `json:"phrase_bid_raise_clicks,omitempty"`     // default: 10
	PositionDropThreshold   int     `json:"position_drop_threshold,omitempty"`     // default: 10
	SERPCompetitorTop       int     `json:"serp_competitor_top,omitempty"`         // default: 5
}

// NotificationSettings configures external notification channels.
type NotificationSettings struct {
	Telegram *TelegramSettings `json:"telegram,omitempty"`
	Email    *EmailSettings    `json:"email,omitempty"`
}

// TelegramSettings configures the Telegram notification channel.
type TelegramSettings struct {
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// EmailSettings configures report email recipients. SMTP delivery is configured separately.
type EmailSettings struct {
	Enabled          bool     `json:"enabled"`
	Recipients       []string `json:"recipients,omitempty"`
	WeeklyDigest     bool     `json:"weekly_digest,omitempty"`
	DailyOwnerDigest bool     `json:"daily_owner_digest,omitempty"`
	ClientReports    bool     `json:"client_reports,omitempty"`
}

// Validate returns field-level validation errors for workspace settings.
func (s WorkspaceSettings) Validate() map[string]string {
	errors := make(map[string]string)
	if s.Notifications == nil || s.Notifications.Email == nil {
		return errors
	}

	email := s.Notifications.Email
	if email.Enabled && len(email.Recipients) == 0 {
		errors["notifications.email.recipients"] = "must contain at least one recipient when email notifications are enabled"
		return errors
	}
	for i, recipient := range email.Recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			errors[emailRecipientField(i)] = "must be a valid email address"
			continue
		}
		address, err := mail.ParseAddress(recipient)
		if err != nil || address.Address != recipient {
			errors[emailRecipientField(i)] = "must be a valid email address"
		}
	}
	return errors
}

func emailRecipientField(index int) string {
	return "notifications.email.recipients." + strconv.Itoa(index)
}

// Defaults for recommendation thresholds.
const (
	DefaultCampaignHighImpressions = 1000
	DefaultCampaignZeroOrdersClick = 40
	DefaultCampaignMaxSpendNoOrder = int64(1500)
	DefaultCampaignMaxTestSpend    = int64(3000)
	DefaultCampaignHighCPC         = 50.0
	DefaultCampaignPoorCPO         = 1500.0
	DefaultCampaignRaiseBidClicks  = 40
	DefaultCampaignRaiseBidOrders  = 5
	DefaultCampaignStrongROAS      = 4.0
	DefaultPhraseHighImpressions   = 500
	DefaultPhraseBidRaiseClicks    = 10
	DefaultPositionDropThreshold   = 10
	DefaultSERPCompetitorTop       = 5
)

// Merged returns thresholds with defaults applied for any zero values.
func (t *RecommendationThresholds) Merged() RecommendationThresholds {
	if t == nil {
		return DefaultThresholds()
	}
	out := *t
	if out.CampaignHighImpressions == 0 {
		out.CampaignHighImpressions = DefaultCampaignHighImpressions
	}
	if out.CampaignZeroOrdersClick == 0 {
		out.CampaignZeroOrdersClick = DefaultCampaignZeroOrdersClick
	}
	if out.CampaignMaxSpendNoOrder == 0 {
		out.CampaignMaxSpendNoOrder = DefaultCampaignMaxSpendNoOrder
	}
	if out.CampaignMaxTestSpend == 0 {
		out.CampaignMaxTestSpend = DefaultCampaignMaxTestSpend
	}
	if out.CampaignHighCPC == 0 {
		out.CampaignHighCPC = DefaultCampaignHighCPC
	}
	if out.CampaignPoorCPO == 0 {
		out.CampaignPoorCPO = DefaultCampaignPoorCPO
	}
	if out.CampaignRaiseBidClicks == 0 {
		out.CampaignRaiseBidClicks = DefaultCampaignRaiseBidClicks
	}
	if out.CampaignRaiseBidOrders == 0 {
		out.CampaignRaiseBidOrders = DefaultCampaignRaiseBidOrders
	}
	if out.CampaignStrongROAS == 0 {
		out.CampaignStrongROAS = DefaultCampaignStrongROAS
	}
	if out.PhraseHighImpressions == 0 {
		out.PhraseHighImpressions = DefaultPhraseHighImpressions
	}
	if out.PhraseBidRaiseClicks == 0 {
		out.PhraseBidRaiseClicks = DefaultPhraseBidRaiseClicks
	}
	if out.PositionDropThreshold == 0 {
		out.PositionDropThreshold = DefaultPositionDropThreshold
	}
	if out.SERPCompetitorTop == 0 {
		out.SERPCompetitorTop = DefaultSERPCompetitorTop
	}
	return out
}

// DefaultThresholds returns all-default thresholds.
func DefaultThresholds() RecommendationThresholds {
	return RecommendationThresholds{
		CampaignHighImpressions: DefaultCampaignHighImpressions,
		CampaignZeroOrdersClick: DefaultCampaignZeroOrdersClick,
		CampaignMaxSpendNoOrder: DefaultCampaignMaxSpendNoOrder,
		CampaignMaxTestSpend:    DefaultCampaignMaxTestSpend,
		CampaignHighCPC:         DefaultCampaignHighCPC,
		CampaignPoorCPO:         DefaultCampaignPoorCPO,
		CampaignRaiseBidClicks:  DefaultCampaignRaiseBidClicks,
		CampaignRaiseBidOrders:  DefaultCampaignRaiseBidOrders,
		CampaignStrongROAS:      DefaultCampaignStrongROAS,
		PhraseHighImpressions:   DefaultPhraseHighImpressions,
		PhraseBidRaiseClicks:    DefaultPhraseBidRaiseClicks,
		PositionDropThreshold:   DefaultPositionDropThreshold,
		SERPCompetitorTop:       DefaultSERPCompetitorTop,
	}
}
