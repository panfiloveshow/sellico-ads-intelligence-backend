package service

import (
	"errors"
	"testing"
)

func TestCampaignBudgetFailureShouldStop(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "rate limited", err: errors.New("rate limited (429) on /adv/v1/budget"), want: true},
		{name: "forbidden means cabinet cannot collect budgets", err: errors.New("client error (403) on /adv/v1/budget"), want: true},
		{name: "timeout should stop after repeated failures", err: errors.New("context deadline exceeded"), want: true},
		{name: "campaign-specific bad request should not stop other campaigns", err: errors.New("client error (400) on /adv/v1/budget?id=123"), want: false},
		{name: "campaign-specific missing budget should not stop other campaigns", err: errors.New("client error (404) on /adv/v1/budget?id=123"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := campaignBudgetFailureShouldStop(tt.err); got != tt.want {
				t.Fatalf("campaignBudgetFailureShouldStop() = %v, want %v", got, tt.want)
			}
		})
	}
}
