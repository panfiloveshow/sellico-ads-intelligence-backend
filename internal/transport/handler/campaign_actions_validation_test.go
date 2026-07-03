package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetBidRequestValidate_RejectsInvalidValues(t *testing.T) {
	errs := setBidRequest{Placement: "bad", NewBid: 0}.validate()

	assert.Contains(t, errs["placement"], "must be one of")
	assert.Equal(t, "must be positive", errs["new_bid"])
}

func TestSetBidRequestValidate_AcceptsSupportedPlacements(t *testing.T) {
	for _, placement := range []string{"search", "recommendations", "combined"} {
		errs := setBidRequest{Placement: placement, NewBid: 100}.validate()
		assert.Empty(t, errs)
	}
}

func TestSetClusterBidRequestValidate_RejectsMissingEvidence(t *testing.T) {
	errs := setClusterBidRequest{}.validate()

	assert.Equal(t, "must be positive", errs["nm_id"])
	assert.Equal(t, "is required", errs["norm_query"])
	assert.Equal(t, "must be positive", errs["new_bid"])
}

func TestDeleteClusterBidRequestValidate_RequiresRealBidTarget(t *testing.T) {
	errs := deleteClusterBidRequest{}.validate()

	assert.Equal(t, "must be positive", errs["nm_id"])
	assert.Equal(t, "is required", errs["norm_query"])
	assert.Equal(t, "must be positive", errs["current_bid"])
}

func TestDepositBudgetRequestValidate_RejectsInvalidAmount(t *testing.T) {
	errs := depositBudgetRequest{Amount: 0}.validate()

	assert.Equal(t, "must be positive", errs["amount"])
}

func TestParsePositiveInt64Query_RejectsMissingOrInvalidValue(t *testing.T) {
	missing := httptest.NewRequest("GET", "/campaigns/1/cluster-minus", nil)
	_, err := parsePositiveInt64Query(missing, "nm_id")
	assert.EqualError(t, err, "nm_id is required")

	invalid := httptest.NewRequest("GET", "/campaigns/1/cluster-minus?nm_id=0", nil)
	_, err = parsePositiveInt64Query(invalid, "nm_id")
	assert.EqualError(t, err, "nm_id must be a positive integer")
}
