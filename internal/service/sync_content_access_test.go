package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/wb"
)

// noContentWBClient — токен только с «Продвижением»: карточки отвечают 403.
type noContentWBClient struct{ WBSyncClient }

func (noContentWBClient) ListProducts(context.Context, string) ([]wb.WBProductDTO, error) {
	return nil, fmt.Errorf("%w (403 on /content/v2/get/cards/list)", wb.ErrNoContentAccess)
}

// Токен без «Контента» не должен делать синк partial: иначе защита автоставок
// отключает ставки кабинета, хотя ставкам карточки не нужны.
func TestSyncProducts_NoContentAccessIsWarningNotIssue(t *testing.T) {
	s := &SyncService{wbClient: noContentWBClient{}, logger: zerolog.Nop()}

	summary, err := s.syncProductsForCabinet(context.Background(), uuid.New(), uuid.New(), "token")

	require.NoError(t, err)
	assert.Empty(t, summary.Issues)
	assert.Zero(t, summary.WBErrors)
	require.Len(t, summary.Warnings, 1)
	assert.Equal(t, "products.content_access", summary.Warnings[0].Stage)

	var total SyncSummary
	total.merge(summary)
	assert.NoError(t, total.Error())
	assert.Len(t, total.Warnings, 1)
}
