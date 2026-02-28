package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tenazas/internal/models"
	"tenazas/internal/storage"
	"tenazas/internal/task"
)

func truncatedNow() time.Time {
	return time.Now().Truncate(time.Second)
}

func (c *CLI) writef(format string, args ...interface{}) {
	c.write(fmt.Sprintf(format, args...))
}

// resolveTasksDir returns the tasks directory for the active session's CWD.
func (c *CLI) resolveTasksDir() (string, bool) {
	if c.sess == nil {
		c.write("Error: no active session. Start or resume a session first.\n")
		return "", false
	}
	tasksDir := filepath.Join(c.Sm.StoragePath, "tasks", storage.Slugify(c.sess.CWD))
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		c.writef("Error creating tasks directory: %v\n", err)
		return "", false
	}
	return tasksDir, true
}

// loadTasks wraps task.ListTasks with error output.
func (c *CLI) loadTasks(tasksDir string) ([]*task.Task, bool) {
	tasks, err := task.ListTasks(tasksDir)
	if err != nil {
		c.writef("Error listing tasks: %v\n", err)
		return nil, false
	}
	return tasks, true
}

// saveTask wraps task.WriteTask with error output.
func (c *CLI) saveTask(t *task.Task) bool {
	if err := task.WriteTask(t.FilePath, t); err != nil {
		c.writef("Error saving task: %v\n", err)
		return false
	}
	return true
}

func findInProgress(tasks []*task.Task) *task.Task {
	for _, t := range tasks {
		if t.Status == task.StatusInProgress {
			return t
		}
	}
	return nil
}

// handleTasks implements "/tasks" — list all tasks.
func (c *CLI) handleTasks() {
	tasksDir, ok := c.resolveTasksDir()
	if !ok {
		return
	}
	tasks, ok := c.loadTasks(tasksDir)
	if !ok {
		return
	}
	var buf bytes.Buffer
	task.RenderList(&buf, tasks)
	c.write(buf.String())
}

// handleTask dispatches "/task <subcommand> [args...]".
func (c *CLI) handleTask(sess *models.Session, args []string) {
	if len(args) == 0 {
		c.write("Usage: /task <show|next|complete|add|unblock> [args...]\n")
		return
	}
	tasksDir, ok := c.resolveTasksDir()
	if !ok {
		return
	}
	switch args[0] {
	case "show":
		c.handleTaskShow(tasksDir, args[1:])
	case "next":
		c.handleTaskNext(tasksDir, sess)
	case "complete":
		c.handleTaskComplete(tasksDir)
	case "add":
		c.handleTaskAdd(tasksDir, args[1:])
	case "unblock":
		c.handleTaskUnblock(tasksDir, args[1:])
	default:
		c.writef("Unknown task command: %s\n", args[0])
	}
}

// handleTaskShow implements "/task show <id>".
func (c *CLI) handleTaskShow(tasksDir string, args []string) {
	if len(args) < 1 {
		c.write("Usage: /task show <id>\n")
		return
	}
	id := task.NormalizeTaskID(args[0])
	allTasks, ok := c.loadTasks(tasksDir)
	if !ok {
		return
	}
	taskMap := make(map[string]*task.Task, len(allTasks))
	for _, t := range allTasks {
		taskMap[t.ID] = t
	}
	target, found := taskMap[id]
	if !found {
		c.writef("Error: task %s not found\n", id)
		return
	}
	var buf bytes.Buffer
	task.RenderShow(&buf, target, taskMap)
	c.write(buf.String())
}

// handleTaskNext implements "/task next".
func (c *CLI) handleTaskNext(tasksDir string, sess *models.Session) {
	tasks, ok := c.loadTasks(tasksDir)
	if !ok {
		return
	}
	if active := findInProgress(tasks); active != nil {
		c.writef("Already in progress: %s — %s\n", active.ID, active.Title)
		return
	}
	next := task.SelectNextTask(tasks)
	if next == nil {
		c.write("No tasks ready to start.\n")
		return
	}
	now := truncatedNow()
	next.Status = task.StatusInProgress
	next.OwnerPID = os.Getpid()
	next.OwnerSessionID = sess.ID
	if next.StartedAt == nil {
		next.StartedAt = &now
	}
	next.UpdatedAt = now
	if c.saveTask(next) {
		c.writef("Started: %s — %s\n", next.ID, next.Title)
	}
}

// handleTaskComplete implements "/task complete".
func (c *CLI) handleTaskComplete(tasksDir string) {
	tasks, ok := c.loadTasks(tasksDir)
	if !ok {
		return
	}
	active := findInProgress(tasks)
	if active == nil {
		c.write("Error: no task currently in progress.\n")
		return
	}
	now := truncatedNow()
	active.Status = task.StatusDone
	active.CompletedAt = &now
	active.ClearOwnership()
	active.UpdatedAt = now
	if c.saveTask(active) {
		c.writef("Completed: %s — %s\n", active.ID, active.Title)
	}
}

// handleTaskAdd implements "/task add <title> <description>".
func (c *CLI) handleTaskAdd(tasksDir string, args []string) {
	if len(args) < 2 {
		c.write("Usage: /task add \"title\" \"description\"\n")
		return
	}
	id, err := task.GetNextTaskID(tasksDir)
	if err != nil {
		c.writef("Error generating task ID: %v\n", err)
		return
	}
	now := truncatedNow()
	t := &task.Task{
		ID:        id,
		Title:     args[0],
		Status:    task.StatusTodo,
		CreatedAt: now,
		UpdatedAt: now,
		Content:   strings.Join(args[1:], " "),
		FilePath:  filepath.Join(tasksDir, id+".md"),
	}
	if c.saveTask(t) {
		c.writef("Created: %s — %s\n", id, args[0])
	}
}

// handleTaskUnblock implements "/task unblock <id>".
func (c *CLI) handleTaskUnblock(tasksDir string, args []string) {
	if len(args) < 1 {
		c.write("Usage: /task unblock <id>\n")
		return
	}
	id := task.NormalizeTaskID(args[0])
	t, err := task.FindTask(tasksDir, id)
	if err != nil {
		c.writef("Error: %v\n", err)
		return
	}
	if t.Status != task.StatusBlocked {
		c.writef("Error: task %s is not blocked (current: %s)\n", id, t.Status)
		return
	}
	t.Status = task.StatusTodo
	t.ClearOwnership()
	t.FailureCount = 0
	t.UpdatedAt = truncatedNow()
	if c.saveTask(t) {
		c.writef("Unblocked: %s — %s\n", id, t.Title)
	}
}
