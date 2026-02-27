package main

import (
	"os"
	"testing"
	"time"
)

func TestEngineMonitoringEvents(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-mon-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	exec := NewExecutor("echo", storageDir) // Dummy
	engine := NewEngine(sm, exec, 5)

	skill := &SkillGraph{
		Name:         "mon-skill",
		InitialState: "start",
		States: map[string]StateDef{
			"start": {
				Type: "end",
			},
		},
	}

	sess := &Session{
		ID:        "sess-mon",
		CWD:       ".",
		SkillName: "mon-skill",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	// Subscribe to events
	eventCh := GlobalBus.Subscribe()
	defer GlobalBus.Unsubscribe(eventCh)

	// Run skill
	engine.Run(skill, sess)

	// Collect events
	var started, completed bool
	timeout := time.After(200 * time.Millisecond)

loop:
	for {
		select {
		case e := <-eventCh:
			if e.SessionID == sess.ID && e.Type == EventTaskStatus {
				payload := e.Payload.(TaskStatusPayload)
				if payload.State == TaskStateStarted {
					started = true
				}
				if payload.State == TaskStateCompleted {
					completed = true
				}
			}
		case <-timeout:
			break loop
		}
	}

	if !started {
		t.Error("Expected TaskStateStarted event")
	}
	if !completed {
		t.Error("Expected TaskStateCompleted event")
	}
}

func TestEngineMonitoringBlockedEvent(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-mon-blocked-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	exec := NewExecutor("echo", storageDir)
	engine := NewEngine(sm, exec, 5)

	skill := &SkillGraph{
		Name:         "blocked-skill",
		InitialState: "step1",
		States: map[string]StateDef{
			"step1": {
				Type:        "action_loop",
				Instruction: "fail",
				VerifyCmd:   "false", // Always fails
				MaxRetries:  1,
				// No OnFailRoute means it will hit intervention/blocked
			},
		},
	}

	sess := &Session{
		ID:        "sess-blocked",
		CWD:       ".",
		SkillName: "blocked-skill",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	eventCh := GlobalBus.Subscribe()
	defer GlobalBus.Unsubscribe(eventCh)

	// Run in background because it will block for intervention
	go engine.Run(skill, sess)

	// Wait for blocked event
	blocked := false
	timeout := time.After(1 * time.Second)

loop:
	for {
		select {
		case e := <-eventCh:
			if e.SessionID == sess.ID && e.Type == EventTaskStatus {
				payload := e.Payload.(TaskStatusPayload)
				if payload.State == TaskStateBlocked {
					blocked = true
					break loop
				}
			}
		case <-timeout:
			break loop
		}
	}

	if !blocked {
		t.Error("Expected TaskStateBlocked event")
	}
}
