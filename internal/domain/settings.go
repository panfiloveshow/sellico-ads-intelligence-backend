package domain

// WorkspaceSettings holds per-workspace configuration stored as JSONB.
type WorkspaceSettings struct {
	RecommendationThresholds *RecommendationThresholds `json:"recommendation_thresholds,omitempty"`
	Notifications            *NotificationSettings     `json:"notifications,omitempty"`
}

// RecommendationThresholds configures the recommendation engine per workspace.
// Zero values mean "use default".
type RecommendationThresholds struct {
	CampaignHighImpressions int     `json:"campaign_high_impressions,omitempty"` // default: 1000
	CampaignZeroOrdersClick int     `json:"campaign_zero_orders_click,omitempty"` // default: 40
	CampaignHighCPC         float64 `json:"campaign_high_cpc,omitempty"`          // default: 50
	CampaignPoorCPO         float64 `json:"campaign_poor_cpo,omitempty"`          // default: 1500
	CampaignRaiseBidClicks  int     `json:"campaign_raise_bid_clicks,omitempty"`  // default: 40
	CampaignRaiseBidOrders  int     `json:"campaign_raise_bid_orders,omitempty"`  // default: 5
	CampaignStrongROAS      float64 `json:"campaign_strong_roas,omitempty"`       // default: 4.0
	PhraseHighImpressions   int     `json:"phrase_high_impressions,omitempty"`    // default: 500
	PhraseBidRaiseClicks    int     `json:"phrase_bid_raise_clicks,omitempty"`    // default: 10
	PositionDropThreshold   int     `json:"position_drop_threshold,omitempty"`    // default: 10
	SERPCompetitorTop       int     `json:"serp_competitor_top,omitempty"`        // default: 5
}

// NotificationSettings configures external notification channels.
type NotificationSettings struct {
	Telegram *TelegramSettings `json:"telegram,omitempty"`
}

// TelegramSettings configures the Telegram notification channel.
type TelegramSettings struct {
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// Defaults for recommendation thresholds.
const (
	DefaultCampaignHighImpressions = 1000
	DefaultCampaignZeroOrdersClick = 40
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
