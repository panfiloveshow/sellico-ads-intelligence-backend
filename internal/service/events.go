package service

import (
	"sync"

	"github.com/google/uuid"
)

// Event represents a real-time event pushed to SSE subscribers.
type Event struct {
	Type        string `json:"type"`        // e.g. "sync_complete", "recommendation_new", "export_ready"
	WorkspaceID string `json:"workspace_id"`
	Payload     any    `json:"payload"`
}

// EventBroker manages SSE subscriptions per workspace.
type EventBroker struct {
	mu          sync.RWMutex
	subscribers map[uuid.UUID]map[chan Event]struct{}
}

// NewEventBroker creates a new EventBroker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		subscribers: make(map[uuid.UUID]map[chan Event]struct{}),
	}
}

// Subscribe creates a new event channel for a workspace.
// The caller must call Unsubscribe when done.
func (b *EventBroker) Subscribe(workspaceID uuid.UUID) chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, 16)
	if _, ok := b.subscribers[workspaceID]; !ok {
		b.subscribers[workspaceID] = make(map[chan Event]struct{})
	}
	b.subscribers[workspaceID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes an event channel.
func (b *EventBroker) Unsubscribe(workspaceID uuid.UUID, ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if subs, ok := b.subscribers[workspaceID]; ok {
		delete(subs, ch)
		close(ch)
		if len(subs) == 0 {
			delete(b.subscribers, workspaceID)
		}
	}
}

// Publish sends an event to all subscribers of a workspace.
func (b *EventBroker) Publish(workspaceID uuid.UUID, event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	subs, ok := b.subscribers[workspaceID]
	if !ok {
		return
	}

	for ch := range subs {
		select {
		case ch <- event:
		default:
			// Drop event if subscriber is too slow
		}
	}
}
