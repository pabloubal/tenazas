package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskFailureCountPersistence(t *testing.T) {
	// Step 2: Task V2 Hardening
	// This test verifies that FailureCount is correctly saved and loaded.

	tmpDir, err := os.MkdirTemp("", "tenazas-task-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	task := &Task{
		ID:           "TSK-000001",
		Title:        "Test Task",
		Status:       "todo",
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
	// Step 2: Task V2 Hardening
	// This test verifies that 'tenazas work next' refuses to start a new task
	// if one is already in-progress.

	tmpStorage, err := os.MkdirTemp("", "tenazas-storage-strict-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpStorage)

	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(tmpStorage, "tasks", Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)

	// Create an in-progress task
	content1 := "---\nid: TSK-000001\ntitle: Task 1\nstatus: in-progress\n---\n\nContent 1"
	os.WriteFile(filepath.Join(tasksDir, "TSK-000001.md"), []byte(content1), 0644)

	// Create a todo task
	content2 := "---\nid: TSK-000002\ntitle: Task 2\nstatus: todo\n---\n\nContent 2"
	os.WriteFile(filepath.Join(tasksDir, "TSK-000002.md"), []byte(content2), 0644)

	// Call HandleWorkCommand("next")
	// Since we are in the same package, we can call it directly.
	// We need to set up the environment or pass storageDir.
	HandleWorkCommand(tmpStorage, []string{"next"})

	// Verify that the second task is STILL 'todo'
	// Currently, it will fail because HandleWorkCommand will make it 'in-progress'
	updatedTodo, _ := ReadTask(filepath.Join(tasksDir, "TSK-000002.md"))
	if updatedTodo.Status != "todo" {
		t.Errorf("Expected second task to remain 'todo' because another task is in-progress, got %s", updatedTodo.Status)
	}
}
