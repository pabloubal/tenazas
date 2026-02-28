package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tenazas/internal/storage"
)

func TestTaskFailureCountPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-task-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	task := &Task{
		ID:           "TSK-000001",
		Title:        "Test Task",
		Status:       StatusTodo,
		FailureCount: 5,
		CreatedAt:    time.Now(),
		Content:      "Test Content",
	}

	if err := WriteTask(taskPath, task); err != nil {
		t.Fatalf("Failed to write task: %v", err)
	}

	readTask, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("Failed to read task: %v", err)
	}

	if readTask.FailureCount != 5 {
		t.Errorf("Expected FailureCount to be 5, got %d", readTask.FailureCount)
	}
}

func TestPriorityPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-priority-persist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	task := &Task{
		ID:        "TSK-000001",
		Title:     "High Priority Task",
		Status:    StatusTodo,
		Priority:  7,
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   "Priority persistence test",
	}

	if err := WriteTask(taskPath, task); err != nil {
		t.Fatalf("WriteTask failed: %v", err)
	}

	readTask, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	if readTask.Priority != 7 {
		t.Errorf("Expected Priority 7, got %d", readTask.Priority)
	}

	// Verify raw file contains the priority field
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"priority": 7`) {
		t.Errorf("Expected raw file to contain '\"priority\": 7', got:\n%s", string(raw))
	}
}

func TestPriorityBackwardCompat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-priority-compat-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write raw JSON frontmatter WITHOUT "priority" field (simulating old task file)
	oldJSON := `{
  "id": "TSK-000001",
  "title": "Legacy Task",
  "status": "todo",
  "failure_count": 0,
  "created_at": "2025-01-01T00:00:00Z",
  "updated_at": "2025-01-01T00:00:00Z"
}`
	fileContent := "---\n" + oldJSON + "\n---\n\nLegacy content."
	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	if err := os.WriteFile(taskPath, []byte(fileContent), 0644); err != nil {
		t.Fatal(err)
	}

	tk, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask should handle missing priority field, got error: %v", err)
	}

	// Priority should default to 0 via Go zero-value
	if tk.Priority != 0 {
		t.Errorf("Expected Priority 0 for old file without priority field, got %d", tk.Priority)
	}
}

func TestWorkAddWithPriority(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-work-add-priority-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Test with --priority flag
	HandleWorkCommand(tmpStorage, []string{"add", "--priority", "3", "Urgent Fix", "Fix the production bug"})

	tasks, err := ListTasks(tasksDir)
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Priority != 3 {
		t.Errorf("Expected Priority 3, got %d", tasks[0].Priority)
	}
	if tasks[0].Title != "Urgent Fix" {
		t.Errorf("Expected Title 'Urgent Fix', got %q", tasks[0].Title)
	}

	// Test without --priority flag (default 0)
	HandleWorkCommand(tmpStorage, []string{"add", "Normal Task", "Regular work"})

	tasks, err = ListTasks(tasksDir)
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("Expected 2 tasks, got %d", len(tasks))
	}

	// Find the second task (Normal Task)
	var normalTask *Task
	for _, tk := range tasks {
		if tk.Title == "Normal Task" {
			normalTask = tk
			break
		}
	}
	if normalTask == nil {
		t.Fatal("Expected to find 'Normal Task'")
	}
	if normalTask.Priority != 0 {
		t.Errorf("Expected default Priority 0 when --priority omitted, got %d", normalTask.Priority)
	}
}

func TestWorkNextStrictness(t *testing.T) {
	tmpStorage, err := os.MkdirTemp("", "tenazas-storage-strict-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", storage.Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create an in-progress task
	content1 := "---\nid: TSK-000001\ntitle: Task 1\nstatus: in-progress\n---\n\nContent 1"
	os.WriteFile(filepath.Join(tasksDir, "TSK-000001.md"), []byte(content1), 0644)

	// Create a todo task
	content2 := "---\nid: TSK-000002\ntitle: Task 2\nstatus: todo\n---\n\nContent 2"
	os.WriteFile(filepath.Join(tasksDir, "TSK-000002.md"), []byte(content2), 0644)

	// Call HandleWorkCommand("next")
	HandleWorkCommand(tmpStorage, []string{"next"})

	// Verify that the second task is STILL 'todo'
	updatedTodo, _ := ReadTask(filepath.Join(tasksDir, "TSK-000002.md"))
	if updatedTodo.Status != StatusTodo {
		t.Errorf("Expected second task to remain 'todo' because another task is in-progress, got %s", updatedTodo.Status)
	}
}
