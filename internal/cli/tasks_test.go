package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tenazas/internal/client"
	"tenazas/internal/engine"
	"tenazas/internal/models"
	"tenazas/internal/registry"
	"tenazas/internal/session"
	"tenazas/internal/storage"
	"tenazas/internal/task"
)

// setupTaskTest creates a CLI instance with a session anchored to a temp dir,
// and a matching tasks directory on disk. Returns the CLI (with captured output),
// the session, and the tasks directory path.
func setupTaskTest(t *testing.T) (*CLI, *models.Session, string) {
	t.Helper()
	tmpDir := t.TempDir()

	sm := session.NewManager(tmpDir)
	reg, _ := registry.NewRegistry(tmpDir)
	c, _ := client.NewClient("gemini", "gemini", filepath.Join(tmpDir, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	sess := &models.Session{
		ID:          uuid.New().String(),
		CWD:         tmpDir,
		LastUpdated: time.Now(),
		RoleCache:   make(map[string]string),
	}
	sm.Save(sess)

	cli := NewCLI(sm, reg, eng, "gemini", "", nil)
	var out bytes.Buffer
	cli.Out = &out
	cli.sess = sess

	tasksDir := filepath.Join(tmpDir, "tasks", storage.Slugify(tmpDir))
	os.MkdirAll(tasksDir, 0755)

	return cli, sess, tasksDir
}

// createTestTask writes a task file to the tasks directory and returns it.
func createTestTask(t *testing.T, tasksDir string, id, title, status string, priority int) *task.Task {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	tk := &task.Task{
		ID:        id,
		Title:     title,
		Status:    status,
		Priority:  priority,
		CreatedAt: now,
		UpdatedAt: now,
		Content:   "Test task content for " + title,
		FilePath:  filepath.Join(tasksDir, id+".md"),
	}
	if err := task.WriteTask(tk.FilePath, tk); err != nil {
		t.Fatalf("failed to create test task %s: %v", id, err)
	}
	return tk
}

// --- /tasks (list) ---

func TestHandleTasksEmpty(t *testing.T) {
	cli, _, _ := setupTaskTest(t)

	cli.handleTasks()

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No tasks found") {
		t.Errorf("expected 'No tasks found' message, got: %s", output)
	}
}

func TestHandleTasksList(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	createTestTask(t, tasksDir, "TSK-000001", "First task", task.StatusTodo, 1)
	createTestTask(t, tasksDir, "TSK-000002", "Second task", task.StatusInProgress, 2)

	cli.handleTasks()

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "TSK-000001") {
		t.Errorf("expected output to contain TSK-000001, got: %s", output)
	}
	if !strings.Contains(output, "TSK-000002") {
		t.Errorf("expected output to contain TSK-000002, got: %s", output)
	}
	if !strings.Contains(output, "First task") {
		t.Errorf("expected output to contain 'First task', got: %s", output)
	}
	if !strings.Contains(output, "Second task") {
		t.Errorf("expected output to contain 'Second task', got: %s", output)
	}
}

// --- /task show ---

func TestHandleTaskShow(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	createTestTask(t, tasksDir, "TSK-000001", "Show me task", task.StatusTodo, 3)

	cli.handleTaskShow(tasksDir, []string{"1"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "TSK-000001") {
		t.Errorf("expected output to contain TSK-000001, got: %s", output)
	}
	if !strings.Contains(output, "Show me task") {
		t.Errorf("expected output to contain task title, got: %s", output)
	}
	if !strings.Contains(output, "todo") {
		t.Errorf("expected output to contain status 'todo', got: %s", output)
	}
}

func TestHandleTaskShowNotFound(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskShow(tasksDir, []string{"999"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "TSK-000999") {
		t.Errorf("expected error to reference TSK-000999, got: %s", output)
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("expected 'not found' error, got: %s", output)
	}
}

func TestHandleTaskShowNoArgs(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskShow(tasksDir, []string{})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Usage") {
		t.Errorf("expected usage message, got: %s", output)
	}
}

// --- /task next ---

func TestHandleTaskNext(t *testing.T) {
	cli, sess, tasksDir := setupTaskTest(t)

	tk := createTestTask(t, tasksDir, "TSK-000001", "Next task", task.StatusTodo, 1)

	cli.handleTaskNext(tasksDir, sess)

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Started") {
		t.Errorf("expected 'Started' confirmation, got: %s", output)
	}
	if !strings.Contains(output, "TSK-000001") {
		t.Errorf("expected task ID in output, got: %s", output)
	}

	// Verify the task was actually updated on disk
	updated, err := task.FindTask(tasksDir, tk.ID)
	if err != nil {
		t.Fatalf("failed to read back task: %v", err)
	}
	if updated.Status != task.StatusInProgress {
		t.Errorf("expected status in-progress, got: %s", updated.Status)
	}
	if updated.OwnerPID == 0 {
		t.Error("expected OwnerPID to be set")
	}
	if updated.OwnerSessionID != sess.ID {
		t.Errorf("expected OwnerSessionID %s, got: %s", sess.ID, updated.OwnerSessionID)
	}
	if updated.StartedAt == nil {
		t.Error("expected StartedAt to be set")
	}
}

func TestHandleTaskNextAlreadyActive(t *testing.T) {
	cli, sess, tasksDir := setupTaskTest(t)

	createTestTask(t, tasksDir, "TSK-000001", "Active task", task.StatusInProgress, 1)
	createTestTask(t, tasksDir, "TSK-000002", "Waiting task", task.StatusTodo, 2)

	cli.handleTaskNext(tasksDir, sess)

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Already in progress") {
		t.Errorf("expected 'Already in progress' message, got: %s", output)
	}
	if !strings.Contains(output, "TSK-000001") {
		t.Errorf("expected active task ID in output, got: %s", output)
	}

	// Verify the waiting task was NOT modified
	waiting, err := task.FindTask(tasksDir, "TSK-000002")
	if err != nil {
		t.Fatalf("failed to read waiting task: %v", err)
	}
	if waiting.Status != task.StatusTodo {
		t.Errorf("waiting task should still be todo, got: %s", waiting.Status)
	}
}

func TestHandleTaskNextEmpty(t *testing.T) {
	cli, sess, tasksDir := setupTaskTest(t)

	cli.handleTaskNext(tasksDir, sess)

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No tasks ready") {
		t.Errorf("expected 'No tasks ready' message, got: %s", output)
	}
}

func TestHandleTaskNextAllDone(t *testing.T) {
	cli, sess, tasksDir := setupTaskTest(t)

	createTestTask(t, tasksDir, "TSK-000001", "Done task", task.StatusDone, 1)

	cli.handleTaskNext(tasksDir, sess)

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "No tasks ready") {
		t.Errorf("expected 'No tasks ready' when all done, got: %s", output)
	}
}

// --- /task complete ---

func TestHandleTaskComplete(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	tk := createTestTask(t, tasksDir, "TSK-000001", "Complete me", task.StatusInProgress, 1)
	// Simulate ownership
	tk.OwnerPID = os.Getpid()
	tk.OwnerSessionID = "some-session"
	task.WriteTask(tk.FilePath, tk)

	cli.handleTaskComplete(tasksDir)

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Completed") {
		t.Errorf("expected 'Completed' confirmation, got: %s", output)
	}
	if !strings.Contains(output, "TSK-000001") {
		t.Errorf("expected task ID in output, got: %s", output)
	}

	// Verify task state on disk
	updated, err := task.FindTask(tasksDir, tk.ID)
	if err != nil {
		t.Fatalf("failed to read back task: %v", err)
	}
	if updated.Status != task.StatusDone {
		t.Errorf("expected status done, got: %s", updated.Status)
	}
	if updated.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
	if updated.OwnerPID != 0 {
		t.Errorf("expected OwnerPID cleared, got: %d", updated.OwnerPID)
	}
	if updated.OwnerSessionID != "" {
		t.Errorf("expected OwnerSessionID cleared, got: %s", updated.OwnerSessionID)
	}
}

func TestHandleTaskCompleteNone(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	createTestTask(t, tasksDir, "TSK-000001", "Todo task", task.StatusTodo, 1)

	cli.handleTaskComplete(tasksDir)

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "no task currently in progress") {
		t.Errorf("expected 'no task currently in progress' error, got: %s", output)
	}
}

func TestHandleTaskCompleteEmpty(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskComplete(tasksDir)

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "no task currently in progress") {
		t.Errorf("expected error when no tasks exist, got: %s", output)
	}
}

// --- /task add ---

func TestHandleTaskAdd(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskAdd(tasksDir, []string{"Fix-login-bug", "The", "login", "page", "returns", "500"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Created") {
		t.Errorf("expected 'Created' confirmation, got: %s", output)
	}

	// Verify the task was written to disk
	tasks, err := task.ListTasks(tasksDir)
	if err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got: %d", len(tasks))
	}
	if tasks[0].Title != "Fix-login-bug" {
		t.Errorf("expected title 'Fix-login-bug', got: %s", tasks[0].Title)
	}
	if tasks[0].Status != task.StatusTodo {
		t.Errorf("expected status todo, got: %s", tasks[0].Status)
	}
	expectedContent := "The login page returns 500"
	if tasks[0].Content != expectedContent {
		t.Errorf("expected content %q, got: %q", expectedContent, tasks[0].Content)
	}
}

func TestHandleTaskAddNoArgs(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskAdd(tasksDir, []string{})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Usage") {
		t.Errorf("expected usage message, got: %s", output)
	}
}

func TestHandleTaskAddSingleArg(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskAdd(tasksDir, []string{"OnlyTitle"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Usage") {
		t.Errorf("expected usage message with single arg, got: %s", output)
	}
}

// --- /task unblock ---

func TestHandleTaskUnblock(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	tk := createTestTask(t, tasksDir, "TSK-000001", "Blocked task", task.StatusBlocked, 1)
	tk.FailureCount = 3
	task.WriteTask(tk.FilePath, tk)

	cli.handleTaskUnblock(tasksDir, []string{"1"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Unblocked") {
		t.Errorf("expected 'Unblocked' confirmation, got: %s", output)
	}
	if !strings.Contains(output, "TSK-000001") {
		t.Errorf("expected task ID in output, got: %s", output)
	}

	// Verify state on disk
	updated, err := task.FindTask(tasksDir, tk.ID)
	if err != nil {
		t.Fatalf("failed to read back task: %v", err)
	}
	if updated.Status != task.StatusTodo {
		t.Errorf("expected status todo, got: %s", updated.Status)
	}
	if updated.FailureCount != 0 {
		t.Errorf("expected failure count reset to 0, got: %d", updated.FailureCount)
	}
}

func TestHandleTaskUnblockNotBlocked(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	createTestTask(t, tasksDir, "TSK-000001", "Todo task", task.StatusTodo, 1)

	cli.handleTaskUnblock(tasksDir, []string{"1"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "not blocked") {
		t.Errorf("expected 'not blocked' error, got: %s", output)
	}
}

func TestHandleTaskUnblockNoArgs(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskUnblock(tasksDir, []string{})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Usage") {
		t.Errorf("expected usage message, got: %s", output)
	}
}

func TestHandleTaskUnblockNotFound(t *testing.T) {
	cli, _, tasksDir := setupTaskTest(t)

	cli.handleTaskUnblock(tasksDir, []string{"999"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Error") {
		t.Errorf("expected error for non-existent task, got: %s", output)
	}
}

// --- /task dispatcher ---

func TestHandleTasksNoSession(t *testing.T) {
	tmpDir := t.TempDir()
	sm := session.NewManager(tmpDir)
	reg, _ := registry.NewRegistry(tmpDir)
	c, _ := client.NewClient("gemini", "gemini", filepath.Join(tmpDir, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	cli := NewCLI(sm, reg, eng, "gemini", "", nil)
	var out bytes.Buffer
	cli.Out = &out
	cli.sess = nil // explicitly no session

	// Should not panic and should print error
	cli.handleTasks()

	output := out.String()
	if !strings.Contains(output, "no active session") {
		t.Errorf("expected 'no active session' error, got: %s", output)
	}
}

func TestHandleTaskUnknownSubcommand(t *testing.T) {
	cli, sess, _ := setupTaskTest(t)

	cli.handleTask(sess, []string{"foo"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Unknown task command") {
		t.Errorf("expected 'Unknown task command' error, got: %s", output)
	}
	if !strings.Contains(output, "foo") {
		t.Errorf("expected 'foo' in error message, got: %s", output)
	}
}

func TestHandleTaskNoArgs(t *testing.T) {
	cli, sess, _ := setupTaskTest(t)

	cli.handleTask(sess, []string{})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Usage") {
		t.Errorf("expected usage message, got: %s", output)
	}
}

func TestHandleTaskDispatchShow(t *testing.T) {
	cli, sess, tasksDir := setupTaskTest(t)

	createTestTask(t, tasksDir, "TSK-000001", "Dispatch test", task.StatusTodo, 1)

	cli.handleTask(sess, []string{"show", "1"})

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "TSK-000001") {
		t.Errorf("expected /task show to dispatch correctly, got: %s", output)
	}
}

// --- /help includes task commands ---

func TestHandleHelpIncludesTaskCommands(t *testing.T) {
	cli, _, _ := setupTaskTest(t)

	cli.handleHelp()

	output := cli.Out.(*bytes.Buffer).String()
	expectedEntries := []string{"/tasks", "/task show", "/task next", "/task complete", "/task add", "/task unblock"}
	for _, entry := range expectedEntries {
		if !strings.Contains(output, entry) {
			t.Errorf("expected help to contain %q, got: %s", entry, output)
		}
	}
}

// --- Tab completion ---

func TestGetCompletionsTask(t *testing.T) {
	cli, _, _ := setupTaskTest(t)

	completions := cli.getCompletions("/ta")

	foundTask := false
	foundTasks := false
	for _, c := range completions {
		if c == "/task" {
			foundTask = true
		}
		if c == "/tasks" {
			foundTasks = true
		}
	}
	if !foundTask {
		t.Errorf("expected /task in completions for '/ta', got: %v", completions)
	}
	if !foundTasks {
		t.Errorf("expected /tasks in completions for '/ta', got: %v", completions)
	}
}

func TestGetCompletionsTaskSub(t *testing.T) {
	cli, _, _ := setupTaskTest(t)

	completions := cli.getCompletions("/task s")

	found := false
	for _, c := range completions {
		if c == "/task show" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected '/task show' in completions for '/task s', got: %v", completions)
	}
}

func TestGetCompletionsTaskSubAll(t *testing.T) {
	cli, _, _ := setupTaskTest(t)

	completions := cli.getCompletions("/task ")

	expected := []string{"show", "next", "complete", "add", "unblock"}
	for _, sub := range expected {
		found := false
		for _, c := range completions {
			if c == "/task "+sub {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected '/task %s' in completions for '/task ', got: %v", sub, completions)
		}
	}
}

// --- handleCommand routing ---

func TestHandleCommandRoutesToTasks(t *testing.T) {
	cli, sess, _ := setupTaskTest(t)

	// /tasks should route to handleTasks (which calls resolveTasksDir → works since we have session)
	cli.handleCommand(sess, "/tasks")

	output := cli.Out.(*bytes.Buffer).String()
	// With no tasks, we expect "No tasks found"
	if !strings.Contains(output, "No tasks found") {
		t.Errorf("expected /tasks command to be routed and show empty list, got: %s", output)
	}
}

func TestHandleCommandRoutesToTask(t *testing.T) {
	cli, sess, _ := setupTaskTest(t)

	cli.handleCommand(sess, "/task")

	output := cli.Out.(*bytes.Buffer).String()
	if !strings.Contains(output, "Usage") {
		t.Errorf("expected /task with no args to show usage, got: %s", output)
	}
}
