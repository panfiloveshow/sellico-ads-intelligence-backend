package sqlcgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetProductStockPersistsRealSnapshotAtomically(t *testing.T) {
	normalized := strings.ToLower(setProductStock)
	assert.Contains(t, normalized, "with updated as")
	assert.Contains(t, normalized, "update products")
	assert.Contains(t, normalized, "insert into product_snapshots")
	assert.Contains(t, normalized, "stock_total")
}
