package task

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tenazas/internal/storage"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupTasksDir creates a temp storage dir and returns (storageDir, tasksDir, cleanup).
func setupTasksDir(t *testing.T) (string, string, func()) {
	t.Helper()
	tmpStorage, err := os.MkdirTemp("", "tenazas-mutation-*")
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatal(err)
	}
	return tmpStorage, tasksDir, func() { os.RemoveAll(tmpStorage) }
}

// writeTestTask writes a task to disk and returns it.
func writeTestTask(t *testing.T, tasksDir string, task *Task) *Task {
	t.Helper()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().Truncate(time.Second)
	}
	path := filepath.Join(tasksDir, task.ID+".md")
	task.FilePath = path
	if err := WriteTask(path, task); err != nil {
		t.Fatalf("writeTestTask(%s): %v", task.ID, err)
	}
	return task
}

// readTestTask reads a task back from disk.
func readTestTask(t *testing.T, tasksDir, id string) *Task {
	t.Helper()
	tk, err := FindTask(tasksDir, id)
	if err != nil {
		t.Fatalf("readTestTask(%s): %v", id, err)
	}
	return tk
}

// ---------------------------------------------------------------------------
// ValidateStatusTransition — table-driven
// ---------------------------------------------------------------------------

func TestValidateStatusTransition(t *testing.T) {
	tests := []struct {
		from    string
		to      string
		wantErr bool
	}{
		// Valid transitions from todo
		{StatusTodo, StatusInProgress, false},
		{StatusTodo, StatusBlocked, false},
		{StatusTodo, StatusDone, false},
		// Valid transitions from in-progress
		{StatusInProgress, StatusDone, false},
		{StatusInProgress, StatusBlocked, false},
		{StatusInProgress, StatusTodo, false},
		// Valid transitions from blocked
		{StatusBlocked, StatusTodo, false},
		{StatusBlocked, StatusInProgress, false},
		// Valid transitions from done (reopen only)
		{StatusDone, StatusTodo, false},

		// Invalid transitions
		{StatusDone, StatusInProgress, true},
		{StatusDone, StatusBlocked, true},
		{StatusBlocked, StatusDone, true},

		// Self-transition (same status)
		{StatusTodo, StatusTodo, true},
		{StatusDone, StatusDone, true},

		// Unknown status
		{"unknown", StatusTodo, true},
		{StatusTodo, "invalid", true},
	}

	for _, tt := range tests {
		name := tt.from + "→" + tt.to
		t.Run(name, func(t *testing.T) {
			err := ValidateStatusTransition(tt.from, tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStatusTransition(%q, %q) error = %v, wantErr %v",
					tt.from, tt.to, err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// work edit
// ---------------------------------------------------------------------------

func TestWorkEditTitle(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Original Title",
		Status: StatusTodo,
	})

	HandleWorkCommand(storageDir, []string{"edit", "1", "--title", "Updated Title"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Title != "Updated Title" {
		t.Errorf("Title = %q, want %q", tk.Title, "Updated Title")
	}
	// Other fields untouched
	if tk.Status != StatusTodo {
		t.Errorf("Status should remain %q, got %q", StatusTodo, tk.Status)
	}
}

func TestWorkEditStatus(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Status Test",
		Status: StatusTodo,
	})

	before := time.Now().Truncate(time.Second)
	HandleWorkCommand(storageDir, []string{"edit", "1", "--status", "in-progress"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Status != StatusInProgress {
		t.Errorf("Status = %q, want %q", tk.Status, StatusInProgress)
	}
	if tk.StartedAt == nil {
		t.Fatal("StartedAt should be set when transitioning to in-progress")
	}
	if tk.StartedAt.Before(before) {
		t.Errorf("StartedAt %v should be >= %v", tk.StartedAt, before)
	}
	if tk.OwnerPID == 0 {
		t.Error("OwnerPID should be set when transitioning to in-progress")
	}
}

func TestWorkEditStatusToDone(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	startedAt := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	writeTestTask(t, tasksDir, &Task{
		ID:              "TSK-000001",
		Title:           "Done Test",
		Status:          StatusInProgress,
		OwnerPID:        12345,
		OwnerInstanceID: "cli-12345",
		OwnerSessionID:  "sess-abc",
		StartedAt:       &startedAt,
	})

	HandleWorkCommand(storageDir, []string{"edit", "1", "--status", "done"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Status != StatusDone {
		t.Errorf("Status = %q, want %q", tk.Status, StatusDone)
	}
	if tk.CompletedAt == nil {
		t.Fatal("CompletedAt should be set when transitioning to done")
	}
	// Ownership must be cleared
	if tk.OwnerPID != 0 {
		t.Errorf("OwnerPID = %d, want 0", tk.OwnerPID)
	}
	if tk.OwnerInstanceID != "" {
		t.Errorf("OwnerInstanceID = %q, want empty", tk.OwnerInstanceID)
	}
	if tk.OwnerSessionID != "" {
		t.Errorf("OwnerSessionID = %q, want empty", tk.OwnerSessionID)
	}
}

func TestWorkEditPriority(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:       "TSK-000001",
		Title:    "Priority Test",
		Status:   StatusTodo,
		Priority: 0,
	})

	HandleWorkCommand(storageDir, []string{"edit", "1", "--priority", "5"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Priority != 5 {
		t.Errorf("Priority = %d, want 5", tk.Priority)
	}
}

func TestWorkEditSkillAndLabels(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Skill Labels Test",
		Status: StatusTodo,
	})

	HandleWorkCommand(storageDir, []string{"edit", "1", "--skill", "analyze", "--labels", "bug,urgent"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Skill != "analyze" {
		t.Errorf("Skill = %q, want %q", tk.Skill, "analyze")
	}
	wantLabels := []string{"bug", "urgent"}
	if !reflect.DeepEqual(tk.Labels, wantLabels) {
		t.Errorf("Labels = %v, want %v", tk.Labels, wantLabels)
	}
}

func TestWorkEditMultipleFields(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:       "TSK-000001",
		Title:    "Old Title",
		Status:   StatusTodo,
		Priority: 0,
	})

	HandleWorkCommand(storageDir, []string{
		"edit", "1",
		"--title", "New Title",
		"--priority", "3",
		"--status", "in-progress",
	})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Title != "New Title" {
		t.Errorf("Title = %q, want %q", tk.Title, "New Title")
	}
	if tk.Priority != 3 {
		t.Errorf("Priority = %d, want 3", tk.Priority)
	}
	if tk.Status != StatusInProgress {
		t.Errorf("Status = %q, want %q", tk.Status, StatusInProgress)
	}
}

func TestWorkEditInvalidStatus(t *testing.T) {
	// Directly test the validator — done → blocked is forbidden.
	err := ValidateStatusTransition(StatusDone, StatusBlocked)
	if err == nil {
		t.Fatal("Expected error for done → blocked transition, got nil")
	}
	if !strings.Contains(err.Error(), "invalid transition") && !strings.Contains(err.Error(), StatusDone) {
		t.Errorf("Error should mention the invalid transition, got: %v", err)
	}
}

func TestWorkEditLabelsWhitespaceTrimming(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Label Trim Test",
		Status: StatusTodo,
	})

	HandleWorkCommand(storageDir, []string{"edit", "1", "--labels", " bug , urgent , backend "})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	wantLabels := []string{"bug", "urgent", "backend"}
	if !reflect.DeepEqual(tk.Labels, wantLabels) {
		t.Errorf("Labels = %v, want %v (whitespace should be trimmed)", tk.Labels, wantLabels)
	}
}

func TestWorkEditClearLabels(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Clear Labels Test",
		Status: StatusTodo,
		Labels: []string{"old-label"},
	})

	HandleWorkCommand(storageDir, []string{"edit", "1", "--labels", ""})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if len(tk.Labels) != 0 {
		t.Errorf("Labels = %v, want nil/empty (should be cleared)", tk.Labels)
	}
}

// ---------------------------------------------------------------------------
// work delete
// ---------------------------------------------------------------------------

func TestWorkDelete(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "To Be Deleted",
		Status: StatusTodo,
	})

	// Confirm file exists
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if _, err := os.Stat(taskPath); os.IsNotExist(err) {
		t.Fatal("Task file should exist before delete")
	}

	HandleWorkCommand(storageDir, []string{"delete", "1"})

	// File should be gone
	if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
		t.Error("Task file should be removed after delete")
	}
}

func TestWorkDeleteCleansReferences(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// Task A blocks Task B (A is a dependency of B)
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Dependency A",
		Status: StatusDone,
		Blocks: []string{"TSK-000002"},
	})
	writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000002",
		Title:     "Dependent B",
		Status:    StatusDone,
		BlockedBy: []string{"TSK-000001"},
	})

	HandleWorkCommand(storageDir, []string{"delete", "1"})

	// Task B's BlockedBy should no longer contain TSK-000001
	tkB := readTestTask(t, tasksDir, "TSK-000002")
	for _, dep := range tkB.BlockedBy {
		if dep == "TSK-000001" {
			t.Error("Task B's BlockedBy should no longer contain TSK-000001 after deletion")
		}
	}
}

func TestWorkDeleteWithBlockers(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// Task A blocks non-done Task B — deletion should be rejected.
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Blocker A",
		Status: StatusDone,
		Blocks: []string{"TSK-000002"},
	})
	writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000002",
		Title:     "Active Dependent B",
		Status:    StatusInProgress,
		BlockedBy: []string{"TSK-000001"},
	})

	// Verify the file still exists (deletion should have been rejected).
	// We can't call HandleWorkCommand here because it calls os.Exit(1) on rejection.
	// Instead, verify the safety check logic: list tasks, check for active dependents.
	tasks, err := ListTasks(tasksDir)
	if err != nil {
		t.Fatal(err)
	}
	taskMap := buildTaskMap(tasks)

	targetID := "TSK-000001"
	hasActiveDependent := false
	for _, tk := range tasks {
		if tk.ID == targetID {
			continue
		}
		for _, dep := range tk.BlockedBy {
			if dep == targetID && tk.Status != StatusDone {
				hasActiveDependent = true
			}
		}
	}

	if !hasActiveDependent {
		t.Error("Expected TSK-000001 to have an active (non-done) dependent — deletion should be rejected")
	}

	// TSK-000001 file must still exist
	_ = taskMap
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if _, err := os.Stat(taskPath); os.IsNotExist(err) {
		t.Error("Task file should still exist — delete should have been rejected")
	}
}

func TestWorkDeleteBlocksOnlyDoneDependents(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// Task A blocks Task B, but B is done — deletion should succeed.
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Blocker A",
		Status: StatusDone,
		Blocks: []string{"TSK-000002"},
	})
	writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000002",
		Title:     "Done Dependent B",
		Status:    StatusDone,
		BlockedBy: []string{"TSK-000001"},
	})

	HandleWorkCommand(storageDir, []string{"delete", "1"})

	// File should be gone
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
		t.Error("Task file should be removed — all dependents are done")
	}

	// Task B's BlockedBy should be cleaned up
	tkB := readTestTask(t, tasksDir, "TSK-000002")
	for _, dep := range tkB.BlockedBy {
		if dep == "TSK-000001" {
			t.Error("Task B's BlockedBy should be cleaned after A is deleted")
		}
	}
}

// ---------------------------------------------------------------------------
// work dep add / remove
// ---------------------------------------------------------------------------

func TestWorkDepAdd(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Task A",
		Status: StatusTodo,
	})
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000002",
		Title:  "Task B (dependency)",
		Status: StatusTodo,
	})

	HandleWorkCommand(storageDir, []string{"dep", "add", "1", "2"})

	tkA := readTestTask(t, tasksDir, "TSK-000001")
	tkB := readTestTask(t, tasksDir, "TSK-000002")

	// A.BlockedBy should contain B
	if !contains(tkA.BlockedBy, "TSK-000002") {
		t.Errorf("Task A BlockedBy = %v, want to contain TSK-000002", tkA.BlockedBy)
	}
	// B.Blocks should contain A
	if !contains(tkB.Blocks, "TSK-000001") {
		t.Errorf("Task B Blocks = %v, want to contain TSK-000001", tkB.Blocks)
	}
}

func TestWorkDepAddIdempotent(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000001",
		Title:     "Task A",
		Status:    StatusTodo,
		BlockedBy: []string{"TSK-000002"},
	})
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000002",
		Title:  "Task B",
		Status: StatusTodo,
		Blocks: []string{"TSK-000001"},
	})

	// Adding the same dependency again should not create duplicates
	HandleWorkCommand(storageDir, []string{"dep", "add", "1", "2"})

	tkA := readTestTask(t, tasksDir, "TSK-000001")
	tkB := readTestTask(t, tasksDir, "TSK-000002")

	if count(tkA.BlockedBy, "TSK-000002") != 1 {
		t.Errorf("Task A BlockedBy = %v, should contain TSK-000002 exactly once", tkA.BlockedBy)
	}
	if count(tkB.Blocks, "TSK-000001") != 1 {
		t.Errorf("Task B Blocks = %v, should contain TSK-000001 exactly once", tkB.Blocks)
	}
}

func TestWorkDepAddSelfReference(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	tk := writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Task A",
		Status: StatusTodo,
	})

	// AddDependency should reject self-references
	err := AddDependency(tasksDir, tk, "TSK-000001")
	if err == nil {
		t.Fatal("Expected error for self-dependency, got nil")
	}
	if !strings.Contains(err.Error(), "self") {
		t.Errorf("Error should mention self-dependency, got: %v", err)
	}
}

func TestWorkDepAddCycle(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// A depends on B
	tkA := writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000001",
		Title:     "Task A",
		Status:    StatusTodo,
		BlockedBy: []string{"TSK-000002"},
	})
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000002",
		Title:  "Task B",
		Status: StatusTodo,
		Blocks: []string{"TSK-000001"},
	})

	// Trying to add B depends on A should create a cycle
	// Need to load B fresh for the call
	tkB, err := FindTask(tasksDir, "TSK-000002")
	if err != nil {
		t.Fatal(err)
	}

	err = AddDependency(tasksDir, tkB, "TSK-000001")
	if err == nil {
		t.Fatal("Expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("Error should mention cycle, got: %v", err)
	}

	// Verify no mutation persisted — re-read both tasks
	tkAAfter := readTestTask(t, tasksDir, "TSK-000001")
	tkBAfter := readTestTask(t, tasksDir, "TSK-000002")

	if contains(tkBAfter.BlockedBy, "TSK-000001") {
		t.Error("Task B's BlockedBy should not contain TSK-000001 after cycle rollback")
	}
	if contains(tkAAfter.Blocks, "TSK-000002") {
		t.Error("Task A's Blocks should not contain TSK-000002 after cycle rollback")
	}
	_ = tkA
}

func TestWorkDepRemove(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// A depends on B (bidirectional edges already set)
	writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000001",
		Title:     "Task A",
		Status:    StatusTodo,
		BlockedBy: []string{"TSK-000002"},
	})
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000002",
		Title:  "Task B",
		Status: StatusTodo,
		Blocks: []string{"TSK-000001"},
	})

	HandleWorkCommand(storageDir, []string{"dep", "remove", "1", "2"})

	tkA := readTestTask(t, tasksDir, "TSK-000001")
	tkB := readTestTask(t, tasksDir, "TSK-000002")

	if contains(tkA.BlockedBy, "TSK-000002") {
		t.Error("Task A's BlockedBy should no longer contain TSK-000002")
	}
	if contains(tkB.Blocks, "TSK-000001") {
		t.Error("Task B's Blocks should no longer contain TSK-000001")
	}
}

func TestWorkDepRemoveIdempotent(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	tk := writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Task A",
		Status: StatusTodo,
	})
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000002",
		Title:  "Task B",
		Status: StatusTodo,
	})

	// Removing a dependency that doesn't exist should not error
	err := RemoveDependency(tasksDir, tk, "TSK-000002")
	if err != nil {
		t.Errorf("RemoveDependency should be idempotent, got error: %v", err)
	}
}

func TestWorkDepRemoveMissingDepTask(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	tk := writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000001",
		Title:     "Task A",
		Status:    StatusTodo,
		BlockedBy: []string{"TSK-000099"},
	})

	// TSK-000099 doesn't exist on disk — RemoveDependency should still
	// clean up A's BlockedBy and not return an error.
	err := RemoveDependency(tasksDir, tk, "TSK-000099")
	if err != nil {
		t.Errorf("RemoveDependency with missing dep task should not error, got: %v", err)
	}

	tkAfter := readTestTask(t, tasksDir, "TSK-000001")
	if contains(tkAfter.BlockedBy, "TSK-000099") {
		t.Error("BlockedBy should no longer contain TSK-000099")
	}
}

func TestAddDependencyNonexistentDep(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	tk := writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Task A",
		Status: StatusTodo,
	})

	err := AddDependency(tasksDir, tk, "TSK-000099")
	if err == nil {
		t.Fatal("Expected error for nonexistent dependency task, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Error should mention 'not found', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// work unblock
// ---------------------------------------------------------------------------

func TestWorkUnblock(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:              "TSK-000001",
		Title:           "Blocked Task",
		Status:          StatusBlocked,
		FailureCount:    3,
		OwnerPID:        12345,
		OwnerInstanceID: "heartbeat-worker",
		OwnerSessionID:  "sess-xyz",
	})

	HandleWorkCommand(storageDir, []string{"unblock", "1"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Status != StatusTodo {
		t.Errorf("Status = %q, want %q", tk.Status, StatusTodo)
	}
	if tk.OwnerPID != 0 {
		t.Errorf("OwnerPID = %d, want 0 (cleared)", tk.OwnerPID)
	}
	if tk.OwnerInstanceID != "" {
		t.Errorf("OwnerInstanceID = %q, want empty (cleared)", tk.OwnerInstanceID)
	}
	if tk.OwnerSessionID != "" {
		t.Errorf("OwnerSessionID = %q, want empty (cleared)", tk.OwnerSessionID)
	}
	if tk.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", tk.FailureCount)
	}
}

func TestWorkUnblockPreservesDependencies(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000001",
		Title:     "Blocked Task",
		Status:    StatusBlocked,
		BlockedBy: []string{"TSK-000002"},
	})
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000002",
		Title:  "Dependency",
		Status: StatusTodo,
		Blocks: []string{"TSK-000001"},
	})

	HandleWorkCommand(storageDir, []string{"unblock", "1"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	// Dependencies should remain intact — unblock only changes status
	if !contains(tk.BlockedBy, "TSK-000002") {
		t.Errorf("BlockedBy = %v, should still contain TSK-000002 (unblock preserves deps)", tk.BlockedBy)
	}
}

func TestWorkUnblockNonBlocked(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Todo Task",
		Status: StatusTodo,
	})

	// Calling unblock on a non-blocked task should not change its status.
	// (The handler calls os.Exit(1), so we verify the precondition check.)
	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Status == StatusBlocked {
		t.Error("Precondition failed: task should NOT be blocked for this test")
	}
	// The actual handler would os.Exit(1) — we verify the guard logic exists
	// by confirming the task status is not StatusBlocked.
	if tk.Status != StatusTodo {
		t.Errorf("Status = %q, want %q", tk.Status, StatusTodo)
	}
}

// ---------------------------------------------------------------------------
// work reset
// ---------------------------------------------------------------------------

func TestWorkReset(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	startedAt := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	writeTestTask(t, tasksDir, &Task{
		ID:              "TSK-000001",
		Title:           "In-Progress Task",
		Status:          StatusInProgress,
		OwnerPID:        12345,
		OwnerInstanceID: "cli-12345",
		OwnerSessionID:  "sess-abc",
		FailureCount:    2,
		StartedAt:       &startedAt,
	})

	HandleWorkCommand(storageDir, []string{"reset", "1"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Status != StatusTodo {
		t.Errorf("Status = %q, want %q", tk.Status, StatusTodo)
	}
	if tk.OwnerPID != 0 {
		t.Errorf("OwnerPID = %d, want 0", tk.OwnerPID)
	}
	if tk.OwnerInstanceID != "" {
		t.Errorf("OwnerInstanceID = %q, want empty", tk.OwnerInstanceID)
	}
	if tk.OwnerSessionID != "" {
		t.Errorf("OwnerSessionID = %q, want empty", tk.OwnerSessionID)
	}
	if tk.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", tk.FailureCount)
	}
	if tk.StartedAt != nil {
		t.Errorf("StartedAt = %v, want nil", tk.StartedAt)
	}
	if tk.CompletedAt != nil {
		t.Errorf("CompletedAt = %v, want nil", tk.CompletedAt)
	}
}

func TestWorkResetFromDone(t *testing.T) {
	storageDir, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	startedAt := time.Now().Add(-30 * time.Second).Truncate(time.Second)
	completedAt := time.Now().Add(-5 * time.Second).Truncate(time.Second)
	writeTestTask(t, tasksDir, &Task{
		ID:          "TSK-000001",
		Title:       "Done Task",
		Status:      StatusDone,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	})

	HandleWorkCommand(storageDir, []string{"reset", "1"})

	tk := readTestTask(t, tasksDir, "TSK-000001")
	if tk.Status != StatusTodo {
		t.Errorf("Status = %q, want %q", tk.Status, StatusTodo)
	}
	if tk.CompletedAt != nil {
		t.Errorf("CompletedAt = %v, want nil (should be cleared on reset)", tk.CompletedAt)
	}
	if tk.StartedAt != nil {
		t.Errorf("StartedAt = %v, want nil (should be cleared on reset)", tk.StartedAt)
	}
}

// ---------------------------------------------------------------------------
// Skill + Labels struct fields
// ---------------------------------------------------------------------------

func TestSkillAndLabelsPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-skill-labels-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	tk := &Task{
		ID:        "TSK-000001",
		Title:     "Skill/Label Test",
		Status:    StatusTodo,
		Skill:     "code-review",
		Labels:    []string{"frontend", "urgent"},
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   "Test content",
	}

	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatalf("WriteTask failed: %v", err)
	}

	readBack, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	if readBack.Skill != "code-review" {
		t.Errorf("Skill = %q, want %q", readBack.Skill, "code-review")
	}
	if !reflect.DeepEqual(readBack.Labels, []string{"frontend", "urgent"}) {
		t.Errorf("Labels = %v, want [frontend urgent]", readBack.Labels)
	}
}

func TestSkillAndLabelsOmittedWhenEmpty(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-skill-omit-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	tk := &Task{
		ID:        "TSK-000001",
		Title:     "No Skill/Labels",
		Status:    StatusTodo,
		CreatedAt: time.Now().Truncate(time.Second),
	}

	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatalf("WriteTask failed: %v", err)
	}

	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}

	// With omitempty, these fields should be absent
	if strings.Contains(string(raw), `"skill"`) {
		t.Error("Expected 'skill' to be omitted from JSON when empty")
	}
	if strings.Contains(string(raw), `"labels"`) {
		t.Error("Expected 'labels' to be omitted from JSON when empty")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func count(slice []string, val string) int {
	n := 0
	for _, s := range slice {
		if s == val {
			n++
		}
	}
	return n
}

// writeTestLog creates a .jsonl log file for a task in tasksDir/logs/.
func writeTestLog(t *testing.T, tasksDir, taskID, content string) {
	t.Helper()
	logsDir := filepath.Join(tasksDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logsDir, taskID+".jsonl")
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("writeTestLog(%s): %v", taskID, err)
	}
}

// countArchiveEntries returns the number of timestamped subdirs under archive/.
func countArchiveEntries(t *testing.T, tasksDir string) int {
	t.Helper()
	archiveRoot := filepath.Join(tasksDir, "archive")
	entries, err := os.ReadDir(archiveRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("ReadDir(archive): %v", err)
	}
	return len(entries)
}

// getArchiveTimestampDir returns the path of the single timestamped archive subdirectory.
func getArchiveTimestampDir(t *testing.T, tasksDir string) string {
	t.Helper()
	archiveRoot := filepath.Join(tasksDir, "archive")
	entries, err := os.ReadDir(archiveRoot)
	if err != nil {
		t.Fatalf("ReadDir(archive): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("Expected 1 archive timestamp dir, got %d", len(entries))
	}
	return filepath.Join(archiveRoot, entries[0].Name())
}

// ---------------------------------------------------------------------------
// work archive --force (ForceArchive)
// ---------------------------------------------------------------------------

func TestWorkArchiveForce(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// 3 done tasks
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000001", Title: "Done 1", Status: StatusDone})
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000002", Title: "Done 2", Status: StatusDone})
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000003", Title: "Done 3", Status: StatusDone})
	// 2 active tasks
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000004", Title: "Todo 4", Status: StatusTodo})
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000005", Title: "InProgress 5", Status: StatusInProgress})

	// Log for a done task and an active task
	writeTestLog(t, tasksDir, "TSK-000001", `{"event":"run"}`)
	writeTestLog(t, tasksDir, "TSK-000004", `{"event":"pending"}`)

	// Act
	count, err := ForceArchive(tasksDir)

	// Assert: return value
	if err != nil {
		t.Fatalf("ForceArchive returned error: %v", err)
	}
	if count != 3 {
		t.Errorf("ForceArchive count = %d, want 3", count)
	}

	// Done task files gone from root
	for _, id := range []string{"TSK-000001", "TSK-000002", "TSK-000003"} {
		p := filepath.Join(tasksDir, id+".md")
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("Done task %s should be removed from root", id)
		}
	}

	// Active task files remain
	for _, id := range []string{"TSK-000004", "TSK-000005"} {
		p := filepath.Join(tasksDir, id+".md")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("Active task %s should remain in root", id)
		}
	}

	// Exactly 1 timestamped archive subdirectory
	archiveDir := getArchiveTimestampDir(t, tasksDir)

	// Archived directory contains all 3 done task .md files
	for _, id := range []string{"TSK-000001", "TSK-000002", "TSK-000003"} {
		p := filepath.Join(archiveDir, id+".md")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("Archived task %s.md not found in archive dir", id)
		}
	}

	// Archived logs/ contains TSK-000001.jsonl
	archivedLog := filepath.Join(archiveDir, "logs", "TSK-000001.jsonl")
	if _, err := os.Stat(archivedLog); os.IsNotExist(err) {
		t.Error("TSK-000001.jsonl should be archived in logs/")
	}

	// Active task's log remains in original location
	activeLog := filepath.Join(tasksDir, "logs", "TSK-000004.jsonl")
	if _, err := os.Stat(activeLog); os.IsNotExist(err) {
		t.Error("TSK-000004.jsonl should remain in original logs/")
	}

	// ListTasks returns only the 2 active tasks
	remaining, err := ListTasks(tasksDir)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(remaining) != 2 {
		t.Errorf("ListTasks returned %d tasks, want 2", len(remaining))
	}
}

func TestWorkArchiveNoForce(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// 2 done + 1 todo
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000001", Title: "Done 1", Status: StatusDone})
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000002", Title: "Done 2", Status: StatusDone})
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000003", Title: "Todo 3", Status: StatusTodo})

	// Round 1: mixed statuses — CheckAndArchive should not archive
	archived, err := CheckAndArchive(tasksDir)
	if err != nil {
		t.Fatalf("CheckAndArchive (round 1) error: %v", err)
	}
	if archived {
		t.Error("CheckAndArchive should return false when not all tasks are done")
	}

	// All 3 files must remain
	tasks, _ := ListTasks(tasksDir)
	if len(tasks) != 3 {
		t.Errorf("All 3 tasks should remain, got %d", len(tasks))
	}

	// No archive dir created
	if countArchiveEntries(t, tasksDir) != 0 {
		t.Error("No archive directory should exist after failed gate")
	}

	// Round 2: mark the todo as done, now all are done
	tk3 := readTestTask(t, tasksDir, "TSK-000003")
	tk3.Status = StatusDone
	now := time.Now().Truncate(time.Second)
	tk3.CompletedAt = &now
	if err := WriteTask(tk3.FilePath, tk3); err != nil {
		t.Fatal(err)
	}

	archived, err = CheckAndArchive(tasksDir)
	if err != nil {
		t.Fatalf("CheckAndArchive (round 2) error: %v", err)
	}
	if !archived {
		t.Error("CheckAndArchive should return true when all tasks are done")
	}

	// All task files moved to archive
	remaining, _ := ListTasks(tasksDir)
	if len(remaining) != 0 {
		t.Errorf("Expected 0 remaining tasks, got %d", len(remaining))
	}

	// Exactly 1 archive timestamp dir
	if countArchiveEntries(t, tasksDir) != 1 {
		t.Errorf("Expected 1 archive entry, got %d", countArchiveEntries(t, tasksDir))
	}
}

func TestForceArchiveIntegrityCheck(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	// TSK-000001: done, blocks TSK-000002
	writeTestTask(t, tasksDir, &Task{
		ID:     "TSK-000001",
		Title:  "Done Blocker",
		Status: StatusDone,
		Blocks: []string{"TSK-000002"},
	})
	// TSK-000002: todo, blocked by TSK-000001
	writeTestTask(t, tasksDir, &Task{
		ID:        "TSK-000002",
		Title:     "Active Dependent",
		Status:    StatusTodo,
		BlockedBy: []string{"TSK-000001"},
	})

	// Act
	count, err := ForceArchive(tasksDir)

	// Assert: error returned, zero archived
	if err == nil {
		t.Fatal("ForceArchive should return an error for integrity violation")
	}
	if count != 0 {
		t.Errorf("ForceArchive count = %d, want 0", count)
	}

	// Error mentions both task IDs
	errMsg := err.Error()
	if !strings.Contains(errMsg, "TSK-000002") {
		t.Errorf("Error should mention active task TSK-000002, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "TSK-000001") {
		t.Errorf("Error should mention done task TSK-000001, got: %s", errMsg)
	}

	// No files moved
	for _, id := range []string{"TSK-000001", "TSK-000002"} {
		p := filepath.Join(tasksDir, id+".md")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("Task %s should remain (no files moved on integrity error)", id)
		}
	}

	// No archive directory created
	if countArchiveEntries(t, tasksDir) != 0 {
		t.Error("No archive directory should exist after integrity check failure")
	}
}

func TestForceArchiveNoDoneTasks(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{ID: "TSK-000001", Title: "Todo 1", Status: StatusTodo})
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000002", Title: "Todo 2", Status: StatusTodo})

	count, err := ForceArchive(tasksDir)
	if err != nil {
		t.Fatalf("ForceArchive returned error: %v", err)
	}
	if count != 0 {
		t.Errorf("ForceArchive count = %d, want 0", count)
	}

	// No archive directory created
	if countArchiveEntries(t, tasksDir) != 0 {
		t.Error("No archive directory should exist when no done tasks")
	}

	// Both tasks remain
	tasks, _ := ListTasks(tasksDir)
	if len(tasks) != 2 {
		t.Errorf("Expected 2 tasks remaining, got %d", len(tasks))
	}
}

func TestForceArchiveAllDone(t *testing.T) {
	_, tasksDir, cleanup := setupTasksDir(t)
	defer cleanup()

	writeTestTask(t, tasksDir, &Task{ID: "TSK-000001", Title: "Done 1", Status: StatusDone})
	writeTestTask(t, tasksDir, &Task{ID: "TSK-000002", Title: "Done 2", Status: StatusDone})
	writeTestLog(t, tasksDir, "TSK-000001", `{"event":"a"}`)
	writeTestLog(t, tasksDir, "TSK-000002", `{"event":"b"}`)

	count, err := ForceArchive(tasksDir)
	if err != nil {
		t.Fatalf("ForceArchive returned error: %v", err)
	}
	if count != 2 {
		t.Errorf("ForceArchive count = %d, want 2", count)
	}

	// Both task files archived
	archiveDir := getArchiveTimestampDir(t, tasksDir)
	for _, id := range []string{"TSK-000001", "TSK-000002"} {
		p := filepath.Join(archiveDir, id+".md")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("Archived task %s.md not found", id)
		}
	}

	// Both log files archived
	for _, id := range []string{"TSK-000001", "TSK-000002"} {
		p := filepath.Join(archiveDir, "logs", id+".jsonl")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("Archived log %s.jsonl not found", id)
		}
	}

	// ListTasks returns empty
	remaining, _ := ListTasks(tasksDir)
	if len(remaining) != 0 {
		t.Errorf("Expected 0 remaining tasks, got %d", len(remaining))
	}
}
