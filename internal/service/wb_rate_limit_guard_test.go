package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWBPricesEndpointsSharePersistedCooldown(t *testing.T) {
	for _, endpoint := range []string{
		wbEndpointPricesList,
		wbEndpointPricesUpload,
		wbEndpointPricesPoll,
		wbEndpointPricesQuarantine,
	} {
		assert.Equal(t, wbEndpointPricesShared, wbRateLimitStorageKey(endpoint))
	}
	assert.Equal(t, wbEndpointBudget, wbRateLimitStorageKey(wbEndpointBudget))
}
