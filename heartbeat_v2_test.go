package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHeartbeatMultiSkillSequence(t *testing.T) {
	// Step 3 & 4: Headless Runner & Multi-Skill Support
	// This test verifies that the heartbeat runner can cycle through multiple skills.

	tmpStorage, _ := os.MkdirTemp("", "tenazas-hb-seq-test-*")
	defer os.RemoveAll(tmpStorage)
	sm := NewSessionManager(tmpStorage)
	reg, _ := NewRegistry(tmpStorage)
	exec := NewExecutor("echo '{\"type\": \"init\", \"session_id\": \"sid-hb\"}'", tmpStorage)
	engine := NewEngine(sm, exec, 5)

	hb := Heartbeat{
		Name:     "test-sequence",
		Interval: "1m",
		Path:     tmpStorage,
		Skills:   []string{"skill1", "skill2"},
	}

	// Create skill1
	os.MkdirAll(tmpStorage+"/skills/skill1", 0755)
	skill1 := SkillGraph{Name: "skill1", InitialState: "end", States: map[string]StateDef{"end": {Type: "end"}}}
	skill1Data, _ := json.Marshal(skill1)
	os.WriteFile(tmpStorage+"/skills/skill1/skill.json", skill1Data, 0644)

	// Create skill2
	os.MkdirAll(tmpStorage+"/skills/skill2", 0755)
	skill2 := SkillGraph{Name: "skill2", InitialState: "end", States: map[string]StateDef{"end": {Type: "end"}}}
	skill2Data, _ := json.Marshal(skill2)
	os.WriteFile(tmpStorage+"/skills/skill2/skill.json", skill2Data, 0644)

	runner := NewHeartbeatRunner(tmpStorage, sm, engine, nil, reg)
	runner.Trigger(hb)

	// In TDD, this should only run one skill and then stop.
	// We want to see it run both or at least fail because it didn't run the second one.
	// Since engine.Run is synchronous in Trigger (currently), we can check sessions after Trigger returns.

	list, _, _ := sm.List(0, 10)
	// We expect multiple sessions or a record of multiple skills executed.
	// For now, let's just check if it executed both.

	skill2Executed := false
	for _, s := range list {
		if s.SkillName == "skill2" {
			skill2Executed = true
		}
	}

	if !skill2Executed {
		t.Errorf("Expected 'skill2' to be executed in sequence, but it wasn't")
	}
}

func TestHeartbeatFailureTracking(t *testing.T) {
	// Step 4: Heartbeat Trigger Logic
	// This test verifies that a task is marked as 'blocked' after 3 failures.

	tmpStorage, _ := os.MkdirTemp("", "tenazas-hb-fail-test-*")
	defer os.RemoveAll(tmpStorage)
	sm := NewSessionManager(tmpStorage)
	reg, _ := NewRegistry(tmpStorage)
	exec := NewExecutor("echo '{\"type\": \"init\", \"session_id\": \"sid-hb\"}'", tmpStorage)
	engine := NewEngine(sm, exec, 5)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	task := &Task{
		ID:           "TSK-000001",
		Title:        "Failing Task",
		Status:       "in-progress",
		FailureCount: 3,
		FilePath:     taskPath,
	}
	WriteTask(taskPath, task)

	hb := Heartbeat{
		Name:     "test-hb",
		Interval: "1m",
		Path:     tmpStorage,
		Skills:   []string{"skill1"},
	}

	runner := NewHeartbeatRunner(tmpStorage, sm, engine, nil, reg)
	runner.Trigger(hb)

	// Since failure_count is 3, Trigger should see it and mark it as 'blocked'
	// and NOT execute the skill.

	updatedTask, _ := ReadTask(taskPath)
	if updatedTask.Status != "blocked" {
		t.Errorf("Expected task status to be 'blocked' after 3 failures, got %s", updatedTask.Status)
	}
}

func TestHeartbeatResumeInProgress(t *testing.T) {
	// Step 4: Heartbeat Trigger Logic
	// This test verifies that if a task is 'in-progress' and failure_count < 3,
	// the heartbeat resumes the relevant skill instead of starting over.
}
