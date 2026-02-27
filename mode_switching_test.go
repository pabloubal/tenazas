package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCycleMode(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-mode-test-*")
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	cli := NewCLI(sm, nil, nil, nil)
	sess := &Session{
		ID:           uuid.New().String(),
		CWD:          tmpDir,
		ApprovalMode: ApprovalModePlan,
		Yolo:         false,
	}
	cli.sess = sess

	// Test Plan -> AutoEdit
	cli.cycleMode(sess)
	if sess.ApprovalMode != ApprovalModeAutoEdit || sess.Yolo {
		t.Errorf("expected AUTO_EDIT, got %s (Yolo: %v)", sess.ApprovalMode, sess.Yolo)
	}

	// Test AutoEdit -> Yolo
	cli.cycleMode(sess)
	if sess.ApprovalMode != ApprovalModeYolo || !sess.Yolo {
		t.Errorf("expected YOLO, got %s (Yolo: %v)", sess.ApprovalMode, sess.Yolo)
	}

	// Test Yolo -> Plan
	cli.cycleMode(sess)
	if sess.ApprovalMode != ApprovalModePlan || sess.Yolo {
		t.Errorf("expected PLAN, got %s (Yolo: %v)", sess.ApprovalMode, sess.Yolo)
	}
}

func TestHandleModeConsistency(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-mode-test-*")
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	cli := NewCLI(sm, nil, nil, nil)
	sess := &Session{
		ID:           uuid.New().String(),
		CWD:          tmpDir,
		ApprovalMode: ApprovalModePlan,
		Yolo:         false,
	}

	// Set to YOLO via /mode
	cli.handleMode(sess, []string{"yolo"})
	if sess.ApprovalMode != ApprovalModeYolo || !sess.Yolo {
		t.Errorf("expected mode=YOLO and yolo=true, got %s and %v", sess.ApprovalMode, sess.Yolo)
	}

	// Set to PLAN via /mode
	cli.handleMode(sess, []string{"plan"})
	if sess.ApprovalMode != ApprovalModePlan || sess.Yolo {
		t.Errorf("expected mode=PLAN and yolo=false, got %s and %v", sess.ApprovalMode, sess.Yolo)
	}
}

func TestShiftTabDetection(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-mode-test-*")
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	cli := NewCLI(sm, nil, nil, nil)
	sess := &Session{
		ID:           uuid.New().String(),
		CWD:          tmpDir,
		ApprovalMode: ApprovalModePlan,
		Yolo:         false,
	}
	cli.sess = sess

	// Simulate Shift+Tab (\x1b[Z) then Enter () and then Ctrl+C (\x03)
	input := "\x1b[Z\x03"
	cli.In = strings.NewReader(input)
	var out bytes.Buffer
	cli.Out = &out

	err := cli.replRaw(sess)
	if err != nil && err != io.EOF {
		t.Errorf("unexpected error: %v", err)
	}

	// Mode should have cycled once: PLAN -> AUTO_EDIT
	if sess.ApprovalMode != ApprovalModeAutoEdit {
		t.Errorf("expected mode AUTO_EDIT after Shift+Tab, got %s", sess.ApprovalMode)
	}

	// Output should contain the footer update for AUTO_EDIT
	output := out.String()
	if !strings.Contains(output, "[AUTO_EDIT]") {
		t.Errorf("expected output to contain [AUTO_EDIT] in footer, got %s", output)
	}
}

func TestFooterUpdateOnModeSwitch(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-mode-test-*")
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	cli := NewCLI(sm, nil, nil, nil)
	sess := &Session{
		ID:           "test-session-id",
		CWD:          tmpDir,
		ApprovalMode: ApprovalModePlan,
		Yolo:         false,
	}
	cli.sess = sess

	var out bytes.Buffer
	cli.Out = &out

	cli.cycleMode(sess) // Should switch to AUTO_EDIT and update footer

	output := out.String()
	if !strings.Contains(output, "[AUTO_EDIT]") {
		t.Errorf("expected footer to contain [AUTO_EDIT], got %s", output)
	}
}
