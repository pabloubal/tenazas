package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionManagerSaveAndLoad(t *testing.T) {
	// Setup a temporary storage path
	storageDir, err := os.MkdirTemp("", "tenazas-test-storage-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	cwd, _ := os.Getwd()

	sess := &Session{
		ID:  "test-session-123",
		CWD: cwd,
		Title: "Test Session",
		RoleCache: make(map[string]string),
	}

	// Test Save
	err = sm.Save(sess)
	if err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// Verify directory creation
	slug := strings.ReplaceAll(cwd, "/", "-")
	if strings.HasPrefix(slug, "-") {
		slug = slug[1:]
	}
	expectedDir := filepath.Join(storageDir, "sessions", slug)
	if _, err := os.Stat(expectedDir); os.IsNotExist(err) {
		t.Errorf("expected directory %s to exist", expectedDir)
	}

	// Test Load
	loaded, err := sm.Load("test-session-123")
	if err != nil {
		t.Fatalf("failed to load session: %v", err)
	}

	if loaded.ID != sess.ID || loaded.Title != sess.Title {
		t.Errorf("loaded session does not match original: got %v, want %v", loaded, sess)
	}
}

func TestSessionManagerListAndAudit(t *testing.T) {
	storageDir, err := os.MkdirTemp("", "tenazas-test-list-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	cwd, _ := os.Getwd()

	s1 := &Session{ID: "s1", CWD: cwd, Title: "S1"}
	s2 := &Session{ID: "s2", CWD: cwd, Title: "S2"}

	sm.Save(s1)
	time.Sleep(10 * time.Millisecond) // Ensure different LastUpdated
	sm.Save(s2)

	// Test List
	list, total, err := sm.List(0, 10)
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}
	if total != 2 {
		t.Errorf("expected 2 sessions, got %d", total)
	}
	// Check sorting (latest first)
	if list[0].ID != "s2" {
		t.Errorf("expected latest session to be s2, got %s", list[0].ID)
	}

	// Test Audit
	entry := AuditEntry{Type: "info", Source: "test", Content: "hello"}
	err = sm.AppendAudit(s1, entry)
	if err != nil {
		t.Fatalf("failed to append audit: %v", err)
	}

	audits, err := sm.GetLastAudit(s1, 1)
	if err != nil {
		t.Fatalf("failed to get last audit: %v", err)
	}
	if len(audits) != 1 || audits[0].Content != "hello" {
		t.Errorf("expected audit content 'hello', got %v", audits)
	}
}

func TestSessionManagerGetLatest(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-test-latest-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	cwd, _ := os.Getwd()

	s1 := &Session{ID: "old", CWD: cwd, LastUpdated: time.Now().Add(-1 * time.Hour)}
	s2 := &Session{ID: "new", CWD: cwd, LastUpdated: time.Now()}

	sm.Save(s1)
	sm.Save(s2)

	latest, err := sm.GetLatest()
	if err != nil {
		t.Fatalf("GetLatest failed: %v", err)
	}

	if latest.ID != "new" {
		t.Errorf("expected latest session to be 'new', got %s", latest.ID)
	}
}
