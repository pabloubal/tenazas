package engine

import (
	"os"
	"testing"

	"tenazas/internal/executor"
	"tenazas/internal/models"
	"tenazas/internal/session"
)

func TestToolFailureRoute(t *testing.T) {
	// Step 1: Engine Robustness (Tool Failures)
	// This test verifies that if a 'tool' state fails (ExitCode != 0),
	// the engine transitions to OnFailRoute if defined.

	skill := &models.SkillGraph{
		Name:         "test_tool_fail",
		InitialState: "run_fail",
		States: map[string]models.StateDef{
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
	sm := session.NewManager(tmpStorage)
	exec := executor.NewExecutor("false", tmpStorage)
	e := NewEngine(sm, exec, 5)

	sess := &models.Session{
		ID:         "test-sess-tool-fail",
		CWD:        tmpStorage,
		ActiveNode: "run_fail",
		Status:     models.StatusRunning,
	}

	e.Run(skill, sess)

	if sess.ActiveNode != "handle_fail" {
		t.Errorf("Expected ActiveNode to be 'handle_fail' after tool failure, got %s", sess.ActiveNode)
	}

	if sess.Status != models.StatusRunning && sess.Status != models.StatusCompleted {
		t.Errorf("Expected session to remain running or be completed, got %s", sess.Status)
	}
}

func TestToolFailureNoRoute(t *testing.T) {
	// This test verifies that if a 'tool' state fails and NO OnFailRoute is defined,
	// the engine terminates the skill with StatusFailed.

	skill := &models.SkillGraph{
		Name:         "test_tool_fail_no_route",
		InitialState: "run_fail",
		States: map[string]models.StateDef{
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
	sm := session.NewManager(tmpStorage)
	exec := executor.NewExecutor("false", tmpStorage)
	e := NewEngine(sm, exec, 5)

	sess := &models.Session{
		ID:         "test-sess-tool-fail-no-route",
		CWD:        tmpStorage,
		ActiveNode: "run_fail",
		Status:     models.StatusRunning,
	}

	e.Run(skill, sess)

	if sess.Status != models.StatusFailed {
		t.Errorf("Expected Status to be StatusFailed when no OnFailRoute is defined, got %s", sess.Status)
	}
}
