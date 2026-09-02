package sqlcgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetProductStockPersistsRealSnapshotAtomically(t *testing.T) {
	normalized := strings.ToLower(setProductStock)
	assert.Contains(t, normalized, "with current as materialized")
	assert.Contains(t, normalized, "for update")
	assert.Contains(t, normalized, "update products")
	assert.Contains(t, normalized, "insert into product_snapshots")
	assert.Contains(t, normalized, "stock_total")
	assert.Contains(t, normalized, "previous_stock_total is distinct from stock_total")
	assert.Contains(t, normalized, "set captured_at = now()")
	assert.Contains(t, normalized, "previous_stock_total is not distinct from")
	assert.Contains(t, normalized, "not exists")
}
