package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventBroker_SubscribeAndPublish(t *testing.T) {
	broker := NewEventBroker()
	wsID := uuid.New()

	ch := broker.Subscribe(wsID)
	defer broker.Unsubscribe(wsID, ch)

	event := Event{Type: "test_event", WorkspaceID: wsID.String(), Payload: "hello"}
	broker.Publish(wsID, event)

	select {
	case received := <-ch:
		assert.Equal(t, "test_event", received.Type)
		assert.Equal(t, "hello", received.Payload)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBroker_MultipleSubscribers(t *testing.T) {
	broker := NewEventBroker()
	wsID := uuid.New()

	ch1 := broker.Subscribe(wsID)
	ch2 := broker.Subscribe(wsID)
	defer broker.Unsubscribe(wsID, ch1)
	defer broker.Unsubscribe(wsID, ch2)

	event := Event{Type: "broadcast", WorkspaceID: wsID.String()}
	broker.Publish(wsID, event)

	select {
	case r := <-ch1:
		assert.Equal(t, "broadcast", r.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1 timed out")
	}

	select {
	case r := <-ch2:
		assert.Equal(t, "broadcast", r.Type)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 timed out")
	}
}

func TestEventBroker_UnsubscribeRemovesChannel(t *testing.T) {
	broker := NewEventBroker()
	wsID := uuid.New()

	ch := broker.Subscribe(wsID)
	broker.Unsubscribe(wsID, ch)

	// Channel should be closed
	_, ok := <-ch
	assert.False(t, ok)
}

func TestEventBroker_PublishToWrongWorkspace(t *testing.T) {
	broker := NewEventBroker()
	ws1 := uuid.New()
	ws2 := uuid.New()

	ch := broker.Subscribe(ws1)
	defer broker.Unsubscribe(ws1, ch)

	broker.Publish(ws2, Event{Type: "other"})

	select {
	case <-ch:
		t.Fatal("should not receive event for different workspace")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestEventBroker_SlowSubscriberDropsEvents(t *testing.T) {
	broker := NewEventBroker()
	wsID := uuid.New()

	ch := broker.Subscribe(wsID)
	defer broker.Unsubscribe(wsID, ch)

	// Fill buffer (16) + overflow
	for i := 0; i < 20; i++ {
		broker.Publish(wsID, Event{Type: "flood"})
	}

	// Should have received up to buffer size, extras dropped
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	require.LessOrEqual(t, count, 16)
}
