package main

import (
	"sync"
)

type EventType string

const (
	EventAudit        EventType = "audit"
	EventIntervention EventType = "intervention"
	EventStatus       EventType = "status"
)

type Event struct {
	Type      EventType
	SessionID string
	Payload   interface{}
}

// EventBus distributes events to active transceivers (CLI, TG)
type EventBus struct {
	subs map[chan Event]bool
	mu   sync.RWMutex
}

func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[chan Event]bool),
	}
}

func (eb *EventBus) Subscribe() chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan Event, 100)
	eb.subs[ch] = true
	return ch
}

func (eb *EventBus) Unsubscribe(ch chan Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.subs, ch)
	close(ch)
}

func (eb *EventBus) Publish(e Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for ch := range eb.subs {
		select {
		case ch <- e:
		default: // non-blocking drop if channel is full
		}
	}
}

// Global Event Bus
var GlobalBus = NewEventBus()
