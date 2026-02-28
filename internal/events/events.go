package events

import (
	"sync"
	"time"
)

// EventType identifies the kind of event being published.
type EventType string

const (
	EventAudit        EventType = "audit"
	EventIntervention EventType = "intervention"
	EventStatus       EventType = "status"
	EventTaskStatus   EventType = "task_status"
)

// Audit type constants identify the kind of audit log entry.
const (
	AuditLLMPrompt    = "llm_prompt"
	AuditLLMResponse  = "llm_response"
	AuditLLMChunk     = "llm_response_chunk"
	AuditLLMThought   = "llm_thought"
	AuditCmdResult    = "cmd_result"
	AuditIntent       = "intent"
	AuditIntervention = "intervention"
	AuditStatus       = "status"
	AuditInfo         = "info"
)

// Task state constants for task lifecycle events.
const (
	TaskStateStarted   = "TASK_STARTED"
	TaskStateBlocked   = "TASK_BLOCKED"
	TaskStateCompleted = "TASK_COMPLETED"
	TaskStateFailed    = "TASK_FAILED"
)

// Conversation role constants indicate who is speaking in the audit log.
const (
	RoleUser      = "user"      // Content sent to the LLM (prompts, feedback)
	RoleAssistant = "assistant" // Content received from the LLM (responses, thoughts)
	RoleSystem    = "system"    // Framework events (status, tools, interventions)
)

// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Role      string    `json:"role,omitempty"`
	Content   string    `json:"content"`
	ExitCode  int       `json:"exit_code,omitempty"`
}

// AuditFormatter defines how to render audit logs for different UIs.
type AuditFormatter interface {
	Format(entry AuditEntry) string
}

// TaskStatusPayload carries task lifecycle event data.
type TaskStatusPayload struct {
	State   string            `json:"state"`
	Details map[string]string `json:"details"`
}

// Event is the unit of communication on the EventBus.
type Event struct {
	Type      EventType
	SessionID string
	Payload   interface{}
}

const maxEventHistory = 10

// EventBus distributes events to active transceivers (CLI, TG).
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
		}
	}
}

// FilterForSession wraps a channel to only receive audit events for a specific session.
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

// GlobalBus is the singleton event bus for the application.
var GlobalBus = NewEventBus()
