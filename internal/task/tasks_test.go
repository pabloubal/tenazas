package task

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadWriteTask(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-tasks-*")
	defer os.RemoveAll(tmpDir)

	task := &Task{
		ID:        "TSK-000001",
		Title:     "Test Task",
		Status:    "todo",
		CreatedAt: time.Now().Truncate(time.Second),
		Blocks:    []string{"TSK-000002"},
		BlockedBy: []string{"TSK-000000"},
		Content: `# Task Description

This is a test task.`,
	}

	taskPath := filepath.Join(tmpDir, "TSK-000001.md")
	err := WriteTask(taskPath, task)
	if err != nil {
		t.Fatalf("WriteTask failed: %v", err)
	}

	readTask, err := ReadTask(taskPath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}

	if readTask.ID != task.ID {
		t.Errorf("Expected ID %s, got %s", task.ID, readTask.ID)
	}
	if readTask.Title != task.Title {
		t.Errorf("Expected Title %s, got %s", task.Title, readTask.Title)
	}
	if readTask.Status != task.Status {
		t.Errorf("Expected Status %s, got %s", task.Status, readTask.Status)
	}
	if !readTask.CreatedAt.Equal(task.CreatedAt) {
		t.Errorf("Expected CreatedAt %v, got %v", task.CreatedAt, readTask.CreatedAt)
	}
	if !reflect.DeepEqual(readTask.Blocks, task.Blocks) {
		t.Errorf("Expected Blocks %v, got %v", task.Blocks, readTask.Blocks)
	}
	if !reflect.DeepEqual(readTask.BlockedBy, task.BlockedBy) {
		t.Errorf("Expected BlockedBy %v, got %v", task.BlockedBy, readTask.BlockedBy)
	}
	if strings.TrimSpace(readTask.Content) != strings.TrimSpace(task.Content) {
		t.Errorf("Expected Content %s, got %s", task.Content, readTask.Content)
	}
}

func TestGetNextTaskID(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-seq-*")
	defer os.RemoveAll(tmpDir)

	id1, err := GetNextTaskID(tmpDir)
	if err != nil {
		t.Fatalf("GetNextTaskID failed: %v", err)
	}
	if id1 != "TSK-000001" {
		t.Errorf("Expected TSK-000001, got %s", id1)
	}

	id2, err := GetNextTaskID(tmpDir)
	if err != nil {
		t.Fatalf("GetNextTaskID failed: %v", err)
	}
	if id2 != "TSK-000002" {
		t.Errorf("Expected TSK-000002, got %s", id2)
	}
}

func TestSelectNextTask(t *testing.T) {
	tasks := []*Task{
		{ID: "TSK-000001", Status: "done"},
		{ID: "TSK-000002", Status: "todo", BlockedBy: []string{"TSK-000001"}},
		{ID: "TSK-000003", Status: "todo", BlockedBy: []string{"TSK-000002"}},
		{ID: "TSK-000004", Status: "blocked", BlockedBy: []string{"TSK-999999"}}, // Unresolvable
	}

	next := SelectNextTask(tasks)
	if next == nil || next.ID != "TSK-000002" {
		t.Errorf("Expected TSK-000002 to be selected, got %v", next)
	}

	// Now mark TSK-000002 as done
	tasks[1].Status = "done"
	next = SelectNextTask(tasks)
	if next == nil || next.ID != "TSK-000003" {
		t.Errorf("Expected TSK-000003 to be selected, got %v", next)
	}
}

func TestCycleDetection(t *testing.T) {
	tasks := []*Task{
		{ID: "TSK-000001", BlockedBy: []string{"TSK-000002"}},
		{ID: "TSK-000002", BlockedBy: []string{"TSK-000003"}},
		{ID: "TSK-000003", BlockedBy: []string{"TSK-000001"}},
	}

	if !HasCycle(tasks) {
		t.Error("Expected cycle to be detected")
	}

	tasks2 := []*Task{
		{ID: "TSK-000001", BlockedBy: []string{"TSK-000002"}},
		{ID: "TSK-000002", BlockedBy: []string{"TSK-000003"}},
		{ID: "TSK-000003", BlockedBy: []string{}},
	}

	if HasCycle(tasks2) {
		t.Error("Expected no cycle to be detected")
	}
}

func TestCheckAndArchive(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-archive-*")
	defer os.RemoveAll(tmpDir)

	tasksDir := filepath.Join(tmpDir, "tasks")
	logsDir := filepath.Join(tasksDir, "logs")
	os.MkdirAll(logsDir, 0755)

	// Create some tasks
	task1 := &Task{ID: "TSK-000001", Status: "done"}
	task2 := &Task{ID: "TSK-000002", Status: "done"}

	WriteTask(filepath.Join(tasksDir, "TSK-000001.md"), task1)
	WriteTask(filepath.Join(tasksDir, "TSK-000002.md"), task2)
	os.WriteFile(filepath.Join(logsDir, "TSK-000001.jsonl"), []byte("{}"), 0644)

	// Should archive
	archived, err := CheckAndArchive(tasksDir)
	if err != nil {
		t.Fatalf("CheckAndArchive failed: %v", err)
	}
	if !archived {
		t.Error("Expected archiving to occur")
	}

	// Verify archive exists
	archiveRoot := filepath.Join(tasksDir, "archive")
	entries, _ := os.ReadDir(archiveRoot)
	if len(entries) != 1 {
		t.Errorf("Expected 1 archive entry, got %d", len(entries))
	}
}

func TestMigration(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-migration-*")
	defer os.RemoveAll(tmpDir)

	tasksDir := filepath.Join(tmpDir, "tasks")
	os.MkdirAll(tasksDir, 0755)

	// Create a V1 task file
	v1Content := `---
id: 1234
title: Old Task
status: todo
created_at: 2023-10-27T10:00:00Z
---

# Old Task

Some description.`
	v1Path := filepath.Join(tasksDir, "task-1234-old-task.md")
	os.WriteFile(v1Path, []byte(v1Content), 0644)

	// Run migration
	err := MigrateTasks(tasksDir)
	if err != nil {
		t.Fatalf("MigrateTasks failed: %v", err)
	}

	// Should have new file TSK-001234.md (or similar mapping)
	v2Path := filepath.Join(tasksDir, "TSK-001234.md")
	if _, err := os.Stat(v2Path); os.IsNotExist(err) {
		t.Errorf("Expected V2 task file %s to exist", v2Path)
	}

	// Verify old file is gone
	if _, err := os.Stat(v1Path); !os.IsNotExist(err) {
		t.Error("Expected V1 task file to be removed")
	}

	// Read migrated task
	task, err := ReadTask(v2Path)
	if err != nil {
		t.Fatalf("Failed to read migrated task: %v", err)
	}
	if task.ID != "TSK-001234" {
		t.Errorf("Expected migrated ID TSK-001234, got %s", task.ID)
	}
}
