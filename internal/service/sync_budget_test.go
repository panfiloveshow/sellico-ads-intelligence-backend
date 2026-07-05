package service

import (
	"errors"
	"testing"
)

// campaignBudgetAccessFailure distinguishes account-level access/limit problems
// (which stop the whole budget phase) from transient per-campaign failures like
// timeouts and one-off 4xx (which are skipped silently as best-effort noise).
func TestCampaignBudgetAccessFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "rate limited stops the phase", err: errors.New("rate limited (429) on /adv/v1/budget"), want: true},
		{name: "forbidden means cabinet cannot collect budgets", err: errors.New("client error (403) on /adv/v1/budget"), want: true},
		{name: "timeout is transient, not an access failure", err: errors.New("context deadline exceeded"), want: false},
		{name: "campaign-specific bad request is transient", err: errors.New("client error (400) on /adv/v1/budget?id=123"), want: false},
		{name: "campaign-specific missing budget is transient", err: errors.New("client error (404) on /adv/v1/budget?id=123"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := campaignBudgetAccessFailure(tt.err); got != tt.want {
				t.Fatalf("campaignBudgetAccessFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}
