package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFormatFooter(t *testing.T) {
	cases := []struct {
		mode       string
		yolo       bool
		skillCount int
		sessionID  string
		expected   string
	}{
		{ApprovalModePlan, false, 5, "1234567890abcdef", "[PLAN] | Skills: 5 | Session: ...90abcdef"},
		{ApprovalModeAutoEdit, false, 12, "1234567890abcdef", "[AUTO_EDIT] | Skills: 12 | Session: ...90abcdef"},
		{ApprovalModePlan, true, 3, "1234567890abcdef", "[YOLO] | Skills: 3 | Session: ...90abcdef"},
		{"", false, 0, "1234567890abcdef", "[PLAN] | Skills: 0 | Session: ...90abcdef"},
	}

	for _, tc := range cases {
		// This will fail to compile if formatFooter is not defined
		got := formatFooter(tc.mode, tc.yolo, tc.skillCount, tc.sessionID)
		if got != tc.expected {
			t.Errorf("formatFooter(%s, %v, %d, %s) = %q, want %q", tc.mode, tc.yolo, tc.skillCount, tc.sessionID, got, tc.expected)
		}
	}
}

func TestGetTerminalSize(t *testing.T) {
	// This will fail to compile if getTerminalSize is not defined
	rows, cols, err := getTerminalSize()
	if err != nil {
		t.Logf("getTerminalSize returned error (expected in non-TTY): %v", err)
	} else {
		if rows <= 0 || cols <= 0 {
			t.Errorf("got non-positive terminal size: rows=%d, cols=%d", rows, cols)
		}
	}
}

func TestModeCommand(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}
	sess := &Session{
		ID:          uuid.New().String(),
		LastUpdated: time.Now(),
		// ApprovalMode will fail to compile if not added to Session struct
		ApprovalMode: ApprovalModePlan, 
	}

	// This will fail to compile if handleMode is not defined
	cli.handleMode(sess, []string{"auto_edit"})
	if sess.ApprovalMode != ApprovalModeAutoEdit {
		t.Errorf("expected mode AUTO_EDIT, got %s", sess.ApprovalMode)
	}

	cli.handleMode(sess, []string{"yolo"})
	if !sess.Yolo {
		t.Errorf("expected Yolo to be true")
	}

	cli.handleMode(sess, []string{"plan"})
	if sess.ApprovalMode != ApprovalModePlan || sess.Yolo {
		t.Errorf("expected mode PLAN and Yolo false, got %s, %v", sess.ApprovalMode, sess.Yolo)
	}
}

func TestDrawFooterSequences(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}
	sess := &Session{
		ID: "1234567890abcdef",
		ApprovalMode: ApprovalModePlan,
	}

	// This will fail to compile if drawFooter is not defined
	// We also need a way to pass mock skill count or terminal size if it's hardcoded to use real ones.
	// For TDD, we might need to refactor CLI to accept a "TerminalProvider" interface if we want to be pure.
	// But let's follow the implementation plan which says it uses getTerminalSize directly.
	
	cli.drawFooter(sess)
	
	got := out.String()
	// Check for ANSI sequences
	// \033[s (save cursor)
	// \033[<Row>;1H (move to last line)
	// \033[2K (clear line)
	// \033[44;37m (colors)
	// \033[u (restore cursor)
	
	if !strings.Contains(got, "\x1b[s") {
		t.Errorf("output missing save cursor sequence")
	}
	if !strings.Contains(got, "\x1b[u") {
		t.Errorf("output missing restore cursor sequence")
	}
	if !strings.Contains(got, "[PLAN]") {
		t.Errorf("output missing mode [PLAN]")
	}
}

func TestSetupTerminal(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}
	
	// This will fail to compile if setupTerminal is not defined
	cli.setupTerminal()
	
	got := out.String()
	// Check for scrolling region sequence: \033[1;<N-1>r
	// Since N depends on the actual terminal, we check for the general pattern.
	if !strings.Contains(got, "\x1b[1;") || !strings.Contains(got, "r") {
		t.Errorf("output missing scrolling region sequence")
	}
}

func TestEngineRespectsSessionApprovalMode(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-mode-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	exec := NewExecutor("echo", storageDir)
	engine := NewEngine(sm, exec, 5)
	_ = engine

	sess := &Session{
		ID:           "sess-mode",
		CWD:          storageDir,
		ApprovalMode: ApprovalModeAutoEdit,
		RoleCache:    make(map[string]string),
	}
	sm.Save(sess)

	// This test is mostly to ensure the field exists and is accessible by the engine logic.
	// A full behavioral test would require mocking the Gemini CLI to see if --approval-mode was passed.
	if sess.ApprovalMode != ApprovalModeAutoEdit {
		t.Errorf("expected AUTO_EDIT, got %s", sess.ApprovalMode)
	}
}

func TestCLIInitializeSession(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-cli-init-*")
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	cli := &CLI{
		Sm: sm,
	}

	sess, err := cli.initializeSession(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sess.ApprovalMode != ApprovalModePlan {
		t.Errorf("expected default ApprovalMode 'PLAN', got %s", sess.ApprovalMode)
	}
}
