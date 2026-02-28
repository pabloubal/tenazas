package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tenazas/internal/storage"
)

// --- Test: Status Constants ---

func TestStatusConstants(t *testing.T) {
	// Guards against accidental value changes that would break backward compat.
	if StatusTodo != "todo" {
		t.Errorf("StatusTodo = %q, want %q", StatusTodo, "todo")
	}
	if StatusInProgress != "in-progress" {
		t.Errorf("StatusInProgress = %q, want %q", StatusInProgress, "in-progress")
	}
	if StatusDone != "done" {
		t.Errorf("StatusDone = %q, want %q", StatusDone, "done")
	}
	if StatusBlocked != "blocked" {
		t.Errorf("StatusBlocked = %q, want %q", StatusBlocked, "blocked")
	}
}

// --- Test: Atomic Write ---

func TestAtomicWrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-atomic-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	tk := &Task{
		ID:        "TSK-000001",
		Title:     "Atomic Write Test",
		Status:    "todo",
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   "Test content for atomic write.",
	}

	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatalf("WriteTask failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(taskPath); os.IsNotExist(err) {
		t.Fatal("Expected task file to exist after WriteTask")
	}

	// Verify no .tmp sibling remains
	tmpFile := taskPath + ".tmp"
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Errorf("Expected no .tmp file after successful write, but %s exists", tmpFile)
	}

	// Read back and verify fields
	readBack, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}
	if readBack.ID != tk.ID {
		t.Errorf("ID mismatch: got %s, want %s", readBack.ID, tk.ID)
	}
	if readBack.Title != tk.Title {
		t.Errorf("Title mismatch: got %s, want %s", readBack.Title, tk.Title)
	}
	if readBack.Status != tk.Status {
		t.Errorf("Status mismatch: got %s, want %s", readBack.Status, tk.Status)
	}
	if strings.TrimSpace(readBack.Content) != strings.TrimSpace(tk.Content) {
		t.Errorf("Content mismatch: got %q, want %q", readBack.Content, tk.Content)
	}
}

func TestAtomicWriteCreatesParentDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-atomic-mkdir-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write to a nested path that doesn't exist yet
	nestedPath := filepath.Join(tmpDir, "subdir", "deep", "TSK-000001.md")
	tk := &Task{
		ID:      "TSK-000001",
		Title:   "Nested Dir Test",
		Status:  "todo",
		Content: "Content",
	}

	if err := WriteTask(nestedPath, tk); err != nil {
		t.Fatalf("WriteTask should create parent dirs, got error: %v", err)
	}

	if _, err := os.Stat(nestedPath); os.IsNotExist(err) {
		t.Error("Expected nested task file to exist")
	}
}

// --- Test: Timestamps ---

func TestTimestampOnNext(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-ts-next-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create a todo task
	tk := &Task{
		ID:        "TSK-000001",
		Title:     "Timestamp Test",
		Status:    StatusTodo,
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   "Test content",
	}
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Truncate(time.Second)

	// Trigger work next
	HandleWorkCommand(tmpStorage, []string{"next"})

	// Read back the task
	updated, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	// StartedAt must be set
	if updated.StartedAt == nil {
		t.Fatal("Expected StartedAt to be set after work next, got nil")
	}

	// Must be truncated to second precision
	if !updated.StartedAt.Equal(updated.StartedAt.Truncate(time.Second)) {
		t.Error("StartedAt should be truncated to second precision")
	}

	// Must be >= before
	if updated.StartedAt.Before(before) {
		t.Errorf("StartedAt %v should not be before %v", updated.StartedAt, before)
	}
}

func TestTimestampOnComplete(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-ts-complete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create an in-progress task with StartedAt
	startedAt := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	tk := &Task{
		ID:        "TSK-000001",
		Title:     "Complete Timestamp Test",
		Status:    StatusInProgress,
		StartedAt: &startedAt,
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   "Test content",
	}
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatal(err)
	}

	// Trigger work complete
	HandleWorkCommand(tmpStorage, []string{"complete"})

	// Read back the task
	updated, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	// CompletedAt must be set
	if updated.CompletedAt == nil {
		t.Fatal("Expected CompletedAt to be set after work complete, got nil")
	}

	// Must be truncated to second precision
	if !updated.CompletedAt.Equal(updated.CompletedAt.Truncate(time.Second)) {
		t.Error("CompletedAt should be truncated to second precision")
	}

	// CompletedAt >= StartedAt
	if updated.StartedAt != nil && updated.CompletedAt.Before(*updated.StartedAt) {
		t.Errorf("CompletedAt %v should not be before StartedAt %v", updated.CompletedAt, updated.StartedAt)
	}
}

func TestTimestampDuration(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-ts-dur-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create an in-progress task with StartedAt 10 seconds ago
	startedAt := time.Now().Add(-10 * time.Second).Truncate(time.Second)
	tk := &Task{
		ID:        "TSK-000001",
		Title:     "Duration Test",
		Status:    StatusInProgress,
		StartedAt: &startedAt,
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   "Test content",
	}
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatal(err)
	}

	HandleWorkCommand(tmpStorage, []string{"complete"})

	updated, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	if updated.CompletedAt == nil || updated.StartedAt == nil {
		t.Fatal("Both StartedAt and CompletedAt must be set")
	}

	duration := updated.CompletedAt.Sub(*updated.StartedAt)
	if duration < 0 {
		t.Errorf("Expected non-negative duration, got %v", duration)
	}
}

// --- Test: Owner Fields ---

func TestOwnerFieldsOnNext(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-owner-next-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	tk := &Task{
		ID:        "TSK-000001",
		Title:     "Owner Test",
		Status:    StatusTodo,
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   "Test content",
	}
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatal(err)
	}

	HandleWorkCommand(tmpStorage, []string{"next"})

	updated, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	if updated.OwnerPID != os.Getpid() {
		t.Errorf("Expected OwnerPID = %d, got %d", os.Getpid(), updated.OwnerPID)
	}
	if updated.StartedAt == nil {
		t.Error("Expected StartedAt to be set on work next")
	}
}

func TestOwnerFieldsClearedOnComplete(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-owner-complete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create an in-progress task with owner fields populated
	startedAt := time.Now().Add(-5 * time.Second).Truncate(time.Second)
	tk := &Task{
		ID:              "TSK-000001",
		Title:           "Owner Clear Test",
		Status:          StatusInProgress,
		OwnerPID:        12345,
		OwnerInstanceID: "cli-12345",
		OwnerSessionID:  "sess-abc",
		StartedAt:       &startedAt,
		CreatedAt:       time.Now().Truncate(time.Second),
		Content:         "Test content",
	}
	taskPath := filepath.Join(tasksDir, "TSK-000001.md")
	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatal(err)
	}

	HandleWorkCommand(tmpStorage, []string{"complete"})

	updated, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	if updated.OwnerPID != 0 {
		t.Errorf("Expected OwnerPID = 0 after complete, got %d", updated.OwnerPID)
	}
	if updated.OwnerInstanceID != "" {
		t.Errorf("Expected OwnerInstanceID = \"\" after complete, got %q", updated.OwnerInstanceID)
	}
	if updated.OwnerSessionID != "" {
		t.Errorf("Expected OwnerSessionID = \"\" after complete, got %q", updated.OwnerSessionID)
	}
}

// --- Test: Backward Compatibility ---

func TestBackwardCompatOldOwnerSessionRole(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-compat-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write a task file with manual JSON frontmatter that includes owner_session_role
	oldJSON := `{
  "id": "TSK-000001",
  "title": "Legacy Task",
  "status": "todo",
  "failure_count": 0,
  "created_at": "2025-01-01T00:00:00Z",
  "updated_at": "2025-01-01T00:00:00Z",
  "owner_session_role": "worker"
}`
	fileContent := "---\n" + oldJSON + "\n---\n\nLegacy content here."
	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	if err := os.WriteFile(taskPath, []byte(fileContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Read it — should not error
	tk, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask should handle old owner_session_role field, got error: %v", err)
	}
	if tk.ID != "TSK-000001" {
		t.Errorf("Expected ID TSK-000001, got %s", tk.ID)
	}

	// Write it back
	if err := WriteTask(taskPath, tk); err != nil {
		t.Fatalf("WriteTask failed: %v", err)
	}

	// Read raw bytes and verify owner_session_role is absent
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "owner_session_role") {
		t.Error("Expected owner_session_role to be absent after WriteTask re-serialization")
	}
}

// --- Test: OwnerSessionRole Removed from Struct ---

func TestOwnerSessionRoleRemovedFromStruct(t *testing.T) {
	// Verify the OwnerSessionRole field no longer exists on the Task struct.
	// We do this by marshaling a Task and checking the JSON output.
	tk := &Task{
		ID:     "TSK-000001",
		Title:  "Test",
		Status: "todo",
	}
	data, err := json.Marshal(tk)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "owner_session_role") {
		t.Error("Task struct should not have OwnerSessionRole field; JSON output contains 'owner_session_role'")
	}
}

// --- Test: work init Enhancement ---

func TestWorkInitMigration(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-init-migrate-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create an old-format task file
	v1Content := "---\nid: 1234\ntitle: Old Task\nstatus: todo\n---\n\n# Old Task"
	v1Path := filepath.Join(tasksDir, "task-1234-old-task.md")
	if err := os.WriteFile(v1Path, []byte(v1Content), 0644); err != nil {
		t.Fatal(err)
	}

	// Run work init which should trigger migration
	HandleWorkCommand(tmpStorage, []string{"init"})

	// Verify old file is migrated to TSK-001234.md
	v2Path := filepath.Join(tasksDir, "TSK-001234.md")
	if _, err := os.Stat(v2Path); os.IsNotExist(err) {
		t.Errorf("Expected migrated file %s to exist after work init", v2Path)
	}

	// Verify old file is removed
	if _, err := os.Stat(v1Path); !os.IsNotExist(err) {
		t.Error("Expected old-format file to be removed after migration")
	}
}

func TestWorkInitSummary(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-init-summary-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create 2 todo tasks and 1 done task
	for i, status := range []string{StatusTodo, StatusTodo, StatusDone} {
		tk := &Task{
			ID:        fmt.Sprintf("TSK-%06d", i+1),
			Title:     fmt.Sprintf("Task %d", i+1),
			Status:    status,
			CreatedAt: time.Now().Truncate(time.Second),
			Content:   "Content",
		}
		path := filepath.Join(tasksDir, tk.ID+".md")
		WriteTask(path, tk)
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	HandleWorkCommand(tmpStorage, []string{"init"})

	w.Close()
	os.Stdout = oldStdout

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	// Verify it contains status counts
	if !strings.Contains(output, "Todo: 2") {
		t.Errorf("Expected output to contain 'Todo: 2', got: %s", output)
	}
	if !strings.Contains(output, "Done: 1") {
		t.Errorf("Expected output to contain 'Done: 1', got: %s", output)
	}
}

// --- Test: Error Handling ---

func TestListTasksErrorPropagation(t *testing.T) {
	// ListTasks on a non-existent directory should return an error
	_, err := ListTasks("/nonexistent/path/that/should/not/exist")
	if err == nil {
		t.Error("Expected ListTasks to return an error for a non-existent directory")
	}
}

func TestIsReadyUsesConstants(t *testing.T) {
	// Verify IsReady uses the correct constant values for status checks
	taskMap := map[string]*Task{
		"TSK-000001": {ID: "TSK-000001", Status: StatusDone},
	}

	// A todo task with all deps done should be ready
	ready := &Task{ID: "TSK-000002", Status: StatusTodo, BlockedBy: []string{"TSK-000001"}}
	if !ready.IsReady(taskMap) {
		t.Error("Expected task with StatusTodo and all deps done to be ready")
	}

	// A non-todo task should not be ready
	notReady := &Task{ID: "TSK-000003", Status: StatusInProgress, BlockedBy: []string{"TSK-000001"}}
	if notReady.IsReady(taskMap) {
		t.Error("Expected task with StatusInProgress to not be ready")
	}

	// A todo task with undone deps should not be ready
	blocked := &Task{ID: "TSK-000004", Status: StatusTodo, BlockedBy: []string{"TSK-000005"}}
	taskMap["TSK-000005"] = &Task{ID: "TSK-000005", Status: StatusInProgress}
	if blocked.IsReady(taskMap) {
		t.Error("Expected task with in-progress dependency to not be ready")
	}
}

func TestCheckAndArchiveUsesConstants(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-archive-const-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tasksDir := filepath.Join(tmpDir, "tasks")
	os.MkdirAll(tasksDir, 0755)

	// Create tasks using constants
	WriteTask(filepath.Join(tasksDir, "TSK-000001.md"), &Task{ID: "TSK-000001", Status: StatusDone})
	WriteTask(filepath.Join(tasksDir, "TSK-000002.md"), &Task{ID: "TSK-000002", Status: StatusDone})

	archived, err := CheckAndArchive(tasksDir)
	if err != nil {
		t.Fatalf("CheckAndArchive failed: %v", err)
	}
	if !archived {
		t.Error("Expected archiving with StatusDone tasks")
	}

	// If one task is not done, should not archive
	tasksDir2 := filepath.Join(tmpDir, "tasks2")
	os.MkdirAll(tasksDir2, 0755)
	WriteTask(filepath.Join(tasksDir2, "TSK-000001.md"), &Task{ID: "TSK-000001", Status: StatusDone})
	WriteTask(filepath.Join(tasksDir2, "TSK-000002.md"), &Task{ID: "TSK-000002", Status: StatusTodo})

	archived2, err := CheckAndArchive(tasksDir2)
	if err != nil {
		t.Fatalf("CheckAndArchive failed: %v", err)
	}
	if archived2 {
		t.Error("Should not archive when a task is StatusTodo")
	}
}
