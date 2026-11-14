package domain

import "encoding/json"

type NativeAdLaunchProduct struct {
	ProductID      string            `json:"product_id"`
	BidsRub        map[string]string `json:"bids_rub"`
	MinimumBidsRub map[string]string `json:"minimum_bids_rub"`
}
type NativeAdLaunchSettings struct {
	Strategy             string                  `json:"strategy"`
	PaymentType          string                  `json:"payment_type"`
	Placements           []string                `json:"placements"`
	Products             []NativeAdLaunchProduct `json:"products"`
	Budget               *NativeAdBudget         `json:"budget"`
	EligibilityVerified  bool                    `json:"eligibility_verified"`
	VendorLimitsVerified bool                    `json:"vendor_limits_verified"`
}
type NativeAdLaunchContext struct {
	Campaign NativeAdCampaign
	Settings NativeAdLaunchSettings
}
type ManagerAdLaunchPlan struct {
	Settings NativeAdLaunchSettings `json:"settings"`
}

// The spent-today counter is an observation, not an approved limit. Settings and
// exact configured bids remain part of the immutable decision.
func StableAdLaunchSettings(v NativeAdLaunchSettings) NativeAdLaunchSettings {
	if v.Budget != nil {
		b := *v.Budget
		b.SpentTodayRub = nil
		v.Budget = &b
	}
	return v
}
func SameAdLaunchSettings(a, b NativeAdLaunchSettings) bool {
	x, err := json.Marshal(StableAdLaunchSettings(a))
	if err != nil {
		return false
	}
	y, err := json.Marshal(StableAdLaunchSettings(b))
	return err == nil && string(x) == string(y)
}
