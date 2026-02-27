package main

import (
	"testing"
	"time"
)

func TestEventBus(t *testing.T) {
	bus := NewEventBus()

	ch := bus.Subscribe()

	ev := Event{Type: EventAudit, SessionID: "test-s", Payload: "hello"}
	bus.Publish(ev)

	select {
	case received := <-ch:
		if received.SessionID != "test-s" {
			t.Errorf("expected session ID test-s, got %s", received.SessionID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}

	bus.Unsubscribe(ch)
}
