package heartbeat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tenazas/internal/client"
	"tenazas/internal/engine"
	"tenazas/internal/models"
	"tenazas/internal/session"
	"tenazas/internal/storage"
	"tenazas/internal/task"
)

func TestHeartbeatMultiSkillSequence(t *testing.T) {
	// Step 3 & 4: Headless Runner & Multi-Skill Support
	// This test verifies that the heartbeat runner can cycle through multiple skills.

	tmpStorage, _ := os.MkdirTemp("", "tenazas-hb-seq-test-*")
	defer os.RemoveAll(tmpStorage)
	sm := session.NewManager(tmpStorage)
	c, _ := client.NewClient("gemini", "echo '{\"type\": \"init\", \"session_id\": \"sid-hb\"}'", filepath.Join(tmpStorage, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	hb := models.Heartbeat{
		Name:     "test-sequence",
		Interval: "1m",
		Path:     tmpStorage,
		Skills:   []string{"skill1", "skill2"},
	}

	// Create skill1
	os.MkdirAll(tmpStorage+"/skills/skill1", 0755)
	skill1 := models.SkillGraph{Name: "skill1", InitialState: "end", States: map[string]models.StateDef{"end": {Type: "end"}}}
	skill1Data, _ := json.Marshal(skill1)
	os.WriteFile(tmpStorage+"/skills/skill1/skill.json", skill1Data, 0644)

	// Create skill2
	os.MkdirAll(tmpStorage+"/skills/skill2", 0755)
	skill2 := models.SkillGraph{Name: "skill2", InitialState: "end", States: map[string]models.StateDef{"end": {Type: "end"}}}
	skill2Data, _ := json.Marshal(skill2)
	os.WriteFile(tmpStorage+"/skills/skill2/skill.json", skill2Data, 0644)

	runner := NewRunner(tmpStorage, sm, eng, nil)
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
	sm := session.NewManager(tmpStorage)
	c2, _ := client.NewClient("gemini", "echo '{\"type\": \"init\", \"session_id\": \"sid-hb\"}'", filepath.Join(tmpStorage, "tenazas.log"))
	clients2 := map[string]client.Client{"gemini": c2}
	eng := engine.NewEngine(sm, clients2, "gemini", 5)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	tk := &task.Task{
		ID:           "TSK-000001",
		Title:        "Failing Task",
		Status:       task.StatusInProgress,
		FailureCount: 3,
		FilePath:     taskPath,
	}
	task.WriteTask(taskPath, tk)

	hb := models.Heartbeat{
		Name:     "test-hb",
		Interval: "1m",
		Path:     tmpStorage,
		Skills:   []string{"skill1"},
	}

	runner := NewRunner(tmpStorage, sm, eng, nil)
	runner.Trigger(hb)

	// Since failure_count is 3, Trigger should see it and mark it as 'blocked'
	// and NOT execute the skill.

	updatedTask, _ := task.ReadTask(taskPath)
	if updatedTask.Status != task.StatusBlocked {
		t.Errorf("Expected task status to be %q after 3 failures, got %s", task.StatusBlocked, updatedTask.Status)
	}
}

func TestHeartbeatResumeInProgress(t *testing.T) {
	// Step 4: Heartbeat Trigger Logic
	// This test verifies that if a task is 'in-progress' and failure_count < 3,
	// the heartbeat resumes the relevant skill instead of starting over.
}

func TestHeartbeatOwnerFieldsOnPickup(t *testing.T) {
	tmpStorage, _ := os.MkdirTemp("", "tenazas-hb-owner-pickup-*")
	defer os.RemoveAll(tmpStorage)
	sm := session.NewManager(tmpStorage)
	c, _ := client.NewClient("gemini", "echo '{\"type\": \"init\", \"session_id\": \"sid-hb\"}'", filepath.Join(tmpStorage, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	tk := &task.Task{
		ID:           "TSK-000001",
		Title:        "Owner Pickup Test",
		Status:       task.StatusInProgress,
		FailureCount: 0,
		FilePath:     taskPath,
	}
	task.WriteTask(taskPath, tk)

	// Create a minimal skill
	os.MkdirAll(tmpStorage+"/skills/testskill", 0755)
	skillData := `{"name":"testskill","initial_state":"end","states":{"end":{"type":"end"}}}`
	os.WriteFile(tmpStorage+"/skills/testskill/skill.json", []byte(skillData), 0644)

	hb := models.Heartbeat{
		Name:     "test-owner",
		Interval: "1m",
		Path:     tmpStorage,
		Skills:   []string{"testskill"},
	}

	runner := NewRunner(tmpStorage, sm, eng, nil)
	runner.Trigger(hb)

	updatedTask, _ := task.ReadTask(taskPath)
	if updatedTask.OwnerPID == 0 {
		t.Error("Expected OwnerPID to be set after heartbeat pickup")
	}
	if updatedTask.OwnerInstanceID != "heartbeat-test-owner" {
		t.Errorf("Expected OwnerInstanceID = %q, got %q", "heartbeat-test-owner", updatedTask.OwnerInstanceID)
	}
}

func TestHeartbeatOwnerFieldsClearedOnBlock(t *testing.T) {
	tmpStorage, _ := os.MkdirTemp("", "tenazas-hb-owner-block-*")
	defer os.RemoveAll(tmpStorage)
	sm := session.NewManager(tmpStorage)
	c, _ := client.NewClient("gemini", "echo '{\"type\": \"init\", \"session_id\": \"sid-hb\"}'", filepath.Join(tmpStorage, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	tk := &task.Task{
		ID:              "TSK-000001",
		Title:           "Block Owner Test",
		Status:          task.StatusInProgress,
		FailureCount:    3,
		OwnerPID:        99999,
		OwnerInstanceID: "heartbeat-old",
		OwnerSessionID:  "sess-old",
		FilePath:        taskPath,
	}
	task.WriteTask(taskPath, tk)

	hb := models.Heartbeat{
		Name:     "test-block",
		Interval: "1m",
		Path:     tmpStorage,
		Skills:   []string{"testskill"},
	}

	runner := NewRunner(tmpStorage, sm, eng, nil)
	runner.Trigger(hb)

	updatedTask, _ := task.ReadTask(taskPath)
	if updatedTask.Status != task.StatusBlocked {
		t.Errorf("Expected status %q, got %q", task.StatusBlocked, updatedTask.Status)
	}
	if updatedTask.OwnerPID != 0 {
		t.Errorf("Expected OwnerPID = 0 after block, got %d", updatedTask.OwnerPID)
	}
	if updatedTask.OwnerInstanceID != "" {
		t.Errorf("Expected OwnerInstanceID = \"\" after block, got %q", updatedTask.OwnerInstanceID)
	}
	if updatedTask.OwnerSessionID != "" {
		t.Errorf("Expected OwnerSessionID = \"\" after block, got %q", updatedTask.OwnerSessionID)
	}
}

func TestHeartbeatStartedAtIdempotent(t *testing.T) {
	tmpStorage, _ := os.MkdirTemp("", "tenazas-hb-startedat-*")
	defer os.RemoveAll(tmpStorage)
	sm := session.NewManager(tmpStorage)
	c, _ := client.NewClient("gemini", "echo '{\"type\": \"init\", \"session_id\": \"sid-hb\"}'", filepath.Join(tmpStorage, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Set StartedAt to a known past time
	originalStarted := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	tk := &task.Task{
		ID:           "TSK-000001",
		Title:        "Idempotent StartedAt",
		Status:       task.StatusInProgress,
		FailureCount: 0,
		StartedAt:    &originalStarted,
		FilePath:     taskPath,
	}
	task.WriteTask(taskPath, tk)

	// Create a minimal skill
	os.MkdirAll(tmpStorage+"/skills/testskill", 0755)
	skillData := `{"name":"testskill","initial_state":"end","states":{"end":{"type":"end"}}}`
	os.WriteFile(tmpStorage+"/skills/testskill/skill.json", []byte(skillData), 0644)

	hb := models.Heartbeat{
		Name:     "test-idempotent",
		Interval: "1m",
		Path:     tmpStorage,
		Skills:   []string{"testskill"},
	}

	runner := NewRunner(tmpStorage, sm, eng, nil)
	runner.Trigger(hb)

	updatedTask, _ := task.ReadTask(taskPath)
	if updatedTask.StartedAt == nil {
		t.Fatal("Expected StartedAt to still be set")
	}
	if !updatedTask.StartedAt.Equal(originalStarted) {
		t.Errorf("Expected StartedAt to remain %v, got %v (should not be overwritten)", originalStarted, *updatedTask.StartedAt)
	}
}
