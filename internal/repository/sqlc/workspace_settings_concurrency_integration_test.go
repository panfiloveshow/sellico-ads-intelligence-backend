//go:build integration

package sqlcgen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/testutil"
)

func TestWorkspaceSettingsConcurrentPartialUpdatesDoNotRestoreStaleCap(t *testing.T) {
	db, cleanup := testutil.NewTestDB(t)
	defer cleanup()
	ctx := context.Background()

	workspace, err := db.Queries.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{Name: "settings-race", Slug: "settings-race"})
	require.NoError(t, err)
	workspaceID := uuid.UUID(workspace.ID.Bytes)
	settingsService := service.NewWorkspaceSettingsService(db.Queries)

	_, err = settingsService.UpdateSettings(ctx, uuid.New(), workspaceID, domain.WorkspaceSettings{
		Automation: &domain.AutomationSettings{Enabled: true, MaxBidChangesPerDay: 10},
	})
	require.NoError(t, err)

	// Hold the exact application lock. With the old read-before-lock merge both
	// requests read cap=10 now and later overwrite one another. With the fixed
	// lock-before-read flow both reads wait, then execute serially.
	blocker, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	_, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':workspace-daily-bid-actions', 0))`, workspace.ID)
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, updateErr := settingsService.UpdateSettings(ctx, uuid.New(), workspaceID, domain.WorkspaceSettings{
			Automation: &domain.AutomationSettings{
				Enabled: true, ManualHold: true, HoldReason: "operator review", MaxBidChangesPerDay: 2,
			},
		})
		results <- updateErr
	}()
	go func() {
		<-start
		_, updateErr := settingsService.UpdateSettings(ctx, uuid.New(), workspaceID, domain.WorkspaceSettings{
			Notifications: &domain.NotificationSettings{
				Telegram: &domain.TelegramSettings{Enabled: false, ChatID: "ops"},
			},
		})
		results <- updateErr
	}()
	close(start)
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, blocker.Commit(ctx))

	for range 2 {
		select {
		case updateErr := <-results:
			require.NoError(t, updateErr)
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent settings update timed out")
		}
	}

	settings, err := settingsService.GetSettings(ctx, workspaceID)
	require.NoError(t, err)
	require.NotNil(t, settings.Automation)
	require.Equal(t, 2, settings.Automation.MaxBidChangesPerDay)
	require.True(t, settings.Automation.ManualHold)
	require.NotNil(t, settings.Notifications)
	require.NotNil(t, settings.Notifications.Telegram)
	require.Equal(t, "ops", settings.Notifications.Telegram.ChatID)
}
