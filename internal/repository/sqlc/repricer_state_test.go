package sqlcgen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepricerActiveWriteReservationSQL(t *testing.T) {
	t.Parallel()
	require.Contains(t, createPriceChange, "ON CONFLICT (seller_cabinet_id, wb_product_id)")
	require.Contains(t, createPriceChange, "wb_status IN ('pending', 'uploaded')")
	require.Contains(t, createPriceChange, "DO NOTHING")
}

func TestPriceTaskAndChangesAreLinkedInOneStatement(t *testing.T) {
	t.Parallel()
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "WITH task AS")
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "UPDATE price_changes")
	require.Contains(t, createPriceUploadTaskAndLinkChanges, "upload_task_id = (SELECT id FROM task)")
	require.NotContains(t, createPriceUploadTaskAndLinkChanges, "SET status = EXCLUDED.status")
}
