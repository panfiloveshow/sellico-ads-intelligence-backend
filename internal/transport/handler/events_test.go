package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"github.com/stretchr/testify/assert"
)

func TestEventsHandler_Stream_MissingWorkspace(t *testing.T) {
	broker := service.NewEventBroker()
	h := NewEventsHandler(broker)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	rec := httptest.NewRecorder()
	h.Stream(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestEventsHandler_Stream_SendsPing(t *testing.T) {
	broker := service.NewEventBroker()
	h := NewEventsHandler(broker)
	wsID := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, wsID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.Stream(rec, req)
		close(done)
	}()

	// Give the handler a moment to write the ping
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	assert.True(t, strings.Contains(body, "event: ping"))
	assert.True(t, strings.Contains(body, `"status":"connected"`))
}

func TestEventsHandler_Stream_ReceivesEvent(t *testing.T) {
	broker := service.NewEventBroker()
	h := NewEventsHandler(broker)
	wsID := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	ctx = context.WithValue(ctx, middleware.WorkspaceIDKey, wsID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.Stream(rec, req)
		close(done)
	}()

	// Wait for subscription, publish event, then cancel
	time.Sleep(50 * time.Millisecond)
	broker.Publish(wsID, service.Event{Type: "test_event", WorkspaceID: wsID.String(), Payload: "data123"})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	assert.Contains(t, body, "event: test_event")
	assert.Contains(t, body, "data123")
}
