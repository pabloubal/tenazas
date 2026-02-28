package logs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tenazas/internal/events"
	"tenazas/internal/models"
)

func TestBackwardCompatibility_OldEntriesWithoutRole(t *testing.T) {
	// Simulate old JSONL entries without the "role" field
	oldEntry := `{"timestamp":"2026-01-15T10:30:00Z","type":"llm_response","source":"engine","content":"Hello world"}`

	dir := t.TempDir()
	path := filepath.Join(dir, "test.audit.jsonl")
	if err := os.WriteFile(path, []byte(oldEntry+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadAuditFile(path, nil)
	if err != nil {
		t.Fatalf("ReadAuditFile failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Role != "" {
		t.Errorf("expected empty role for old entry, got %q", entries[0].Role)
	}
	if entries[0].Type != events.AuditLLMResponse {
		t.Errorf("expected type %q, got %q", events.AuditLLMResponse, entries[0].Type)
	}
	if entries[0].Content != "Hello world" {
		t.Errorf("expected content %q, got %q", "Hello world", entries[0].Content)
	}
}

func TestBackwardCompatibility_NewEntriesWithRole(t *testing.T) {
	entry := events.AuditEntry{
		Timestamp: time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC),
		Type:      events.AuditLLMPrompt,
		Source:    "architect",
		Role:      events.RoleUser,
		Content:   "Write tests",
	}
	data, _ := json.Marshal(entry)

	dir := t.TempDir()
	path := filepath.Join(dir, "test.audit.jsonl")
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	entries, err := ReadAuditFile(path, nil)
	if err != nil {
		t.Fatalf("ReadAuditFile failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Role != events.RoleUser {
		t.Errorf("expected role %q, got %q", events.RoleUser, entries[0].Role)
	}
}

func TestFilter_ByType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.audit.jsonl")
	writeTestEntries(t, path, []events.AuditEntry{
		{Type: events.AuditLLMPrompt, Role: events.RoleUser, Content: "prompt"},
		{Type: events.AuditLLMResponse, Role: events.RoleAssistant, Content: "response"},
		{Type: events.AuditStatus, Role: events.RoleSystem, Content: "status change"},
	})

	entries, err := ReadAuditFile(path, &Filter{Type: events.AuditLLMResponse})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Content != "response" {
		t.Errorf("type filter failed: got %d entries", len(entries))
	}
}

func TestFilter_ByRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.audit.jsonl")
	writeTestEntries(t, path, []events.AuditEntry{
		{Type: events.AuditLLMPrompt, Role: events.RoleUser, Content: "prompt"},
		{Type: events.AuditLLMResponse, Role: events.RoleAssistant, Content: "response"},
		{Type: events.AuditStatus, Role: events.RoleSystem, Content: "status"},
	})

	entries, err := ReadAuditFile(path, &Filter{Role: events.RoleAssistant})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Content != "response" {
		t.Errorf("role filter failed: got %d entries", len(entries))
	}
}

func TestFilter_BySearch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.audit.jsonl")
	writeTestEntries(t, path, []events.AuditEntry{
		{Type: events.AuditInfo, Content: "Starting deployment"},
		{Type: events.AuditInfo, Content: "Running tests"},
		{Type: events.AuditInfo, Content: "Deployment complete"},
	})

	entries, err := ReadAuditFile(path, &Filter{Search: "deployment"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("search filter failed: expected 2 entries, got %d", len(entries))
	}
}

func TestFilter_BySinceUntil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.audit.jsonl")
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	writeTestEntries(t, path, []events.AuditEntry{
		{Timestamp: t1, Type: events.AuditInfo, Content: "early"},
		{Timestamp: t2, Type: events.AuditInfo, Content: "mid"},
		{Timestamp: t3, Type: events.AuditInfo, Content: "late"},
	})

	entries, err := ReadAuditFile(path, &Filter{
		Since: time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC),
		Until: time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Content != "mid" {
		t.Errorf("time filter failed: got %d entries", len(entries))
	}
}

func TestSummarize(t *testing.T) {
	entries := []events.AuditEntry{
		{Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), Type: events.AuditStatus, Content: "Started skill deploy at node init"},
		{Timestamp: time.Date(2026, 1, 1, 10, 0, 5, 0, time.UTC), Type: events.AuditLLMPrompt, Role: events.RoleUser},
		{Timestamp: time.Date(2026, 1, 1, 10, 0, 10, 0, time.UTC), Type: events.AuditLLMResponse, Role: events.RoleAssistant},
		{Timestamp: time.Date(2026, 1, 1, 10, 0, 15, 0, time.UTC), Type: events.AuditCmdResult, ExitCode: 1},
		{Timestamp: time.Date(2026, 1, 1, 10, 0, 20, 0, time.UTC), Type: events.AuditCmdResult, ExitCode: 0},
		{Timestamp: time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC), Type: events.AuditIntervention},
	}

	sess := &models.Session{ID: "test-123", Title: "Test Session", Status: models.StatusCompleted}
	s := Summarize(entries, sess)

	if s.PromptCount != 1 {
		t.Errorf("expected 1 prompt, got %d", s.PromptCount)
	}
	if s.ResponseCount != 1 {
		t.Errorf("expected 1 response, got %d", s.ResponseCount)
	}
	if s.CmdResultCount != 2 {
		t.Errorf("expected 2 cmd results, got %d", s.CmdResultCount)
	}
	if s.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", s.ErrorCount)
	}
	if s.Interventions != 1 {
		t.Errorf("expected 1 intervention, got %d", s.Interventions)
	}
	if s.Duration != time.Minute {
		t.Errorf("expected duration 1m, got %v", s.Duration)
	}
}

func TestFormatEntry_WithRole(t *testing.T) {
	entry := events.AuditEntry{
		Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Type:      events.AuditLLMPrompt,
		Source:    "architect",
		Role:      events.RoleUser,
		Content:   "Write a function",
	}
	output := FormatEntry(entry)
	if output == "" {
		t.Error("expected non-empty formatted output")
	}
}

func writeTestEntries(t *testing.T, path string, entries []events.AuditEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	for _, e := range entries {
		data, _ := json.Marshal(e)
		f.Write(append(data, '\n'))
	}
}
