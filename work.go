package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		if len(args) < 3 {
			fmt.Println("Usage: tenazas work add \"Title\" \"Description\"")
			os.Exit(1)
		}
		title, desc := args[1], args[2]
		id := time.Now().UnixNano() % 10000
		slug := strings.ReplaceAll(strings.ToLower(title), " ", "-")
		filename := filepath.Join(tasksDir, fmt.Sprintf("task-%d-%s.md", id, slug))

		content := fmt.Sprintf("---\nid: %d\ntitle: %s\nstatus: todo\ncreated_at: %s\n---\n\n# %s\n\n%s\n",
			id, title, time.Now().Format(time.RFC3339), title, desc)

		os.WriteFile(filename, []byte(content), 0644)
		fmt.Printf("Created task: %s\n", filename)

	case "next":
		files, err := filepath.Glob(filepath.Join(tasksDir, "*.md"))
		if err != nil {
			fmt.Printf("Error searching for tasks: %v\n", err)
			os.Exit(1)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if strings.Contains(string(data), "status: todo") {
				newContent := strings.Replace(string(data), "status: todo", "status: in-progress", 1)
				if err := os.WriteFile(f, []byte(newContent), 0644); err != nil {
					fmt.Printf("Error updating task: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("STARTING:%s\n\n%s", f, string(data))
				return
			}
		}
		fmt.Println("EMPTY")
		os.Exit(1)

	case "complete":
		files, err := filepath.Glob(filepath.Join(tasksDir, "*.md"))
		if err != nil {
			fmt.Printf("Error searching for tasks: %v\n", err)
			os.Exit(1)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if strings.Contains(string(data), "status: in-progress") {
				newContent := strings.Replace(string(data), "status: in-progress", "status: done", 1)
				if err := os.WriteFile(f, []byte(newContent), 0644); err != nil {
					fmt.Printf("Error updating task: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("COMPLETED:%s\n", filepath.Base(f))
				return
			}
		}
		fmt.Println("ERROR: No task in progress")
		os.Exit(1)

	case "status":
		files, _ := filepath.Glob(filepath.Join(tasksDir, "*.md"))
		todo, ip, done := 0, 0, 0
		for _, f := range files {
			data, _ := os.ReadFile(f)
			s := string(data)
			if strings.Contains(s, "status: todo") {
				todo++
			}
			if strings.Contains(s, "status: in-progress") {
				ip++
			}
			if strings.Contains(s, "status: done") {
				done++
			}
		}
		fmt.Printf("Todo: %d | In-Progress: %d | Done: %d\n", todo, ip, done)
	}
}

func getTasksDir(storageDir string) string {
	cwd, _ := os.Getwd()
	tasksDir := filepath.Join(storageDir, "tasks", Slugify(cwd))
	os.MkdirAll(tasksDir, 0755)
	return tasksDir
}
