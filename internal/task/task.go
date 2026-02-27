package task

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Task represents a work item managed by the task system.
type Task struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Status           string     `json:"status"`
	FailureCount     int        `json:"failure_count"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Blocks           []string   `json:"blocks,omitempty"`
	BlockedBy        []string   `json:"blocked_by,omitempty"`
	OwnerPID         int        `json:"owner_pid,omitempty"`
	OwnerInstanceID  string     `json:"owner_instance_id,omitempty"`
	OwnerSessionID   string     `json:"owner_session_id,omitempty"`
	OwnerSessionRole string     `json:"owner_session_role,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	Content          string     `json:"-"`
	FilePath         string     `json:"-"`
}

// IsReady checks if a task is 'todo' and all its dependencies are 'done'.
func (t *Task) IsReady(taskMap map[string]*Task) bool {
	if t.Status != "todo" {
		return false
	}
	for _, id := range t.BlockedBy {
		blocker, ok := taskMap[id]
		if !ok || blocker.Status != "done" {
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
	return os.WriteFile(path, []byte(data), 0644)
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
		}
	}
	return t
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

func SelectNextTask(tasks []*Task) *Task {
	taskMap := make(map[string]*Task, len(tasks))
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

	for _, t := range tasks {
		if t.IsReady(taskMap) {
			return t
		}
	}
	return nil
}

func HasCycle(tasks []*Task) bool {
	taskMap := make(map[string]*Task, len(tasks))
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

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
		if t.Status != "done" {
			return false, nil
		}
	}

	timestamp := time.Now().Format(time.RFC3339)
	archiveDir := filepath.Join(tasksDir, "archive", timestamp)
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return false, err
	}

	for _, t := range tasks {
		destPath := filepath.Join(archiveDir, filepath.Base(t.FilePath))
		if err := os.Rename(t.FilePath, destPath); err != nil {
			fmt.Printf("Error moving task %s to archive: %v\n", t.ID, err)
		}
	}

	logsDir := filepath.Join(tasksDir, "logs")
	if logFiles, _ := filepath.Glob(filepath.Join(logsDir, "*.jsonl")); len(logFiles) > 0 {
		archiveLogs := filepath.Join(archiveDir, "logs")
		if err := os.MkdirAll(archiveLogs, 0755); err != nil {
			fmt.Printf("Error creating archive logs dir: %v\n", err)
		} else {
			for _, f := range logFiles {
				dest := filepath.Join(archiveLogs, filepath.Base(f))
				if err := os.Rename(f, dest); err != nil {
					fmt.Printf("Error moving log %s to archive: %v\n", f, err)
				}
			}
		}
	}

	return true, nil
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
