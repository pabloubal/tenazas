package task

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// FindTask
// ---------------------------------------------------------------------------

func TestFindTask(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-find-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create 3 tasks on disk.
	for i, title := range []string{"Alpha", "Bravo", "Charlie"} {
		tk := &Task{
			ID:        idFor(i + 1),
			Title:     title,
			Status:    StatusTodo,
			CreatedAt: time.Now().Truncate(time.Second),
			Content:   "content " + title,
		}
		if err := WriteTask(filepath.Join(tmpDir, tk.ID+".md"), tk); err != nil {
			t.Fatal(err)
		}
	}

	// Find the second task.
	found, err := FindTask(tmpDir, "TSK-000002")
	if err != nil {
		t.Fatalf("FindTask returned unexpected error: %v", err)
	}
	if found.ID != "TSK-000002" {
		t.Errorf("ID = %q, want TSK-000002", found.ID)
	}
	if found.Title != "Bravo" {
		t.Errorf("Title = %q, want Bravo", found.Title)
	}
	if found.Status != StatusTodo {
		t.Errorf("Status = %q, want %q", found.Status, StatusTodo)
	}
}

func TestFindTaskNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-find-nf-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create one task so the directory isn't empty.
	tk := &Task{ID: "TSK-000001", Title: "Solo", Status: StatusTodo, CreatedAt: time.Now().Truncate(time.Second), Content: "c"}
	WriteTask(filepath.Join(tmpDir, tk.ID+".md"), tk)

	found, err := FindTask(tmpDir, "TSK-999999")
	if err == nil {
		t.Fatal("Expected an error for non-existent task, got nil")
	}
	if found != nil {
		t.Errorf("Expected nil task, got %+v", found)
	}
	if !strings.Contains(err.Error(), "TSK-999999") {
		t.Errorf("Error should contain the task ID, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FormatDuration
// ---------------------------------------------------------------------------

func TestFormatDuration(t *testing.T) {
	mkTime := func(base time.Time, add time.Duration) *time.Time {
		v := base.Add(add)
		return &v
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		task *Task
		want string
	}{
		{
			name: "done_2h30m",
			task: &Task{Status: StatusDone, StartedAt: &base, CompletedAt: mkTime(base, 2*time.Hour+30*time.Minute)},
			want: "2h 30m",
		},
		{
			name: "done_45s",
			task: &Task{Status: StatusDone, StartedAt: &base, CompletedAt: mkTime(base, 45*time.Second)},
			want: "45s",
		},
		{
			name: "done_5m10s",
			task: &Task{Status: StatusDone, StartedAt: &base, CompletedAt: mkTime(base, 5*time.Minute+10*time.Second)},
			want: "5m 10s",
		},
		{
			name: "in_progress",
			task: &Task{Status: StatusInProgress},
			want: "(running)",
		},
		{
			name: "todo_returns_dash",
			task: &Task{Status: StatusTodo},
			want: "—",
		},
		{
			name: "done_nil_started_at",
			task: &Task{Status: StatusDone, CompletedAt: mkTime(base, time.Hour)},
			want: "—",
		},
		{
			name: "done_nil_completed_at",
			task: &Task{Status: StatusDone, StartedAt: &base},
			want: "—",
		},
		{
			name: "blocked_returns_dash",
			task: &Task{Status: StatusBlocked},
			want: "—",
		},
		{
			name: "done_zero_duration",
			task: &Task{Status: StatusDone, StartedAt: &base, CompletedAt: &base},
			want: "0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.task)
			if got != tt.want {
				t.Errorf("FormatDuration() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatHumanDuration (unexported)
// ---------------------------------------------------------------------------

func TestFormatHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{5*time.Minute + 10*time.Second, "5m 10s"},
		{2*time.Hour + 30*time.Minute, "2h 30m"},
		{1 * time.Hour, "1h 0m"},
		{-5 * time.Second, "0s"}, // negative clamped to 0
	}
	for _, tt := range tests {
		t.Run(tt.d.String(), func(t *testing.T) {
			got := formatHumanDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatHumanDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// truncateTitle (unexported)
// ---------------------------------------------------------------------------

func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		title  string
		maxLen int
		want   string
	}{
		{"Short", 30, "Short"},
		{"Exactly thirty characters long!", 30, "Exactly thirty characters lon…"},
		{"A very long title that exceeds the maximum length by quite a bit", 30, "A very long title that exceed…"},
		{"", 30, ""},
		{"Hi", 2, "Hi"},
		{"Hello", 3, "He…"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := truncateTitle(tt.title, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateTitle(%q, %d) = %q, want %q", tt.title, tt.maxLen, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RenderList
// ---------------------------------------------------------------------------

func TestRenderList(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	completed := base.Add(30 * time.Minute)

	tasks := []*Task{
		{ID: "TSK-000001", Title: "Task Alpha", Status: StatusTodo, Priority: 1, CreatedAt: base, UpdatedAt: base},
		{ID: "TSK-000002", Title: "Task Bravo", Status: StatusInProgress, Priority: 2, CreatedAt: base, UpdatedAt: base},
		{ID: "TSK-000003", Title: "Task Charlie", Status: StatusDone, Priority: 0, CreatedAt: base, UpdatedAt: base, StartedAt: &base, CompletedAt: &completed},
	}

	var buf bytes.Buffer
	RenderList(&buf, tasks)
	out := buf.String()

	// Header present.
	if !strings.Contains(out, "ID") || !strings.Contains(out, "STATUS") || !strings.Contains(out, "TITLE") || !strings.Contains(out, "DURATION") {
		t.Errorf("Expected header columns, got:\n%s", out)
	}

	// All task IDs appear.
	for _, id := range []string{"TSK-000001", "TSK-000002", "TSK-000003"} {
		if !strings.Contains(out, id) {
			t.Errorf("Expected output to contain %s", id)
		}
	}

	// Status summary at the end.
	if !strings.Contains(out, "Todo: 1") {
		t.Errorf("Expected summary to contain 'Todo: 1', got:\n%s", out)
	}
	if !strings.Contains(out, "In-Progress: 1") {
		t.Errorf("Expected summary to contain 'In-Progress: 1', got:\n%s", out)
	}
	if !strings.Contains(out, "Done: 1") {
		t.Errorf("Expected summary to contain 'Done: 1', got:\n%s", out)
	}

	// Duration for done task.
	if !strings.Contains(out, "30m 0s") {
		t.Errorf("Expected duration '30m 0s' for completed task, got:\n%s", out)
	}
}

func TestRenderListEmpty(t *testing.T) {
	var buf bytes.Buffer
	RenderList(&buf, []*Task{})
	out := buf.String()

	want := "No tasks found. Use 'tenazas work add \"Title\" \"Description\"' to create one.\n"
	if out != want {
		t.Errorf("RenderList(empty) = %q, want %q", out, want)
	}
}

func TestRenderListSorting(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	completed := base.Add(time.Hour)

	tasks := []*Task{
		{ID: "TSK-000001", Title: "Done Task", Status: StatusDone, Priority: 0, CreatedAt: base, UpdatedAt: base, StartedAt: &base, CompletedAt: &completed},
		{ID: "TSK-000002", Title: "In Progress", Status: StatusInProgress, Priority: 3, CreatedAt: base, UpdatedAt: base},
		{ID: "TSK-000003", Title: "Blocked Task", Status: StatusBlocked, Priority: 1, CreatedAt: base, UpdatedAt: base},
		{ID: "TSK-000004", Title: "Todo High", Status: StatusTodo, Priority: 5, CreatedAt: base, UpdatedAt: base},
		{ID: "TSK-000005", Title: "Todo Low", Status: StatusTodo, Priority: 0, CreatedAt: base, UpdatedAt: base},
	}

	var buf bytes.Buffer
	RenderList(&buf, tasks)
	out := buf.String()

	// Expected order: in-progress → blocked → todo(pri=5) → todo(pri=0) → done.
	lines := strings.Split(out, "\n")
	var ids []string
	for _, line := range lines {
		if strings.HasPrefix(line, "TSK-") {
			ids = append(ids, strings.Fields(line)[0])
		}
	}

	expected := []string{"TSK-000002", "TSK-000003", "TSK-000004", "TSK-000005", "TSK-000001"}
	if len(ids) != len(expected) {
		t.Fatalf("Expected %d task rows, got %d: %v", len(expected), len(ids), ids)
	}
	for i, want := range expected {
		if ids[i] != want {
			t.Errorf("Row %d: got %s, want %s (full order: %v)", i, ids[i], want, ids)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// sortTasksForList (unexported)
// ---------------------------------------------------------------------------

func TestSortTasksForList(t *testing.T) {
	tasks := []*Task{
		{ID: "TSK-000003", Status: StatusTodo, Priority: 0},
		{ID: "TSK-000001", Status: StatusDone, Priority: 5},
		{ID: "TSK-000002", Status: StatusInProgress, Priority: 1},
		{ID: "TSK-000004", Status: StatusBlocked, Priority: 2},
		{ID: "TSK-000005", Status: StatusTodo, Priority: 3},
	}

	sortTasksForList(tasks)

	wantIDs := []string{"TSK-000002", "TSK-000004", "TSK-000005", "TSK-000003", "TSK-000001"}
	for i, want := range wantIDs {
		if tasks[i].ID != want {
			t.Errorf("Position %d: got %s, want %s", i, tasks[i].ID, want)
		}
	}
}

func TestSortTasksForListTertiaryByID(t *testing.T) {
	// Same status and priority — should break tie by ID ascending.
	tasks := []*Task{
		{ID: "TSK-000003", Status: StatusTodo, Priority: 1},
		{ID: "TSK-000001", Status: StatusTodo, Priority: 1},
		{ID: "TSK-000002", Status: StatusTodo, Priority: 1},
	}

	sortTasksForList(tasks)

	for i, want := range []string{"TSK-000001", "TSK-000002", "TSK-000003"} {
		if tasks[i].ID != want {
			t.Errorf("Position %d: got %s, want %s", i, tasks[i].ID, want)
		}
	}
}

// ---------------------------------------------------------------------------
// printStatusSummaryTo (unexported)
// ---------------------------------------------------------------------------

func TestPrintStatusSummaryTo(t *testing.T) {
	tasks := []*Task{
		{Status: StatusTodo},
		{Status: StatusTodo},
		{Status: StatusInProgress},
		{Status: StatusDone},
		{Status: StatusDone},
		{Status: StatusDone},
		{Status: StatusBlocked},
	}

	var buf bytes.Buffer
	printStatusSummaryTo(&buf, tasks)
	out := buf.String()

	if !strings.Contains(out, "Todo: 2") {
		t.Errorf("Expected 'Todo: 2', got: %s", out)
	}
	if !strings.Contains(out, "In-Progress: 1") {
		t.Errorf("Expected 'In-Progress: 1', got: %s", out)
	}
	if !strings.Contains(out, "Done: 3") {
		t.Errorf("Expected 'Done: 3', got: %s", out)
	}
	if !strings.Contains(out, "Blocked: 1") {
		t.Errorf("Expected 'Blocked: 1', got: %s", out)
	}
}

func TestPrintStatusSummaryToEmpty(t *testing.T) {
	var buf bytes.Buffer
	printStatusSummaryTo(&buf, []*Task{})
	out := buf.String()

	if !strings.Contains(out, "Todo: 0") || !strings.Contains(out, "Done: 0") {
		t.Errorf("Expected all-zero summary for empty list, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// RenderShow
// ---------------------------------------------------------------------------

func TestRenderShow(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	completed := base.Add(2*time.Hour + 15*time.Minute)

	task := &Task{
		ID:              "TSK-000003",
		Title:           "Implement feature X",
		Status:          StatusDone,
		Priority:        5,
		CreatedAt:       base,
		UpdatedAt:       base,
		StartedAt:       &base,
		CompletedAt:     &completed,
		OwnerPID:        1234,
		OwnerInstanceID: "cli-1234",
		OwnerSessionID:  "sess-abc",
		BlockedBy:       []string{"TSK-000001"},
		Blocks:          []string{"TSK-000004"},
		Content:         "Detailed description of the feature.\nMultiple lines.",
	}

	taskMap := map[string]*Task{
		"TSK-000001": {ID: "TSK-000001", Status: StatusDone},
		"TSK-000003": task,
		"TSK-000004": {ID: "TSK-000004", Status: StatusBlocked},
	}

	var buf bytes.Buffer
	RenderShow(&buf, task, taskMap)
	out := buf.String()

	// Title banner.
	if !strings.Contains(out, "TSK-000003") || !strings.Contains(out, "Implement feature X") {
		t.Errorf("Expected title banner, got:\n%s", out)
	}

	// Metadata fields.
	for _, want := range []string{"Status:", "Priority:", "Created:", "Updated:", "Duration:"} {
		if !strings.Contains(out, want) {
			t.Errorf("Expected metadata field %q, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, StatusDone) {
		t.Errorf("Expected status value %q in output", StatusDone)
	}

	// Duration for this done task.
	if !strings.Contains(out, "2h 15m") {
		t.Errorf("Expected duration '2h 15m', got:\n%s", out)
	}

	// Owner section.
	if !strings.Contains(out, "Owner:") {
		t.Errorf("Expected Owner section, got:\n%s", out)
	}
	if !strings.Contains(out, "1234") {
		t.Errorf("Expected PID 1234 in output")
	}
	if !strings.Contains(out, "cli-1234") {
		t.Errorf("Expected Instance cli-1234 in output")
	}
	if !strings.Contains(out, "sess-abc") {
		t.Errorf("Expected Session sess-abc in output")
	}

	// Dependencies.
	if !strings.Contains(out, "Blocked By:") {
		t.Errorf("Expected 'Blocked By:' section")
	}
	if !strings.Contains(out, "TSK-000001 (done)") {
		t.Errorf("Expected resolved dep 'TSK-000001 (done)', got:\n%s", out)
	}
	if !strings.Contains(out, "Blocks:") {
		t.Errorf("Expected 'Blocks:' section")
	}
	if !strings.Contains(out, "TSK-000004 (blocked)") {
		t.Errorf("Expected resolved dep 'TSK-000004 (blocked)', got:\n%s", out)
	}

	// Content body.
	if !strings.Contains(out, "Detailed description of the feature.") {
		t.Errorf("Expected content body in output")
	}
}

func TestRenderShowNoOwner(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &Task{
		ID:        "TSK-000001",
		Title:     "No Owner Task",
		Status:    StatusTodo,
		Priority:  0,
		CreatedAt: base,
		UpdatedAt: base,
		Content:   "Some content",
	}

	var buf bytes.Buffer
	RenderShow(&buf, task, map[string]*Task{"TSK-000001": task})
	out := buf.String()

	// Metadata must still be rendered even without owner fields.
	if !strings.Contains(out, "TSK-000001") {
		t.Errorf("Expected task ID in output, got:\n%s", out)
	}
	if !strings.Contains(out, "No Owner Task") {
		t.Errorf("Expected task title in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Status:") {
		t.Errorf("Expected Status field in output, got:\n%s", out)
	}

	// Owner section must be absent.
	if strings.Contains(out, "Owner:") {
		t.Errorf("Expected no Owner section when all owner fields are zero/empty, got:\n%s", out)
	}
}

func TestRenderShowUnknownDependency(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &Task{
		ID:        "TSK-000002",
		Title:     "Dep Test",
		Status:    StatusBlocked,
		CreatedAt: base,
		UpdatedAt: base,
		BlockedBy: []string{"TSK-999999"}, // not in taskMap
	}

	var buf bytes.Buffer
	RenderShow(&buf, task, map[string]*Task{"TSK-000002": task})
	out := buf.String()

	if !strings.Contains(out, "TSK-999999 (unknown)") {
		t.Errorf("Expected unknown dep to show '(unknown)', got:\n%s", out)
	}
}

func TestRenderShowNoContent(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &Task{
		ID:        "TSK-000001",
		Title:     "Empty Content",
		Status:    StatusTodo,
		CreatedAt: base,
		UpdatedAt: base,
		Content:   "",
	}

	var buf bytes.Buffer
	RenderShow(&buf, task, map[string]*Task{})
	out := buf.String()

	// Metadata must still be rendered even without content.
	if !strings.Contains(out, "TSK-000001") {
		t.Errorf("Expected task ID in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Empty Content") {
		t.Errorf("Expected task title in output, got:\n%s", out)
	}

	// The separator should not appear when content is empty.
	if strings.Contains(out, strings.Repeat("─", 40)) {
		t.Errorf("Expected no content separator when Content is empty, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// normalizeTaskID (unexported, in work.go)
// ---------------------------------------------------------------------------

func TestNormalizeTaskID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"3", "TSK-000003"},
		{"000003", "TSK-000003"},
		{"TSK-000003", "TSK-000003"},
		{"42", "TSK-000042"},
		{"tsk-000010", "TSK-000010"},
		{"0", "TSK-000000"},
		{"123456", "TSK-123456"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeTaskID(tt.input)
			if got != tt.want {
				t.Errorf("normalizeTaskID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Title truncation in RenderList context
// ---------------------------------------------------------------------------

func TestTitleTruncationInRenderList(t *testing.T) {
	longTitle := "This is a very long task title that exceeds thirty characters easily"
	task := &Task{
		ID:        "TSK-000001",
		Title:     longTitle,
		Status:    StatusTodo,
		Priority:  0,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	var buf bytes.Buffer
	RenderList(&buf, []*Task{task})
	out := buf.String()

	// Full title must NOT appear.
	if strings.Contains(out, longTitle) {
		t.Errorf("Expected title to be truncated, but full title found in output")
	}

	// Truncated title (29 chars + "…") must appear.
	truncated := longTitle[:29] + "…"
	if !strings.Contains(out, truncated) {
		t.Errorf("Expected truncated title %q in output, got:\n%s", truncated, out)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func idFor(n int) string {
	return "TSK-" + padNum(n)
}

func padNum(n int) string {
	s := "000000"
	ns := strings.TrimLeft(s, "0")
	_ = ns
	// Simple zero-padding.
	result := ""
	v := n
	for i := 5; i >= 0; i-- {
		pow := 1
		for j := 0; j < i; j++ {
			pow *= 10
		}
		digit := v / pow
		v %= pow
		result += string(rune('0' + digit))
	}
	return result
}
