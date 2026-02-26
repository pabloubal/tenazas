package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func HandleWorkCommand(args []string) {
	if len(args) < 1 {
		fmt.Println("Usage: tenazas work [init|add|next|complete|status]")
		os.Exit(1)
	}

	cmd := args[0]
	if cmd == "init" {
		os.MkdirAll(".tenazas/tasks", 0755)
		fmt.Println("Initialized .tenazas/tasks in current directory")
		return
	}

	tasksDir, err := findTasksDir()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
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
		files, _ := filepath.Glob(filepath.Join(tasksDir, "*.md"))
		for _, f := range files {
			data, _ := os.ReadFile(f)
			if strings.Contains(string(data), "status: todo") {
				newContent := strings.Replace(string(data), "status: todo", "status: in-progress", 1)
				os.WriteFile(f, []byte(newContent), 0644)
				fmt.Printf("STARTING:%s\n\n%s", f, string(data))
				return
			}
		}
		fmt.Println("EMPTY")
		os.Exit(1)

	case "complete":
		files, _ := filepath.Glob(filepath.Join(tasksDir, "*.md"))
		for _, f := range files {
			data, _ := os.ReadFile(f)
			if strings.Contains(string(data), "status: in-progress") {
				newContent := strings.Replace(string(data), "status: in-progress", "status: done", 1)
				os.WriteFile(f, []byte(newContent), 0644)
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

func findTasksDir() (string, error) {
	curr, _ := os.Getwd()
	for {
		path := filepath.Join(curr, ".tenazas", "tasks")
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path, nil
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return "", fmt.Errorf("could not find .tenazas/tasks directory. Run 'tenazas work init' at project root")
}
