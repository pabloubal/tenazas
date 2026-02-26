package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

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

	// Mock input for /last command then exit
	input := "/last 1\n"
	in := strings.NewReader(input)
	var out bytes.Buffer

	cli := NewCLI(sm, exec, reg, engine)
	cli.In = in
	cli.Out = &out

	// Run CLI in a way that it will exit when input is exhausted
	err := cli.Run(true)
	if err != nil && err != io.EOF {
		t.Errorf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Test log entry") {
		t.Errorf("expected output to contain 'Test log entry', got %s", output)
	}

	// Verify Branding and Session Info
	if !strings.Contains(output, "TENAZAS") {
		t.Errorf("expected output to contain 'TENAZAS' banner, got %s", output)
	}
	if !strings.Contains(output, sess.ID) {
		t.Errorf("expected output to contain session ID: %s", sess.ID)
	}
}
