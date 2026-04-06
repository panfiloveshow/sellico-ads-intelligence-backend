package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/service"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// EventsHandler handles the SSE endpoint for real-time workspace events.
type EventsHandler struct {
	broker *service.EventBroker
}

// NewEventsHandler creates a new EventsHandler.
func NewEventsHandler(broker *service.EventBroker) *EventsHandler {
	return &EventsHandler{broker: broker}
}

// Stream handles GET /api/v1/events — SSE stream for workspace events.
func (h *EventsHandler) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	workspaceID, ok := r.Context().Value(middleware.WorkspaceIDKey).(uuid.UUID)
	if !ok {
		http.Error(w, "missing workspace context", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	ch := h.broker.Subscribe(workspaceID)
	defer h.broker.Unsubscribe(workspaceID, ch)

	// Send initial ping
	fmt.Fprintf(w, "event: ping\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		}
	}
}
