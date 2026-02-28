package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tenazas/internal/storage"
)

const taskIDPrefix = "TSK-"

func HandleWorkCommand(storageDir string, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: tenazas work [init|add|next|complete|status|list|show]")
		os.Exit(1)
	}

	cmd := args[0]
	tasksDir := GetTasksDir(storageDir)

	switch cmd {
	case "init":
		handleWorkInit(tasksDir)
	case "add":
		handleWorkAdd(tasksDir, args[1:])
	case "next":
		handleWorkNext(tasksDir)
	case "complete":
		handleWorkComplete(tasksDir)
	case "status":
		handleWorkStatus(tasksDir)
	case "list":
		handleWorkList(tasksDir)
	case "show":
		handleWorkShow(tasksDir, args[1:])
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func handleWorkInit(tasksDir string) {
	if err := MigrateTasks(tasksDir); err != nil {
		fmt.Fprintf(os.Stderr, "Migration error: %v\n", err)
	}

	fmt.Printf("Tasks directory: %s\n", tasksDir)

	tasks := listTasksOrDie(tasksDir)
	if len(tasks) == 0 {
		fmt.Println("No tasks found. Use 'tenazas work add \"Title\" \"Description\"' to create one.")
		return
	}

	printStatusSummaryTo(os.Stdout, tasks)
}

// extractPriorityFlag separates --priority <int> from positional args.
func extractPriorityFlag(args []string) (int, []string, error) {
	var priority int
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--priority" {
			if i+1 >= len(args) {
				return 0, nil, fmt.Errorf("--priority requires a value")
			}
			p, err := strconv.Atoi(args[i+1])
			if err != nil || p < 0 {
				return 0, nil, fmt.Errorf("--priority must be a non-negative integer")
			}
			priority = p
			i++
		} else {
			positional = append(positional, args[i])
		}
	}
	return priority, positional, nil
}

func handleWorkAdd(tasksDir string, args []string) {
	priority, positional, err := extractPriorityFlag(args)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if len(positional) < 2 {
		fmt.Println("Usage: tenazas work add [--priority <int>] \"Title\" \"Description\"")
		os.Exit(1)
	}

	id, err := GetNextTaskID(tasksDir)
	if err != nil {
		fmt.Printf("Error generating task ID: %v\n", err)
		os.Exit(1)
	}

	task := &Task{
		ID:        id,
		Title:     positional[0],
		Status:    StatusTodo,
		Priority:  priority,
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   positional[1],
	}

	taskPath := filepath.Join(tasksDir, id+".md")
	if err := WriteTask(taskPath, task); err != nil {
		fmt.Printf("Error writing task: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created task: %s\n", taskPath)
}

func handleWorkNext(tasksDir string) {
	tasks, err := ListTasks(tasksDir)
	if err != nil {
		fmt.Printf("Error searching for tasks: %v\n", err)
		os.Exit(1)
	}

	if active := findInProgress(tasks); active != nil {
		fmt.Printf("ALREADY IN PROGRESS: %s\n", active.ID)
		return
	}

	next := SelectNextTask(tasks)
	if next == nil {
		fmt.Println("EMPTY")
		os.Exit(1)
	}

	next.Status = StatusInProgress
	next.OwnerPID = os.Getpid()
	now := time.Now().Truncate(time.Second)
	next.StartedAt = &now
	updateAndPrintTask(next)
}

func handleWorkComplete(tasksDir string) {
	tasks := listTasksOrDie(tasksDir)
	active := findInProgress(tasks)
	if active == nil {
		fmt.Println("ERROR: No task in progress")
		os.Exit(1)
	}

	active.Status = StatusDone
	now := time.Now().Truncate(time.Second)
	active.CompletedAt = &now
	active.ClearOwnership()
	if err := WriteTask(active.FilePath, active); err != nil {
		fmt.Printf("Error updating task: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("COMPLETED:%s\n", filepath.Base(active.FilePath))
}

func handleWorkStatus(tasksDir string) {
	printStatusSummaryTo(os.Stdout, listTasksOrDie(tasksDir))
}

func findInProgress(tasks []*Task) *Task {
	for _, t := range tasks {
		if t.Status == StatusInProgress {
			return t
		}
	}
	return nil
}

func listTasksOrDie(tasksDir string) []*Task {
	tasks, err := ListTasks(tasksDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing tasks: %v\n", err)
		os.Exit(1)
	}
	return tasks
}

func updateAndPrintTask(t *Task) {
	if err := WriteTask(t.FilePath, t); err != nil {
		fmt.Printf("Error updating task: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("STARTING:%s\n\n", t.FilePath)
	data, err := os.ReadFile(t.FilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading task file: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func GetTasksDir(storageDir string) string {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}
	tasksDir := filepath.Join(storageDir, "tasks", storage.Slugify(cwd))
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating tasks directory: %v\n", err)
		os.Exit(1)
	}
	return tasksDir
}

func handleWorkList(tasksDir string) {
	tasks := listTasksOrDie(tasksDir)
	RenderList(os.Stdout, tasks)
}

func handleWorkShow(tasksDir string, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tenazas work show <task-id>")
		os.Exit(1)
	}
	id := normalizeTaskID(args[0])
	allTasks := listTasksOrDie(tasksDir)
	taskMap := buildTaskMap(allTasks)
	task, ok := taskMap[id]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: Task %s not found\n", id)
		os.Exit(1)
	}
	RenderShow(os.Stdout, task, taskMap)
}

func normalizeTaskID(input string) string {
	s := strings.TrimSpace(input)
	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, taskIDPrefix) {
		return upper
	}
	if n, err := strconv.Atoi(s); err == nil {
		return fmt.Sprintf("%s%06d", taskIDPrefix, n)
	}
	return s
}
