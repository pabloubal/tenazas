package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func HandleWorkCommand(storageDir string, args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: tenazas work [init|add|next|complete|status]")
		os.Exit(1)
	}

	cmd := args[0]
	tasksDir := getTasksDir(storageDir)

	if cmd == "init" {
		fmt.Printf("Initialized tasks directory: %s\n", tasksDir)
		return
	}

	switch cmd {
	case "add":
		handleWorkAdd(tasksDir, args[1:])
	case "next":
		handleWorkNext(tasksDir)
	case "complete":
		handleWorkComplete(tasksDir)
	case "status":
		handleWorkStatus(tasksDir)
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func handleWorkAdd(tasksDir string, args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: tenazas work add \"Title\" \"Description\"")
		os.Exit(1)
	}

	id, err := getNextTaskID(tasksDir)
	if err != nil {
		fmt.Printf("Error generating task ID: %v\n", err)
		os.Exit(1)
	}

	task := &Task{
		ID:        id,
		Title:     args[0],
		Status:    "todo",
		CreatedAt: time.Now().Truncate(time.Second),
		Content:   args[1],
	}

	taskPath := filepath.Join(tasksDir, id+".md")
	if err := WriteTask(taskPath, task); err != nil {
		fmt.Printf("Error writing task: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created task: %s\n", taskPath)
}

func handleWorkNext(tasksDir string) {
	tasks, err := listTasks(tasksDir)
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

	next.Status = "in-progress"
	updateAndPrintTask(next)
}

func handleWorkComplete(tasksDir string) {
	tasks, _ := listTasks(tasksDir)
	if active := findInProgress(tasks); active != nil {
		active.Status = "done"
		if err := WriteTask(active.FilePath, active); err != nil {
			fmt.Printf("Error updating task: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("COMPLETED:%s\n", filepath.Base(active.FilePath))
		return
	}
	fmt.Println("ERROR: No task in progress")
	os.Exit(1)
}

func handleWorkStatus(tasksDir string) {
	tasks, _ := listTasks(tasksDir)
	counts := make(map[string]int)
	for _, t := range tasks {
		counts[t.Status]++
	}
	fmt.Printf("Todo: %d | In-Progress: %d | Done: %d | Blocked: %d\n",
		counts["todo"], counts["in-progress"], counts["done"], counts["blocked"])
}

func findInProgress(tasks []*Task) *Task {
	for _, t := range tasks {
		if t.Status == "in-progress" {
			return t
		}
	}
	return nil
}

func updateAndPrintTask(t *Task) {
	if err := WriteTask(t.FilePath, t); err != nil {
		fmt.Printf("Error updating task: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("STARTING:%s\n\n", t.FilePath)
	data, _ := os.ReadFile(t.FilePath)
	fmt.Println(string(data))
}

func getTasksDir(storageDir string) string {
	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(storageDir, "tasks", Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)
	return tasksDir
}
