package task

import (
	"os"
	"path/filepath"
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
