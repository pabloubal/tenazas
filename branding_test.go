package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestVisualBranding(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "tenazas-branding-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	reg, _ := NewRegistry(tmpDir)
	exec := NewExecutor("gemini", tmpDir)
	engine := NewEngine(sm, exec, 5)

	var out bytes.Buffer
	cli := NewCLI(sm, exec, reg, engine)
	cli.Out = &out

	// Initialize session so drawBranding has something to show
	sess, err := cli.initializeSession(false)
	if err != nil {
		t.Fatal(err)
	}
	cli.sess = sess

	// Run only the branding logic
	cli.drawBranding()

	output := out.String()

	// 1. Check for ASCII Banner "TENAZAS"
	// We check for fragments of the Unicode ASCII art
	if !strings.Contains(output, "████████╗███████╗███╗") {
		t.Errorf("Expected output to contain Unicode ASCII banner top line")
	}
	if !strings.Contains(output, "╚═╝   ╚══════╝╚═╝") {
		t.Errorf("Expected output to contain Unicode ASCII banner bottom line")
	}

	// 2. Check for ANSI Escape Codes
	// Bold Cyan: \x1b[1;36m
	escBoldCyan := "\x1b[1;36m"
	if !strings.Contains(output, escBoldCyan) {
		t.Errorf("Expected output to contain Bold Cyan escape code (%q)", escBoldCyan)
	}

	// Dim: \x1b[2m
	escDim := "\x1b[2m"
	if !strings.Contains(output, escDim) {
		t.Errorf("Expected output to contain Dim escape code (%q)", escDim)
	}

	// Reset: \x1b[0m
	escReset := "\x1b[0m"
	if !strings.Contains(output, escReset) {
		t.Errorf("Expected output to contain Reset escape code (%q)", escReset)
	}

	// 3. Check for Metadata
	if !strings.Contains(output, "Session:") {
		t.Errorf("Expected output to contain 'Session:' label")
	}
	if !strings.Contains(output, "Path:") {
		t.Errorf("Expected output to contain 'Path:' label")
	}

	// Verify the session ID and CWD are present
	if cli.sess == nil {
		t.Fatal("cli.sess should not be nil after Run")
	}
	if !strings.Contains(output, cli.sess.ID) {
		t.Errorf("Expected output to contain session ID: %s", cli.sess.ID)
	}
	if !strings.Contains(output, cli.sess.CWD) {
		t.Errorf("Expected output to contain CWD: %s", cli.sess.CWD)
	}
}
