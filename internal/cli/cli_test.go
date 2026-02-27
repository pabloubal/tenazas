package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"path/filepath"

	"tenazas/internal/client"
	"tenazas/internal/engine"
	"tenazas/internal/events"
	"tenazas/internal/models"
	"tenazas/internal/registry"
	"tenazas/internal/session"
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

	sm := session.NewManager(tmpDir)
	reg, _ := registry.NewRegistry(tmpDir)
	c, _ := client.NewClient("gemini", "gemini", filepath.Join(tmpDir, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	// Create a dummy session and audit log
	sess := &models.Session{
		ID:          uuid.New().String(),
		CWD:         tmpDir,
		LastUpdated: time.Now(),
		RoleCache:   make(map[string]string),
	}
	sm.Save(sess)
	sm.AppendAudit(sess, events.AuditEntry{Type: events.AuditInfo, Content: "Test log entry"})

	var out bytes.Buffer
	cli := NewCLI(sm, reg, eng, "gemini")
	cli.Out = &out

	// Directly test the command handling logic instead of the full REPL
	cli.handleLast(sess, 1)

	output := out.String()
	if !strings.Contains(output, "Test log entry") {
		t.Errorf("expected output to contain 'Test log entry', got %s", output)
	}
}
