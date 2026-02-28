package task

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

var statusOrder = map[string]int{
	StatusInProgress: 0,
	StatusBlocked:    1,
	StatusTodo:       2,
	StatusDone:       3,
}

func sortTasksForList(tasks []*Task) {
	sort.Slice(tasks, func(i, j int) bool {
		oi, oj := statusOrder[tasks[i].Status], statusOrder[tasks[j].Status]
		if oi != oj {
			return oi < oj
		}
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority > tasks[j].Priority
		}
		return tasks[i].ID < tasks[j].ID
	})
}

func FormatDuration(t *Task) string {
	if t.Status == StatusDone && t.StartedAt != nil && t.CompletedAt != nil {
		return formatHumanDuration(t.CompletedAt.Sub(*t.StartedAt))
	}
	if t.Status == StatusInProgress {
		return "(running)"
	}
	return "—"
}

func formatHumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h, m, s := total/3600, total%3600/60, total%60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func truncateTitle(title string, maxLen int) string {
	runes := []rune(title)
	if len(runes) <= maxLen {
		return title
	}
	return string(runes[:maxLen-1]) + "…"
}

func printStatusSummaryTo(w io.Writer, tasks []*Task) {
	counts := map[string]int{StatusTodo: 0, StatusInProgress: 0, StatusDone: 0, StatusBlocked: 0}
	for _, t := range tasks {
		counts[t.Status]++
	}
	fmt.Fprintf(w, "Todo: %d | In-Progress: %d | Done: %d | Blocked: %d\n",
		counts[StatusTodo], counts[StatusInProgress], counts[StatusDone], counts[StatusBlocked])
}

func RenderList(w io.Writer, tasks []*Task) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks found. Use 'tenazas work add \"Title\" \"Description\"' to create one.")
		return
	}
	sortTasksForList(tasks)
	fmt.Fprintf(w, "%-12s %-13s %-4s %-30s %s\n", "ID", "STATUS", "PRI", "TITLE", "DURATION")
	fmt.Fprintln(w, strings.Repeat("─", 72))
	for _, t := range tasks {
		title := truncateTitle(t.Title, 30)
		dur := FormatDuration(t)
		fmt.Fprintf(w, "%-12s %-13s %-4d %-30s %s\n", t.ID, t.Status, t.Priority, title, dur)
	}
	fmt.Fprintln(w)
	printStatusSummaryTo(w, tasks)
}

func RenderShow(w io.Writer, task *Task, taskMap map[string]*Task) {
	fmt.Fprintf(w, "═══ %s: %s ═══\n\n", task.ID, task.Title)
	fmt.Fprintf(w, "  Status:      %s\n", task.Status)
	fmt.Fprintf(w, "  Priority:    %d\n", task.Priority)
	fmt.Fprintf(w, "  Created:     %s\n", task.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "  Updated:     %s\n", task.UpdatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "  Duration:    %s\n", FormatDuration(task))

	if task.OwnerPID != 0 || task.OwnerInstanceID != "" || task.OwnerSessionID != "" {
		fmt.Fprintf(w, "\n  Owner:\n")
		if task.OwnerPID != 0 {
			fmt.Fprintf(w, "    PID:       %d\n", task.OwnerPID)
		}
		if task.OwnerInstanceID != "" {
			fmt.Fprintf(w, "    Instance:  %s\n", task.OwnerInstanceID)
		}
		if task.OwnerSessionID != "" {
			fmt.Fprintf(w, "    Session:   %s\n", task.OwnerSessionID)
		}
	}

	if len(task.BlockedBy) > 0 {
		renderDeps(w, "Blocked By:", task.BlockedBy, taskMap)
	}
	if len(task.Blocks) > 0 {
		renderDeps(w, "Blocks:", task.Blocks, taskMap)
	}

	if task.Content != "" {
		fmt.Fprintf(w, "\n%s\n", strings.Repeat("─", 40))
		fmt.Fprintln(w, task.Content)
	}
}

func renderDeps(w io.Writer, label string, ids []string, taskMap map[string]*Task) {
	fmt.Fprintf(w, "\n  %s\n", label)
	for _, id := range ids {
		if dep, ok := taskMap[id]; ok {
			fmt.Fprintf(w, "    %s (%s)\n", id, dep.Status)
		} else {
			fmt.Fprintf(w, "    %s (unknown)\n", id)
		}
	}
}
