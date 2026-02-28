package task

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Task status constants.
const (
	StatusTodo       = "todo"
	StatusInProgress = "in-progress"
	StatusDone       = "done"
	StatusBlocked    = "blocked"
)

// Task represents a work item managed by the task system.
type Task struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	FailureCount    int        `json:"failure_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Blocks          []string   `json:"blocks,omitempty"`
	BlockedBy       []string   `json:"blocked_by,omitempty"`
	OwnerPID        int        `json:"owner_pid,omitempty"`
	OwnerInstanceID string     `json:"owner_instance_id,omitempty"`
	OwnerSessionID  string     `json:"owner_session_id,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Skill           string     `json:"skill,omitempty"`
	Labels          []string   `json:"labels,omitempty"`
	Content         string     `json:"-"`
	FilePath        string     `json:"-"`
}

// NormalizeTaskID converts user input into canonical TSK-XXXXXX format.
// Examples: "6" → "TSK-000006", "tsk-6" → "TSK-6", "TSK-000006" → "TSK-000006"
func NormalizeTaskID(input string) string {
	return normalizeTaskID(input)
}

// ClearOwnership resets all owner fields after a task is completed or blocked.
func (t *Task) ClearOwnership() {
	t.OwnerPID = 0
	t.OwnerInstanceID = ""
	t.OwnerSessionID = ""
}

// IsReady checks if a task is 'todo' and all its dependencies are 'done'.
func (t *Task) IsReady(taskMap map[string]*Task) bool {
	if t.Status != StatusTodo {
		return false
	}
	for _, id := range t.BlockedBy {
		blocker, ok := taskMap[id]
		if !ok || blocker.Status != StatusDone {
			return false
		}
	}
	return true
}

func WriteTask(path string, task *Task) error {
	task.UpdatedAt = time.Now().Truncate(time.Second)
	fm, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}

	data := fmt.Sprintf("---\n%s\n---\n\n%s", string(fm), task.Content)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tempFile := path + ".tmp"
	if err := os.WriteFile(tempFile, []byte(data), 0644); err != nil {
		return err
	}

	if err := os.Rename(tempFile, path); err != nil {
		os.Remove(tempFile)
		return err
	}
	return nil
}

func ReadTask(path string) (*Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var fmLines []string
	var content strings.Builder
	var inFM, fmDone bool

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			if !fmDone && !inFM {
				inFM = true
			} else if inFM {
				inFM = false
				fmDone = true
			}
			continue
		}

		if inFM {
			fmLines = append(fmLines, line)
		} else if fmDone {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}

	task := &Task{}
	fmRaw := strings.Join(fmLines, "\n")
	if err := json.Unmarshal([]byte(fmRaw), task); err != nil {
		task = parseSimpleKV(fmLines)
	}

	task.Content = strings.TrimSpace(content.String())
	task.FilePath = path
	return task, scanner.Err()
}

func parseSimpleKV(lines []string) *Task {
	t := &Task{}
	for _, line := range lines {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "id":
			t.ID = v
		case "title":
			t.Title = v
		case "status":
			t.Status = v
		case "priority":
			t.Priority, _ = strconv.Atoi(v)
		case "failure_count":
			t.FailureCount, _ = strconv.Atoi(v)
		case "created_at", "updated_at":
			parsed, _ := time.Parse(time.RFC3339, v)
			if k == "created_at" {
				t.CreatedAt = parsed
			} else {
				t.UpdatedAt = parsed
			}
		case "blocks", "blocked_by":
			list := parseList(v)
			if k == "blocks" {
				t.Blocks = list
			} else {
				t.BlockedBy = list
			}
		case "skill":
			t.Skill = v
		case "labels":
			t.Labels = parseList(v)
		}
	}
	return t
}

// allowedTransitions defines the valid status state machine.
var allowedTransitions = map[string][]string{
	StatusTodo:       {StatusInProgress, StatusBlocked, StatusDone},
	StatusInProgress: {StatusDone, StatusBlocked, StatusTodo},
	StatusBlocked:    {StatusTodo, StatusInProgress},
	StatusDone:       {StatusTodo},
}

// ValidateStatusTransition returns an error if moving from `from` to `to` is not allowed.
func ValidateStatusTransition(from, to string) error {
	targets, ok := allowedTransitions[from]
	if !ok {
		return fmt.Errorf("unknown status: %s", from)
	}
	if !sliceContains(targets, to) {
		return fmt.Errorf("invalid transition: %s → %s", from, to)
	}
	return nil
}

// AddDependency adds depID to task.BlockedBy and the reverse Blocks edge.
// It is idempotent and performs cycle detection.
func AddDependency(tasksDir string, task *Task, depID string) error {
	if depID == task.ID {
		return fmt.Errorf("cannot add self-dependency")
	}
	if sliceContains(task.BlockedBy, depID) {
		return nil // idempotent
	}
	dep, err := FindTask(tasksDir, depID)
	if err != nil {
		return fmt.Errorf("dependency %s not found", depID)
	}

	// Tentatively add edges; rollback undoes both on failure.
	task.BlockedBy = append(task.BlockedBy, depID)
	dep.Blocks = append(dep.Blocks, task.ID)
	rollback := func() {
		task.BlockedBy = removeFromSlice(task.BlockedBy, depID)
		dep.Blocks = removeFromSlice(dep.Blocks, task.ID)
	}

	allTasks, err := ListTasks(tasksDir)
	if err != nil {
		rollback()
		return err
	}
	for i, t := range allTasks {
		if t.ID == task.ID {
			allTasks[i] = task
		} else if t.ID == dep.ID {
			allTasks[i] = dep
		}
	}
	if HasCycle(allTasks) {
		rollback()
		return fmt.Errorf("adding dependency %s → %s would create a cycle", task.ID, depID)
	}

	if err := WriteTask(task.FilePath, task); err != nil {
		return err
	}
	return WriteTask(dep.FilePath, dep)
}

// RemoveDependency removes depID from task.BlockedBy and the reverse Blocks edge.
// Idempotent — no error if the edge doesn't exist.
func RemoveDependency(tasksDir string, task *Task, depID string) error {
	task.BlockedBy = removeFromSlice(task.BlockedBy, depID)
	if err := WriteTask(task.FilePath, task); err != nil {
		return err
	}

	dep, err := FindTask(tasksDir, depID)
	if err != nil {
		return nil // dep task missing, local side already cleaned
	}
	dep.Blocks = removeFromSlice(dep.Blocks, task.ID)
	return WriteTask(dep.FilePath, dep)
}

func sliceContains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

func removeFromSlice(slice []string, val string) []string {
	var result []string
	for _, s := range slice {
		if s != val {
			result = append(result, s)
		}
	}
	return result
}

func parseList(val string) []string {
	val = strings.Trim(val, "[]")
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), "\"")
	}
	return parts
}

func GetNextTaskID(tasksDir string) (string, error) {
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		return "", err
	}

	f, err := os.OpenFile(filepath.Join(tasksDir, ".task_sequence"), os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	var seq int
	fmt.Fscanf(f, "%d", &seq)
	seq++

	f.Seek(0, 0)
	f.Truncate(0)
	fmt.Fprintf(f, "%d", seq)

	return fmt.Sprintf("TSK-%06d", seq), nil
}

func buildTaskMap(tasks []*Task) map[string]*Task {
	m := make(map[string]*Task, len(tasks))
	for _, t := range tasks {
		m[t.ID] = t
	}
	return m
}

func SelectNextTask(tasks []*Task) *Task {
	taskMap := buildTaskMap(tasks)

	var ready []*Task
	for _, t := range tasks {
		if t.IsReady(taskMap) {
			ready = append(ready, t)
		}
	}

	if len(ready) == 0 {
		return nil
	}

	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})

	return ready[0]
}

func HasCycle(tasks []*Task) bool {
	taskMap := buildTaskMap(tasks)

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(string) bool
	dfs = func(id string) bool {
		visited[id] = true
		recStack[id] = true

		if t, ok := taskMap[id]; ok {
			for _, neighbor := range t.BlockedBy {
				if !visited[neighbor] {
					if dfs(neighbor) {
						return true
					}
				} else if recStack[neighbor] {
					return true
				}
			}
		}

		recStack[id] = false
		return false
	}

	for _, t := range tasks {
		if !visited[t.ID] && dfs(t.ID) {
			return true
		}
	}
	return false
}

func CheckAndArchive(tasksDir string) (bool, error) {
	tasks, err := ListTasks(tasksDir)
	if err != nil || len(tasks) == 0 {
		return false, err
	}

	for _, t := range tasks {
		if t.Status != StatusDone {
			return false, nil
		}
	}

	if err := archiveTaskFiles(tasksDir, tasks); err != nil {
		return false, err
	}
	return true, nil
}

// ForceArchive selectively archives only done tasks, leaving active tasks in place.
// Returns an error if any active task has a BlockedBy reference to a done task.
func ForceArchive(tasksDir string) (int, error) {
	tasks, err := ListTasks(tasksDir)
	if err != nil || len(tasks) == 0 {
		return 0, err
	}

	var doneTasks, activeTasks []*Task
	for _, t := range tasks {
		if t.Status == StatusDone {
			doneTasks = append(doneTasks, t)
		} else {
			activeTasks = append(activeTasks, t)
		}
	}

	if len(doneTasks) == 0 {
		return 0, nil
	}

	doneIDs := make(map[string]bool, len(doneTasks))
	for _, t := range doneTasks {
		doneIDs[t.ID] = true
	}

	for _, t := range activeTasks {
		for _, depID := range t.BlockedBy {
			if doneIDs[depID] {
				return 0, fmt.Errorf("cannot archive: active task %s still references completed task %s", t.ID, depID)
			}
		}
	}

	if err := archiveTaskFiles(tasksDir, doneTasks); err != nil {
		return 0, err
	}
	return len(doneTasks), nil
}

// archiveTaskFiles creates a timestamped archive directory and moves the given
// task files and their associated log files into it.
func archiveTaskFiles(tasksDir string, tasks []*Task) error {
	timestamp := time.Now().Format(time.RFC3339)
	archiveDir := filepath.Join(tasksDir, "archive", timestamp)
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}

	for _, t := range tasks {
		dest := filepath.Join(archiveDir, filepath.Base(t.FilePath))
		if err := os.Rename(t.FilePath, dest); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not move task %s: %v\n", t.ID, err)
		}
	}

	logsDir := filepath.Join(tasksDir, "logs")
	archiveLogs := filepath.Join(archiveDir, "logs")
	logsDirCreated := false
	for _, t := range tasks {
		logFile := filepath.Join(logsDir, t.ID+".jsonl")
		if _, err := os.Stat(logFile); err != nil {
			continue
		}
		if !logsDirCreated {
			if err := os.MkdirAll(archiveLogs, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not create archive logs dir: %v\n", err)
				break
			}
			logsDirCreated = true
		}
		dest := filepath.Join(archiveLogs, t.ID+".jsonl")
		if err := os.Rename(logFile, dest); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not move log %s: %v\n", logFile, err)
		}
	}

	return nil
}

func ListTasks(dir string) ([]*Task, error) {
	var tasks []*Task
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == "archive" {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasPrefix(info.Name(), "TSK-") && strings.HasSuffix(info.Name(), ".md") {
			if t, err := ReadTask(path); err == nil {
				tasks = append(tasks, t)
			}
		}
		return nil
	})
	return tasks, err
}

func FindTask(dir string, id string) (*Task, error) {
	path := filepath.Join(dir, id+".md")
	t, err := ReadTask(path)
	if err != nil {
		return nil, fmt.Errorf("task %s not found", id)
	}
	return t, nil
}

func MigrateTasks(tasksDir string) error {
	var files []string
	err := filepath.Walk(tasksDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasPrefix(info.Name(), "task-") && strings.HasSuffix(info.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	re := regexp.MustCompile(`task-(\d+)-`)
	for _, f := range files {
		m := re.FindStringSubmatch(filepath.Base(f))
		if len(m) < 2 {
			continue
		}

		t, err := ReadTask(f)
		if err != nil {
			continue
		}

		idNum, _ := strconv.Atoi(m[1])
		t.ID = fmt.Sprintf("TSK-%06d", idNum)
		destPath := filepath.Join(filepath.Dir(f), t.ID+".md")

		if _, err := os.Stat(destPath); err == nil {
			fmt.Printf("Skipping migration for %s: %s already exists\n", f, t.ID)
			continue
		}

		if err := WriteTask(destPath, t); err == nil {
			if err := os.Remove(f); err != nil {
				fmt.Printf("Failed to remove old task file %s: %v\n", f, err)
			}
		} else {
			fmt.Printf("Failed to migrate %s: %v\n", f, err)
		}
	}
	return nil
}
