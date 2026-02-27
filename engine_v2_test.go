package main

import (
	"os"
	"testing"
)

func TestToolFailureRoute(t *testing.T) {
	// Step 1: Engine Robustness (Tool Failures)
	// This test verifies that if a 'tool' state fails (ExitCode != 0),
	// the engine transitions to OnFailRoute if defined.

	skill := &SkillGraph{
		Name:         "test_tool_fail",
		InitialState: "run_fail",
		States: map[string]StateDef{
			"run_fail": {
				Type:        "tool",
				Command:     "false", // Exit code 1
				Next:        "should_not_reach",
				OnFailRoute: "handle_fail",
			},
			"handle_fail": {
				Type:       "end",
				IsTerminal: true,
			},
			"should_not_reach": {
				Type:       "end",
				IsTerminal: true,
			},
		},
	}

	tmpStorage, _ := os.MkdirTemp("", "tenazas-engine-test-*")
	defer os.RemoveAll(tmpStorage)
	sm := NewSessionManager(tmpStorage)
	exec := NewExecutor("false", tmpStorage)
	e := NewEngine(sm, exec, 5)

	sess := &Session{
		ID:         "test-sess-tool-fail",
		CWD:        tmpStorage,
		ActiveNode: "run_fail",
		Status:     StatusRunning,
	}

	e.Run(skill, sess)

	if sess.ActiveNode != "handle_fail" {
		t.Errorf("Expected ActiveNode to be 'handle_fail' after tool failure, got %s", sess.ActiveNode)
	}

	if sess.Status != StatusRunning && sess.Status != StatusCompleted {
		t.Errorf("Expected session to remain running or be completed, got %s", sess.Status)
	}
}

func TestToolFailureNoRoute(t *testing.T) {
	// This test verifies that if a 'tool' state fails and NO OnFailRoute is defined,
	// the engine terminates the skill with StatusFailed.

	skill := &SkillGraph{
		Name:         "test_tool_fail_no_route",
		InitialState: "run_fail",
		States: map[string]StateDef{
			"run_fail": {
				Type:    "tool",
				Command: "false",
				Next:    "should_not_reach",
			},
			"should_not_reach": {
				Type:       "end",
				IsTerminal: true,
			},
		},
	}

	tmpStorage, _ := os.MkdirTemp("", "tenazas-engine-test-no-route-*")
	defer os.RemoveAll(tmpStorage)
	sm := NewSessionManager(tmpStorage)
	exec := NewExecutor("false", tmpStorage)
	e := NewEngine(sm, exec, 5)

	sess := &Session{
		ID:         "test-sess-tool-fail-no-route",
		CWD:        tmpStorage,
		ActiveNode: "run_fail",
		Status:     StatusRunning,
	}

	e.Run(skill, sess)

	if sess.Status != StatusFailed {
		t.Errorf("Expected Status to be StatusFailed when no OnFailRoute is defined, got %s", sess.Status)
	}
}
