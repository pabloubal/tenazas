package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// isTTY checks if the given file descriptor is a terminal.
func isTTY(f *os.File) bool {
	fileInfo, err := f.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}


func TestCLIBasic(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-cli-test-*")
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	reg, _ := NewRegistry(tmpDir)
	exec := NewExecutor("gemini", tmpDir)
	engine := NewEngine(sm, exec, 5)

	// Create a dummy session and audit log
	sess := &Session{
		ID:          uuid.New().String(),
		CWD:         tmpDir,
		LastUpdated: time.Now(),
		RoleCache:   make(map[string]string),
	}
	sm.Save(sess)
	sm.AppendAudit(sess, AuditEntry{Type: AuditInfo, Content: "Test log entry"})

	var out bytes.Buffer
	cli := NewCLI(sm, exec, reg, engine)
	cli.Out = &out

	// Directly test the command handling logic instead of the full REPL
	cli.handleLast(sess, 1)

	output := out.String()
	if !strings.Contains(output, "Test log entry") {
		t.Errorf("expected output to contain 'Test log entry', got %s", output)
	}
}
