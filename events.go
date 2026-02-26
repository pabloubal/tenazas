package main

import (
	"sync"
	"time"
)

type EventType string

const (
	EventAudit        EventType = "audit"
	EventIntervention EventType = "intervention"
	EventStatus       EventType = "status"
)

const (
	AuditLLMPrompt    = "llm_prompt"
	AuditLLMResponse  = "llm_response"
	AuditLLMChunk     = "llm_response_chunk"
	AuditLLMThought   = "llm_thought"
	AuditCmdResult    = "cmd_result"
	AuditIntervention = "intervention"
	AuditStatus       = "status"
	AuditInfo         = "info"
)

type Event struct {
	Type      EventType
	SessionID string
	Payload   interface{}
}

const (
	maxEventHistory = 10
)

// EventBus distributes events to active transceivers (CLI, TG)
type EventBus struct {
	subs map[chan Event]bool
	mu   sync.RWMutex
	last []Event
}

func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[chan Event]bool),
		last: make([]Event, 0, maxEventHistory),
	}
}

func (eb *EventBus) Subscribe() chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan Event, 100)
	eb.subs[ch] = true
	// Replay last events
	for _, e := range eb.last {
		ch <- e
	}
	return ch
}

func (eb *EventBus) Unsubscribe(ch chan Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	delete(eb.subs, ch)
	close(ch)
}

func (eb *EventBus) Publish(e Event) {
	eb.mu.Lock()
	// Update history
	eb.last = append(eb.last, e)
	if len(eb.last) > maxEventHistory {
		eb.last = eb.last[1:]
	}
	eb.mu.Unlock()

	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for ch := range eb.subs {
		select {
		case ch <- e:
		case <-time.After(10 * time.Millisecond):
			// Drop if it still can't send after 10ms (prevents blocking publisher indefinitely)
		}
	}
}

// FilterForSession wraps a channel to only receive events for a specific session
func FilterForSession(in chan Event, sessionID string) chan AuditEntry {
	out := make(chan AuditEntry, 10)
	go func() {
		defer close(out)
		for e := range in {
			if e.SessionID == sessionID && e.Type == EventAudit {
				if audit, ok := e.Payload.(AuditEntry); ok {
					out <- audit
				}
			}
		}
	}()
	return out
}

// Global Event Bus
var GlobalBus = NewEventBus()
