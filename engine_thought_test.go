package main

import (
	"os"
	"testing"
	"time"
)

func TestEngineThoughtEvents(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-thought-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	engine := NewEngine(sm, nil, 5)

	sess := &Session{
		ID:        "sess-thought-test",
		CWD:       ".",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	state := &StateDef{SessionRole: "assistant"}

	// Subscribe to events
	eventCh := GlobalBus.Subscribe()
	defer GlobalBus.Unsubscribe(eventCh)

	// Trigger chunks via Engine's onChunk
	parse := engine.onChunk(sess, state)

	// Simulating streaming chunks
	chunks := []string{"Hello ", "<tho", "ught>thinking</thou", "ght> World"}
	for _, c := range chunks {
		parse(c)
	}

	// Collect events
	var gotChunks, gotThoughts []string
	timeout := time.After(1 * time.Second)

collect:
	for {
		select {
		case e := <-eventCh:
			if e.SessionID == sess.ID && e.Type == EventAudit {
				audit := e.Payload.(AuditEntry)
				if audit.Type == AuditLLMChunk {
					gotChunks = append(gotChunks, audit.Content)
				} else if audit.Type == AuditLLMThought {
					gotThoughts = append(gotThoughts, audit.Content)
				}
			}

			// We expect "Hello ", " World" (possibly split) and "thinking"
			// Actually "Hello " and " World" should be at least 2 chunks.
			// "thinking" should be 1 chunk.
			combinedText := ""
			for _, c := range gotChunks {
				combinedText += c
			}
			combinedThoughts := ""
			for _, th := range gotThoughts {
				combinedThoughts += th
			}

			if combinedText == "Hello  World" && combinedThoughts == "thinking" {
				break collect
			}
		case <-timeout:
			t.Fatalf("timed out waiting for events. text: %q, thoughts: %q", gotChunks, gotThoughts)
		}
	}
}

func TestEngineNoThoughtEvents(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-nothought-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	engine := NewEngine(sm, nil, 5)

	sess := &Session{
		ID:        "sess-nothought-test",
		CWD:       ".",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	state := &StateDef{SessionRole: "assistant"}
	eventCh := GlobalBus.Subscribe()
	defer GlobalBus.Unsubscribe(eventCh)

	parse := engine.onChunk(sess, state)
	parse("Just normal text")

	select {
	case e := <-eventCh:
		if e.SessionID == sess.ID && e.Type == EventAudit {
			audit := e.Payload.(AuditEntry)
			if audit.Type == AuditLLMThought {
				t.Error("did not expect AuditLLMThought event")
			}
			if audit.Type == AuditLLMChunk && audit.Content != "Just normal text" {
				t.Errorf("expected chunk 'Just normal text', got %q", audit.Content)
			}
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for chunk event")
	}
}
