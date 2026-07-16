package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

func TestClassifySellerCabinetLookupErrorOnlyNoRowsIsNotFound(t *testing.T) {
	notFound := classifySellerCabinetLookupError(pgx.ErrNoRows)
	assert.True(t, apperror.Is(notFound, apperror.ErrNotFound))

	deadline := classifySellerCabinetLookupError(context.DeadlineExceeded)
	assert.False(t, apperror.Is(deadline, apperror.ErrNotFound))
	assert.True(t, errors.Is(deadline, context.DeadlineExceeded))

	dbErr := errors.New("database unavailable")
	classified := classifySellerCabinetLookupError(dbErr)
	assert.False(t, apperror.Is(classified, apperror.ErrNotFound))
	assert.ErrorIs(t, classified, dbErr)
}

func TestCabinetReadinessWriteContextSurvivesParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := cabinetReadinessWriteContext(parent)
	defer cancel()

	require.NoError(t, ctx.Err())
	select {
	case <-ctx.Done():
		t.Fatalf("detached readiness context ended early: %v", ctx.Err())
	case <-time.After(5 * time.Millisecond):
	}
}

func TestCabinetSyncReadinessStatusRequiresCompleteIssueFreeRun(t *testing.T) {
	assert.Equal(t, "ready", cabinetSyncReadinessStatus(SyncSummary{Campaigns: 1}, nil))
	assert.Equal(t, "partial", cabinetSyncReadinessStatus(SyncSummary{
		Campaigns: 1,
		Issues:    []SyncIssue{{Stage: "phrase_stats.bids", Message: "incomplete"}},
	}, nil))
	assert.Equal(t, "failed", cabinetSyncReadinessStatus(SyncSummary{}, errors.New("sync failed")))
}
