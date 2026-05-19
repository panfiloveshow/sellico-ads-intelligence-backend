package handler

import (
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
